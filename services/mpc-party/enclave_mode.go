package main

// Enclave mode: the same party, inside an AWS Nitro Enclave.
//
// Off unless ENCLAVE_MODE=1, which only an enclave image sets. With it off,
// nothing in this file runs and the party behaves exactly as before.
//
// What changes in enclave mode:
//
//   - The party listens on vsock, not TCP. The host relays TCP to it byte
//     for byte (cmd/mpc-party-host), so TLS ends inside the enclave and the
//     host sees only ciphertext. tss-lib sends each party its share of the
//     secret in the clear inside key-generation messages, so this is the
//     property the whole design rests on.
//   - Its TLS key is generated in the enclave, in a self-signed certificate
//     that carries an attestation document. Peers accept only an enclave
//     running the same image (PCR0 equal to their own), in debug mode never,
//     with the IAM role pinned for the party it claims (PCR3). The
//     orchestrator may still connect with a PKI certificate, but a PKI
//     certificate naming a party is refused.
//   - Every outbound connection goes through a fixed egress map; anything
//     not in it is refused.
//   - Shares are sealed with a KMS data key that KMS releases only to an
//     attested enclave of this image (enclave/kms.go), and Vault stores only
//     that envelope. The file store, unauthenticated peers and the fake Vault
//     are refused.
//   - A ceremony's peer list must give each party an address that names it
//     (party-N.*), so the attested-name check on every connection also checks
//     the message reaches the party it is for.
//
// Settings come from two places. Those that protect a share are baked into
// the image as environment variables, so they are covered by the image
// measurement (an enclave has no other environment). The rest come from the
// host at start-up over the control channel, and the enclave refuses any key
// not in enclave.RuntimeKeys.
//
// Baked (measured):
//
//	ENCLAVE_MODE=1
//	ENCLAVE_VSOCK_PORT            port the party listens on (default 5000)
//	ENCLAVE_CONTROL_PORT          host control port (default 9000)
//	ENCLAVE_KMS_REGION            e.g. af-south-1
//	ENCLAVE_KMS_KEYS              1=arn:aws:kms:...,2=...,3=...
//	ENCLAVE_PARTY_PCR3            1=<sha384 hex>,2=...,3=...
//	ENCLAVE_REQUIRE_PARTY_PIN     default 1; 0 only for the hardware spike
//	ENCLAVE_EXTRA_IMAGE_PCR0      optional, comma-separated, during a rollout
//	ENCLAVE_ORCHESTRATOR_CA_PEM   optional PEM; without it only parties connect
//	CEREMONY_AUTHORIZER_PUBKEY    required in enclave mode
//
// From the host: see enclave.RuntimeKeys.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"

	"openfireblocks.com/services/mpc-party/enclave"
	"openfireblocks.com/services/mpc-party/nitroattest"
)

func enclaveModeRequested(getenv func(string) string) bool {
	return strings.TrimSpace(getenv("ENCLAVE_MODE")) == "1"
}

// shareSealer is what enclave mode seals shares with. enclave.Sealer in
// production; registered per party so in-process tests can run several.
type shareSealer interface {
	Seal(ctx context.Context, plaintext []byte, ec map[string]string) ([]byte, error)
	Open(ctx context.Context, sealed []byte, ec map[string]string) ([]byte, error)
}

var (
	enclaveMu      sync.RWMutex
	enclaveSealers = map[int]shareSealer{}
	// enclaveVault is how Vault is reached in enclave mode: through the
	// egress map, with a CA the host supplied (a hostile Vault learns only
	// envelopes, so the CA need not be measured).
	enclaveVault *enclaveVaultConfig
)

type enclaveVaultConfig struct {
	dial   func(ctx context.Context, network, addr string) (net.Conn, error)
	caPEM  []byte
	active bool
}

func sealerFor(partyID int) (shareSealer, bool) {
	enclaveMu.RLock()
	defer enclaveMu.RUnlock()
	s, ok := enclaveSealers[partyID]
	return s, ok
}

func enclaveActive() bool {
	enclaveMu.RLock()
	defer enclaveMu.RUnlock()
	return len(enclaveSealers) > 0
}

func registerEnclaveSealer(partyID int, s shareSealer) {
	enclaveMu.Lock()
	defer enclaveMu.Unlock()
	enclaveSealers[partyID] = s
}

