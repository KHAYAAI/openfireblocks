// Command forge-enclave-admin runs operator procedures against signing
// parties in Nitro Enclaves. It talks to each party over TLS that it checks
// by attestation (nitroattest), presenting a PKI client certificate whose
// common name the enclaves accept as the orchestrator's.
//
//	forge-enclave-admin migrate -ceremony C -policy policy.json \
//	  -cert client.crt -key client.key [-ca ca.pem] \
//	  -domain internal -port 7000 -parties 3 \
//	  (-authorizer-key-file seed.hex | -authorizations auth.json)
//
// migrate moves a key that predates enclave mode into enclave sealing:
//
//  1. every party loads its clear-text share for migration
//     (POST /tss/keygen/restore with migrate_legacy, authorised "migrate");
//  2. every party refreshes the key inside its enclave
//     (POST /tss/reshare/start, reshare id "migrate-<ceremony>", authorised
//     "reshare");
//  3. it waits until every party reports the refresh complete, the key
//     unchanged, the new share sealed, and the clear-text versions destroyed.
//
// The two authorisations are bound to the ceremony and the reshare id, so a
// co-signer can sign them offline with infrastructure/kind/authorize and hand
// over a file:
//
//	{"migrate": {"authorization": "...", "signature": "..."},
//	 "reshare": {"authorization": "...", "signature": "..."}}
//
// -authorizer-key-file signs them here instead; use it only where the
// co-signer key may be on this machine (a rehearsal).
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"openfireblocks.com/services/mpc-party/nitroattest"
)

type signedAuth struct {
	Authorization string `json:"authorization"`
	Signature     string `json:"signature"`
}

type migrateOptions struct {
	ceremony string
	parties  int
	domain   string
	port     int
	scheme   string
	client   *http.Client
	auths    map[string]signedAuth
	timeout  time.Duration
	poll     time.Duration
	out      io.Writer
}

func (o migrateOptions) partyURL(id int) string {
	return fmt.Sprintf("%s://party-%d.%s:%d", o.scheme, id, o.domain, o.port)
}

func (o migrateOptions) peers() map[string]string {
	p := map[string]string{}
	for i := 1; i <= o.parties; i++ {
		p[fmt.Sprint(i)] = o.partyURL(i)
	}
	return p
}

// ReshareID is the refresh a migration runs; fixed so it can be authorised
// in advance.
func ReshareID(ceremony string) string { return "migrate-" + ceremony }

// authorization canonicalisation, as services/mpc-party/authorizer.go.
func canonical(op, ceremony string, issuedAt int64) []byte {
	return []byte(strings.Join([]string{"openfireblocks-authorization-v1", op, ceremony, "", fmt.Sprint(issuedAt)}, "\n"))
}

func signWith(priv ed25519.PrivateKey, op, ceremony string) signedAuth {
	now := time.Now().Unix()
	req, _ := json.Marshal(map[string]any{"operation": op, "ceremony_id": ceremony, "issued_at": now})
	return signedAuth{Authorization: string(req), Signature: hex.EncodeToString(ed25519.Sign(priv, canonical(op, ceremony, now)))}
}

