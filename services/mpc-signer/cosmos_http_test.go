package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	btcecdsa "github.com/btcsuite/btcd/btcec/v2/ecdsa"

	"forge-crypto/mpc-signer/chains"
)

// A stand-in Cosmos LCD. The handlers are under test; the chain is scenery.
type fakeLCD struct {
	accounts   map[string][2]string // address -> number, sequence
	balances   map[string]map[string]string
	chainID    string
	broadcast  []byte
	rejectCode int
	rejectLog  string
	txs        map[string]string // hash -> code
}

func (f *fakeLCD) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/cosmos/auth/v1beta1/accounts/"):
			a, ok := f.accounts[strings.TrimPrefix(p, "/cosmos/auth/v1beta1/accounts/")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":5,"message":"account not found"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"account": map[string]interface{}{
				"@type": "/cosmos.auth.v1beta1.BaseAccount", "account_number": a[0], "sequence": a[1]}})
		case strings.Contains(p, "/cosmos/bank/v1beta1/balances/") && strings.HasSuffix(p, "/by_denom"):
			addr := strings.TrimSuffix(strings.TrimPrefix(p, "/cosmos/bank/v1beta1/balances/"), "/by_denom")
			denom := r.URL.Query().Get("denom")
			amt := f.balances[addr][denom]
			if amt == "" {
				amt = "0"
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"balance": map[string]string{"denom": denom, "amount": amt}})
		case p == "/cosmos/base/tendermint/v1beta1/node_info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"default_node_info": map[string]string{"network": f.chainID}})
		case p == "/cosmos/tx/v1beta1/txs" && r.Method == http.MethodPost:
			var in struct {
				TxBytes string `json:"tx_bytes"`
				Mode    string `json:"mode"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.Mode != "BROADCAST_MODE_SYNC" {
				t.Errorf("broadcast mode = %q", in.Mode)
			}
			f.broadcast, _ = base64.StdEncoding.DecodeString(in.TxBytes)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"tx_response": map[string]interface{}{
				"code": f.rejectCode, "raw_log": f.rejectLog, "txhash": strings.Repeat("AB", 32)}})
		case strings.HasPrefix(p, "/cosmos/tx/v1beta1/txs/"):
			code, ok := f.txs[strings.TrimPrefix(p, "/cosmos/tx/v1beta1/txs/")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":5,"message":"tx not found"}`))
				return
			}
			c := 0
			if code != "0" {
				c = 11
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"tx_response": map[string]interface{}{
				"height": "900", "txhash": strings.TrimPrefix(p, "/cosmos/tx/v1beta1/txs/"), "code": c, "raw_log": "out of gas"}})
		default:
			w.WriteHeader(http.StatusNotImplemented)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func cosmosServer(t *testing.T, lcd *fakeLCD) *server {
	t.Helper()
	t.Setenv("COSMOS_LCD_URL", lcd.start(t))
	t.Setenv("COSMOS_CHAIN_ID", "")
	return &server{signerRouter: chains.NewSignerRouter()}
}

const cosmosDest = "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu"

func cosmosKey(t *testing.T) (*btcec.PrivateKey, string, string) {
	t.Helper()
	k, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	pubHex := hex.EncodeToString(k.PubKey().SerializeUncompressed()) // as a DKG stores it
	from, _, err := cosmosAddressFromHex(pubHex, "cosmos")
	if err != nil {
		t.Fatal(err)
	}
	return k, pubHex, from
}

func cosmosPrep(t *testing.T, s *server, pubHex, dest, amount, denom string) (int, map[string]interface{}) {
	t.Helper()
	return post(t, s.handleCosmosPrepare, map[string]interface{}{"pubkey_hex": pubHex, "destination": dest, "amount": amount, "denom": denom})
}

func TestCosmosPrepareReadsTheChainAndBuildsThePlan(t *testing.T) {
	_, pubHex, from := cosmosKey(t)
	lcd := &fakeLCD{chainID: "cosmoshub-4", accounts: map[string][2]string{from: {"1234", "7"}},
		balances: map[string]map[string]string{from: {"uatom": "10000000"}}}
	code, body := cosmosPrep(t, cosmosServer(t, lcd), pubHex, cosmosDest, "1000000", "")
	if code != http.StatusOK {
		t.Fatalf("prepare returned %d: %v", code, body["error"])
	}
	plan := body["plan"].(map[string]interface{})
	if plan["chain_id"] != "cosmoshub-4" || plan["account_number"].(float64) != 1234 {
		t.Errorf("plan = %v", plan)
	}
	if body["fee"] != "5000" || body["fee_denom"] != "uatom" || body["from"] != from {
		t.Errorf("fee/from = %v %v %v", body["fee"], body["fee_denom"], body["from"])
	}
	if len(plan["digest_hex"].(string)) != 64 {
		t.Errorf("digest must be 32 bytes, got %v", plan["digest_hex"])
	}
}