// checkEnclavePeers refuses a peer list whose addresses do not name their
// parties. Only enforced in enclave mode.
func checkEnclavePeers(peers map[int]string) error {
	if !enclaveActive() {
		return nil
	}
	for id, raw := range peers {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" {
			return fmt.Errorf("enclave mode: peer %d address %q must be an https URL", id, raw)
		}
		named, ok := nitroattest.PartyFromName(u.Hostname())
		if !ok || named != id {
			return fmt.Errorf("enclave mode: peer %d address %q does not name party %d (expected https://party-%d.<domain>); "+
				"the attested name on each connection is what proves a message reaches the party it is for", id, raw, id, id)
		}
	}
	return nil
}

// enclaveDeps are the parts of enclave start-up that touch hardware or AWS,
// so tests can replace them.
type enclaveDeps struct {
	attester enclave.Attester
	roots    *x509.CertPool // nil: the AWS Nitro root
	dialHost func(ctx context.Context, port uint32) (net.Conn, error)
	listen   func(port uint32) (net.Listener, error)
	newKMS   func(region string, client *http.Client, creds aws.CredentialsProvider) enclave.KMSAPI
	setenv   func(key, value string) error
	getenv   func(string) string
}

func productionEnclaveDeps() (enclaveDeps, error) {
	a, err := enclave.NewAttester()
	if err != nil {
		return enclaveDeps{}, err
	}
	return enclaveDeps{
		attester: a,
		dialHost: enclave.DialHost,
		listen:   enclave.ListenVsock,
		newKMS: func(region string, client *http.Client, creds aws.CredentialsProvider) enclave.KMSAPI {
			return kms.New(kms.Options{Region: region, HTTPClient: client, Credentials: creds})
		},
		setenv: os.Setenv,
		getenv: os.Getenv,
	}, nil
}

// enclaveRuntime is what main needs from enclave start-up.
type enclaveRuntime struct {
	partyID        int
	listener       net.Listener
	serverTLS      *tls.Config
	relayTransport *http.Transport
	policy         nitroattest.Policy
	imagePCR0      string
	rolePCR3       string
	dialCtrl       func(context.Context) (net.Conn, error)
	sealer         shareSealer
}

// measuredKeys must come from the image. If the host sends one, start-up
// fails (enclave.ParseRuntimeConfig); if it is set in the process
// environment it came from the image, since an enclave has no other.
var refusedInEnclave = []string{
	"TSS_ALLOW_UNAUTHENTICATED_PEERS", "SHARE_STORE_DIR", "SHARE_STORE_KEY_FILE",
	"OFB_FAKE_VAULT", envMTLSCertFile, envMTLSKeyFile, envMTLSCAFile, envHealthPort,
}

func parseIntMap(name, spec string) (map[int]string, error) {
	out := map[int]string{}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		k, v, ok := strings.Cut(entry, "=")
		n, err := strconv.Atoi(strings.TrimSpace(k))
		if !ok || err != nil || n <= 0 || strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("%s: %q is not party=value", name, entry)
		}
		out[n] = strings.TrimSpace(v)
	}
	return out, nil
}

func portFrom(getenv func(string) string, key string, def uint32) (uint32, error) {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return def, nil
	}
	p, err := strconv.ParseUint(v, 10, 32)
	if err != nil || p == 0 {
		return 0, fmt.Errorf("%s=%q is not a port", key, v)
	}
	return uint32(p), nil
}

