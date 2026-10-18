package main

import (
	"bytes"
	"crypto/ed25519"
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

// A stand-in node holding accounts as raw bytes, the way the chain does.
type fakeSPLNode struct {
	accounts map[string]fakeAccount
	lamports map[string]uint64
	fee      uint64
	rent     uint64
	sentTx   []byte
}

type fakeAccount struct {
	owner string
	data  []byte
}

func (f *fakeSPLNode) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply := func(result interface{}) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": result})
		}
		var addr string
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params[0], &addr)
		}
		switch req.Method {
		case "getAccountInfo":
			a, ok := f.accounts[addr]
			if !ok {
				reply(map[string]interface{}{"value": nil})
				return
			}
			reply(map[string]interface{}{"value": map[string]interface{}{
				"owner": a.owner, "lamports": 2039280, "data": []string{base64.StdEncoding.EncodeToString(a.data), "base64"}}})
		case "getBalance":
			reply(map[string]interface{}{"value": f.lamports[addr]})
		case "getLatestBlockhash":
			reply(map[string]interface{}{"value": map[string]interface{}{
				"blockhash": base58.Encode(bytes.Repeat([]byte{7}, 32)), "lastValidBlockHeight": 99}})
		case "getFeeForMessage":
			reply(map[string]interface{}{"value": f.fee})
		case "getMinimumBalanceForRentExemption":
			reply(f.rent)
		case "sendTransaction":
			var b64 string
			_ = json.Unmarshal(req.Params[0], &b64)
			f.sentTx, _ = base64.StdEncoding.DecodeString(b64)
			reply(base58.Encode(f.sentTx[1:65]))
		default:
			t.Errorf("unexpected method %s", req.Method)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

const testMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

func mintBytes(decimals byte) []byte {
	d := make([]byte, chains.SPLMintSize)
	binary.LittleEndian.PutUint64(d[36:44], 1_000_000_000_000)
	d[44], d[45] = decimals, 1
	return d
}

func tokenAccountBytes(mint, owner string, amount uint64, state byte) []byte {
	d := make([]byte, chains.SPLTokenAccountSize)
	copy(d[0:32], base58.Decode(mint))
	copy(d[32:64], base58.Decode(owner))
	binary.LittleEndian.PutUint64(d[64:72], amount)
	d[108] = state
	return d
}

// A key holding 10 USDC and 0.01 SOL; a recipient wallet with no USDC account.
func splFixture(t *testing.T) (*server, *fakeSPLNode, ed25519.PrivateKey, string, string, string) {
	t.Helper()
	priv, pubHex, from := solKey(t)
	to := otherAddr()
	srcATA, _ := chains.AssociatedTokenAddress(from, testMint)
	node := &fakeSPLNode{fee: 5000, rent: 2_039_280,
		lamports: map[string]uint64{from: 10_000_000},
		accounts: map[string]fakeAccount{
			testMint: {chains.TokenProgramID, mintBytes(6)},
			srcATA:   {chains.TokenProgramID, tokenAccountBytes(testMint, from, 10_000_000, 1)},
		}}
	t.Setenv("SOLANA_RPC_URL", node.start(t))
	return &server{signerRouter: chains.NewSignerRouter()}, node, priv, pubHex, from, to
}

func tokenPrepare(t *testing.T, s *server, body map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	return post(t, s.handleSolanaTokenPrepare, body)
}

func TestSolanaTokenTransferPreparesSignsAndRelays(t *testing.T) {
	s, node, priv, pubHex, from, to := splFixture(t)
	code, body := tokenPrepare(t, s, map[string]interface{}{
		"pubkey_hex": pubHex, "destination": to, "mint": testMint, "amount": "2500000", "decimals": 6})
	if code != http.StatusOK {
		t.Fatalf("prepare returned %d: %v", code, body["error"])
	}
	wantDst, _ := chains.AssociatedTokenAddress(to, testMint)
	if body["destination_token_account"] != wantDst || body["creates_token_account"] != true || body["rent"] != "2039280" {
		t.Fatalf("prepare = %v", body)
	}
	msg := mustHex(t, body["message_hex"].(string))
	if payer, _ := chains.SolanaMessageFeePayer(msg); base58.Encode(payer) != from {
		t.Fatal("the key must pay the fee")
	}

	// The ceremony's output is an Ed25519 signature over exactly these bytes;
	// finalize is the same endpoint native SOL uses.
	sig := ed25519.Sign(priv, msg)
	code, fin := post(t, s.handleSolanaFinalize, map[string]interface{}{
		"message_hex": body["message_hex"], "signature_hex": hex.EncodeToString(sig), "broadcast": true})
	if code != http.StatusOK || fin["broadcast"] != true {
		t.Fatalf("finalize returned %d: %v", code, fin)
	}
	if !bytes.Equal(node.sentTx[65:], msg) || !bytes.Equal(node.sentTx[1:65], sig) {
		t.Fatal("the relayed transaction is not the prepared message with its signature")
	}
}

func TestSolanaTokenPrepareRefusesWhatWouldFailOrMislead(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(n *fakeSPLNode, from, to string, body map[string]interface{})
		want   string
	}{
		{"registry decimals disagree with the chain", func(n *fakeSPLNode, _, _ string, b map[string]interface{}) { b["decimals"] = 18 },
			"6 decimals on chain"},
		{"decimals missing", func(n *fakeSPLNode, _, _ string, b map[string]interface{}) { delete(b, "decimals") },
			"decimals is required"},
		{"Token-2022 mint", func(n *fakeSPLNode, _, _ string, _ map[string]interface{}) {
			n.accounts[testMint] = fakeAccount{chains.Token2022ProgramID, mintBytes(6)}
		}, "Token-2022"},
		{"not a mint", func(n *fakeSPLNode, _, _ string, _ map[string]interface{}) {
			n.accounts[testMint] = fakeAccount{chains.SystemProgramID, nil}
		}, "not a token mint"},
		{"more tokens than held", func(_ *fakeSPLNode, _, _ string, b map[string]interface{}) { b["amount"] = "10000001" },
			"insufficient token balance"},
		{"no SOL for the fee and the new account's rent", func(n *fakeSPLNode, from, _ string, _ map[string]interface{}) {
			n.lamports[from] = 1_000_000
		}, "insufficient SOL"},
		{"destination is a token account, not a wallet", func(n *fakeSPLNode, _, to string, _ map[string]interface{}) {
			n.accounts[to] = fakeAccount{chains.TokenProgramID, tokenAccountBytes(testMint, otherAddr(), 0, 1)}
		}, "not a wallet"},
		{"frozen source", func(n *fakeSPLNode, from, _ string, _ map[string]interface{}) {
			ata, _ := chains.AssociatedTokenAddress(from, testMint)
			n.accounts[ata] = fakeAccount{chains.TokenProgramID, tokenAccountBytes(testMint, from, 10_000_000, 2)}
		}, "frozen"},
		{"zero amount", func(_ *fakeSPLNode, _, _ string, b map[string]interface{}) { b["amount"] = "0" },
			"positive integer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, node, _, pubHex, from, to := splFixture(t)
			body := map[string]interface{}{"pubkey_hex": pubHex, "destination": to, "mint": testMint, "amount": "2500000", "decimals": 6}
			c.mutate(node, from, to, body)
			code, resp := tokenPrepare(t, s, body)
			if code != http.StatusBadRequest {
				t.Fatalf("got %d (%v), want 400", code, resp)
			}
			if msg, _ := resp["error"].(string); !strings.Contains(msg, c.want) {
				t.Fatalf("error %q does not mention %q", msg, c.want)
			}
		})
	}
}

