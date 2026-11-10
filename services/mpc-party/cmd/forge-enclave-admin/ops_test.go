package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openfireblocks.com/services/mpc-party/enclave"
	"openfireblocks.com/services/mpc-party/nitroattest"
	"openfireblocks.com/services/mpc-party/nitroattest/nitroattesttest"
)

func template(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "infrastructure", "nitro", "kms-key-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRenderKMSPolicyForAnUpgrade(t *testing.T) {
	a, b := nitroattesttest.Hex("image-a"), nitroattesttest.Hex("image-b")
	out, err := renderKMSPolicy(template(t), "111122223333", "arn:aws:iam::111122223333:role/forge-party-2", 2, []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{a, b, "arn:aws:iam::111122223333:root", "arn:aws:iam::111122223333:role/forge-party-2", `"party-2"`} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered policy lacks %s", want)
		}
	}
	for _, placeholder := range []string{"ACCOUNT_ID", "PARTY_ROLE_ARN", "PCR0_HEX", "PARTY_NUMBER"} {
		if strings.Contains(s, placeholder) {
			t.Errorf("placeholder %s left in", placeholder)
		}
	}
	var doc struct {
		Statement []struct {
			Sid       string
			Condition map[string]map[string]any
		}
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	images := doc.Statement[1].Condition["StringEqualsIgnoreCase"]["kms:RecipientAttestation:ImageSha384"].([]any)
	if len(images) != 2 {
		t.Fatalf("images: %v", images)
	}
	if doc.Statement[2].Condition["Null"]["kms:RecipientAttestation:ImageSha384"] != "true" {
		t.Fatal("the deny-without-attestation statement was lost")
	}
}