// startEnclave runs every enclave-mode check and builds the runtime. Any
// failure is fatal to the process: there is no non-enclave fallback.
func startEnclave(ctx context.Context, d enclaveDeps) (*enclaveRuntime, error) {
	getenv := d.getenv
	ctrlPort, err := portFrom(getenv, "ENCLAVE_CONTROL_PORT", 9000)
	if err != nil {
		return nil, err
	}
	dialCtrl := func(ctx context.Context) (net.Conn, error) { return d.dialHost(ctx, ctrlPort) }

	// 1. Runtime settings from the host, restricted to RuntimeKeys.
	cfg, err := enclave.FetchRuntimeConfig(ctx, dialCtrl)
	if err != nil {
		return nil, err
	}
	for k, v := range cfg {
		if err := d.setenv(k, v); err != nil {
			return nil, err
		}
	}

	// 2. Settings that have no place in an enclave.
	for _, k := range refusedInEnclave {
		if getenv(k) != "" {
			return nil, fmt.Errorf("enclave mode: %s must not be set", k)
		}
	}
	if strings.TrimSpace(getenv("CEREMONY_AUTHORIZER_PUBKEY")) == "" {
		return nil, errors.New("enclave mode: CEREMONY_AUTHORIZER_PUBKEY must be baked into the image; " +
			"without a co-signer anyone who can reach the party can start a ceremony")
	}
	partyID, err := strconv.Atoi(strings.TrimSpace(getenv("PARTY_ID")))
	if err != nil || partyID <= 0 {
		return nil, errors.New("enclave mode: the host sent no valid PARTY_ID")
	}

	// 3. The policy: peers must run this image.
	selfDoc, err := d.attester.Attest(nitroattest.Claims{Version: nitroattest.ClaimsVersion, Purpose: nitroattest.PurposePartyTLS, PartyID: partyID}.Marshal(), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("enclave mode: attesting to self: %w", err)
	}
	own, err := nitroattest.ReadMeasurements(selfDoc, d.roots)
	if err != nil {
		return nil, err
	}
	policy := nitroattest.Policy{
		ImagePCR0:       []string{fmt.Sprintf("%x", own[0])},
		RequirePartyPin: strings.TrimSpace(getenv("ENCLAVE_REQUIRE_PARTY_PIN")) != "0",
		Parties:         map[string]nitroattest.PartyPolicy{},
	}
	for _, extra := range strings.Split(getenv("ENCLAVE_EXTRA_IMAGE_PCR0"), ",") {
		if extra = strings.TrimSpace(extra); extra != "" {
			policy.ImagePCR0 = append(policy.ImagePCR0, extra)
		}
	}
	pins, err := parseIntMap("ENCLAVE_PARTY_PCR3", getenv("ENCLAVE_PARTY_PCR3"))
	if err != nil {
		return nil, err
	}
	for id, pcr3 := range pins {
		policy.Parties[strconv.Itoa(id)] = nitroattest.PartyPolicy{PCR3: pcr3}
	}
	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("enclave mode: %w (is this enclave running in debug mode?)", err)
	}
	if pin, ok := pins[partyID]; ok && !strings.EqualFold(pin, fmt.Sprintf("%x", own[3])) {
		return nil, fmt.Errorf("enclave mode: this enclave claims party %d, but its parent instance's IAM role (PCR3 %x) "+
			"is not the role pinned for that party; refusing to start", partyID, own[3])
	} else if !ok && policy.RequirePartyPin {
		return nil, fmt.Errorf("enclave mode: party %d has no IAM-role pin in ENCLAVE_PARTY_PCR3 "+
			"(this instance's role measures PCR3 %x; pin that value if this is the right role)", partyID, own[3])
	}
	verifier := &nitroattest.Verifier{Policy: policy, Roots: d.roots}

	// 4. TLS identity and configs.
	identity, err := enclave.NewIdentity(partyID, d.attester, enclave.IdentityOptions{})
	if err != nil {
		return nil, err
	}
	var orchCAs *x509.CertPool
	pem := getenv("ENCLAVE_ORCHESTRATOR_CA_PEM")
	if f := strings.TrimSpace(getenv("ENCLAVE_ORCHESTRATOR_CA_FILE")); f != "" && strings.TrimSpace(pem) == "" {
		// A file in the image: measured like everything else in it.
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("enclave mode: ENCLAVE_ORCHESTRATOR_CA_FILE: %w", err)
		}
		pem = string(raw)
	}
	if strings.TrimSpace(pem) != "" {
		orchCAs = x509.NewCertPool()
		if !orchCAs.AppendCertsFromPEM([]byte(pem)) {
			return nil, errors.New("enclave mode: ENCLAVE_ORCHESTRATOR_CA_PEM holds no certificate")
		}
	}
	serverTLS, err := enclave.ServerTLSConfig(enclave.ServerOptions{Identity: identity, Verifier: verifier, OrchestratorCAs: orchCAs})
	if err != nil {
		return nil, err
	}

	// 5. Egress.
	routes, err := enclave.ParseEgress(getenv("ENCLAVE_EGRESS"))
	if err != nil {
		return nil, err
	}
	egress := enclave.NewEgress(routes, d.dialHost)
	relay := &http.Transport{
		DialContext:         egress.DialContext,
		TLSClientConfig:     enclave.PeerClientTLSConfig(identity, verifier),
		ForceAttemptHTTP2:   false,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}

	// 6. KMS sealing.
	region := strings.TrimSpace(getenv("ENCLAVE_KMS_REGION"))
	keys, err := parseIntMap("ENCLAVE_KMS_KEYS", getenv("ENCLAVE_KMS_KEYS"))
	if err != nil {
		return nil, err
	}
	keyID, ok := keys[partyID]
	if region == "" || !ok {
		return nil, fmt.Errorf("enclave mode: ENCLAVE_KMS_REGION and a key for party %d in ENCLAVE_KMS_KEYS must be baked into the image", partyID)
	}
	kmsClient := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		DialContext: egress.DialContext,
		// System roots from the image. KMS's own certificate is checked
		// normally; this is ordinary TLS to AWS, relayed by the host.
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}}
	creds := aws.NewCredentialsCache(enclave.HostCredentials{Dial: dialCtrl})
	sealer := &enclave.Sealer{KMS: d.newKMS(region, kmsClient, creds), KeyID: keyID, Attester: d.attester, PartyID: partyID}
	registerEnclaveSealer(partyID, sealer)

	enclaveMu.Lock()
	enclaveVault = &enclaveVaultConfig{dial: egress.DialContext, caPEM: []byte(getenv("VAULT_CACERT_PEM")), active: true}
	enclaveMu.Unlock()

	// 7. Listener.
	port, err := portFrom(getenv, "ENCLAVE_VSOCK_PORT", 5000)
	if err != nil {
		return nil, err
	}
	ln, err := d.listen(port)
	if err != nil {
		return nil, fmt.Errorf("enclave mode: listening on vsock port %d: %w", port, err)
	}
	log.Printf("enclave mode: party %d, image %x, %d pinned parties, KMS key %s, listening on vsock %d",
		partyID, own[0], len(pins), keyID, port)
	rt := &enclaveRuntime{partyID: partyID, listener: ln, serverTLS: serverTLS, relayTransport: relay, policy: policy,
		imagePCR0: fmt.Sprintf("%x", own[0]), rolePCR3: fmt.Sprintf("%x", own[3]), dialCtrl: dialCtrl, sealer: sealer}
	if strings.TrimSpace(getenv("ENCLAVE_SELFTEST")) == "1" {
		if err := rt.selfTest(ctx); err != nil {
			ln.Close()
			return nil, err
		}
	}
	return rt, nil
}

