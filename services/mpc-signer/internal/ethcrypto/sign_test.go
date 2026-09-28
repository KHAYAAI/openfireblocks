package ethcrypto

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"testing"
)

// Signature layout, pinned to what go-ethereum produced.
//
// The failure this guards against does not look like a failure. Ethereum
// orders a compact signature [R || S || V]; btcec orders it [V+27 || R ||
// S]. Hand one to the other's parser and nothing errors -- it reads R
// where V is, recovers a well-formed public key, and returns a valid
// address for an account nobody controls. A test that only checked
// "recovery returned something" would pass against exactly that bug.

//go:embed testdata_sigvectors.json
var sigVectorsJSON []byte

type sigVector struct {
	Priv         string `json:"priv"`
	Digest       string `json:"digest"`
	Sig          string `json:"sig"`
	Uncompressed string `json:"uncompressed"`
	Address      string `json:"address"`
}

func sigVectors(t *testing.T) []sigVector {
	t.Helper()
	var vs []sigVector
	if err := json.Unmarshal(sigVectorsJSON, &vs); err != nil {
		t.Fatalf("reading signature vectors: %v", err)
	}
	if len(vs) < 20 {
		t.Fatalf("only %d signature vectors; the golden file looks truncated", len(vs))
	}
	return vs
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex in vector: %v", err)
	}
	return b
}

// btcec derives its nonce per RFC 6979, so signing is deterministic and
// the bytes must match go-ethereum's exactly -- not merely verify.
func TestSigningReproducesGoEthereumByteForByte(t *testing.T) {
	for _, v := range sigVectors(t) {
		priv, err := HexToECDSA(v.Priv)
		if err != nil {
			t.Fatalf("parsing %s: %v", v.Priv, err)
		}
		sig, err := Sign(mustHex(t, v.Digest), priv)
		if err != nil {
			t.Fatalf("signing with %s: %v", v.Priv, err)
		}
		if got := hex.EncodeToString(sig); got != v.Sig {
			t.Errorf("priv %s:\n got %s\nwant %s", v.Priv, got, v.Sig)
		}
	}
}

func TestRecoveryReturnsTheSigningKey(t *testing.T) {
	for _, v := range sigVectors(t) {
		x, y, err := SigToPub(mustHex(t, v.Digest), mustHex(t, v.Sig))
		if err != nil {
			t.Fatalf("recovering for %s: %v", v.Priv, err)
		}
		if got := hex.EncodeToString(MarshalPubkey(x, y)); got != v.Uncompressed {
			t.Errorf("priv %s recovered the wrong key:\n got %s\nwant %s",
				v.Priv, got, v.Uncompressed)
		}
	}
}

func TestRecoverAddressReturnsTheSigner(t *testing.T) {
	for _, v := range sigVectors(t) {
		got, err := RecoverAddress(mustHex(t, v.Digest), mustHex(t, v.Sig))
		if err != nil {
			t.Fatalf("recovering for %s: %v", v.Priv, err)
		}
		if got != v.Address {
			t.Errorf("priv %s:\n got %s\nwant %s", v.Priv, got, v.Address)
		}
	}
}

// The byte-order bug, made explicit. If the recovery id were read from
// the wrong end, this would still recover *a* key -- just not this one.
func TestRecoveringWithTheByteOrderReversedDoesNotYieldTheSigner(t *testing.T) {
	v := sigVectors(t)[0]
	sig := mustHex(t, v.Sig)

	// btcec's own ordering: [V+27 || R || S].
	wrong := make([]byte, 65)
	wrong[0] = sig[64] + 27
	copy(wrong[1:], sig[:64])

	if got, err := RecoverAddress(mustHex(t, v.Digest), wrong); err == nil && got == v.Address {
		t.Fatal("a signature in btcec's byte order recovered the correct address; " +
			"the layouts are not actually being distinguished")
	}
}

func TestVerifySignatureAcceptsTheSignature(t *testing.T) {
	for _, v := range sigVectors(t) {
		sig := mustHex(t, v.Sig)
		pub := mustHex(t, v.Uncompressed)
		if !VerifySignature(pub, mustHex(t, v.Digest), sig[:64]) {
			t.Errorf("priv %s: a valid signature did not verify", v.Priv)
		}
	}
}

func TestVerifySignatureRejectsADifferentDigest(t *testing.T) {
	v := sigVectors(t)[0]
	sig := mustHex(t, v.Sig)
	other := Keccak256([]byte("a different message entirely"))

	if VerifySignature(mustHex(t, v.Uncompressed), other, sig[:64]) {
		t.Fatal("a signature verified against a message it does not commit to")
	}
}

func TestVerifySignatureRejectsADifferentKey(t *testing.T) {
	vs := sigVectors(t)
	sig := mustHex(t, vs[0].Sig)

	if VerifySignature(mustHex(t, vs[1].Uncompressed), mustHex(t, vs[0].Digest), sig[:64]) {
		t.Fatal("a signature verified against a key that did not produce it")
	}
}

