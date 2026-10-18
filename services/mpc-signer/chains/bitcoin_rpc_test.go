package chains

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeNode answers JSON-RPC calls with canned results, so the client's
// decoding and arithmetic can be tested without a Bitcoin node.
func fakeNode(t *testing.T, results map[string]interface{}) *BitcoinRPC {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		result, ok := results[req.Method]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": nil,
				"error":  map[string]interface{}{"code": -32601, "message": "Method not found"},
			})
			return
		}
		if errText, isErr := result.(error); isErr {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": nil,
				"error":  map[string]interface{}{"code": -26, "message": errText.Error()},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"result": result, "error": nil})
	}))
	t.Cleanup(srv.Close)
	return &BitcoinRPC{url: srv.URL, client: srv.Client()}
}

// The conversion nobody thinks is worth testing until an amount is one
// satoshi out and a transaction will not balance. 0.1 is not representable
// in binary floating point, so the float route gets this wrong.
func TestBTCAmountsConvertToSatoshisExactly(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{"0.00000001", 1},
		{"0.1", 10_000_000},
		{"0.29", 29_000_000},
		{"1", 100_000_000},
		{"1.0", 100_000_000},
		{"21000000", 2_100_000_000_000_000},
		{"0.00000546", 546},
		{"-0.5", -50_000_000},
	}
	for _, tc := range cases {
		got, err := btcToSatoshis(tc.in)
		if err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s converted to %d sats, want %d", tc.in, got, tc.want)
		}
	}

	// More precision than a satoshi is a node saying something impossible.
	// Rounding it would produce an amount that does not match the chain.
	if _, err := btcToSatoshis("0.000000001"); err == nil {
		t.Error("accepted an amount with sub-satoshi precision")
	}
	if _, err := btcToSatoshis("not-a-number"); err == nil {
		t.Error("accepted a non-numeric amount")
	}
}

func TestListUnspentReportsAmountsAndConfirmations(t *testing.T) {
	rpc := fakeNode(t, map[string]interface{}{
		"scantxoutset": map[string]interface{}{
			"success": true,
			"height":  105,
			"unspents": []map[string]interface{}{
				{"txid": txidN(1), "vout": 0, "scriptPubKey": "0014" + strings.Repeat("11", 20),
					"amount": 0.5, "height": 105},
				{"txid": txidN(2), "vout": 3, "scriptPubKey": "0014" + strings.Repeat("22", 20),
					"amount": 0.00000546, "height": 100},
			},
		},
	})

	utxos, err := rpc.ListUnspentForAddress(context.Background(), "bcrt1qexample")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(utxos) != 2 {
		t.Fatalf("got %d outputs, want 2", len(utxos))
	}
	if utxos[0].Amount != 50_000_000 {
		t.Errorf("first amount %d sats, want 50000000", utxos[0].Amount)
	}
	// Mined in the tip block, so one confirmation -- not zero.
	if utxos[0].Confirmations != 1 {
		t.Errorf("an output in the tip block has %d confirmations, want 1", utxos[0].Confirmations)
	}
	if utxos[1].Confirmations != 6 {
		t.Errorf("an output 5 blocks back has %d confirmations, want 6", utxos[1].Confirmations)
	}
	if utxos[1].Amount != 546 {
		t.Errorf("second amount %d sats, want 546", utxos[1].Amount)
	}
}

// A scan that did not complete returns success:false with a partial list.
// Treating that as "these are all your coins" would select from an
// incomplete set and could report insufficient funds against a full wallet.
func TestAnIncompleteScanIsAnError(t *testing.T) {
	rpc := fakeNode(t, map[string]interface{}{
		"scantxoutset": map[string]interface{}{"success": false, "height": 100, "unspents": []interface{}{}},
	})
	if _, err := rpc.ListUnspentForAddress(context.Background(), "bcrt1qexample"); err == nil {
		t.Fatal("accepted an incomplete UTXO scan as a complete answer")
	}
}

func TestFeeRateConvertsFromBTCPerKilovbyte(t *testing.T) {
	// 0.00002 BTC/kvB = 2000 sat/kvB = 2 sat/vB.
	rpc := fakeNode(t, map[string]interface{}{
		"estimatesmartfee": map[string]interface{}{"feerate": 0.00002},
	})
	rate, err := rpc.EstimateFeeRate(context.Background(), 6, 5)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if rate != 2 {
		t.Errorf("fee rate %d sat/vB, want 2", rate)
	}
}

// A node with no fee history returns no estimate at all -- normal on
// regtest, on a freshly synced node, and in quiet periods. Failing there
// would make the platform unusable on the networks it gets tested on.
func TestNoFeeEstimateFallsBackRatherThanFailing(t *testing.T) {
	rpc := fakeNode(t, map[string]interface{}{
		"estimatesmartfee": map[string]interface{}{
			"errors": []string{"Insufficient data or no feerate found"},
		},
	})
	rate, err := rpc.EstimateFeeRate(context.Background(), 6, 7)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if rate != 7 {
		t.Errorf("fell back to %d sat/vB, want the supplied fallback of 7", rate)
	}
}

// Nothing relays below one satoshi per vbyte, whatever the estimator says.
func TestFeeRateNeverGoesBelowOne(t *testing.T) {
	rpc := fakeNode(t, map[string]interface{}{
		"estimatesmartfee": map[string]interface{}{"feerate": 0.000001}, // 0.1 sat/vB
	})
	rate, err := rpc.EstimateFeeRate(context.Background(), 6, 5)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if rate != 1 {
		t.Errorf("fee rate %d sat/vB, want a floor of 1", rate)
	}
}

// A rejection has to reach the caller with the node's own reason. "Broadcast
// failed" sends somebody to look at the network; "min relay fee not met" or
// "missing inputs" says exactly what is wrong.
func TestABroadcastRejectionCarriesTheNodesReason(t *testing.T) {
	rpc := fakeNode(t, map[string]interface{}{
		"sendrawtransaction": errNode("min relay fee not met, 100 < 141"),
	})
	signer := &BitcoinSigner{rpc: rpc}
	_, err := signer.BroadcastTransaction(context.Background(), []byte("0200000001abcd"))
	if err == nil {
		t.Fatal("a rejected broadcast reported success")
	}
	if !strings.Contains(err.Error(), "min relay fee") {
		t.Errorf("the node's reason was lost: %v", err)
	}
}

func TestBroadcastReturnsTheTxid(t *testing.T) {
	rpc := fakeNode(t, map[string]interface{}{
		"sendrawtransaction": txidN(7),
	})
	signer := &BitcoinSigner{rpc: rpc}
	txid, err := signer.BroadcastTransaction(context.Background(), []byte("0200000001abcd"))
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if txid != txidN(7) {
		t.Errorf("txid %q, want %q", txid, txidN(7))
	}
}

// Without a node the platform can still derive addresses and sign; it just
// cannot relay. Saying so plainly is the difference between a deployment
// mistake and an apparent outage.
func TestBroadcastWithoutANodeSaysSo(t *testing.T) {
	signer := &BitcoinSigner{}
	_, err := signer.BroadcastTransaction(context.Background(), []byte("0200000001abcd"))
	if err == nil {
		t.Fatal("broadcast succeeded with no node configured")
	}
	if !strings.Contains(err.Error(), "BITCOIN_RPC_URL") {
		t.Errorf("the error does not say how to configure a node: %v", err)
	}
}

type nodeError string

func (e nodeError) Error() string { return string(e) }

func errNode(msg string) error { return nodeError(msg) }
