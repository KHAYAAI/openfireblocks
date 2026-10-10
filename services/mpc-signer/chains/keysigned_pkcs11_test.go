//go:build pkcs11

package chains

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcutil"

	"forge-crypto/mpc-signer/keys"
	"forge-crypto/mpc-signer/keys/softhsmtest"
)

// End to end through a PKCS#11 token: a key generated inside it signs
// for all three secp256k1 chains, and a Bitcoin spend it signs is
// accepted by btcd's script engine with standard flags (low S, strict
// DER). The token's nonces are random, so unlike keysigned_test.go this
// cannot compare bytes with the software path -- it checks what a node
// checks instead.
func TestAHardwareKeySignsForEveryKeySignedChain(t *testing.T) {
	cfg := softhsmtest.NewToken(t, "chains", "hot-wallet")
	if err := keys.GeneratePKCS11Key(cfg); err != nil {
		t.Fatal(err)
	}
	k, err := keys.OpenPKCS11(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer k.(io.Closer).Close()
	ctx := context.Background()

	ethAddr, _ := keys.Address(k)
	router := NewSignerRouter()
	for i := 0; i < 10; i++ {
		h := sha256.Sum256([]byte(fmt.Sprintf("hw %d", i)))
		for _, chain := range []string{"ethereum", "bitcoin", "cosmos-hub"} {
			resp, err := router.SignMultiChainWithKey(ctx, &ChainSignRequest{ChainID: chain, Message: h[:]}, k)
			if err != nil {
				t.Fatalf("%s: %v", chain, err)
			}
			if chain == "ethereum" && resp.From != ethAddr {
				t.Fatalf("ethereum signature recovers to %s, key is %s", resp.From, ethAddr)
			}
		}
	}

	pub, _ := keys.CompressedPublicKey(k)
	addr, err := btcutil.NewAddressPubKeyHash(btcutil.Hash160(pub), &chaincfg.TestNet3Params)
	if err != nil {
		t.Fatal(err)
	}
	prevOut, _ := txscript.PayToAddrScript(addr)
	signer := NewBitcoinSigner().(*BitcoinSigner)
	rawTx, err := signer.BuildTransaction(ctx, &BitcoinSignRequest{
		Network: "testnet",
		Inputs: []BitcoinInput{
			{Txid: fmt.Sprintf("%064x", 1), Vout: 0, Amount: 100000},
			{Txid: fmt.Sprintf("%064x", 2), Vout: 1, Amount: 100000},
		},
		Outputs: []BitcoinOutput{{Address: addr.EncodeAddress(), Amount: 190000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	signed := rawTx
	for idx := 0; idx < 2; idx++ {
		if signed, err = signer.SignTransactionInputWithKey(ctx, signed, idx, prevOut, k); err != nil {
			t.Fatal(err)
		}
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(signed)); err != nil {
		t.Fatal(err)
	}
	for idx := range tx.TxIn {
		vm, err := txscript.NewEngine(prevOut, tx, idx, txscript.StandardVerifyFlags, nil, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.Execute(); err != nil {
			t.Fatalf("input %d: the script engine rejected the hardware-signed spend: %v", idx, err)
		}
	}
}
