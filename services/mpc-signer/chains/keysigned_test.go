package chains

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcutil"

	"forge-crypto/mpc-signer/keys"
)

// The KeySigner paths are tested against the raw-key paths, not against
// hand-computed values. For the same key and message they must produce
// byte-identical output: both are RFC 6979 deterministic and both
// normalise to low S. Anything that differs by one byte is a bug in the
// re-encoding, and the raw-key path is the one already proven against
// each chain's specification in correctness_test.go.

func equivalenceKeys(t *testing.T) []string {
	t.Helper()
	out := []string{testPrivKey}
	for i := 1; i <= 20; i++ {
		out = append(out, fmt.Sprintf("%064x", 0xabcdef*i))
	}
	return out
}

func TestEveryKeySignedChainMatchesItsRawKeyPath(t *testing.T) {
	ctx := context.Background()
	router := NewSignerRouter()
	for _, chain := range []string{"ethereum", "bitcoin", "cosmos-hub"} {
		for i, privHex := range equivalenceKeys(t) {
			key, err := keys.RawKeySignerFromHex(privHex)
			if err != nil {
				t.Fatal(err)
			}
			h := sha256.Sum256([]byte(fmt.Sprintf("%s message %d", chain, i)))

			want, err := router.Signer(chain).SignMessage(ctx, h[:], privHex)
			if err != nil {
				t.Fatalf("%s raw: %v", chain, err)
			}
			got, err := router.Signer(chain).(KeySignedChain).SignMessageWithKey(ctx, h[:], key)
			if err != nil {
				t.Fatalf("%s keyed: %v", chain, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s key %d: keyed %+v, raw %+v", chain, i, got, want)
			}

			// And the router agrees on who signed.
			rawResp, err := router.SignMultiChain(ctx, &ChainSignRequest{ChainID: chain, Message: h[:]}, privHex)
			if err != nil {
				t.Fatal(err)
			}
			keyResp, err := router.SignMultiChainWithKey(ctx, &ChainSignRequest{ChainID: chain, Message: h[:]}, key)
			if err != nil {
				t.Fatal(err)
			}
			if keyResp.From != rawResp.From || keyResp.Signature.SignatureBytes != rawResp.Signature.SignatureBytes {
				t.Fatalf("%s key %d: router keyed from=%s raw from=%s", chain, i, keyResp.From, rawResp.From)
			}
		}
	}
}

// Solana is Ed25519. A hardware-backed deployment must refuse it by name,
// never fall back to signing it some other way.
func TestSolanaIsRefusedRatherThanSignedWithSomethingElse(t *testing.T) {
	key, _ := keys.RawKeySignerFromHex(testPrivKey)
	h := sha256.Sum256([]byte("sol"))
	_, err := NewSignerRouter().SignMultiChainWithKey(context.Background(),
		&ChainSignRequest{ChainID: "solana", Message: h[:]}, key)
	if err == nil || !strings.Contains(err.Error(), "solana") {
		t.Fatalf("expected solana to be refused by name, got %v", err)
	}
}

func TestAKeySignerThatReturnsGarbageIsRefused(t *testing.T) {
	h := sha256.Sum256([]byte("x"))
	for name, sig := range map[string][]byte{
		"short":    make([]byte, 64),
		"bad V":    append(make([]byte, 64), 7),
		"too long": make([]byte, 66),
	} {
		_, err := (&EthereumSigner{}).SignMessageWithKey(context.Background(), h[:], fixedSigner{sig})
		if err == nil {
			t.Errorf("%s signature was accepted", name)
		}
	}
}

type fixedSigner struct{ sig []byte }

func (f fixedSigner) PublicKey() []byte { return nil }
func (f fixedSigner) SignDigest(context.Context, []byte) ([]byte, error) {
	return f.sig, nil
}
func (f fixedSigner) Describe() string { return "fixed test signer" }

// The Bitcoin transaction path, checked two ways: byte-identical to the
// raw-key SignTransactionInput, and accepted by btcd's own script engine
// with the standard verification flags -- which enforce low S and strict
// DER, the two things a hand-assembled signature script gets wrong.
func TestBitcoinInputSignedWithAKeySignerMatchesAndSpends(t *testing.T) {
	ctx := context.Background()
	signer := NewBitcoinSigner().(*BitcoinSigner)

	for i, privHex := range equivalenceKeys(t) {
		privBytes, _ := hex.DecodeString(privHex)
		_, pub := btcec.PrivKeyFromBytes(btcec.S256(), privBytes)
		addr, err := btcutil.NewAddressPubKeyHash(btcutil.Hash160(pub.SerializeCompressed()), &chaincfg.TestNet3Params)
		if err != nil {
			t.Fatal(err)
		}
		prevOutScript, err := txscript.PayToAddrScript(addr)
		if err != nil {
			t.Fatal(err)
		}

		rawTx, err := signer.BuildTransaction(ctx, &BitcoinSignRequest{
			Network: "testnet",
			Inputs: []BitcoinInput{
				{Txid: fmt.Sprintf("%064x", i+1), Vout: 0, Amount: 100000},
				{Txid: fmt.Sprintf("%064x", i+1000), Vout: 3, Amount: 50000},
			},
			Outputs: []BitcoinOutput{{Address: addr.EncodeAddress(), Amount: 140000}},
		})
		if err != nil {
			t.Fatal(err)
		}

		// Sign both inputs, in order, each way.
		want, got := rawTx, rawTx
		for idx := 0; idx < 2; idx++ {
			if want, err = signer.SignTransactionInput(want, idx, prevOutScript, privHex); err != nil {
				t.Fatal(err)
			}
			key, _ := keys.RawKeySignerFromHex(privHex)
			if got, err = signer.SignTransactionInputWithKey(ctx, got, idx, prevOutScript, key); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("key %d: keyed transaction\n%x\nraw\n%x", i, got, want)
		}

		tx := wire.NewMsgTx(wire.TxVersion)
		if err := tx.Deserialize(bytes.NewReader(got)); err != nil {
			t.Fatal(err)
		}
		for idx := range tx.TxIn {
			vm, err := txscript.NewEngine(prevOutScript, tx, idx, txscript.StandardVerifyFlags, nil, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := vm.Execute(); err != nil {
				t.Fatalf("key %d input %d: the script engine rejected the spend: %v", i, idx, err)
			}
		}
	}
}