// Sixty-four bytes, not sixty-five. A caller passing the recovery byte
// through would otherwise have it silently read as part of S.
func TestVerifySignatureRefusesTheRecoveryByte(t *testing.T) {
	v := sigVectors(t)[0]

	if VerifySignature(mustHex(t, v.Uncompressed), mustHex(t, v.Digest), mustHex(t, v.Sig)) {
		t.Error("a 65-byte signature was accepted where 64 bytes are expected")
	}
}

// Both S and N-S satisfy the verification equation. Accepting either means
// one authorised transaction has two valid signatures, and therefore two
// transaction hashes -- which is a reconciliation problem and, on some
// chains, a replay one.
func TestAHighSSignatureIsRejected(t *testing.T) {
	v := sigVectors(t)[0]
	sig := mustHex(t, v.Sig)

	// s' = n - s
	n := S256().Params().N
	s := new(big.Int).SetBytes(sig[32:64])
	flipped := new(big.Int).Sub(n, s)

	malleable := make([]byte, 64)
	copy(malleable, sig[:32])
	flipped.FillBytes(malleable[32:])

	if VerifySignature(mustHex(t, v.Uncompressed), mustHex(t, v.Digest), malleable) {
		t.Error("a high-S (malleable) signature was accepted")
	}
}

func TestSignRefusesADigestThatIsNotThirtyTwoBytes(t *testing.T) {
	priv, err := GenerateKey()
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	for _, n := range []int{0, 31, 33, 64} {
		if _, err := Sign(make([]byte, n), priv); err == nil {
			t.Errorf("a %d-byte digest was signed", n)
		}
	}
}

func TestSigToPubRefusesAnImpossibleRecoveryId(t *testing.T) {
	v := sigVectors(t)[0]
	sig := mustHex(t, v.Sig)
	sig[64] = 9

	if _, _, err := SigToPub(mustHex(t, v.Digest), sig); err == nil {
		t.Error("a recovery id of 9 was accepted")
	}
}

// 27/28 is the older spelling of the same recovery id and this platform
// has produced both. Refusing one would reject its own history.
func TestSigToPubAcceptsTheTwentySevenSpelling(t *testing.T) {
	v := sigVectors(t)[0]
	sig := mustHex(t, v.Sig)
	sig[64] += 27

	got, err := RecoverAddress(mustHex(t, v.Digest), sig)
	if err != nil {
		t.Fatalf("a 27-style recovery id was refused: %v", err)
	}
	if got != v.Address {
		t.Errorf("recovered %s, want %s", got, v.Address)
	}
}

// -- key parsing --

func TestHexToECDSARoundTripsAndDerivesTheRightAddress(t *testing.T) {
	for _, v := range sigVectors(t) {
		priv, err := HexToECDSA(v.Priv)
		if err != nil {
			t.Fatalf("parsing %s: %v", v.Priv, err)
		}
		if hex.EncodeToString(FromECDSA(priv)) != v.Priv {
			t.Errorf("private key did not round-trip: %s", v.Priv)
		}
		if got := PubkeyToAddress(priv.PublicKey); got != v.Address {
			t.Errorf("priv %s derived %s, want %s", v.Priv, got, v.Address)
		}
		if got := hex.EncodeToString(FromECDSAPub(&priv.PublicKey)); got != v.Uncompressed {
			t.Errorf("priv %s public key mismatch", v.Priv)
		}
	}
}

// A scalar outside [1, n-1] is not a private key. btcec would reduce it
// modulo n rather than refuse, which turns an invalid key into a perfectly
// usable one for a different account -- silently.
func TestHexToECDSARefusesScalarsOutsideTheGroup(t *testing.T) {
	n := S256().Params().N
	cases := map[string]string{
		"zero":        "0000000000000000000000000000000000000000000000000000000000000000",
		"the order n": hex.EncodeToString(n.Bytes()),
		"n plus one":  hex.EncodeToString(new(big.Int).Add(n, big.NewInt(1)).Bytes()),
		"all ones":    "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		"too short":   "01",
		"not hex":     "zz00000000000000000000000000000000000000000000000000000000000000",
	}
	for name, s := range cases {
		if _, err := HexToECDSA(s); err == nil {
			t.Errorf("%s was accepted as a private key", name)
		}
	}
}

func TestGenerateKeyProducesUsableDistinctKeys(t *testing.T) {
	a, err := GenerateKey()
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	b, err := GenerateKey()
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	if a.D.Cmp(b.D) == 0 {
		t.Fatal("two generated keys are identical")
	}

	digest := Keccak256([]byte("round trip"))
	sig, err := Sign(digest, a)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	got, err := RecoverAddress(digest, sig)
	if err != nil {
		t.Fatalf("recovering: %v", err)
	}
	if got != PubkeyToAddress(a.PublicKey) {
		t.Error("a freshly generated key's signature recovered to a different address")
	}
}