func post(ctx context.Context, c *http.Client, url string, body any) error {
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: %d %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

type reshareStatus struct {
	Status       string `json:"status"`
	KeyUnchanged bool   `json:"key_unchanged"`
	Sealed       bool   `json:"sealed"`
	Address      string `json:"address"`
	Error        string `json:"error"`
	Migration    *struct {
		LegacyVersionsDestroyed int    `json:"legacy_versions_destroyed"`
		Error                   string `json:"error"`
	} `json:"migration"`
}

func migrate(ctx context.Context, o migrateOptions) error {
	for _, k := range []string{"migrate", "reshare"} {
		if o.auths[k].Signature == "" {
			return fmt.Errorf("no %q authorisation", k)
		}
	}
	peers := o.peers()
	fmt.Fprintf(o.out, "==> loading ceremony %s for migration on %d parties\n", o.ceremony, o.parties)
	for i := 1; i <= o.parties; i++ {
		err := post(ctx, o.client, o.partyURL(i)+"/tss/keygen/restore", map[string]any{
			"ceremony_id": o.ceremony, "peers": peers, "migrate_legacy": true,
			"authorization": o.auths["migrate"].Authorization, "authorization_signature": o.auths["migrate"].Signature,
		})
		if err != nil {
			return fmt.Errorf("party %d: loading for migration: %w", i, err)
		}
		fmt.Fprintf(o.out, "    party %d: loaded, blocked from signing until refreshed\n", i)
	}
	rid := ReshareID(o.ceremony)
	fmt.Fprintf(o.out, "==> refreshing inside the enclaves (%s)\n", rid)
	for i := 1; i <= o.parties; i++ {
		err := post(ctx, o.client, o.partyURL(i)+"/tss/reshare/start", map[string]any{
			"reshare_id": rid, "source_ceremony_id": o.ceremony, "peers": peers,
			"authorization": o.auths["reshare"].Authorization, "authorization_signature": o.auths["reshare"].Signature,
		})
		if err != nil {
			return fmt.Errorf("party %d: starting the refresh: %w", i, err)
		}
	}
	deadline := time.Now().Add(o.timeout)
	done := map[int]reshareStatus{}
	for len(done) < o.parties {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out: %d of %d parties finished", len(done), o.parties)
		}
		for i := 1; i <= o.parties; i++ {
			if _, ok := done[i]; ok {
				continue
			}
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, o.partyURL(i)+"/tss/reshare/"+rid+"/status", nil)
			resp, err := o.client.Do(req)
			if err != nil {
				continue
			}
			var st reshareStatus
			_ = json.NewDecoder(resp.Body).Decode(&st)
			resp.Body.Close()
			switch st.Status {
			case "failed":
				return fmt.Errorf("party %d: refresh failed: %s", i, st.Error)
			case "completed":
				done[i] = st
			}
		}
		time.Sleep(o.poll)
	}
	var problems []string
	for i := 1; i <= o.parties; i++ {
		st := done[i]
		switch {
		case !st.KeyUnchanged:
			problems = append(problems, fmt.Sprintf("party %d: the key changed", i))
		case !st.Sealed:
			problems = append(problems, fmt.Sprintf("party %d: the refreshed share was not sealed", i))
		case st.Migration == nil:
			problems = append(problems, fmt.Sprintf("party %d: reported no migration result", i))
		case st.Migration.Error != "":
			problems = append(problems, fmt.Sprintf("party %d: %s", i, st.Migration.Error))
		default:
			fmt.Fprintf(o.out, "    party %d: key unchanged (%s), enclave-sealed, %d clear-text version(s) destroyed\n",
				i, st.Address, st.Migration.LegacyVersionsDestroyed)
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	fmt.Fprintln(o.out, "==> migrated. Old shares of this key still reconstruct it if threshold-many were copied "+
		"before today; purge backups taken before the migration")
	return nil
}

func loadAuths(keyFile, authFile, ceremony string) (map[string]signedAuth, error) {
	switch {
	case keyFile != "" && authFile != "":
		return nil, errors.New("give -authorizer-key-file or -authorizations, not both")
	case authFile != "":
		raw, err := os.ReadFile(authFile)
		if err != nil {
			return nil, err
		}
		var m map[string]signedAuth
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("-authorizations: %w", err)
		}
		return m, nil
	case keyFile != "":
		raw, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, err
		}
		seed, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, errors.New("-authorizer-key-file must hold a 32-byte ed25519 seed in hex")
		}
		priv := ed25519.NewKeyFromSeed(seed)
		return map[string]signedAuth{"migrate": signWith(priv, "migrate", ceremony), "reshare": signWith(priv, "reshare", ReshareID(ceremony))}, nil
	}
	return nil, errors.New("give -authorizer-key-file or -authorizations")
}

const usage = `usage: forge-enclave-admin <command> [flags]

  migrate          move a pre-enclave key into enclave sealing
  inventory        show which image each party runs, and whether the policy accepts it
  kms-policy       render a party's KMS key policy for one or more images
  recovery-keygen  make a break-glass recovery key pair (run offline)
  recover          open a share's recovery envelope (run offline, break-glass only)

Run a command with -h for its flags.`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "migrate":
		mainMigrate(os.Args[2:])
		return
	case "inventory":
		err = mainInventory(os.Args[2:], os.Stdout)
	case "kms-policy":
		err = mainKMSPolicy(os.Args[2:], os.Stdout)
	case "recovery-keygen":
		err = mainRecoveryKeygen(os.Args[2:], os.Stdout)
	case "recover":
		err = mainRecover(os.Args[2:], os.Stdout)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fatal(err)
	}
}

func mainMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	ceremony := fs.String("ceremony", "", "ceremony id of the key to migrate")
	policyFile := fs.String("policy", "", "attestation policy (nitroattest JSON)")
	certFile := fs.String("cert", "", "client certificate the enclaves accept as the orchestrator's")
	keyFile := fs.String("key", "", "its private key")
	domain := fs.String("domain", "internal", "parties are party-N.<domain>")
	port := fs.Int("port", 7000, "party port")
	parties := fs.Int("parties", 3, "number of parties")
	authKey := fs.String("authorizer-key-file", "", "co-signer ed25519 seed (hex), rehearsal only")
	authFile := fs.String("authorizations", "", "pre-signed authorisations (JSON)")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait for the refresh")
	_ = fs.Parse(args)
	if *ceremony == "" || *policyFile == "" || *certFile == "" || *keyFile == "" {
		fs.Usage()
		os.Exit(2)
	}
	policy, err := nitroattest.LoadPolicy(*policyFile)
	if err != nil {
		fatal(err)
	}
	cert, err := loadClientCert(*certFile, *keyFile)
	if err != nil {
		fatal(err)
	}
	auths, err := loadAuths(*authKey, *authFile, *ceremony)
	if err != nil {
		fatal(err)
	}
	v := &nitroattest.Verifier{Policy: policy}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		TLSClientConfig: v.ClientTLSConfig(func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return cert, nil }),
	}}
	err = migrate(context.Background(), migrateOptions{
		ceremony: *ceremony, parties: *parties, domain: *domain, port: *port, scheme: "https",
		client: client, auths: auths, timeout: *timeout, poll: 2 * time.Second, out: os.Stdout,
	})
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "forge-enclave-admin:", err)
	os.Exit(1)
}

func loadClientCert(certFile, keyFile string) (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	if cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
		return nil, err
	}
	return &cert, nil
}
