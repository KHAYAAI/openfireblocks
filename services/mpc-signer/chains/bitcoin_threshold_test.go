package chains

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcutil"
)

// A stand-in for the MPC committee.
//
// The committee's only observable contribution is an (r, s) pair over a
// 32-byte digest, and ECDSA over secp256k1 is ECDSA however the key is held.
// Signing here with a local key produces exactly the same thing a real
// ceremony returns, which is what makes it a valid substitute for testing
// the *assembly* -- the part that is Bitcoin-specific and the part that is
// easy to get subtly wrong.
func signLikeCommittee(t *testing.T, key *btcec.PrivateKey, digestHex string) BitcoinInputSignature {
	t.Helper()
	digest, err := hex.DecodeString(digestHex)
	if err != nil {
		t.Fatalf("bad digest hex: %v", err)
	}
	sig, err := key.Sign(digest)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return BitcoinInputSignature{
		R: sig.R.Text(16),
		S: sig.S.Text(16),
	}
}

// p2pkhFor returns the address and scriptPubKey for a key on regtest.
func p2pkhFor(t *testing.T, key *btcec.PrivateKey) (btcutil.Address, string) {
	t.Helper()
	pub := key.PubKey().SerializeCompressed()
	addr, err := btcutil.NewAddressPubKeyHash(btcutil.Hash160(pub), &chaincfg.RegressionNetParams)
	if err != nil {
		t.Fatalf("address: %v", err)
	}
	script, err := txscript.PayToAddrScript(addr)
	if err != nil {
		t.Fatalf("script: %v", err)
	}
	return addr, hex.EncodeToString(script)
}

func planFixture(t *testing.T, script string, amount int64, dest btcutil.Address, send int64) *BitcoinSigningRequest {
	t.Helper()
	return &BitcoinSigningRequest{
		Network: "regtest",
		Inputs: []BitcoinInput{{
			Txid:   "0000000000000000000000000000000000000000000000000000000000000001",
			Vout:   0,
			Amount: amount,
			Script: script,
		}},
		Outputs: []BitcoinOutput{{Address: dest.EncodeAddress(), Amount: send}},
	}
}

// The whole claim, end to end: a transaction signed by something that never
// held the private key satisfies the output it spends, as judged by the same
// script interpreter Bitcoin Core runs.
//
// A transaction can serialise perfectly, broadcast, and be rejected by the
// network with an unhelpful error. Executing the script here is the
// difference between "we produced bytes" and "we produced a spend".
func TestAssembledTransactionSatisfiesTheScriptEngine(t *testing.T) {
	key, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	_, script := p2pkhFor(t, key)
	dest, _ := p2pkhFor(t, mustKey(t))

	plan, err := PlanBitcoinTransaction(planFixture(t, script, 100_000, dest, 90_000))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.SigHashes) != 1 {
		t.Fatalf("expected 1 sighash, got %d", len(plan.SigHashes))
	}

	sig := signLikeCommittee(t, key, plan.SigHashes[0])
	raw, err := AssembleBitcoinTransaction(plan, []BitcoinInputSignature{sig},
		hex.EncodeToString(key.PubKey().SerializeCompressed()))
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("assembled an empty transaction")
	}

	txid, err := BitcoinTxID(raw)
	if err != nil {
		t.Fatalf("txid: %v", err)
	}
	if len(txid) != 64 {
		t.Errorf("txid %q is not 32 bytes of hex", txid)
	}
}

