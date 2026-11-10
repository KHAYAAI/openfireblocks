package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"openfireblocks.com/services/mpc-party/enclave"
	"openfireblocks.com/services/mpc-party/nitroattest"
)

// ---- inventory ----

type partyImage struct {
	Party    int    `json:"party"`
	Address  string `json:"address"`
	PCR0     string `json:"image_pcr0,omitempty"`
	PCR3     string `json:"role_pcr3,omitempty"`
	Attested int    `json:"attested_party,omitempty"`
	Accepted bool   `json:"accepted"`
	Problem  string `json:"problem,omitempty"`
}

// inspectParty completes a TLS handshake with a party and reads the image
// its certificate attests, whether or not the policy accepts it: during an
// upgrade the point is to see both. The handshake itself proves the party
// holds the certificate's key; the verdict comes from the policy.
func inspectParty(ctx context.Context, dial func(ctx context.Context, addr string) (net.Conn, error), addr string, party int,
	clientCert *tls.Certificate, v *nitroattest.Verifier, roots *x509.CertPool) partyImage {
	out := partyImage{Party: party, Address: addr}
	raw, err := dial(ctx, addr)
	if err != nil {
		out.Problem = err.Error()
		return out
	}
	defer raw.Close()
	conn := tls.Client(raw, &tls.Config{
		ServerName:         fmt.Sprintf("party-%d", party),
		InsecureSkipVerify: true, //nolint:gosec // inventory only; the verdict below is the policy's
		MinVersion:         tls.VersionTLS13,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			if clientCert == nil {
				return &tls.Certificate{}, nil
			}
			return clientCert, nil
		},
	})
	if err := conn.HandshakeContext(ctx); err != nil {
		out.Problem = "handshake: " + err.Error()
		return out
	}
	leaf := conn.ConnectionState().PeerCertificates[0]
	doc, ok := nitroattest.AttestationDocument(leaf)
	if !ok {
		out.Problem = "the party presented no attestation document (not in an enclave)"
		return out
	}
	if pcrs, err := nitroattest.ReadMeasurements(doc, roots); err == nil {
		out.PCR0, out.PCR3 = hex.EncodeToString(pcrs[0]), hex.EncodeToString(pcrs[3])
	}
	att, err := v.VerifyCertificate(leaf)
	if err != nil {
		out.Problem = err.Error()
		return out
	}
	out.Attested = att.Claims.PartyID
	if att.Claims.PartyID != party {
		out.Problem = fmt.Sprintf("dialled party %d, reached an enclave attesting party %d", party, att.Claims.PartyID)
		return out
	}
	out.Accepted = true
	return out
}

