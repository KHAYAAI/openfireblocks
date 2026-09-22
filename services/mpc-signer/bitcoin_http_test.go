package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcutil"

	"forge-crypto/mpc-signer/chains"
)

// A stand-in bitcoind. The handlers are what is under test; the node is
// scenery, so it answers with whatever the case needs.
type fakeBitcoind struct {
	unspents  []map[string]interface{}
	height    int64
	sendErr   string
	sentRaw   string
	sentTxID  string
	scanCalls int
}

func (f *fakeBitcoind) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string        `json:"method"`
			Params []interface{} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		reply := func(result interface{}, errMsg string) {
			w.Header().Set("Content-Type", "application/json")
			body := map[string]interface{}{"result": result, "error": nil}
			if errMsg != "" {
				body["result"] = nil
				body["error"] = map[string]interface{}{"code": -26, "message": errMsg}
			}
			_ = json.NewEncoder(w).Encode(body)
		}

		switch req.Method {
		case "scantxoutset":
			f.scanCalls++
			reply(map[string]interface{}{
				"success": true, "height": f.height, "unspents": f.unspents,
			}, "")
		case "estimatesmartfee":
			reply(map[string]interface{}{"errors": []string{"Insufficient data"}}, "")
		case "sendrawtransaction":
			if f.sendErr != "" {
				reply(nil, f.sendErr)
				return
			}
			if len(req.Params) > 0 {
				f.sentRaw, _ = req.Params[0].(string)
			}
			reply(f.sentTxID, "")
		default:
			reply(nil, "Method not found")
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// newServerWithNode builds the real server wiring against a fake node.
func newServerWithNode(t *testing.T, url string) *server {
	t.Helper()
	t.Setenv("BITCOIN_RPC_URL", url)
	return &server{signerRouter: chains.NewSignerRouter()}
}

func post(t *testing.T, h http.HandlerFunc, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw)))

	var decoded map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("the handler returned a body that is not JSON (%d): %s", rec.Code, rec.Body.String())
	}
	return rec.Code, decoded
}

func btcTestKey(t *testing.T) (*btcec.PrivateKey, string) {
	t.Helper()
	key, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return key, hex.EncodeToString(key.PubKey().SerializeCompressed())
}

// p2wpkhScript is the scriptPubKey a node would report for the key's segwit
// address: OP_0 followed by the 20-byte hash of the compressed key.
func p2wpkhScript(key *btcec.PrivateKey) string {
	return "0014" + hex.EncodeToString(btcutil.Hash160(key.PubKey().SerializeCompressed()))
}

func unspent(txid string, vout int, btc float64, script string, height int64) map[string]interface{} {
	return map[string]interface{}{
		"txid": txid, "vout": vout, "scriptPubKey": script, "amount": btc, "height": height,
	}
}

const someTxid = "0000000000000000000000000000000000000000000000000000000000000001"

func TestPrepareScansBothOfTheKeysAddresses(t *testing.T) {
	key, pub := btcTestKey(t)
	node := &fakeBitcoind{
		height:   200,
		unspents: []map[string]interface{}{unspent(someTxid, 0, 0.5, p2wpkhScript(key), 190)},
	}
	s := newServerWithNode(t, node.start(t))
	dest, _, _ := chains.BitcoinAddressesForPubKey(mustOtherPubKey(t), "regtest")

	code, body := post(t, s.handleBitcoinPrepare, map[string]interface{}{
		"network": "regtest", "pubkey_hex": pub,
		"destination": dest, "amount": 10_000_000,
	})
	if code != http.StatusOK {
		t.Fatalf("prepare returned %d: %v", code, body["error"])
	}
	if node.scanCalls != 1 {
		t.Errorf("the node was scanned %d times; both addresses should go in one pass", node.scanCalls)
	}
	addrs, _ := body["addresses"].([]interface{})
	if len(addrs) != 2 {
		t.Errorf("scanned %v, want both the segwit and legacy addresses", body["addresses"])
	}
	if body["balance"].(float64) != 50_000_000 {
		t.Errorf("balance %v, want 50000000", body["balance"])
	}
	plan, _ := body["plan"].(map[string]interface{})
	if sh, _ := plan["sighashes"].([]interface{}); len(sh) != 1 {
		t.Errorf("got %d sighashes, want 1", len(sh))
	}
}