func TestRenderKMSPolicyRefusals(t *testing.T) {
	tm := template(t)
	role := "arn:aws:iam::1:role/r"
	for name, pcrs := range map[string][]string{
		"none":     nil,
		"zero":     {strings.Repeat("00", 48)},
		"short":    {"abcd"},
		"not hex":  {strings.Repeat("zz", 48)},
		"one zero": {nitroattesttest.Hex("a"), strings.Repeat("00", 48)},
	} {
		if _, err := renderKMSPolicy(tm, "1", role, 1, pcrs); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := renderKMSPolicy(tm, "1", "role/r", 1, []string{nitroattesttest.Hex("a")}); err == nil {
		t.Error("a role that is not an ARN was accepted")
	}
	if _, err := renderKMSPolicy(tm, "1", role, 0, []string{nitroattesttest.Hex("a")}); err == nil {
		t.Error("party 0 accepted")
	}
}

func TestRecoverEntry(t *testing.T) {
	priv, pub, _ := enclave.RecoveryKeygen()
	e, _ := enclave.NewEscrow(pub)
	payload, _ := json.Marshal(map[string]any{"party_id": 2, "ceremony_id": "cer", "save_data": `{"curve":"ed25519"}`, "ceremony_context": `{"threshold":1}`})
	env, _ := e.Seal(payload, 2, "cer")
	data := map[string]any{"party_id": 2, "ceremony_id": "cer", "curve": "ed25519", "enclave_envelope": "{}", "recovery_envelope": string(env)}
	vaultOut, _ := json.Marshal(map[string]any{"data": map[string]any{"data": data, "metadata": map[string]any{"version": 3}}})
	inner, _ := json.Marshal(data)
	for name, in := range map[string][]byte{"vault kv get output": vaultOut, "entry": inner} {
		got, err := recoverEntry(priv, in, 2, "cer")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.SaveData != `{"curve":"ed25519"}` || got.CeremonyContext != `{"threshold":1}` || got.Curve != "ed25519" || got.PartyID != 2 {
			t.Fatalf("%s: %+v", name, got)
		}
	}
	if _, err := recoverEntry(priv, vaultOut, 3, "cer"); err == nil {
		t.Fatal("opened as another party")
	}
	other, _, _ := enclave.RecoveryKeygen()
	if _, err := recoverEntry(other, vaultOut, 2, "cer"); err == nil {
		t.Fatal("opened with another recovery key")
	}
	noEnv, _ := json.Marshal(map[string]any{"enclave_envelope": "{}"})
	if _, err := recoverEntry(priv, noEnv, 2, "cer"); err == nil || !strings.Contains(err.Error(), "no recovery_envelope") {
		t.Fatalf("entry without escrow: %v", err)
	}
}

func TestRecoveryKeygenAndRecoverCommands(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "party-1.key")
	var out bytes.Buffer
	if err := mainRecoveryKeygen([]string{"-out", keyFile}, &out); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(keyFile); info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode %o", info.Mode().Perm())
	}
	if err := mainRecoveryKeygen([]string{"-out", keyFile}, &out); err == nil {
		t.Fatal("overwrote an existing private key")
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	pub := lines[len(lines)-1]
	e, err := enclave.NewEscrow(pub)
	if err != nil {
		t.Fatalf("printed public key does not parse: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"party_id": 1, "ceremony_id": "c", "save_data": "S"})
	env, _ := e.Seal(payload, 1, "c")
	in := filepath.Join(dir, "entry.json")
	raw, _ := json.Marshal(map[string]any{"recovery_envelope": string(env)})
	_ = os.WriteFile(in, raw, 0o600)
	dst := filepath.Join(dir, "out.json")
	if err := mainRecover([]string{"-key", keyFile, "-in", in, "-party", "1", "-ceremony", "c", "-out", dst}, &out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dst)
	if !strings.Contains(string(b), `"save_data": "S"`) {
		t.Fatalf("recovered entry: %s", b)
	}
}

// ---- inventory ----

func startAttestedParty(t *testing.T, party int, attester enclave.Attester) string {
	t.Helper()
	id, err := enclave.NewIdentity(party, attester, enclave.IdentityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.TLS = &tls.Config{GetCertificate: id.GetCertificate, ClientAuth: tls.RequestClientCert}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

func TestInventoryReportsImagesAndRefusals(t *testing.T) {
	root := nitroattesttest.New()
	v := &nitroattest.Verifier{Policy: nitroattest.Policy{ImagePCR0: []string{nitroattesttest.Hex("image")}}, Roots: root.Roots()}
	dial := func(ctx context.Context, addr string) (net.Conn, error) { return (&net.Dialer{}).DialContext(ctx, "tcp", addr) }

	a1 := startAttestedParty(t, 1, root.Clone())
	newImage := root.Clone()
	newImage.PCRs[0] = nitroattesttest.Digest("image-b")
	a2 := startAttestedParty(t, 2, newImage)
	a3 := startAttestedParty(t, 4, root.Clone()) // answers as party 4 at party 3's address

	images := []partyImage{
		inspectParty(context.Background(), dial, a1, 1, nil, v, root.Roots()),
		inspectParty(context.Background(), dial, a2, 2, nil, v, root.Roots()),
		inspectParty(context.Background(), dial, a3, 3, nil, v, root.Roots()),
	}
	if !images[0].Accepted || images[0].PCR0 != nitroattesttest.Hex("image") || images[0].PCR3 != hex.EncodeToString(nitroattesttest.Digest("role")) {
		t.Fatalf("party 1: %+v", images[0])
	}
	if images[1].Accepted || images[1].PCR0 != nitroattesttest.Hex("image-b") || !strings.Contains(images[1].Problem, "PCR0") {
		t.Fatalf("party 2 (unlisted image) should be reported and refused: %+v", images[1])
	}
	if images[2].Accepted || !strings.Contains(images[2].Problem, "attesting party 4") {
		t.Fatalf("party 3 (wrong party) should be refused: %+v", images[2])
	}
	var out bytes.Buffer
	err := inventory(context.Background(), &out, images, true)
	if err == nil || !strings.Contains(out.String(), "2 different images") {
		t.Fatalf("inventory: %v\n%s", err, out.String())
	}

	// Mid-upgrade, with both images in the policy: accepted, but mixed.
	v2 := &nitroattest.Verifier{Policy: nitroattest.Policy{ImagePCR0: []string{nitroattesttest.Hex("image"), nitroattesttest.Hex("image-b")}}, Roots: root.Roots()}
	mixed := []partyImage{
		inspectParty(context.Background(), dial, a1, 1, nil, v2, root.Roots()),
		inspectParty(context.Background(), dial, a2, 2, nil, v2, root.Roots()),
	}
	if err := inventory(context.Background(), &out, mixed, true); err != nil {
		t.Fatalf("mixed, allowed: %v", err)
	}
	if err := inventory(context.Background(), &out, mixed, false); err == nil || !strings.Contains(err.Error(), "different images") {
		t.Fatalf("mixed, not allowed: %v", err)
	}

	// A plain TLS server is reported as not in an enclave.
	plain := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)
	if im := inspectParty(context.Background(), dial, plain.Listener.Addr().String(), 1, nil, v, root.Roots()); im.Accepted || !strings.Contains(im.Problem, "not in an enclave") {
		t.Fatalf("plain server: %+v", im)
	}
}