func TestCosmosPrepareRefusesWhatWouldFailAfterTheCeremony(t *testing.T) {
	_, pubHex, from := cosmosKey(t)
	acct := map[string][2]string{from: {"1", "0"}}
	cases := map[string]struct {
		lcd    *fakeLCD
		dest   string
		amount string
		denom  string
		want   string
	}{
		"overdraft":               {&fakeLCD{accounts: acct, balances: map[string]map[string]string{from: {"uatom": "1000"}}}, cosmosDest, "2000", "", "insufficient"},
		"fee not covered":         {&fakeLCD{accounts: acct, balances: map[string]map[string]string{from: {"uatom": "1000000"}}}, cosmosDest, "999000", "", "insufficient"},
		"other denom, no fee gas": {&fakeLCD{accounts: acct, balances: map[string]map[string]string{from: {"uusdc": "9000000", "uatom": "10"}}}, cosmosDest, "1000", "uusdc", "insufficient uatom for the fee"},
		"other denom, short":      {&fakeLCD{accounts: acct, balances: map[string]map[string]string{from: {"uusdc": "10", "uatom": "99999"}}}, cosmosDest, "1000", "uusdc", "insufficient uusdc"},
		"never funded":            {&fakeLCD{accounts: map[string][2]string{}}, cosmosDest, "1000", "", "never been funded"},
		"another chain's address": {&fakeLCD{accounts: acct, balances: map[string]map[string]string{from: {"uatom": "99999999"}}}, "osmo1w508d6qejxtdg4y5r3zarvary0c5xw7kjxy2e2", "1000", "", `"osmo"`},
		"not bech32":              {&fakeLCD{accounts: acct}, "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045", "1000", "", "bech32"},
	}
	for name, c := range cases {
		c.lcd.chainID = "cosmoshub-4"
		code, body := cosmosPrep(t, cosmosServer(t, c.lcd), pubHex, c.dest, c.amount, c.denom)
		if code != http.StatusBadRequest || !strings.Contains(body["error"].(string), c.want) {
			t.Errorf("%s: got %d %v, want 400 mentioning %q", name, code, body["error"], c.want)
		}
	}
}

func TestCosmosPinnedChainIDMustAgreeWithTheNode(t *testing.T) {
	_, pubHex, from := cosmosKey(t)
	lcd := &fakeLCD{chainID: "cosmoshub-4", accounts: map[string][2]string{from: {"1", "0"}},
		balances: map[string]map[string]string{from: {"uatom": "10000000"}}}
	t.Setenv("COSMOS_LCD_URL", lcd.start(t))
	t.Setenv("COSMOS_CHAIN_ID", "osmosis-1")
	s := &server{signerRouter: chains.NewSignerRouter()}
	code, body := cosmosPrep(t, s, pubHex, cosmosDest, "1000", "")
	if code != http.StatusBadGateway || !strings.Contains(body["error"].(string), "osmosis-1") {
		t.Fatalf("got %d %v; a pinned chain id that disagrees with the node must stop the transfer", code, body["error"])
	}
}

