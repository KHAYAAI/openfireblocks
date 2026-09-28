//go:build pkcs11

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forge-crypto/mpc-signer/chains"
	"forge-crypto/mpc-signer/keys"
	"forge-crypto/mpc-signer/keys/softhsmtest"
)

// The service in HSM mode, from environment variables to a signed
// Ethereum transaction, against a SoftHSM token.
func TestTheServiceSignsAnEthereumTransactionWithAHardwareKey(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "service", "eth-hot")
	if err := keys.GeneratePKCS11Key(cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv(keys.EnvPKCS11Library, cfg.Library)
	t.Setenv(keys.EnvPKCS11TokenLabel, cfg.TokenLabel)
	t.Setenv(keys.EnvPKCS11PIN, cfg.PIN)
	t.Setenv(keys.EnvPKCS11KeyLabel, cfg.KeyLabel)

	signer, keyHex, hardware, err := resolveSigner(context.Background(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !hardware || keyHex != "" {
		t.Fatalf("hardware=%v, and a software key is present (%d chars)", hardware, len(keyHex))
	}

	// Both transaction types. WithSignature recovers the sender from the
	// signature and refuses if it is not this key, so From matching is
	// the proof the token signed with the key it reported.
	for _, req := range []*SignRequest{
		{ChainID: 11155111, To: "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045", Value: "1", GasLimit: 21000, GasPrice: "20000000000", Nonce: 1},
		{ChainID: 11155111, To: "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045", Value: "0", GasLimit: 50000, Nonce: 2,
			MaxFeePerGas: "30000000000", MaxPriorityFeePerGas: "2000000000"},
	} {
		out, err := signer.SignTransaction(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if out.From != signer.Address() {
			t.Fatalf("signed as %s, key is %s", out.From, signer.Address())
		}
	}

	s := &server{signer: signer, signerRouter: chains.NewSignerRouter(), hardware: true}

	rec := httptest.NewRecorder()
	s.handleAddress(rec, httptest.NewRequest(http.MethodGet, "/address", nil))
	var addr map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &addr)
	if !strings.HasPrefix(addr["keySource"], "PKCS#11") {
		t.Fatalf("GET /address keySource = %q", addr["keySource"])
	}

	post := func(chain string) *httptest.ResponseRecorder {
		body := `{"chainId":"` + chain + `","message":"0123456789abcdef0123456789abcdef"}`
		rec := httptest.NewRecorder()
		s.handleSignMultiChain(rec, httptest.NewRequest(http.MethodPost, "/sign-multi-chain", strings.NewReader(body)))
		return rec
	}
	if rec := post("ethereum"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), signer.Address()) {
		t.Fatalf("ethereum: %d %s", rec.Code, rec.Body)
	}
	if rec := post("solana"); rec.Code == http.StatusOK {
		t.Fatalf("solana was signed in hardware mode: %s", rec.Body)
	}
}
