package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/btcsuite/btcutil/base58"

	"forge-crypto/mpc-signer/chains"
)

// A stand-in Solana node. The handlers are under test; the node is scenery.
type fakeSolana struct {
	balances   map[string]uint64
	fee        uint64
	sendErr    string
	sentTx     []byte
	statusResp interface{}
}

func (f *fakeSolana) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply := func(result interface{}, errMsg string) {
			body := map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": result}
			if errMsg != "" {
				body = map[string]interface{}{"jsonrpc": "2.0", "id": 1, "error": map[string]interface{}{"code": -32002, "message": errMsg}}
			}
			_ = json.NewEncoder(w).Encode(body)
		}
		switch req.Method {
		case "getLatestBlockhash":
			reply(map[string]interface{}{"value": map[string]interface{}{
				"blockhash": base58.Encode(bytes.Repeat([]byte{7}, 32)), "lastValidBlockHeight": 1234}}, "")
		case "getBalance":
			var addr string
			_ = json.Unmarshal(req.Params[0], &addr)
			reply(map[string]interface{}{"value": f.balances[addr]}, "")
		case "getFeeForMessage":
			reply(map[string]interface{}{"value": f.fee}, "")
		case "sendTransaction":
			if f.sendErr != "" {
				reply(nil, f.sendErr)
				return
			}
			var b64 string
			_ = json.Unmarshal(req.Params[0], &b64)
			f.sentTx, _ = base64.StdEncoding.DecodeString(b64)
			reply(base58.Encode(f.sentTx[1:65]), "")
		case "getSignatureStatuses":
			reply(f.statusResp, "")
		default:
			reply(nil, "unexpected method "+req.Method)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func solServer(t *testing.T, node *fakeSolana) *server {
	t.Helper()
	t.Setenv("SOLANA_RPC_URL", node.start(t))
	return &server{signerRouter: chains.NewSignerRouter()}
}

func solKey(t *testing.T) (ed25519.PrivateKey, string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, hex.EncodeToString(pub), base58.Encode(pub)
}

func otherAddr() string { return base58.Encode(bytes.Repeat([]byte{9}, 32)) }

func prepare(t *testing.T, s *server, pubHex, dest, amount string) (int, map[string]interface{}) {
	t.Helper()
	return post(t, s.handleSolanaPrepare, map[string]interface{}{"pubkey_hex": pubHex, "destination": dest, "amount": amount})
}

func TestSolanaPrepareBuildsTheTransferTheKeyMustSign(t *testing.T) {
	_, pubHex, from := solKey(t)
	node := &fakeSolana{fee: 5000, balances: map[string]uint64{from: 5_000_000_000, otherAddr(): 1_000_000}}
	s := solServer(t, node)

	code, body := prepare(t, s, pubHex, otherAddr(), "1000000000")
	if code != http.StatusOK {
		t.Fatalf("prepare returned %d: %v", code, body["error"])
	}
	msg, _ := hex.DecodeString(body["message_hex"].(string))

	// Header: 1 signature required, 0 readonly signed, 1 readonly unsigned
	// (the System Program).
	if msg[0] != 1 || msg[1] != 0 || msg[2] != 1 {
		t.Fatalf("header = %v, want [1 0 1]", msg[:3])
	}
	if msg[3] != 3 {
		t.Fatalf("account count = %d, want 3 (payer, destination, system program)", msg[3])
	}
	if got := base58.Encode(msg[4:36]); got != from {
		t.Fatalf("first account %s is not the fee payer %s", got, from)
	}
	// The instruction data must be System Transfer (2) with the exact amount.
	data := msg[len(msg)-12:]
	if binary.LittleEndian.Uint32(data[:4]) != 2 || binary.LittleEndian.Uint64(data[4:]) != 1_000_000_000 {
		t.Fatalf("instruction data %x does not encode Transfer(1000000000)", data)
	}
	if body["fee"] != "5000" || body["balance"] != "5000000000" {
		t.Errorf("fee/balance = %v/%v", body["fee"], body["balance"])
	}
}

func TestSolanaPrepareRefusesWhatWouldFailAfterTheCeremony(t *testing.T) {
	_, pubHex, from := solKey(t)
	cases := map[string]struct {
		balances map[string]uint64
		amount   string
		want     string
	}{
		"overdraft":             {map[string]uint64{from: 1_000_000, otherAddr(): 1}, "2000000", "insufficient balance"},
		"fee not covered":       {map[string]uint64{from: 1_000_000, otherAddr(): 1}, "999999", "insufficient balance"},
		"dust left behind":      {map[string]uint64{from: 2_000_000, otherAddr(): 1}, "1200000", "rent-exempt"},
		"new account too small": {map[string]uint64{from: 5_000_000, otherAddr(): 0}, "100", "does not exist yet"},
	}
	for name, c := range cases {
		node := &fakeSolana{fee: 5000, balances: c.balances}
		code, body := prepare(t, solServer(t, node), pubHex, otherAddr(), c.amount)
		if code != http.StatusBadRequest || !strings.Contains(body["error"].(string), c.want) {
			t.Errorf("%s: got %d %v, want 400 mentioning %q", name, code, body["error"], c.want)
		}
	}
	// Sending everything but the fee is fine.
	node := &fakeSolana{fee: 5000, balances: map[string]uint64{from: 1_000_000, otherAddr(): 1_000_000}}
	if code, body := prepare(t, solServer(t, node), pubHex, otherAddr(), "995000"); code != http.StatusOK {
		t.Errorf("sweeping the balance less the fee was refused: %v", body["error"])
	}
}

func TestSolanaPrepareValidatesItsInputs(t *testing.T) {
	_, pubHex, _ := solKey(t)
	s := solServer(t, &fakeSolana{})
	for name, c := range map[string][3]string{
		"short pubkey":   {"abcd", otherAddr(), "1"},
		"bad dest":       {pubHex, "not-an-address", "1"},
		"zero amount":    {pubHex, otherAddr(), "0"},
		"negative":       {pubHex, otherAddr(), "-5"},
		"fractional":     {pubHex, otherAddr(), "1.5"},
		"send to itself": {pubHex, base58.Encode(mustHex(t, pubHex)), "100"},
	} {
		if code, _ := prepare(t, s, c[0], c[1], c[2]); code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, code)
		}
	}
}