func TestCosmosFinalizeSignsRelaysAndFollowsTheTransaction(t *testing.T) {
	k, pubHex, from := cosmosKey(t)
	lcd := &fakeLCD{chainID: "cosmoshub-4", accounts: map[string][2]string{from: {"1234", "7"}},
		balances: map[string]map[string]string{from: {"uatom": "10000000"}}, txs: map[string]string{strings.Repeat("AB", 32): "0"}}
	s := cosmosServer(t, lcd)
	_, prep := cosmosPrep(t, s, pubHex, cosmosDest, "1000000", "")
	plan := prep["plan"].(map[string]interface{})
	digest, _ := hex.DecodeString(plan["digest_hex"].(string))
	compact := btcecdsa.SignCompact(k, digest, true)

	code, body := post(t, s.handleCosmosFinalize, map[string]interface{}{
		"plan": plan, "r": hex.EncodeToString(compact[1:33]), "s": hex.EncodeToString(compact[33:65]),
		"pubkey_hex": pubHex, "broadcast": true})
	if code != http.StatusOK || body["broadcast"] != true {
		t.Fatalf("finalize returned %d: %v", code, body)
	}
	if len(lcd.broadcast) == 0 {
		t.Fatal("nothing reached the node")
	}
	if got := base64.StdEncoding.EncodeToString(lcd.broadcast); got != body["raw_tx_base64"] {
		t.Error("the relayed bytes are not the bytes finalize reported")
	}

	rec := httptest.NewRecorder()
	s.handleCosmosStatus(rec, httptest.NewRequest(http.MethodGet, "/cosmos/status?txhash="+strings.Repeat("AB", 32), nil))
	var st map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st["found"] != true || st["height"] != "900" {
		t.Errorf("status = %v", st)
	}
	rec = httptest.NewRecorder()
	s.handleCosmosStatus(rec, httptest.NewRequest(http.MethodGet, "/cosmos/status?txhash="+strings.Repeat("CD", 32), nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if st["found"] != false {
		t.Errorf("a transaction the node has not seen must be found=false, got %v", st)
	}
}

func TestCosmosFinalizeSurfacesARejectionAndKeepsTheBytes(t *testing.T) {
	k, pubHex, from := cosmosKey(t)
	lcd := &fakeLCD{chainID: "cosmoshub-4", rejectCode: 13, rejectLog: "insufficient fees",
		accounts: map[string][2]string{from: {"1", "0"}}, balances: map[string]map[string]string{from: {"uatom": "10000000"}}}
	s := cosmosServer(t, lcd)
	_, prep := cosmosPrep(t, s, pubHex, cosmosDest, "1000", "")
	plan := prep["plan"].(map[string]interface{})
	digest, _ := hex.DecodeString(plan["digest_hex"].(string))
	c := btcecdsa.SignCompact(k, digest, true)
	code, body := post(t, s.handleCosmosFinalize, map[string]interface{}{"plan": plan,
		"r": hex.EncodeToString(c[1:33]), "s": hex.EncodeToString(c[33:65]), "pubkey_hex": pubHex, "broadcast": true})
	if code != http.StatusBadGateway || body["raw_tx_base64"] == nil || !strings.Contains(body["error"].(string), "insufficient fees") {
		t.Fatalf("got %d %v", code, body)
	}
}

func TestCosmosFinalizeRefusesAnotherKeysSignature(t *testing.T) {
	_, pubHex, from := cosmosKey(t)
	stranger, _, _ := cosmosKey(t)
	lcd := &fakeLCD{chainID: "cosmoshub-4", accounts: map[string][2]string{from: {"1", "0"}},
		balances: map[string]map[string]string{from: {"uatom": "10000000"}}}
	s := cosmosServer(t, lcd)
	_, prep := cosmosPrep(t, s, pubHex, cosmosDest, "1000", "")
	plan := prep["plan"].(map[string]interface{})
	digest, _ := hex.DecodeString(plan["digest_hex"].(string))
	c := btcecdsa.SignCompact(stranger, digest, true)
	code, _ := post(t, s.handleCosmosFinalize, map[string]interface{}{"plan": plan,
		"r": hex.EncodeToString(c[1:33]), "s": hex.EncodeToString(c[33:65]), "pubkey_hex": pubHex, "broadcast": true})
	if code != http.StatusBadRequest || lcd.broadcast != nil {
		t.Fatalf("got %d, broadcast=%v; a foreign signature must never reach the node", code, lcd.broadcast != nil)
	}
}

func TestCosmosAddressRoute(t *testing.T) {
	_, pubHex, from := cosmosKey(t)
	t.Setenv("COSMOS_LCD_URL", "")
	s := &server{signerRouter: chains.NewSignerRouter()}
	rec := httptest.NewRecorder()
	s.handleCosmosAddresses(rec, httptest.NewRequest(http.MethodGet, "/cosmos/addresses?pubkey="+pubHex, nil))
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["address"] != from {
		t.Fatalf("got %v want %s", out["address"], from)
	}
	if code, _ := cosmosPrep(t, s, pubHex, cosmosDest, "1", ""); code != http.StatusServiceUnavailable {
		t.Errorf("no node: got %d, want 503", code)
	}
}
