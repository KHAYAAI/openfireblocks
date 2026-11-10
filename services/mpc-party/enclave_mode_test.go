package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/gorilla/mux"

	"openfireblocks.com/services/mpc-party/enclave"
	"openfireblocks.com/services/mpc-party/enclave/enclavetest"
	"openfireblocks.com/services/mpc-party/nitroattest"
	"openfireblocks.com/services/mpc-party/nitroattest/nitroattesttest"
)

func resetEnclaveState(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		enclaveMu.Lock()
		enclaveSealers = map[int]shareSealer{}
		enclaveVault = nil
		enclaveMu.Unlock()
	})
}

// enclaveParty is one simulated enclave: its own fake NSM (same root, same
// image, its own IAM role), identity, TLS server and relay client.
type enclaveParty struct {
	id     int
	nsm    *nitroattesttest.FakeNSM
	mgr    *TSSPartyManager
	server *httptest.Server
}

type enclaveCluster struct {
	root     *nitroattesttest.FakeNSM
	verifier *nitroattest.Verifier
	kms      *enclavetest.FakeKMS
	parties  map[int]*enclaveParty
	addrs    map[int]string
	peers    map[int]string
}

func newEnclaveCluster(t *testing.T, n int) *enclaveCluster {
	t.Helper()
	resetEnclaveState(t)
	root := nitroattesttest.New()
	policy := nitroattest.Policy{
		ImagePCR0:       []string{nitroattesttest.Hex("image")},
		RequirePartyPin: true,
		Parties:         map[string]nitroattest.PartyPolicy{},
	}
	for i := 1; i <= n; i++ {
		policy.Parties[fmt.Sprint(i)] = nitroattest.PartyPolicy{PCR3: nitroattesttest.Hex(fmt.Sprintf("role-%d", i))}
	}
	c := &enclaveCluster{
		root:     root,
		verifier: &nitroattest.Verifier{Policy: policy, Roots: root.Roots()},
		kms:      enclavetest.NewFakeKMS(&nitroattest.Verifier{Policy: nitroattest.Policy{ImagePCR0: policy.ImagePCR0}, Roots: root.Roots()}),
		parties:  map[int]*enclaveParty{},
		addrs:    map[int]string{},
		peers:    map[int]string{},
	}
	for i := 1; i <= n; i++ {
		c.peers[i] = fmt.Sprintf("https://party-%d.internal:443", i)
	}
	for i := 1; i <= n; i++ {
		c.startParty(t, i)
	}
	return c
}

// startParty (re)starts party i with fresh memory: a new process in a new
// enclave, as after a restart.
func (c *enclaveCluster) startParty(t *testing.T, i int) *enclaveParty {
	t.Helper()
	return c.startPartyAs(t, i, fmt.Sprintf("role-%d", i))
}

