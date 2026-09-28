package ethrpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Talking to a node, against a fake one.
//
// The behaviour worth pinning is not the happy path -- it is how the three
// failure modes are told apart. A pending transaction has no receipt, and
// a client that reported that as an error would make the confirmation loop
// treat every unmined transaction as an outage.

func serve(t *testing.T, handler func(method string, params []interface{}) (interface{}, *rpcError)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		result, rpcErr := handler(req.Method, req.Params)
		w.Header().Set("Content-Type", "application/json")
		out := map[string]interface{}{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			out["error"] = rpcErr
		} else {
			out["result"] = result
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)

	c, err := Dial(srv.URL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	return c
}

func TestQuantitiesAreParsedFromHex(t *testing.T) {
	c := serve(t, func(method string, _ []interface{}) (interface{}, *rpcError) {
		switch method {
		case "eth_chainId":
			return "0x67932", nil // 424242
		case "eth_getBalance":
			return "0xde0b6b3a7640000", nil // 1 ETH
		case "eth_gasPrice":
			return "0x4a817c800", nil // 20 gwei
		case "eth_getTransactionCount":
			return "0x2a", nil // 42
		}
		return nil, &rpcError{Code: -32601, Message: "no such method"}
	})
	ctx := context.Background()

	if id, err := c.ChainID(ctx); err != nil || id.Int64() != 424242 {
		t.Errorf("chain id %v, %v", id, err)
	}
	if b, err := c.BalanceAt(ctx, "0x0"); err != nil || b.String() != "1000000000000000000" {
		t.Errorf("balance %v, %v", b, err)
	}
	if p, err := c.SuggestGasPrice(ctx); err != nil || p.Int64() != 20000000000 {
		t.Errorf("gas price %v, %v", p, err)
	}
	if n, err := c.PendingNonceAt(ctx, "0x0"); err != nil || n != 42 {
		t.Errorf("nonce %d, %v", n, err)
	}
}

// The nonce must be asked for against pending state. Counting only mined
// transactions would reuse the nonce of one still in the mempool, which
// replaces it rather than following it.
func TestTheNonceIsAskedForAgainstPendingState(t *testing.T) {
	var sawBlock string
	c := serve(t, func(method string, params []interface{}) (interface{}, *rpcError) {
		if method == "eth_getTransactionCount" && len(params) == 2 {
			sawBlock, _ = params[1].(string)
		}
		return "0x1", nil
	})

	if _, err := c.PendingNonceAt(context.Background(), "0xabc"); err != nil {
		t.Fatalf("nonce: %v", err)
	}
	if sawBlock != "pending" {
		t.Errorf("asked for the nonce at %q, want \"pending\"", sawBlock)
	}
}

// A null result is "no such thing", not a failure.
func TestAMissingReceiptIsNotFoundRatherThanAnError(t *testing.T) {
	c := serve(t, func(string, []interface{}) (interface{}, *rpcError) { return nil, nil })

	_, err := c.TransactionReceipt(context.Background(), "0xdead")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("a pending transaction reported %v, want ErrNotFound", err)
	}
}

func TestAReceiptIsRead(t *testing.T) {
	c := serve(t, func(string, []interface{}) (interface{}, *rpcError) {
		return map[string]string{
			"status": "0x1", "blockNumber": "0x10", "gasUsed": "0x5208",
			"transactionHash": "0xabc",
		}, nil
	})

	r, err := c.TransactionReceipt(context.Background(), "0xabc")
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if r.Status != 1 || r.BlockNumber != 16 || r.GasUsed != 21000 {
		t.Errorf("receipt read as %+v", r)
	}
}

// A mined transaction that reverted is not a success. It is on chain, it
// paid its gas, and it moved nothing -- reporting it as confirmed would
// tell a customer their money arrived when it did not.
func TestARevertedTransactionIsDistinguishedFromASuccessfulOne(t *testing.T) {
	c := serve(t, func(string, []interface{}) (interface{}, *rpcError) {
		return map[string]string{"status": "0x0", "blockNumber": "0x10", "gasUsed": "0x5208"}, nil
	})

	r, err := c.TransactionReceipt(context.Background(), "0xabc")
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if r.Status != 0 {
		t.Error("a reverted transaction was reported as successful")
	}
}

// An error from the node is an error, not a silent zero.
func TestANodeErrorIsSurfaced(t *testing.T) {
	c := serve(t, func(string, []interface{}) (interface{}, *rpcError) {
		return nil, &rpcError{Code: -32000, Message: "insufficient funds for gas * price + value"}
	})

	_, err := c.SendRawTransaction(context.Background(), "0xdeadbeef")
	if err == nil {
		t.Fatal("a rejected broadcast was reported as success")
	}
	// The node's own words, kept. "Broadcast failed" is not actionable;
	// "insufficient funds" is.
	if !contains(err.Error(), "insufficient funds") {
		t.Errorf("the node's reason was lost: %v", err)
	}
}

func TestAnUnreachableNodeIsReportedAsSuch(t *testing.T) {
	c, err := Dial("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if _, err := c.ChainID(context.Background()); err == nil {
		t.Fatal("an unreachable node returned success")
	}
}

func TestBroadcastPrefixesTheHexIfNeeded(t *testing.T) {
	var sent string
	c := serve(t, func(_ string, params []interface{}) (interface{}, *rpcError) {
		sent, _ = params[0].(string)
		return "0xhash", nil
	})

	if _, err := c.SendRawTransaction(context.Background(), "deadbeef"); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if sent != "0xdeadbeef" {
		t.Errorf("sent %q, want 0xdeadbeef", sent)
	}
}

func TestDialRefusesSomethingThatIsNotAnHttpUrl(t *testing.T) {
	for _, u := range []string{"", "ws://localhost:8546", "localhost:8545", "ftp://x"} {
		if _, err := Dial(u); err == nil {
			t.Errorf("%q was accepted as an RPC endpoint", u)
		}
	}
}

func TestAnHttpErrorIsSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream is having a bad day", http.StatusBadGateway)
	}))
	defer srv.Close()

	c, _ := Dial(srv.URL)
	if _, err := c.ChainID(context.Background()); err == nil {
		t.Fatal("HTTP 502 was treated as success")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