// selfTest seals and opens a random secret through KMS and reports the
// outcome to the host: the hardware spike's proof that KMS releases a data
// key to this image. Baked in with ENCLAVE_SELFTEST=1; off in production.
func (rt *enclaveRuntime) selfTest(ctx context.Context) error {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	ec := map[string]string{"forge:purpose": "selftest", "forge:party": fmt.Sprintf("party-%d", rt.partyID)}
	sealed, err := rt.sealer.Seal(ctx, secret, ec)
	if err == nil {
		var opened []byte
		if opened, err = rt.sealer.Open(ctx, sealed, ec); err == nil && !bytes.Equal(opened, secret) {
			err = errors.New("opened a different secret")
		}
	}
	rep := enclave.Report{Event: "selftest", OK: err == nil, Party: rt.partyID, ImagePCR: rt.imagePCR0, RolePCR: rt.rolePCR3}
	if err != nil {
		rep.Detail = err.Error()
	} else {
		var env enclave.Envelope
		_ = json.Unmarshal(sealed, &env)
		sum := sha256.Sum256(secret)
		rep.Detail = fmt.Sprintf("KMS sealed and opened a 32-byte secret, sha256 %x", sum)
		rep.EncryptedKey = env.EncryptedKey
	}
	_ = enclave.SendReport(ctx, rt.dialCtrl, rep)
	if err != nil {
		return fmt.Errorf("enclave self-test: %w", err)
	}
	return nil
}

// reportEnclaveStart tells the host whether start-up worked. A production
// enclave has no console; without this a refusal is silent.
func reportEnclaveStart(ctx context.Context, d enclaveDeps, rt *enclaveRuntime, startErr error) {
	port, err := portFrom(d.getenv, "ENCLAVE_CONTROL_PORT", 9000)
	if err != nil {
		return
	}
	dial := func(ctx context.Context) (net.Conn, error) { return d.dialHost(ctx, port) }
	r := enclave.Report{Event: "started", OK: startErr == nil}
	if startErr != nil {
		r.Event, r.Detail = "failed", startErr.Error()
	} else {
		r.Party, r.ImagePCR, r.RolePCR = rt.partyID, rt.imagePCR0, rt.rolePCR3
	}
	_ = enclave.SendReport(ctx, dial, r)
}