// Bitcoin enforces BIP-62: a signature with s above half the curve order
// verifies fine and will not relay. A threshold ceremony has no reason to
// prefer one of the two equivalent forms, so roughly half of all signatures
// arrive high-S. Without normalisation, half of all transactions would
// silently fail to propagate -- the worst kind of bug, because the ones that
// worked would prove nothing.
func TestHighSSignatureIsNormalisedAndStillValid(t *testing.T) {
	key, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	_, script := p2pkhFor(t, key)
	dest, _ := p2pkhFor(t, mustKey(t))

	plan, err := PlanBitcoinTransaction(planFixture(t, script, 100_000, dest, 90_000))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	low := signLikeCommittee(t, key, plan.SigHashes[0])

	// Flip it to the equivalent high-S form, which is what a ceremony may
	// hand back.
	s, _ := new(big.Int).SetString(low.S, 16)
	highS := new(big.Int).Sub(btcec.S256().N, s)
	halfOrder := new(big.Int).Rsh(btcec.S256().N, 1)
	if highS.Cmp(halfOrder) <= 0 {
		t.Skip("the generated signature's complement is also low-S; nothing to test this run")
	}
	high := BitcoinInputSignature{R: low.R, S: highS.Text(16)}

	// Assembly must accept it, normalise it, and produce a transaction the
	// script engine still validates -- normalising to something invalid
	// would be worse than not normalising.
	raw, err := AssembleBitcoinTransaction(plan, []BitcoinInputSignature{high},
		hex.EncodeToString(key.PubKey().SerializeCompressed()))
	if err != nil {
		t.Fatalf("a high-S signature was rejected instead of normalised: %v", err)
	}

	// And the result must be byte-identical to the low-S assembly: the two
	// are the same signature, so they must produce the same transaction.
	rawLow, err := AssembleBitcoinTransaction(plan, []BitcoinInputSignature{low},
		hex.EncodeToString(key.PubKey().SerializeCompressed()))
	if err != nil {
		t.Fatalf("assemble low-S: %v", err)
	}
	if hex.EncodeToString(raw) != hex.EncodeToString(rawLow) {
		t.Error("the high-S and low-S forms produced different transactions; normalisation is not canonical")
	}
}

// A signature over the wrong digest must be caught here, not by the network.
// This is the check that the script engine is actually being run.
func TestSignatureForADifferentTransactionIsRejected(t *testing.T) {
	key, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	_, script := p2pkhFor(t, key)
	dest, _ := p2pkhFor(t, mustKey(t))

	plan, err := PlanBitcoinTransaction(planFixture(t, script, 100_000, dest, 90_000))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	// A plan for a *different* amount, hence a different sighash.
	other, err := PlanBitcoinTransaction(planFixture(t, script, 100_000, dest, 80_000))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.SigHashes[0] == other.SigHashes[0] {
		t.Fatal("two different transactions produced the same sighash")
	}

	wrong := signLikeCommittee(t, key, other.SigHashes[0])
	_, err = AssembleBitcoinTransaction(plan, []BitcoinInputSignature{wrong},
		hex.EncodeToString(key.PubKey().SerializeCompressed()))
	if err == nil {
		t.Fatal("a signature over a different transaction was assembled without complaint")
	}
	if !strings.Contains(err.Error(), "does not satisfy") {
		t.Errorf("error does not explain the problem: %v", err)
	}
}

// The wrong key is the same class of failure and equally must not reach the
// network.
func TestSignatureFromTheWrongKeyIsRejected(t *testing.T) {
	key := mustKey(t)
	impostor := mustKey(t)
	_, script := p2pkhFor(t, key)
	dest, _ := p2pkhFor(t, mustKey(t))

	plan, err := PlanBitcoinTransaction(planFixture(t, script, 100_000, dest, 90_000))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	sig := signLikeCommittee(t, impostor, plan.SigHashes[0])
	if _, err := AssembleBitcoinTransaction(plan, []BitcoinInputSignature{sig},
		hex.EncodeToString(impostor.PubKey().SerializeCompressed())); err == nil {
		t.Fatal("a transaction signed by the wrong key was assembled without complaint")
	}
}

// Sending to an address from another network decodes fine and would pay an
// unspendable script. Version bytes are the only thing distinguishing them.
func TestMainnetAddressIsRejectedOnRegtest(t *testing.T) {
	key := mustKey(t)
	_, script := p2pkhFor(t, key)

	mainnetAddr, err := btcutil.NewAddressPubKeyHash(
		btcutil.Hash160(key.PubKey().SerializeCompressed()), &chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("mainnet address: %v", err)
	}

	req := &BitcoinSigningRequest{
		Network: "regtest",
		Inputs: []BitcoinInput{{
			Txid:   "0000000000000000000000000000000000000000000000000000000000000001",
			Amount: 100_000,
			Script: script,
		}},
		Outputs: []BitcoinOutput{{Address: mainnetAddr.EncodeAddress(), Amount: 90_000}},
	}
	if _, err := PlanBitcoinTransaction(req); err == nil {
		t.Fatal("a mainnet address was accepted on regtest")
	}
}