func mustHex(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSolanaWithoutANodeIsUnavailableNotBroken(t *testing.T) {
	t.Setenv("SOLANA_RPC_URL", "")
	s := &server{signerRouter: chains.NewSignerRouter()}
	_, pubHex, _ := solKey(t)
	if code, _ := prepare(t, s, pubHex, otherAddr(), "1"); code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", code)
	}
}

// The signature a threshold ceremony returns is an ordinary Ed25519
// signature, so an ordinary key stands in for it here. What is under test is
// that finalize produces the wire format a validator parses and refuses a
// signature that does not belong.
func TestSolanaFinalizeAssemblesAWireTransactionAndRelaysIt(t *testing.T) {
	priv, pubHex, from := solKey(t)
	node := &fakeSolana{fee: 5000, balances: map[string]uint64{from: 5_000_000_000, otherAddr(): 1_000_000}}
	s := solServer(t, node)
	_, prep := prepare(t, s, pubHex, otherAddr(), "1000000000")
	msgHex := prep["message_hex"].(string)
	msg := mustHex(t, msgHex)
	sig := ed25519.Sign(priv, msg)

	code, body := post(t, s.handleSolanaFinalize, map[string]interface{}{
		"message_hex": msgHex, "signature_hex": hex.EncodeToString(sig), "broadcast": true})
	if code != http.StatusOK || body["broadcast"] != true {
		t.Fatalf("finalize returned %d: %v", code, body)
	}
	tx := node.sentTx
	if tx[0] != 1 || !bytes.Equal(tx[1:65], sig) || !bytes.Equal(tx[65:], msg) {
		t.Fatal("the relayed transaction is not <1><signature><message>")
	}
	pub := ed25519.PublicKey(mustHex(t, pubHex))
	if !ed25519.Verify(pub, tx[65:], tx[1:65]) {
		t.Fatal("a validator would reject the relayed transaction's signature")
	}
	if body["signature"] != base58.Encode(sig) {
		t.Errorf("transaction id %v is not the base58 signature", body["signature"])
	}
}