func TestSolanaTokenPrepareToAnExistingTokenAccountChargesNoRent(t *testing.T) {
	s, node, _, pubHex, _, to := splFixture(t)
	dst, _ := chains.AssociatedTokenAddress(to, testMint)
	node.accounts[dst] = fakeAccount{chains.TokenProgramID, tokenAccountBytes(testMint, to, 0, 1)}
	code, body := tokenPrepare(t, s, map[string]interface{}{
		"pubkey_hex": pubHex, "destination": to, "mint": testMint, "amount": "1", "decimals": 6})
	if code != http.StatusOK || body["creates_token_account"] != false || body["rent"] != "0" {
		t.Fatalf("got %d: %v", code, body)
	}
}

func TestSolanaTokenMintAndBalance(t *testing.T) {
	s, _, _, _, from, _ := splFixture(t)
	rec := httptest.NewRecorder()
	s.handleSolanaTokenMint(rec, httptest.NewRequest(http.MethodGet, "/solana/token/mint?mint="+testMint, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"decimals":6`) {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	s.handleSolanaTokenBalance(rec, httptest.NewRequest(http.MethodGet, "/solana/token/balance?owner="+from+"&mint="+testMint, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"amount":"10000000"`) {
		t.Fatalf("balance: %d %s", rec.Code, rec.Body)
	}
	// A wallet with no token account holds zero; that is not an error.
	rec = httptest.NewRecorder()
	s.handleSolanaTokenBalance(rec, httptest.NewRequest(http.MethodGet, "/solana/token/balance?owner="+otherAddr()+"&mint="+testMint, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"exists":false`) || !strings.Contains(rec.Body.String(), `"amount":"0"`) {
		t.Fatalf("empty balance: %d %s", rec.Code, rec.Body)
	}
}