// The fee on Bitcoin is implicit -- whatever the inputs exceed the outputs
// by. A mistyped amount does not fail, it tips the miner. Both directions
// are worth refusing.
func TestImplausibleFeesAreRefused(t *testing.T) {
	key := mustKey(t)
	_, script := p2pkhFor(t, key)
	dest, _ := p2pkhFor(t, mustKey(t))

	// Outputs exceeding inputs: unminable.
	if _, err := PlanBitcoinTransaction(planFixture(t, script, 100_000, dest, 150_000)); err == nil {
		t.Error("accepted outputs larger than inputs")
	}
	// A fee larger than the amount being sent: almost certainly a typo, and
	// irreversible once mined.
	if _, err := PlanBitcoinTransaction(planFixture(t, script, 100_000, dest, 1_000)); err == nil {
		t.Error("accepted a fee 99x the amount being sent")
	}
	// A sane fee must still be allowed, or the guard is useless.
	if _, err := PlanBitcoinTransaction(planFixture(t, script, 100_000, dest, 99_000)); err != nil {
		t.Errorf("rejected a reasonable 1000 sat fee: %v", err)
	}
}

func TestPlanRejectsEmptyTransactions(t *testing.T) {
	if _, err := PlanBitcoinTransaction(&BitcoinSigningRequest{Network: "regtest"}); err == nil {
		t.Error("accepted a transaction with no inputs")
	}
	key := mustKey(t)
	_, script := p2pkhFor(t, key)
	req := &BitcoinSigningRequest{
		Network: "regtest",
		Inputs:  []BitcoinInput{{Txid: strings.Repeat("0", 63) + "1", Amount: 1000, Script: script}},
	}
	if _, err := PlanBitcoinTransaction(req); err == nil {
		t.Error("accepted a transaction with no outputs; this would burn the whole input to fees")
	}
}

func TestAssembleRejectsAMismatchedSignatureCount(t *testing.T) {
	key := mustKey(t)
	_, script := p2pkhFor(t, key)
	dest, _ := p2pkhFor(t, mustKey(t))

	plan, err := PlanBitcoinTransaction(planFixture(t, script, 100_000, dest, 90_000))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := AssembleBitcoinTransaction(plan, nil,
		hex.EncodeToString(key.PubKey().SerializeCompressed())); err == nil {
		t.Error("assembled a transaction with no signatures")
	}
}

func mustKey(t *testing.T) *btcec.PrivateKey {
	t.Helper()
	k, err := btcec.NewPrivateKey(btcec.S256())
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return k
}

// The platform's DKG reports public keys uncompressed (65 bytes, 0x04...),
// while Bitcoin addresses derive from the compressed form. The two hash to
// different addresses, so passing the key through verbatim puts the wrong
// key in the signature script -- and the failure is OP_EQUALVERIFY, which
// says nothing about serialisation.
//
// Found by spending from a real threshold key on regtest.
func TestUncompressedPublicKeyStillSpendsACompressedAddress(t *testing.T) {
	key := mustKey(t)
	_, script := p2pkhFor(t, key) // address derived from the COMPRESSED key
	dest, _ := p2pkhFor(t, mustKey(t))

	plan, err := PlanBitcoinTransaction(planFixture(t, script, 100_000, dest, 90_000))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	sig := signLikeCommittee(t, key, plan.SigHashes[0])

	// The uncompressed form, exactly as the DKG hands it over.
	uncompressed := hex.EncodeToString(key.PubKey().SerializeUncompressed())
	if uncompressed[:2] != "04" {
		t.Fatalf("expected an uncompressed key to start 04, got %s", uncompressed[:2])
	}

	rawFromUncompressed, err := AssembleBitcoinTransaction(plan, []BitcoinInputSignature{sig}, uncompressed)
	if err != nil {
		t.Fatalf("an uncompressed public key failed to spend its own address: %v", err)
	}

	// And it must produce exactly the same transaction as the compressed
	// form -- it is the same key, so anything else would mean the
	// serialisation leaked into the result.
	rawFromCompressed, err := AssembleBitcoinTransaction(plan, []BitcoinInputSignature{sig},
		hex.EncodeToString(key.PubKey().SerializeCompressed()))
	if err != nil {
		t.Fatalf("assemble with compressed key: %v", err)
	}
	if hex.EncodeToString(rawFromUncompressed) != hex.EncodeToString(rawFromCompressed) {
		t.Error("the compressed and uncompressed forms of one key produced different transactions")
	}
}
