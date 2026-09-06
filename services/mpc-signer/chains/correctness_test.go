package chains

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcutil"
	"github.com/btcsuite/btcutil/base58"
	"github.com/btcsuite/btcutil/bech32"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/ripemd160" //nolint:staticcheck // matching the implementation under test
)

// These tests check each chain implementation against values computed
// independently in the test from the chain's own specification, rather than
// against whatever the implementation happens to produce. That distinction
// matters here: the previous version of this package derived Cosmos
// addresses with Keccak256 (Ethereum's hash), which self-consistent tests
// would have happily confirmed, and which would have produced valid-looking
// addresses that no one can ever spend from.

// --- Bitcoin ---------------------------------------------------------------

func TestBitcoin_SignRecoverRoundTrip(t *testing.T) {
	signer := NewBitcoinSigner()
	ctx := context.Background()
	message := hash32("bitcoin round trip")

	sig, err := signer.SignMessage(ctx, message, testPrivKey)
	if err != nil {
		t.Fatalf("SignMessage failed: %v", err)
	}

	recovered, err := signer.RecoverAddress(ctx, message, sig)
	if err != nil {
		t.Fatalf("RecoverAddress failed: %v", err)
	}

	// Independently derive the expected P2PKH address from the private key:
	// base58check(0x00 || RIPEMD160(SHA256(compressed pubkey))).
	privBytes, _ := hex.DecodeString(testPrivKey)
	priv, pub := btcec.PrivKeyFromBytes(btcec.S256(), privBytes)
	if priv == nil {
		t.Fatal("failed to parse the test private key")
	}
	expectedAddr, err := btcutil.NewAddressPubKeyHash(
		btcutil.Hash160(pub.SerializeCompressed()), &chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("failed to derive expected address: %v", err)
	}

	if recovered != expectedAddr.EncodeAddress() {
		t.Errorf("recovered %s, expected %s", recovered, expectedAddr.EncodeAddress())
	}
}

func TestBitcoin_VerifySignature(t *testing.T) {
	signer := NewBitcoinSigner()
	ctx := context.Background()
	message := hash32("bitcoin verify")

	sig, err := signer.SignMessage(ctx, message, testPrivKey)
	if err != nil {
		t.Fatalf("SignMessage failed: %v", err)
	}

	privBytes, _ := hex.DecodeString(testPrivKey)
	_, pub := btcec.PrivKeyFromBytes(btcec.S256(), privBytes)
	pubHex := hex.EncodeToString(pub.SerializeCompressed())

	ok, err := signer.VerifySignature(ctx, message, sig, pubHex)
	if err != nil {
		t.Fatalf("VerifySignature failed: %v", err)
	}
	if !ok {
		t.Error("expected the signature to verify against its own public key")
	}

	// A different message must not verify.
	ok, err = signer.VerifySignature(ctx, hash32("a different message"), sig, pubHex)
	if err == nil && ok {
		t.Error("signature must not verify against a different message")
	}
}

func TestBitcoin_BuildAndSignTransaction(t *testing.T) {
	signer := NewBitcoinSigner().(*BitcoinSigner)
	ctx := context.Background()

	privBytes, _ := hex.DecodeString(testPrivKey)
	_, pub := btcec.PrivKeyFromBytes(btcec.S256(), privBytes)
	addr, err := btcutil.NewAddressPubKeyHash(
		btcutil.Hash160(pub.SerializeCompressed()), &chaincfg.TestNet3Params)
	if err != nil {
		t.Fatalf("failed to derive address: %v", err)
	}

	req := &BitcoinSignRequest{
		Network: "testnet",
		Inputs: []BitcoinInput{{
			// A syntactically valid 32-byte txid.
			Txid:   "0000000000000000000000000000000000000000000000000000000000000001",
			Vout:   0,
			Amount: 100000,
		}},
		Outputs: []BitcoinOutput{{Address: addr.EncodeAddress(), Amount: 90000}},
	}

	rawTx, err := signer.BuildTransaction(ctx, req)
	if err != nil {
		t.Fatalf("BuildTransaction failed: %v", err)
	}
	if len(rawTx) == 0 {
		t.Fatal("expected a serialized transaction")
	}

	// Signing an input must attach a signature script and change the
	// serialization -- i.e. it actually did something.
	prevOutScript, err := txscriptPayToAddr(addr)
	if err != nil {
		t.Fatalf("failed to build prevout script: %v", err)
	}
	signedTx, err := signer.SignTransactionInput(rawTx, 0, prevOutScript, testPrivKey)
	if err != nil {
		t.Fatalf("SignTransactionInput failed: %v", err)
	}
	if bytes.Equal(rawTx, signedTx) {
		t.Error("signed transaction is identical to the unsigned one")
	}
	if len(signedTx) <= len(rawTx) {
		t.Error("signed transaction should be larger (it carries a signature script)")
	}
}

