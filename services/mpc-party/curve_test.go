package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bnb-chain/tss-lib/v2/crypto"
	ecdsakeygen "github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	eddsakeygen "github.com/bnb-chain/tss-lib/v2/eddsa/keygen"
	tsscommon "github.com/bnb-chain/tss-lib/v2/tss"
	"github.com/btcsuite/btcutil/base58"
	"github.com/decred/dcrd/dcrec/edwards/v2"
)

// Which curve a key is generated on, and what happens when that is wrong.
//
// The consequence of getting it wrong is not an error at key creation. It
// is a key that completes its DKG, reports success, gets an address, gets
// funded -- and cannot produce a signature the chain accepts. The failure
// surfaces the first time a customer tries to move money.

func TestChainsAreMappedToTheCurveTheyActuallyUse(t *testing.T) {
	secp := []string{"ethereum", "polygon", "arbitrum", "optimism", "base",
		"bitcoin", "bitcoin-regtest", "cosmos-hub"}
	for _, chain := range secp {
		t.Run(chain, func(t *testing.T) {
			curve, err := CurveForChain(chain)
			if err != nil {
				t.Fatalf("%s has no curve: %v", chain, err)
			}
			if curve != CurveSecp256k1 {
				t.Errorf("%s maps to %s, want secp256k1", chain, curve)
			}
		})
	}

	curve, err := CurveForChain("solana")
	if err != nil {
		t.Fatalf("solana has no curve: %v", err)
	}
	if curve != CurveEd25519 {
		t.Errorf("solana maps to %s, want ed25519", curve)
	}
}

// An unknown chain is refused rather than defaulted. Defaulting to
// secp256k1 would provision a silently unusable key for any chain added
// later, and the only person who would find out is a customer.
func TestAnUnknownChainHasNoCurve(t *testing.T) {
	if _, err := CurveForChain("aptos"); err == nil {
		t.Error("a chain with no defined curve was given one anyway")
	}
}

func TestBothCurvesResolveToATssLibCurve(t *testing.T) {
	if _, err := CurveSecp256k1.ellipticCurve(); err != nil {
		t.Errorf("secp256k1: %v", err)
	}
	if _, err := CurveEd25519.ellipticCurve(); err != nil {
		t.Errorf("ed25519: %v", err)
	}
	if _, err := Curve("p256").ellipticCurve(); err == nil {
		t.Error("an unknown curve resolved to a tss-lib curve")
	}
}

// -- sealing and reading back --

// A sealed share has to say which curve it came from. Without the tag it
// is a blob of JSON that happens to parse as either package's struct, and
// unmarshalling into the wrong one produces a party that cannot sign --
// discovered at signing time, on a key holding money.
func TestASealedShareCarriesItsCurve(t *testing.T) {
	share := &KeyShare{
		Curve: CurveEd25519,
		EdDSA: &eddsakeygen.LocalPartySaveData{},
	}

	raw, err := MarshalShare(share)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"curve":"ed25519"`) {
		t.Errorf("the sealed share does not name its curve: %s", raw)
	}

	read, err := UnmarshalShare(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if read.Curve != CurveEd25519 {
		t.Errorf("read back as %s", read.Curve)
	}
}

func TestAShareWithNoCurveTagIsRefused(t *testing.T) {
	// What a share sealed before the tag existed looks like: the ECDSA
	// save data, bare.
	legacy, err := json.Marshal(&ecdsakeygen.LocalPartySaveData{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	_, err = UnmarshalShare(legacy)

	if err == nil {
		t.Fatal("an untagged share was accepted; the curve would have been guessed")
	}
	if !strings.Contains(err.Error(), "curve") {
		t.Errorf("the refusal does not explain what is missing: %v", err)
	}
}

func TestAMistaggedShareIsRefused(t *testing.T) {
	// Tagged ed25519, carrying secp256k1 data. Accepting this would hand
	// the eddsa signing party a struct it cannot use.
	raw := []byte(`{"curve":"ed25519","ecdsa":{}}`)

	if _, err := UnmarshalShare(raw); err == nil {
		t.Error("a share tagged with one curve and carrying another's data was accepted")
	}
}

func TestAShareOnAnUnknownCurveIsRefused(t *testing.T) {
	if _, err := UnmarshalShare([]byte(`{"curve":"p256","ecdsa":{}}`)); err == nil {
		t.Error("a share on an unrecognised curve was accepted")
	}
}

// -- address derivation --

// The encoding that took a failing signature to find.
//
// An Ed25519 public key is not the Y coordinate's big-endian bytes. It is
// 32 bytes of little-endian Y with the sign of X folded into the top bit
// of the last byte. Deriving it the obvious way produces a well-formed
// 32-byte address for a completely different account, and every signature
// against it fails verification -- which is exactly what happened, and is
// why this asserts against the curve library's own encoding rather than
// against a value copied from a previous run.
func TestASolanaAddressIsTheProperlyEncodedPublicKey(t *testing.T) {
	// A known Ed25519 key, so the expected encoding can be computed
	// independently of the code under test.
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	parsed, err := edwards.ParsePubKey(pub)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	point, err := crypto.NewECPoint(tsscommon.Edwards(), parsed.GetX(), parsed.GetY())
	if err != nil {
		t.Fatalf("point: %v", err)
	}
	share := &KeyShare{Curve: CurveEd25519, EdDSA: &eddsakeygen.LocalPartySaveData{EDDSAPub: point}}

	pubKeyHex, address, err := share.PublicKey()
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	if pubKeyHex != hex.EncodeToString(pub) {
		t.Errorf("derived public key %s, want %s", pubKeyHex, hex.EncodeToString(pub))
	}
	if address != base58.Encode(pub) {
		t.Errorf("derived address %s, want %s", address, base58.Encode(pub))
	}

	// A Solana address is base58 of 32 bytes, which is 43 or 44
	// characters. A 31-byte key -- what dropping a leading zero would give
	// -- encodes shorter, and would be a valid-looking address for an
	// account nobody controls.
	if len(address) < 43 || len(address) > 44 {
		t.Errorf("address %q is %d characters; a 32-byte key encodes to 43 or 44",
			address, len(address))
	}
}

func TestAShareWithNoPublicKeyIsRefusedRatherThanPanicking(t *testing.T) {
	for name, share := range map[string]*KeyShare{
		"ed25519 with no point":   {Curve: CurveEd25519, EdDSA: &eddsakeygen.LocalPartySaveData{}},
		"secp256k1 with no point": {Curve: CurveSecp256k1, ECDSA: &ecdsakeygen.LocalPartySaveData{}},
		"unknown curve":           {Curve: "p256"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := share.PublicKey(); err == nil {
				t.Error("a share with no usable public key derived one anyway")
			}
		})
	}
}