// startPartyAs starts party i on an instance whose IAM role is role.
func (c *enclaveCluster) startPartyAs(t *testing.T, i int, role string) *enclaveParty {
	t.Helper()
	nsm := c.root.Clone()
	nsm.PCRs[3] = nitroattesttest.Digest(role)
	id, err := enclave.NewIdentity(i, nsm, enclave.IdentityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := enclave.ServerTLSConfig(enclave.ServerOptions{Identity: id, Verifier: c.verifier})
	if err != nil {
		t.Fatal(err)
	}
	// The egress map, with each "vsock port" standing for a party's host relay.
	routes := map[string]uint32{}
	for j := range c.peers {
		routes[fmt.Sprintf("party-%d.internal:443", j)] = uint32(7100 + j)
	}
	egress := enclave.NewEgress(routes, func(ctx context.Context, port uint32) (net.Conn, error) {
		addr, ok := c.addrs[int(port-7100)]
		if !ok {
			return nil, fmt.Errorf("no relay on port %d", port)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	})
	client := &http.Client{Timeout: 90 * time.Second, Transport: &http.Transport{
		DialContext:     egress.DialContext,
		TLSClientConfig: enclave.PeerClientTLSConfig(id, c.verifier),
	}}
	mgr := NewTSSPartyManager(i, client)
	registerEnclaveSealer(i, &enclave.Sealer{KMS: c.kms, KeyID: fmt.Sprintf("alias/forge-party-%d", i), Attester: nsm, PartyID: i})

	router := mux.NewRouter()
	ps := &PartyServer{partyID: i, tssManager: mgr}
	router.HandleFunc("/tss/keygen/message", ps.HandleTSSKeygenMessage).Methods(http.MethodPost)
	router.HandleFunc("/tss/sign/message", ps.HandleTSSSignMessage).Methods(http.MethodPost)
	router.HandleFunc("/tss/reshare/message", ps.HandleTSSReshareMessage).Methods(http.MethodPost)
	srv := httptest.NewUnstartedServer(router)
	srv.TLS = serverTLS
	srv.StartTLS()
	t.Cleanup(srv.Close)
	if old, ok := c.parties[i]; ok {
		old.server.Close()
	}
	p := &enclaveParty{id: i, nsm: nsm, mgr: mgr, server: srv}
	c.parties[i] = p
	c.addrs[i] = srv.Listener.Addr().String()
	return p
}

func waitKeygen(t *testing.T, c *enclaveCluster, ceremonyID string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	pub := map[int]string{}
	for len(pub) < len(c.parties) && time.Now().Before(deadline) {
		for i, p := range c.parties {
			st, err := p.mgr.GetStatus(ceremonyID)
			if err != nil {
				t.Fatal(err)
			}
			if st.Status == ceremonyFailed {
				t.Fatalf("party %d: %s", i, st.Error)
			}
			if st.Status == ceremonyCompleted {
				pub[i] = st.PublicKey
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(pub) != len(c.parties) {
		t.Fatal("keygen timed out")
	}
	for i := range pub {
		if pub[i] != pub[1] {
			t.Fatal("parties disagree on the public key")
		}
	}
	return pub[1]
}

// The whole enclave path except the hardware: a real Ed25519 DKG between
// three simulated enclaves over attested TLS through the egress map, shares
// sealed with KMS envelopes into Vault, every party restarted with empty
// memory and restored, then a threshold signature that verifies.
func TestEnclaveModeKeygenSealRestoreSign(t *testing.T) {
	t.Setenv("TSS_PREPARAMS_POOL", "0")
	startFakeVault(t)
	c := newEnclaveCluster(t, 3)
	const cer = "enclave-e2e"
	for i, p := range c.parties {
		if err := p.mgr.StartKeygen(cer, 1, c.peers, CurveEd25519); err != nil {
			t.Fatalf("party %d: %v", i, err)
		}
	}
	pubHex := waitKeygen(t, c, cer)

	// Vault holds only envelopes.
	for i := 1; i <= 3; i++ {
		cfg, _ := vaultShareConfigFromEnv(os.Getenv, i, cer)
		client, err := newVaultClient(cfg.addr)
		if err != nil {
			t.Fatal(err)
		}
		client.SetToken(cfg.token)
		sec, err := client.KVv2(cfg.mount).Get(context.Background(), cfg.keyPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := sec.Data["save_data"]; ok {
			t.Fatalf("party %d: Vault holds the share in the clear", i)
		}
		if _, ok := sec.Data["ceremony_context"]; ok {
			t.Fatalf("party %d: Vault holds the ceremony context in the clear", i)
		}
		env, _ := sec.Data["enclave_envelope"].(string)
		if env == "" {
			t.Fatalf("party %d: no envelope in Vault", i)
		}
	}

	// Every party restarts with nothing in memory and restores from Vault.
	for i := 1; i <= 3; i++ {
		c.startParty(t, i)
	}
	for i, p := range c.parties {
		if err := p.mgr.RestoreCeremony(cer, c.peers); err != nil {
			t.Fatalf("party %d restore: %v", i, err)
		}
	}

	msg := []byte("signed by three enclaves that never showed the host a share")
	committee := []int{1, 3}
	for _, i := range committee {
		if err := c.parties[i].mgr.StartSigning("enclave-sign", cer, msg, committee); err != nil {
			t.Fatal(err)
		}
	}
	var sigHex string
	deadline := time.Now().Add(2 * time.Minute)
	for sigHex == "" && time.Now().Before(deadline) {
		st, err := c.parties[1].mgr.GetSigningStatus("enclave-sign")
		if err != nil {
			t.Fatal(err)
		}
		if st.Status == ceremonyFailed {
			t.Fatalf("signing failed: %s", st.Error)
		}
		sigHex = st.Signature
		time.Sleep(100 * time.Millisecond)
	}
	sig, _ := hex.DecodeString(sigHex)
	pub, _ := hex.DecodeString(pubHex)
	if !ed25519.Verify(pub, msg, sig) {
		t.Fatal("the signature from restored enclaves does not verify")
	}

	// A rebuilt image cannot open the sealed shares: KMS refuses it.
	rogue := c.root.Clone()
	rogue.PCRs[0] = nitroattesttest.Digest("image with one byte changed")
	registerEnclaveSealer(2, &enclave.Sealer{KMS: c.kms, KeyID: "alias/forge-party-2", Attester: rogue, PartyID: 2})
	if _, _, err := LoadSealedShare(context.Background(), os.Getenv, 2, cer); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("a rebuilt image opened party 2's share: %v", err)
	}
	// Outside enclave mode the envelope is refused, never misread.
	enclaveMu.Lock()
	delete(enclaveSealers, 2)
	enclaveMu.Unlock()
	if _, _, err := LoadSealedShare(context.Background(), os.Getenv, 2, cer); err == nil || !strings.Contains(err.Error(), "sealed by an enclave") {
		t.Fatalf("an enclave envelope was read outside enclave mode: %v", err)
	}
}

func TestEnclaveModeRefusesAPartyUnderAnotherRole(t *testing.T) {
	t.Setenv("TSS_PREPARAMS_POOL", "0")
	startFakeVault(t)
	c := newEnclaveCluster(t, 3)
	// Party 2's enclave runs the right image, but on an instance with party
	// 3's IAM role: its peers must refuse it, and no ceremony can complete.
	c.startPartyAs(t, 2, "role-3")
	for _, p := range c.parties {
		_ = p.mgr.StartKeygen("wrong-role", 1, c.peers, CurveEd25519)
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		for i, p := range c.parties {
			st, _ := p.mgr.GetStatus("wrong-role")
			if st == nil {
				continue
			}
			if st.Status == ceremonyCompleted {
				t.Fatalf("party %d completed a ceremony with a peer under the wrong IAM role", i)
			}
			if st.Status == ceremonyFailed && i != 2 && strings.Contains(st.Error, "PCR3") {
				return // refused at the handshake, for the right reason
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("no party refused the peer under the wrong IAM role")
}

func TestEnclavePeerListMustNameParties(t *testing.T) {
	resetEnclaveState(t)
	if err := checkEnclavePeers(map[int]string{1: "http://anything"}); err != nil {
		t.Fatal("checked outside enclave mode")
	}
	registerEnclaveSealer(1, &enclave.Sealer{})
	good := map[int]string{1: "https://party-1.internal:7000", 2: "https://party-2.internal"}
	if err := checkEnclavePeers(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[int]string{
		{2: "https://party-3.internal"},
		{2: "http://party-2.internal"},
		{2: "https://vault.internal"},
		{2: "https://10.0.0.2:7000"},
		{2: "::bad"},
	} {
		if err := checkEnclavePeers(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestEnclaveModeRefusesToSealWithoutVault(t *testing.T) {
	resetEnclaveState(t)
	t.Setenv("VAULT_ADDR", "")
	t.Setenv("SHARE_STORE_DIR", t.TempDir())
	registerEnclaveSealer(1, &enclave.Sealer{})
	if _, err := SealKeyShareWithContext(context.Background(), os.Getenv, 1, "c", &KeyShare{Curve: CurveEd25519}, nil); err == nil || !strings.Contains(err.Error(), "VAULT_ADDR") {
		t.Fatalf("sealed without Vault in enclave mode: %v", err)
	}
}

func TestEnclaveModeRefusesAShareSealedInTheClear(t *testing.T) {
	resetEnclaveState(t)
	_, _, handled, err := func() (*KeyShare, *CeremonyContext, bool, error) {
		registerEnclaveSealer(1, &enclave.Sealer{})
		return openEnclaveEntry(context.Background(), 1, "c", "secret/x", map[string]interface{}{"save_data": "{}"})
	}()
	if !handled || err == nil || !strings.Contains(err.Error(), "not sealed by an enclave") {
		t.Fatalf("clear share accepted in enclave mode: %v", err)
	}
}

// ---- start-up ----

type startupFixture struct {
	// hostile answers the config request without the host-side check, as a
	// modified host would.
	hostile  bool
	reports  chan []byte
	nsm      *nitroattesttest.FakeNSM
	hostCfg  map[string]string
	env      map[string]string
	ctrlAddr string
}

func (f *startupFixture) deps(t *testing.T) enclaveDeps {
	return enclaveDeps{
		attester: f.nsm,
		roots:    f.nsm.Roots(),
		dialHost: func(ctx context.Context, port uint32) (net.Conn, error) {
			if port != 9000 {
				return nil, fmt.Errorf("nothing on host port %d", port)
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", f.ctrlAddr)
		},
		listen: func(uint32) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") },
		newKMS: func(string, *http.Client, aws.CredentialsProvider) enclave.KMSAPI {
			return enclavetest.NewFakeKMS(&nitroattest.Verifier{Policy: nitroattest.Policy{ImagePCR0: []string{nitroattesttest.Hex("image")}}, Roots: f.nsm.Roots()})
		},
		setenv: func(k, v string) error { t.Setenv(k, v); return nil },
		getenv: os.Getenv,
	}
}

func newStartupFixture(t *testing.T) *startupFixture {
	resetEnclaveState(t)
	nsm := nitroattesttest.New()
	nsm.PCRs[3] = nitroattesttest.Digest("role-1")
	return &startupFixture{
		nsm: nsm,
		hostCfg: map[string]string{
			"PARTY_ID":       "1",
			"ENCLAVE_EGRESS": "party-2.internal:443=7102,kms.af-south-1.amazonaws.com:443=8443",
		},
		env: map[string]string{
			"ENCLAVE_MODE":               "1",
			"ENCLAVE_KMS_REGION":         "af-south-1",
			"ENCLAVE_KMS_KEYS":           "1=arn:aws:kms:af-south-1:111:key/a,2=arn:aws:kms:af-south-1:222:key/b",
			"ENCLAVE_PARTY_PCR3":         "1=" + nitroattesttest.Hex("role-1") + ",2=" + nitroattesttest.Hex("role-2"),
			"CEREMONY_AUTHORIZER_PUBKEY": "baked",
		},
	}
}

func (f *startupFixture) run(t *testing.T) (*enclaveRuntime, error) {
	t.Helper()
	for k, v := range f.env {
		t.Setenv(k, v)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if f.hostile {
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_, _, _ = enclave.ReadFrame(c)
				body, _ := json.Marshal(f.hostCfg)
				_ = enclave.WriteFrame(c, enclave.FrameConfigResponse, body)
				c.Close()
			}
		}()
		t.Cleanup(func() { ln.Close() })
	} else {
		go (&enclave.Control{Config: f.hostCfg, Reports: func(b []byte) {
			if f.reports != nil {
				f.reports <- append([]byte(nil), b...)
			}
		}}).Serve(ctx, ln)
	}
	f.ctrlAddr = ln.Addr().String()
	rt, err := startEnclave(context.Background(), f.deps(t))
	if rt != nil {
		t.Cleanup(func() { rt.listener.Close() })
	}
	return rt, err
}

func TestStartEnclave(t *testing.T) {
	f := newStartupFixture(t)
	rt, err := f.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if rt.partyID != 1 || rt.policy.ImagePCR0[0] != nitroattesttest.Hex("image") || !rt.policy.RequirePartyPin {
		t.Fatalf("runtime: %+v", rt.policy)
	}
	if _, ok := sealerFor(1); !ok || !enclaveActive() {
		t.Fatal("no sealer registered")
	}
}

func TestStartEnclaveRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(f *startupFixture)
		want   string
	}{
		{"debug mode", func(f *startupFixture) {
			for i := range f.nsm.PCRs {
				f.nsm.PCRs[i] = make([]byte, 48)
			}
		}, "debug mode"},
		{"claims another party's number", func(f *startupFixture) { f.hostCfg["PARTY_ID"] = "2" }, "not the role pinned"},
		{"unpinned party", func(f *startupFixture) { f.hostCfg["PARTY_ID"] = "3" }, ""},
		{"no co-signer", func(f *startupFixture) { f.env["CEREMONY_AUTHORIZER_PUBKEY"] = "" }, "CEREMONY_AUTHORIZER_PUBKEY"},
		{"unauthenticated peers baked in", func(f *startupFixture) { f.env["TSS_ALLOW_UNAUTHENTICATED_PEERS"] = "1" }, "TSS_ALLOW_UNAUTHENTICATED_PEERS"},
		{"file store baked in", func(f *startupFixture) { f.env["SHARE_STORE_DIR"] = "/tmp/x" }, "SHARE_STORE_DIR"},
		{"host sets the KMS key", func(f *startupFixture) { f.hostile = true; f.hostCfg["ENCLAVE_KMS_KEYS"] = "1=arn:attacker" }, "may not set"},
		{"host sets the co-signer key", func(f *startupFixture) { f.hostile = true; f.hostCfg["CEREMONY_AUTHORIZER_PUBKEY"] = "attacker" }, "may not set"},
		{"no KMS key for this party", func(f *startupFixture) { f.env["ENCLAVE_KMS_KEYS"] = "2=arn:x" }, "ENCLAVE_KMS_KEYS"},
		{"no egress", func(f *startupFixture) { delete(f.hostCfg, "ENCLAVE_EGRESS") }, "egress"},
		{"no party id", func(f *startupFixture) { delete(f.hostCfg, "PARTY_ID") }, "PARTY_ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStartupFixture(t)
			tc.mutate(f)
			_, err := f.run(t)
			if err == nil {
				t.Fatal("enclave started")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestEnclaveSelfTestAndReports(t *testing.T) {
	f := newStartupFixture(t)
	f.reports = make(chan []byte, 4)
	f.env["ENCLAVE_SELFTEST"] = "1"
	rt, err := f.run(t)
	if err != nil {
		t.Fatal(err)
	}
	var rep enclave.Report
	_ = json.Unmarshal(<-f.reports, &rep)
	if rep.Event != "selftest" || !rep.OK || len(rep.EncryptedKey) == 0 || rep.ImagePCR != nitroattesttest.Hex("image") {
		t.Fatalf("self-test report: %+v", rep)
	}
	reportEnclaveStart(context.Background(), f.deps(t), rt, nil)
	_ = json.Unmarshal(<-f.reports, &rep)
	if rep.Event != "started" || !rep.OK || rep.Party != 1 {
		t.Fatalf("start report: %+v", rep)
	}
	reportEnclaveStart(context.Background(), f.deps(t), nil, fmt.Errorf("no co-signer"))
	_ = json.Unmarshal(<-f.reports, &rep)
	if rep.Event != "failed" || rep.OK || rep.Detail != "no co-signer" {
		t.Fatalf("failure report: %+v", rep)
	}
}

// The self-test fails, and says so, when KMS refuses the image: the spike's
// negative case.
func TestEnclaveSelfTestReportsAKMSRefusal(t *testing.T) {
	f := newStartupFixture(t)
	f.reports = make(chan []byte, 4)
	f.env["ENCLAVE_SELFTEST"] = "1"
	f.nsm.PCRs[0] = nitroattesttest.Digest("image with one byte changed")
	if _, err := f.run(t); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("self-test passed for an image KMS does not allow: %v", err)
	}
	var rep enclave.Report
	_ = json.Unmarshal(<-f.reports, &rep)
	if rep.OK || !strings.Contains(rep.Detail, "AccessDenied") {
		t.Fatalf("report: %+v", rep)
	}
}