func TestBitcoin_RejectsBadInput(t *testing.T) {
	signer := NewBitcoinSigner()
	ctx := context.Background()

	if _, err := signer.SignMessage(ctx, []byte("too short"), testPrivKey); err == nil {
		t.Error("expected a non-32-byte hash to be rejected")
	}
	if _, err := signer.BuildTransaction(ctx, &BitcoinSignRequest{}); err == nil {
		t.Error("expected a transaction with no inputs to be rejected")
	}
	if _, err := signer.BuildTransaction(ctx, &BitcoinSignRequest{
		Network: "not-a-network",
		Inputs:  []BitcoinInput{{Txid: "00", Vout: 0}},
		Outputs: []BitcoinOutput{{Address: "x", Amount: 1}},
	}); err == nil {
		t.Error("expected an unknown network to be rejected")
	}
}

// --- Cosmos ----------------------------------------------------------------

// The regression that matters most in this package: Cosmos addresses are
// bech32(prefix, RIPEMD160(SHA256(compressed pubkey))). The expected value
// here is computed from that definition directly, so an implementation that
// reverted to Keccak256 would fail.
func TestCosmos_AddressDerivationMatchesSpec(t *testing.T) {
	privBytes, _ := hex.DecodeString(testPrivKey)
	priv, _ := btcec.PrivKeyFromBytes(btcec.S256(), privBytes)
	compressed := priv.PubKey().SerializeCompressed()

	got, err := CosmosAddressFromPubKey(compressed, DefaultCosmosPrefix)
	if err != nil {
		t.Fatalf("CosmosAddressFromPubKey failed: %v", err)
	}

	sha := sha256.Sum256(compressed)
	hasher := ripemd160.New()
	hasher.Write(sha[:])
	want20 := hasher.Sum(nil)

	hrp, data, err := bech32.Decode(got)
	if err != nil {
		t.Fatalf("produced address is not valid bech32: %v", err)
	}
	if hrp != "cosmos" {
		t.Errorf("expected hrp \"cosmos\", got %q", hrp)
	}
	decoded, err := bech32.ConvertBits(data, 5, 8, false)
	if err != nil {
		t.Fatalf("failed to convert bech32 payload: %v", err)
	}
	if !bytes.Equal(decoded, want20) {
		t.Errorf("address payload %x != RIPEMD160(SHA256(pubkey)) %x", decoded, want20)
	}
	if len(decoded) != 20 {
		t.Errorf("expected a 20-byte address payload, got %d", len(decoded))
	}
}

func TestCosmos_CustomPrefix(t *testing.T) {
	privBytes, _ := hex.DecodeString(testPrivKey)
	priv, _ := btcec.PrivKeyFromBytes(btcec.S256(), privBytes)

	addr, err := CosmosAddressFromPubKey(priv.PubKey().SerializeCompressed(), "osmo")
	if err != nil {
		t.Fatalf("CosmosAddressFromPubKey failed: %v", err)
	}
	hrp, _, err := bech32.Decode(addr)
	if err != nil {
		t.Fatalf("not valid bech32: %v", err)
	}
	if hrp != "osmo" {
		t.Errorf("expected hrp \"osmo\", got %q", hrp)
	}
}

func TestCosmos_RejectsUncompressedPubKey(t *testing.T) {
	privBytes, _ := hex.DecodeString(testPrivKey)
	priv, _ := btcec.PrivKeyFromBytes(btcec.S256(), privBytes)

	if _, err := CosmosAddressFromPubKey(priv.PubKey().SerializeUncompressed(), "cosmos"); err == nil {
		t.Error("expected a 65-byte uncompressed key to be rejected: Cosmos hashes the compressed form")
	}
}

