package enclave

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"openfireblocks.com/services/mpc-party/nitroattest"
	"openfireblocks.com/services/mpc-party/nitroattest/nitroattesttest"
)

// ---- frames ----

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	for _, p := range [][]byte{nil, []byte("x"), bytes.Repeat([]byte{7}, 70000)} {
		if err := WriteFrame(&buf, FramePing, p); err != nil {
			t.Fatal(err)
		}
		typ, got, err := ReadFrame(&buf)
		if err != nil || typ != FramePing || !bytes.Equal(got, p) {
			t.Fatalf("round trip: typ=%d err=%v len=%d", typ, err, len(got))
		}
	}
}

func TestFrameRefusals(t *testing.T) {
	var buf bytes.Buffer
	_ = WriteFrame(&buf, FramePing, []byte("hello"))
	full := buf.Bytes()
	for cut := 1; cut < len(full); cut++ {
		if _, _, err := ReadFrame(bytes.NewReader(full[:cut])); err == nil {
			t.Fatalf("truncated frame (%d of %d bytes) was accepted", cut, len(full))
		}
	}
	huge := make([]byte, 5)
	binary.BigEndian.PutUint32(huge, MaxFrame+2)
	huge[4] = FramePing
	if _, _, err := ReadFrame(bytes.NewReader(huge)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversize frame: %v", err)
	}
	if err := WriteFrame(io.Discard, FramePing, make([]byte, MaxFrame+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversize write: %v", err)
	}
	unknown := []byte{0, 0, 0, 1, 99}
	if _, _, err := ReadFrame(bytes.NewReader(unknown)); err == nil || !strings.Contains(err.Error(), "unknown frame type") {
		t.Fatalf("unknown type: %v", err)
	}
	if err := WriteFrame(io.Discard, 99, nil); err == nil {
		t.Fatal("writing an unknown type succeeded")
	}
	if _, _, err := ReadFrame(bytes.NewReader([]byte{0, 0, 0, 0})); err == nil {
		t.Fatal("empty frame accepted")
	}
}

// ---- egress ----

func TestParseEgress(t *testing.T) {
	r, err := ParseEgress(" party-2.internal:7000=7102, Vault.internal:8200=7200 ,")
	if err != nil {
		t.Fatal(err)
	}
	if r["party-2.internal:7000"] != 7102 || r["vault.internal:8200"] != 7200 || len(r) != 2 {
		t.Fatalf("routes: %v", r)
	}
	for _, bad := range []string{"", "party-2.internal=7102", "party-2.internal:7000", "party-2.internal:7000=x", "a:1=0", "a:1=5,a:1=6", ":1=5"} {
		if _, err := ParseEgress(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestEgressRefusesUnlistedDestinations(t *testing.T) {
	var dialled []uint32
	e := NewEgress(map[string]uint32{"vault.internal:8200": 7200}, func(_ context.Context, port uint32) (net.Conn, error) {
		dialled = append(dialled, port)
		a, b := net.Pipe()
		b.Close()
		return a, nil
	})
	if c, err := e.DialContext(context.Background(), "tcp", "VAULT.internal:8200"); err != nil {
		t.Fatal(err)
	} else {
		c.Close()
	}
	for _, addr := range []string{"evil.example:443", "vault.internal:8201", "169.254.169.254:80"} {
		if _, err := e.DialContext(context.Background(), "tcp", addr); err == nil || !strings.Contains(err.Error(), "not an allowed destination") {
			t.Errorf("%s: %v", addr, err)
		}
	}
	if _, err := e.DialContext(context.Background(), "udp", "vault.internal:8200"); err == nil {
		t.Error("udp relayed")
	}
	if len(dialled) != 1 || dialled[0] != 7200 {
		t.Fatalf("dialled %v", dialled)
	}
}

// ---- relay ----

func TestServeRelaysBothWays(t *testing.T) {
	echo, _ := net.Listen("tcp", "127.0.0.1:0")
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); c.Close() }()
		}
	}()
	front, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, front, func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", echo.Addr().String())
	}, t.Logf)

	c, err := net.Dial("tcp", front.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	msg := bytes.Repeat([]byte("forge"), 10000)
	go func() { _, _ = c.Write(msg); _ = c.(*net.TCPConn).CloseWrite() }()
	got, err := io.ReadAll(c)
	if err != nil || !bytes.Equal(got, msg) {
		t.Fatalf("relay echoed %d bytes, err %v", len(got), err)
	}
}

// ---- identity and TLS ----

type fixture struct {
	nsm      *nitroattesttest.FakeNSM
	verifier *nitroattest.Verifier
}

func newFixture() *fixture {
	nsm := nitroattesttest.New()
	return &fixture{nsm: nsm, verifier: &nitroattest.Verifier{
		Policy: nitroattest.Policy{ImagePCR0: []string{nitroattesttest.Hex("image")}},
		Roots:  nsm.Roots(),
	}}
}