// Insufficient funds is the caller's problem, and the response has to say so
// with a 4xx and the balance -- not a 500 that reads as an outage.
func TestPrepareReportsInsufficientFundsAsAClientError(t *testing.T) {
	key, pub := btcTestKey(t)
	node := &fakeBitcoind{
		height:   200,
		unspents: []map[string]interface{}{unspent(someTxid, 0, 0.001, p2wpkhScript(key), 190)},
	}
	s := newServerWithNode(t, node.start(t))
	dest, _, _ := chains.BitcoinAddressesForPubKey(mustOtherPubKey(t), "regtest")

	code, body := post(t, s.handleBitcoinPrepare, map[string]interface{}{
		"network": "regtest", "pubkey_hex": pub,
		"destination": dest, "amount": 50_000_000,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("prepare returned %d, want 400: %v", code, body)
	}
	if body["balance"].(float64) != 100_000 {
		t.Errorf("the response does not report the balance: %v", body["balance"])
	}
}

// Without a node the route cannot work, and saying which variable is missing
// turns an apparent outage into a deployment fix.
func TestPrepareWithoutANodeIsUnavailableNotBroken(t *testing.T) {
	t.Setenv("BITCOIN_RPC_URL", "")
	s := &server{signerRouter: chains.NewSignerRouter()}

	code, body := post(t, s.handleBitcoinPrepare, map[string]interface{}{
		"network": "regtest", "pubkey_hex": "02" + strings.Repeat("11", 32),
	})
	if code != http.StatusServiceUnavailable {
		t.Fatalf("returned %d, want 503", code)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "BITCOIN_RPC_URL") {
		t.Errorf("the error does not name the missing configuration: %v", body["error"])
	}
}

// The whole path a customer's spend takes through this service: prepare,
// sign each digest the way the committee would, finalize, broadcast.
func TestPrepareSignFinalizeAndBroadcast(t *testing.T) {
	key, pub := btcTestKey(t)
	node := &fakeBitcoind{
		height:   200,
		unspents: []map[string]interface{}{unspent(someTxid, 0, 0.5, p2wpkhScript(key), 190)},
		sentTxID: "feed" + strings.Repeat("0", 60),
	}
	s := newServerWithNode(t, node.start(t))
	dest, _, _ := chains.BitcoinAddressesForPubKey(mustOtherPubKey(t), "regtest")

	code, prepared := post(t, s.handleBitcoinPrepare, map[string]interface{}{
		"network": "regtest", "pubkey_hex": pub,
		"destination": dest, "amount": 10_000_000,
	})
	if code != http.StatusOK {
		t.Fatalf("prepare returned %d: %v", code, prepared["error"])
	}

	plan := prepared["plan"].(map[string]interface{})
	sighashes := plan["sighashes"].([]interface{})
	sigs := make([]map[string]string, 0, len(sighashes))
	for _, digestAny := range sighashes {
		digest, err := hex.DecodeString(digestAny.(string))
		if err != nil {
			t.Fatalf("sighash: %v", err)
		}
		sig, err := key.Sign(digest)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		sigs = append(sigs, map[string]string{
			"r": sig.R.Text(16), "s": sig.S.Text(16),
		})
	}

	code, final := post(t, s.handleBitcoinFinalize, map[string]interface{}{
		"plan": plan, "signatures": sigs, "pubkey_hex": pub, "broadcast": true,
	})
	if code != http.StatusOK {
		t.Fatalf("finalize returned %d: %v", code, final["error"])
	}
	if final["broadcast"] != true {
		t.Error("the transaction was not broadcast")
	}
	if final["broadcast_txid"] != node.sentTxID {
		t.Errorf("broadcast txid %v, want %v", final["broadcast_txid"], node.sentTxID)
	}
	if node.sentRaw != final["raw_tx_hex"] {
		t.Error("the bytes sent to the node are not the bytes returned to the caller")
	}
}

// A node rejection must not read as "the transaction is malformed", and the
// caller must get the bytes back so a retry does not need another ceremony.
func TestABroadcastRejectionReturnsTheTransactionForRetry(t *testing.T) {
	key, pub := btcTestKey(t)
	node := &fakeBitcoind{
		height:   200,
		unspents: []map[string]interface{}{unspent(someTxid, 0, 0.5, p2wpkhScript(key), 190)},
		sendErr:  "txn-mempool-conflict",
	}
	s := newServerWithNode(t, node.start(t))
	dest, _, _ := chains.BitcoinAddressesForPubKey(mustOtherPubKey(t), "regtest")

	_, prepared := post(t, s.handleBitcoinPrepare, map[string]interface{}{
		"network": "regtest", "pubkey_hex": pub, "destination": dest, "amount": 10_000_000,
	})
	plan := prepared["plan"].(map[string]interface{})

	var sigs []map[string]string
	for _, digestAny := range plan["sighashes"].([]interface{}) {
		digest, _ := hex.DecodeString(digestAny.(string))
		sig, _ := key.Sign(digest)
		sigs = append(sigs, map[string]string{"r": sig.R.Text(16), "s": sig.S.Text(16)})
	}

	code, final := post(t, s.handleBitcoinFinalize, map[string]interface{}{
		"plan": plan, "signatures": sigs, "pubkey_hex": pub, "broadcast": true,
	})
	if code != http.StatusBadGateway {
		t.Fatalf("returned %d, want 502: the node refused a transaction this service built correctly", code)
	}
	if final["raw_tx_hex"] == nil || final["txid"] == nil {
		t.Error("a rejected broadcast did not return the transaction, so retrying would need another ceremony")
	}
	if msg, _ := final["error"].(string); !strings.Contains(msg, "mempool-conflict") {
		t.Errorf("the node's reason was lost: %v", final["error"])
	}
}

// A signature from the wrong key produces a transaction that serialises
// perfectly and spends nothing. Assembly runs the script engine, so it is
// caught here rather than at the node.
func TestFinalizeRefusesASignatureFromTheWrongKey(t *testing.T) {
	key, pub := btcTestKey(t)
	wrongKey, _ := btcTestKey(t)
	node := &fakeBitcoind{
		height:   200,
		unspents: []map[string]interface{}{unspent(someTxid, 0, 0.5, p2wpkhScript(key), 190)},
	}
	s := newServerWithNode(t, node.start(t))
	dest, _, _ := chains.BitcoinAddressesForPubKey(mustOtherPubKey(t), "regtest")

	_, prepared := post(t, s.handleBitcoinPrepare, map[string]interface{}{
		"network": "regtest", "pubkey_hex": pub, "destination": dest, "amount": 10_000_000,
	})
	plan := prepared["plan"].(map[string]interface{})

	var sigs []map[string]string
	for _, digestAny := range plan["sighashes"].([]interface{}) {
		digest, _ := hex.DecodeString(digestAny.(string))
		sig, _ := wrongKey.Sign(digest)
		sigs = append(sigs, map[string]string{"r": sig.R.Text(16), "s": sig.S.Text(16)})
	}

	code, _ := post(t, s.handleBitcoinFinalize, map[string]interface{}{
		"plan": plan, "signatures": sigs, "pubkey_hex": pub, "broadcast": true,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("returned %d, want 400", code)
	}
	if node.sentRaw != "" {
		t.Error("an invalid transaction was sent to the node")
	}
}

func mustOtherPubKey(t *testing.T) string {
	t.Helper()
	_, pub := btcTestKey(t)
	return pub
}