func TestCosmos_SignRecoverRoundTrip(t *testing.T) {
	signer := NewCosmosSigner()
	ctx := context.Background()
	message := hash32("cosmos round trip")

	sig, err := signer.SignMessage(ctx, message, testPrivKey)
	if err != nil {
		t.Fatalf("SignMessage failed: %v", err)
	}
	recovered, err := signer.RecoverAddress(ctx, message, sig)
	if err != nil {
		t.Fatalf("RecoverAddress failed: %v", err)
	}

	privKey, _ := crypto.HexToECDSA(testPrivKey)
	want, err := CosmosAddressFromPubKey(crypto.CompressPubkey(&privKey.PublicKey), DefaultCosmosPrefix)
	if err != nil {
		t.Fatalf("failed to derive expected address: %v", err)
	}
	if recovered != want {
		t.Errorf("recovered %s, expected %s", recovered, want)
	}
}

// The amino JSON SignDoc must be canonical: identical input produces
// identical bytes, and a changed field changes the hash.
func TestCosmos_BuildTransactionIsDeterministic(t *testing.T) {
	signer := NewCosmosSigner()
	ctx := context.Background()

	req := &CosmosSignRequest{
		ChainID:    "cosmoshub-4",
		AccountNum: 1,
		Sequence:   2,
		Fee:        CosmosFee{Amount: "1000", Gas: "200000"},
		Messages:   []map[string]interface{}{{"type": "cosmos-sdk/MsgSend", "value": map[string]interface{}{"amount": "100"}}},
	}

	first, err := signer.BuildTransaction(ctx, req)
	if err != nil {
		t.Fatalf("BuildTransaction failed: %v", err)
	}
	second, err := signer.BuildTransaction(ctx, req)
	if err != nil {
		t.Fatalf("BuildTransaction failed: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("SignDoc hashing is not deterministic")
	}
	if len(first) != 32 {
		t.Errorf("expected a 32-byte SHA256 digest, got %d bytes", len(first))
	}

	req.Sequence = 3
	changed, err := signer.BuildTransaction(ctx, req)
	if err != nil {
		t.Fatalf("BuildTransaction failed: %v", err)
	}
	if bytes.Equal(first, changed) {
		t.Error("changing the sequence must change the SignDoc hash")
	}
}

func TestCosmos_BuildTransactionRejectsIncomplete(t *testing.T) {
	signer := NewCosmosSigner()
	ctx := context.Background()

	if _, err := signer.BuildTransaction(ctx, &CosmosSignRequest{ChainID: "cosmoshub-4"}); err == nil {
		t.Error("expected a request with no messages to be rejected")
	}
	if _, err := signer.BuildTransaction(ctx, &CosmosSignRequest{
		Messages: []map[string]interface{}{{"type": "x"}},
	}); err == nil {
		t.Error("expected a request with no chain_id to be rejected")
	}
}

// --- Solana ----------------------------------------------------------------

func TestSolana_SignVerifyAgainstStdlib(t *testing.T) {
	signer := NewSolanaSigner()
	ctx := context.Background()
	message := []byte("solana signs the message, not a digest")

	sig, err := signer.SignMessage(ctx, message, testPrivKey)
	if err != nil {
		t.Fatalf("SignMessage failed: %v", err)
	}

	// Verify independently with the standard library.
	seed, _ := hex.DecodeString(testPrivKey)
	key := ed25519.NewKeyFromSeed(seed)
	sigBytes, err := hex.DecodeString(sig.SignatureBytes)
	if err != nil {
		t.Fatalf("bad signature hex: %v", err)
	}
	if !ed25519.Verify(key.Public().(ed25519.PublicKey), message, sigBytes) {
		t.Error("signature does not verify with crypto/ed25519")
	}

	// And through the signer's own verification, using the base58 address.
	addr := SolanaAddressFromPubKey(key.Public().(ed25519.PublicKey))
	ok, err := signer.VerifySignature(ctx, message, sig, addr)
	if err != nil {
		t.Fatalf("VerifySignature failed: %v", err)
	}
	if !ok {
		t.Error("expected the signature to verify against its own address")
	}
}

func TestSolana_RecoverAddressIsRefused(t *testing.T) {
	signer := NewSolanaSigner()
	if _, err := signer.RecoverAddress(context.Background(), []byte("x"), &Signature{}); err == nil {
		t.Error("Ed25519 has no key recovery; RecoverAddress must return an error rather than a value")
	}
}

// Checks the serialized message against Solana's documented legacy layout:
// a 3-byte header, then compact-u16 account count, 32-byte keys, the
// blockhash, and compact-u16 instruction data.
func TestSolana_BuildTransactionLayout(t *testing.T) {
	signer := NewSolanaSigner()
	ctx := context.Background()

	feePayer := base58.Encode(bytes.Repeat([]byte{1}, 32))
	writable := base58.Encode(bytes.Repeat([]byte{2}, 32))
	readonly := base58.Encode(bytes.Repeat([]byte{3}, 32))
	program := base58.Encode(bytes.Repeat([]byte{4}, 32))
	blockhash := base58.Encode(bytes.Repeat([]byte{5}, 32))

	req := &SolanaSignRequest{
		RecentBlockhash: blockhash,
		FeePayer:        feePayer,
		Instructions: []SolanaInstruction{{
			ProgramID: program,
			Accounts: []SolanaAccountMeta{
				{Pubkey: writable, IsSigner: false, IsWritable: true},
				{Pubkey: readonly, IsSigner: false, IsWritable: false},
			},
			Data: "0102030405",
		}},
	}

	msg, err := signer.BuildTransaction(ctx, req)
	if err != nil {
		t.Fatalf("BuildTransaction failed: %v", err)
	}

	// Header: 1 required signature (the fee payer), 0 readonly signed,
	// 2 readonly unsigned (the readonly account and the program id).
	if msg[0] != 1 {
		t.Errorf("numRequiredSignatures = %d, want 1", msg[0])
	}
	if msg[1] != 0 {
		t.Errorf("numReadonlySignedAccounts = %d, want 0", msg[1])
	}
	if msg[2] != 2 {
		t.Errorf("numReadonlyUnsignedAccounts = %d, want 2 (readonly account + program id)", msg[2])
	}

	// 4 distinct accounts: fee payer, writable, readonly, program.
	if msg[3] != 4 {
		t.Errorf("account count = %d, want 4", msg[3])
	}

	// The fee payer must be first -- the runtime requires it.
	if !bytes.Equal(msg[4:36], bytes.Repeat([]byte{1}, 32)) {
		t.Error("fee payer must be the first account key")
	}

	// Then the blockhash, immediately after the 4 account keys.
	blockhashOffset := 4 + 4*32
	if !bytes.Equal(msg[blockhashOffset:blockhashOffset+32], bytes.Repeat([]byte{5}, 32)) {
		t.Error("recent blockhash is not at the expected offset")
	}

	// One instruction follows.
	if msg[blockhashOffset+32] != 1 {
		t.Errorf("instruction count = %d, want 1", msg[blockhashOffset+32])
	}
}

func TestSolana_BuildTransactionRejectsBadInput(t *testing.T) {
	signer := NewSolanaSigner()
	ctx := context.Background()

	valid := base58.Encode(bytes.Repeat([]byte{1}, 32))

	if _, err := signer.BuildTransaction(ctx, &SolanaSignRequest{}); err == nil {
		t.Error("expected a request with no fee payer to be rejected")
	}
	if _, err := signer.BuildTransaction(ctx, &SolanaSignRequest{
		FeePayer:        valid,
		RecentBlockhash: "not-a-valid-blockhash",
		Instructions:    []SolanaInstruction{{ProgramID: valid}},
	}); err == nil {
		t.Error("expected an invalid blockhash to be rejected rather than zero-padded")
	}
}

func TestSolana_CompactU16(t *testing.T) {
	// Solana's shortvec: <128 is one byte, larger values continue with the
	// high bit set.
	for _, tc := range []struct {
		value int
		want  []byte
	}{
		{0, []byte{0x00}},
		{5, []byte{0x05}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{16384, []byte{0x80, 0x80, 0x01}},
	} {
		var buf bytes.Buffer
		writeCompactU16(&buf, tc.value)
		if !bytes.Equal(buf.Bytes(), tc.want) {
			t.Errorf("writeCompactU16(%d) = %x, want %x", tc.value, buf.Bytes(), tc.want)
		}
	}
}

// txscriptPayToAddr keeps the txscript import out of the test's main import
// block while still exercising the real script builder.
func txscriptPayToAddr(addr btcutil.Address) ([]byte, error) {
	return payToAddrScriptForTest(addr)
}