func (f *fixture) identity(t *testing.T, party int) *Identity {
	t.Helper()
	id, err := NewIdentity(party, f.nsm, IdentityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// serve starts an HTTPS server with the enclave server config, reporting
// the authenticated peer.
func serve(t *testing.T, cfg *tls.Config) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.TLS.PeerCertificates[0].Subject.CommonName)
	}))
	srv.TLS = cfg
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

func call(addr, serverName string, cfg *tls.Config) (string, error) {
	tr := &http.Transport{
		TLSClientConfig: cfg,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		},
	}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get("https://" + serverName + "/")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), nil
}

func TestPartiesAuthenticateEachOtherByAttestation(t *testing.T) {
	f := newFixture()
	server, err := ServerTLSConfig(ServerOptions{Identity: f.identity(t, 1), Verifier: f.verifier})
	if err != nil {
		t.Fatal(err)
	}
	addr := serve(t, server)
	got, err := call(addr, "party-1.internal", PeerClientTLSConfig(f.identity(t, 2), f.verifier))
	if err != nil {
		t.Fatalf("attested peer refused: %v", err)
	}
	if got != "party-2.internal" {
		t.Fatalf("server saw %q", got)
	}
}

func TestClientRefusesServerThatIsNotTheDialledParty(t *testing.T) {
	f := newFixture()
	server, _ := ServerTLSConfig(ServerOptions{Identity: f.identity(t, 3), Verifier: f.verifier})
	addr := serve(t, server)
	// The host routed "party-1" traffic to party 3's enclave.
	if _, err := call(addr, "party-1.internal", PeerClientTLSConfig(f.identity(t, 2), f.verifier)); err == nil || !strings.Contains(err.Error(), "dialled party 1") {
		t.Fatalf("expected a refusal naming the dialled party, got %v", err)
	}
}

func TestClientRefusesAnUnmeasuredServer(t *testing.T) {
	f := newFixture()
	// Documents that chain to the trusted root but measure another image.
	id, _ := NewIdentity(1, &pcrOverride{f.nsm, nitroattesttest.Digest("rebuilt image")}, IdentityOptions{})
	server, _ := ServerTLSConfig(ServerOptions{Identity: id, Verifier: f.verifier})
	addr := serve(t, server)
	if _, err := call(addr, "party-1.internal", PeerClientTLSConfig(f.identity(t, 2), f.verifier)); err == nil || !strings.Contains(err.Error(), "PCR0") {
		t.Fatalf("expected a PCR0 refusal, got %v", err)
	}
}

// pcrOverride attests with a different PCR0.
type pcrOverride struct {
	nsm  *nitroattesttest.FakeNSM
	pcr0 []byte
}

func (p *pcrOverride) Attest(u, n, k []byte) ([]byte, error) {
	old := p.nsm.PCRs[0]
	p.nsm.PCRs[0] = p.pcr0
	defer func() { p.nsm.PCRs[0] = old }()
	return p.nsm.Attest(u, n, k)
}

func TestClientRefusesAPlainPKIServer(t *testing.T) {
	f := newFixture()
	ca, caKey := testCA(t)
	cfg := &tls.Config{Certificates: []tls.Certificate{leaf(t, ca, caKey, "party-1.internal")}}
	addr := serve(t, cfg)
	if _, err := call(addr, "party-1.internal", PeerClientTLSConfig(f.identity(t, 2), f.verifier)); err == nil || !strings.Contains(err.Error(), "no attestation document") {
		t.Fatalf("expected refusal of an unattested server, got %v", err)
	}
}

func TestServerClientPolicy(t *testing.T) {
	f := newFixture()
	ca, caKey := testCA(t)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	server, _ := ServerTLSConfig(ServerOptions{Identity: f.identity(t, 1), Verifier: f.verifier, OrchestratorCAs: pool})
	addr := serve(t, server)
	attested := f.verifier.ClientTLSConfig

	// The orchestrator, with a PKI certificate, is accepted.
	orch := leaf(t, ca, caKey, "temporal-worker.internal")
	got, err := call(addr, "party-1.internal", attested(func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &orch, nil }))
	if err != nil || got != "temporal-worker.internal" {
		t.Fatalf("orchestrator: %q %v", got, err)
	}

	// A PKI certificate that names a party is refused: parties must attest.
	party := leaf(t, ca, caKey, "party-2.internal")
	if _, err := call(addr, "party-1.internal", attested(func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &party, nil })); err == nil {
		t.Fatal("a PKI party certificate was accepted in enclave mode")
	}

	// Another PKI name is refused.
	other := leaf(t, ca, caKey, "gateway.internal")
	if _, err := call(addr, "party-1.internal", attested(func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &other, nil })); err == nil {
		t.Fatal("a non-orchestrator PKI certificate was accepted")
	}

	// A certificate from another CA is refused.
	ca2, ca2Key := testCA(t)
	foreign := leaf(t, ca2, ca2Key, "temporal-worker.internal")
	if _, err := call(addr, "party-1.internal", attested(func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &foreign, nil })); err == nil {
		t.Fatal("an orchestrator certificate from another CA was accepted")
	}

	// No client certificate is refused.
	if _, err := call(addr, "party-1.internal", attested(nil)); err == nil {
		t.Fatal("a client with no certificate was accepted")
	}

	// Without an orchestrator CA, PKI certificates are refused outright.
	strict, _ := ServerTLSConfig(ServerOptions{Identity: f.identity(t, 1), Verifier: f.verifier})
	addr2 := serve(t, strict)
	if _, err := call(addr2, "party-1.internal", attested(func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &orch, nil })); err == nil {
		t.Fatal("orchestrator accepted with no orchestrator CA configured")
	}
}