func inventory(ctx context.Context, w io.Writer, images []partyImage, allowMixed bool) error {
	distinct := map[string]bool{}
	var problems []string
	for _, im := range images {
		state := "accepted"
		if !im.Accepted {
			state = "REFUSED: " + im.Problem
			problems = append(problems, fmt.Sprintf("party %d: %s", im.Party, im.Problem))
		}
		fmt.Fprintf(w, "party %d  %-28s image %s  role %s  %s\n", im.Party, im.Address, short(im.PCR0), short(im.PCR3), state)
		if im.PCR0 != "" {
			distinct[im.PCR0] = true
		}
	}
	if len(distinct) > 1 {
		fmt.Fprintf(w, "%d different images are running\n", len(distinct))
		if !allowMixed {
			problems = append(problems, "the parties run different images; peers that do not list each other's image refuse each other")
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func short(h string) string {
	if len(h) > 16 {
		return h[:16] + "…"
	}
	if h == "" {
		return "-"
	}
	return h
}

func mainInventory(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("inventory", flag.ContinueOnError)
	policyFile := fs.String("policy", "", "attestation policy (nitroattest JSON)")
	certFile := fs.String("cert", "", "client certificate the enclaves accept as the orchestrator's")
	keyFile := fs.String("key", "", "its private key")
	domain := fs.String("domain", "internal", "parties are party-N.<domain>")
	port := fs.Int("port", 7000, "party port")
	parties := fs.Int("parties", 3, "number of parties")
	allowMixed := fs.Bool("allow-mixed", false, "do not fail when parties run different images (mid-upgrade)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *policyFile == "" || *certFile == "" || *keyFile == "" {
		return errors.New("inventory needs -policy, -cert and -key")
	}
	policy, err := nitroattest.LoadPolicy(*policyFile)
	if err != nil {
		return err
	}
	cert, err := loadClientCert(*certFile, *keyFile)
	if err != nil {
		return err
	}
	v := &nitroattest.Verifier{Policy: policy}
	dial := func(ctx context.Context, addr string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
	}
	var images []partyImage
	for i := 1; i <= *parties; i++ {
		addr := fmt.Sprintf("party-%d.%s:%d", i, *domain, *port)
		images = append(images, inspectParty(context.Background(), dial, addr, i, cert, v, nil))
	}
	if *asJSON {
		_ = json.NewEncoder(w).Encode(images)
	}
	return inventory(context.Background(), w, images, *allowMixed)
}

// ---- kms-policy ----

// renderKMSPolicy fills infrastructure/nitro/kms-key-policy.json for one
// party and a set of images. During an upgrade it lists both the old and the
// new image; afterwards, the new one only.
func renderKMSPolicy(template []byte, account, roleARN string, party int, pcr0s []string) ([]byte, error) {
	if len(pcr0s) == 0 {
		return nil, errors.New("at least one image (PCR0) is required")
	}
	for _, p := range pcr0s {
		b, err := hex.DecodeString(p)
		if err != nil || len(b) != 48 {
			return nil, fmt.Errorf("%q is not a 48-byte PCR0 in hex", p)
		}
		zero := true
		for _, x := range b {
			zero = zero && x == 0
		}
		if zero {
			return nil, errors.New("an all-zero PCR0 is a debug-mode enclave; never allow it")
		}
	}
	if !strings.HasPrefix(roleARN, "arn:aws:iam::") || account == "" || party <= 0 {
		return nil, errors.New("account, role ARN (arn:aws:iam::...) and party are required")
	}
	var doc any
	if err := json.Unmarshal(template, &doc); err != nil {
		return nil, fmt.Errorf("template: %w", err)
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				t[k] = walk(x)
			}
			return t
		case []any:
			if len(t) == 1 && t[0] == "PCR0_HEX" {
				out := make([]any, len(pcr0s))
				for i, p := range pcr0s {
					out[i] = strings.ToLower(p)
				}
				return out
			}
			for i, x := range t {
				t[i] = walk(x)
			}
			return t
		case string:
			r := strings.NewReplacer("ACCOUNT_ID", account, "PARTY_ROLE_ARN", roleARN, "PARTY_NUMBER", fmt.Sprint(party))
			return r.Replace(t)
		}
		return v
	}
	out, err := json.MarshalIndent(walk(doc), "", "  ")
	if err != nil {
		return nil, err
	}
	if strings.Contains(string(out), "PCR0_HEX") {
		return nil, errors.New("template has a PCR0_HEX placeholder outside a one-element list")
	}
	return out, nil
}

func mainKMSPolicy(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("kms-policy", flag.ContinueOnError)
	tmpl := fs.String("template", "infrastructure/nitro/kms-key-policy.json", "policy template")
	account := fs.String("account", "", "AWS account id holding the key")
	role := fs.String("role", "", "IAM role ARN of the party's parent instance")
	party := fs.Int("party", 0, "party number")
	pcr0 := fs.String("pcr0", "", "comma-separated PCR0 values to allow (old,new during an upgrade)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := os.ReadFile(*tmpl)
	if err != nil {
		return err
	}
	var list []string
	for _, p := range strings.Split(*pcr0, ",") {
		if p = strings.TrimSpace(p); p != "" {
			list = append(list, p)
		}
	}
	out, err := renderKMSPolicy(raw, *account, *role, *party, list)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(out))
	return err
}

// ---- recovery ----

func mainRecoveryKeygen(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("recovery-keygen", flag.ContinueOnError)
	out := fs.String("out", "", "file to write the private key to (created 0600; must not exist)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("recovery-keygen needs -out")
	}
	priv, pub, err := enclave.RecoveryKeygen()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(priv + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(w, "private key written to %s. Keep it offline, with this party's recovery officers only.\n", *out)
	fmt.Fprintf(w, "public key (for ENCLAVE_RECOVERY_PUBKEYS):\n%s\n", pub)
	return nil
}

// legacyEntry is the Vault entry format of a share sealed before enclave
// mode: what KEY-RECOVERY.md Route A restores into a party, and Route B reads.
type legacyEntry struct {
	PartyID         int    `json:"party_id"`
	CeremonyID      string `json:"ceremony_id"`
	Curve           string `json:"curve,omitempty"`
	SaveData        string `json:"save_data"`
	CeremonyContext string `json:"ceremony_context,omitempty"`
}

// recoverEntry opens the recovery envelope in a Vault entry. It accepts
// either `vault kv get -format=json` output or the entry's data map.
func recoverEntry(privHex string, vaultJSON []byte, party int, ceremony string) (*legacyEntry, error) {
	var outer struct {
		Data struct {
			Data map[string]any `json:"data"`
		} `json:"data"`
	}
	data := map[string]any{}
	if err := json.Unmarshal(vaultJSON, &outer); err == nil && outer.Data.Data != nil {
		data = outer.Data.Data
	} else if err := json.Unmarshal(vaultJSON, &data); err != nil {
		return nil, errors.New("input is neither `vault kv get -format=json` output nor an entry")
	}
	env, _ := data["recovery_envelope"].(string)
	if env == "" {
		return nil, errors.New("this entry has no recovery_envelope: it was sealed without a recovery key, and only the enclave can open it")
	}
	plain, err := enclave.OpenRecovery(privHex, []byte(env), party, ceremony)
	if err != nil {
		return nil, err
	}
	var p struct {
		PartyID         int    `json:"party_id"`
		CeremonyID      string `json:"ceremony_id"`
		SaveData        string `json:"save_data"`
		CeremonyContext string `json:"ceremony_context"`
	}
	if err := json.Unmarshal(plain, &p); err != nil || p.PartyID != party || p.CeremonyID != ceremony || p.SaveData == "" {
		return nil, errors.New("the recovery envelope opened but holds no share for this party and ceremony")
	}
	curve, _ := data["curve"].(string)
	return &legacyEntry{PartyID: p.PartyID, CeremonyID: p.CeremonyID, Curve: curve, SaveData: p.SaveData, CeremonyContext: p.CeremonyContext}, nil
}

func mainRecover(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("recover", flag.ContinueOnError)
	keyFile := fs.String("key", "", "this party's recovery private key file")
	in := fs.String("in", "", "the share's Vault entry (`vault kv get -format=json` output)")
	party := fs.Int("party", 0, "party number")
	ceremony := fs.String("ceremony", "", "ceremony id")
	out := fs.String("out", "", "file to write the recovered entry to (created 0600; must not exist)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyFile == "" || *in == "" || *party <= 0 || *ceremony == "" || *out == "" {
		return errors.New("recover needs -key, -in, -party, -ceremony and -out")
	}
	priv, err := os.ReadFile(*keyFile)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	entry, err := recoverEntry(string(priv), raw, *party, *ceremony)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(entry, "", "  ")
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(w, "party %d's share of %s written to %s, in the pre-enclave Vault format (KEY-RECOVERY.md step 3).\n"+
		"This is a key share in the clear: keep it on this machine, and destroy the machine afterwards.\n", *party, *ceremony, *out)
	return nil
}