func TestSolanaFinalizeRefusesASignatureThatIsNotTheFeePayers(t *testing.T) {
	_, pubHex, from := solKey(t)
	node := &fakeSolana{fee: 5000, balances: map[string]uint64{from: 5_000_000_000, otherAddr(): 1_000_000}}
	s := solServer(t, node)
	_, prep := prepare(t, s, pubHex, otherAddr(), "1000000000")
	stranger, _, _ := solKey(t)
	msgHex := prep["message_hex"].(string)

	for name, sig := range map[string][]byte{
		"another key":   ed25519.Sign(stranger, mustHex(t, msgHex)),
		"wrong message": ed25519.Sign(stranger, []byte("something else")),
		"short":         make([]byte, 10),
	} {
		code, _ := post(t, s.handleSolanaFinalize, map[string]interface{}{
			"message_hex": msgHex, "signature_hex": hex.EncodeToString(sig), "broadcast": true})
		if code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, code)
		}
	}
	if node.sentTx != nil {
		t.Fatal("a transaction with a bad signature reached the node")
	}
}

func TestSolanaFinalizeReturnsTheBytesWhenTheNodeRefuses(t *testing.T) {
	priv, pubHex, from := solKey(t)
	node := &fakeSolana{fee: 5000, sendErr: "Blockhash not found", balances: map[string]uint64{from: 5_000_000_000, otherAddr(): 1_000_000}}
	s := solServer(t, node)
	_, prep := prepare(t, s, pubHex, otherAddr(), "1000000000")
	msgHex := prep["message_hex"].(string)
	sig := ed25519.Sign(priv, mustHex(t, msgHex))
	code, body := post(t, s.handleSolanaFinalize, map[string]interface{}{
		"message_hex": msgHex, "signature_hex": hex.EncodeToString(sig), "broadcast": true})
	if code != http.StatusBadGateway || body["raw_tx_base64"] == nil || !strings.Contains(body["error"].(string), "Blockhash not found") {
		t.Fatalf("got %d %v", code, body)
	}
}

func TestSolanaStatusReportsNotFoundAndFailureDistinctly(t *testing.T) {
	sig := base58.Encode(bytes.Repeat([]byte{3}, 64))
	get := func(resp interface{}) map[string]interface{} {
		s := solServer(t, &fakeSolana{statusResp: resp})
		rec := httptest.NewRecorder()
		s.handleSolanaStatus(rec, httptest.NewRequest(http.MethodGet, "/solana/status?signature="+sig, nil))
		var out map[string]interface{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	if out := get(map[string]interface{}{"value": []interface{}{nil}}); out["found"] != false {
		t.Errorf("an unknown signature must be found=false, got %v", out)
	}
	ok := get(map[string]interface{}{"value": []interface{}{map[string]interface{}{"slot": 9, "confirmations": nil, "err": nil, "confirmationStatus": "finalized"}}})
	if ok["found"] != true || ok["confirmation_status"] != "finalized" || ok["err"] != nil {
		t.Errorf("finalized success = %v", ok)
	}
	bad := get(map[string]interface{}{"value": []interface{}{map[string]interface{}{"slot": 9, "err": map[string]interface{}{"InstructionError": []interface{}{0, "Custom"}}, "confirmationStatus": "confirmed"}}})
	if bad["err"] == nil {
		t.Errorf("a transaction that ran and failed must carry its error, got %v", bad)
	}
}

func TestSolanaAddressIsThePublicKey(t *testing.T) {
	_, pubHex, addr := solKey(t)
	s := &server{signerRouter: chains.NewSignerRouter()}
	rec := httptest.NewRecorder()
	s.handleSolanaAddresses(rec, httptest.NewRequest(http.MethodGet, "/solana/addresses?pubkey="+pubHex, nil))
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["address"] != addr {
		t.Fatalf("got %v want %s", out["address"], addr)
	}
}