func TestIdentityRefreshAndAttesterFailure(t *testing.T) {
	f := newFixture()
	now := time.Now()
	clock := func() time.Time { return now }
	flaky := &flakyAttester{inner: f.nsm}
	id, err := NewIdentity(4, flaky, IdentityOptions{Now: clock, Refresh: time.Hour, Validity: 2 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := id.Certificate()
	if again, _ := id.Certificate(); again != first {
		t.Fatal("certificate re-minted before refresh")
	}
	if _, err := f.verifier.VerifyCertificate(first.Leaf); err != nil {
		t.Fatalf("minted certificate does not verify: %v", err)
	}
	now = now.Add(61 * time.Minute)
	second, _ := id.Certificate()
	if second == first {
		t.Fatal("certificate not refreshed")
	}
	if bytes.Equal(second.Leaf.RawSubjectPublicKeyInfo, first.Leaf.RawSubjectPublicKeyInfo) {
		t.Fatal("refresh reused the key")
	}
	// The NSM fails: the still-valid certificate keeps serving...
	flaky.fail = true
	now = now.Add(61 * time.Minute)
	if c, err := id.Certificate(); err != nil || c != second {
		t.Fatalf("expected the previous certificate during an NSM outage, got %v", err)
	}
	// ...until it expires, and then handshakes fail.
	now = now.Add(2 * time.Hour)
	if _, err := id.Certificate(); err == nil {
		t.Fatal("an expired certificate was served")
	}
	if _, err := NewIdentity(1, flaky, IdentityOptions{}); err == nil {
		t.Fatal("identity created with a failing attester")
	}
	if _, err := NewIdentity(0, f.nsm, IdentityOptions{}); err == nil {
		t.Fatal("identity for party 0")
	}
	if _, err := NewIdentity(1, nil, IdentityOptions{}); err == nil {
		t.Fatal("identity without an attester")
	}
}

type flakyAttester struct {
	inner Attester
	fail  bool
}

func (f *flakyAttester) Attest(u, n, k []byte) ([]byte, error) {
	if f.fail {
		return nil, errors.New("nsm unavailable")
	}
	return f.inner.Attest(u, n, k)
}

func TestStubAttesterRefuses(t *testing.T) {
	if BuiltForNitro {
		t.Skip("nitro build")
	}
	if _, err := NewAttester(); err == nil {
		t.Fatal("a non-nitro build produced an attester")
	}
}

// ---- credentials ----

type staticCreds struct {
	c   aws.Credentials
	err error
}

func (s staticCreds) Retrieve(context.Context) (aws.Credentials, error) { return s.c, s.err }

func TestCredentialForwarding(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	go ServeCredentials(ctx, ln, staticCreds{c: aws.Credentials{AccessKeyID: "AKIA", SecretAccessKey: "s", SessionToken: "t", CanExpire: true, Expires: exp}}, t.Logf)
	h := HostCredentials{Dial: func(ctx context.Context) (net.Conn, error) { return net.Dial("tcp", ln.Addr().String()) }}
	c, err := h.Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.AccessKeyID != "AKIA" || c.SessionToken != "t" || !c.Expires.Equal(exp) || !c.CanExpire {
		t.Fatalf("credentials: %+v", c)
	}

	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	go ServeCredentials(ctx, ln2, staticCreds{err: errors.New("imds down")}, t.Logf)
	h2 := HostCredentials{Dial: func(ctx context.Context) (net.Conn, error) { return net.Dial("tcp", ln2.Addr().String()) }}
	if _, err := h2.Retrieve(context.Background()); err == nil || !strings.Contains(err.Error(), "could not obtain") {
		t.Fatalf("expected host error, got %v", err)
	}

	// A host that answers with junk.
	ln3, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() {
		c, _ := ln3.Accept()
		_, _, _ = ReadFrame(c)
		_ = WriteFrame(c, FrameCredentialsResponse, []byte(`{"access_key_id":""}`))
		c.Close()
	}()
	h3 := HostCredentials{Dial: func(ctx context.Context) (net.Conn, error) { return net.Dial("tcp", ln3.Addr().String()) }}
	if _, err := h3.Retrieve(context.Background()); err == nil {
		t.Fatal("malformed credentials accepted")
	}
}

// ---- PKI helpers ----

func testCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c, key
}

func leaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string) tls.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn}, DNSNames: []string{cn}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
