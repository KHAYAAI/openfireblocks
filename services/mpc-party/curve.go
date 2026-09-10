package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/hex"
	"encoding/json"
	"fmt"

	ecdsakeygen "github.com/bnb-chain/tss-lib/v2/ecdsa/keygen"
	eddsakeygen "github.com/bnb-chain/tss-lib/v2/eddsa/keygen"
	tsscommon "github.com/bnb-chain/tss-lib/v2/tss"
	"github.com/btcsuite/btcutil/base58"
	"github.com/decred/dcrd/dcrec/edwards/v2"
	"github.com/ethereum/go-ethereum/crypto"
)

// Which curve a key lives on.
//
// Until now every ceremony was secp256k1, because that is what Ethereum and
// Bitcoin use and they were the only chains with a threshold path. Solana
// signs with Ed25519, and the consequence was that Solana had no threshold
// signing at all -- services/mpc-signer/chains/solana.go holds a whole
// private key. Describing that as MPC custody would have been false, which
// is the reason this exists.
//
// It needs no new protocol and no new dependency. tss-lib ships eddsa
// alongside ecdsa: the same LocalParty shape, the same round-driven state
// machine, the same BuildLocalSaveDataSubset committee handling, on
// tss.Edwards() instead of tss.S256(). What was actually missing was that
// the ceremony code named one curve in four places.
//
// The two differences worth knowing:
//
//   - EdDSA keygen takes no pre-parameters. ECDSA needs Paillier keys,
//     whose safe-prime generation is the slow part of a DKG (seconds to
//     minutes). An Ed25519 ceremony skips it entirely and is much faster.
//   - Ed25519 has no public-key recovery, so an address cannot be derived
//     from a signature. It is derived from the group public key here and
//     carried alongside, which is what solana.go already documents.
type Curve string

const (
	// CurveSecp256k1 is Ethereum, Bitcoin and the Cosmos SDK chains.
	CurveSecp256k1 Curve = "secp256k1"
	// CurveEd25519 is Solana.
	CurveEd25519 Curve = "ed25519"
)

// CurveForChain says which curve a blockchain's keys must be generated on.
//
// This is not a preference. A key generated on the wrong curve cannot
// produce a signature the chain will accept, and the failure surfaces at
// signing time -- after the DKG, after the customer has been told the key
// is ready, possibly after they have funded the address. Deciding it at
// key creation and recording it is what makes that impossible.
//
// Unknown chains are refused rather than defaulted. Defaulting to
// secp256k1 would silently provision an unusable key for any chain added
// later, and "unusable" would only be discovered by a customer.
func CurveForChain(blockchain string) (Curve, error) {
	switch blockchain {
	case "ethereum", "polygon", "arbitrum", "optimism", "base", "bsc", "avalanche":
		return CurveSecp256k1, nil
	case "bitcoin", "bitcoin-testnet", "bitcoin-regtest":
		return CurveSecp256k1, nil
	case "cosmos", "cosmos-hub", "osmosis":
		return CurveSecp256k1, nil
	case "solana":
		return CurveEd25519, nil
	default:
		return "", fmt.Errorf("no signing curve is defined for blockchain %q", blockchain)
	}
}

// ellipticCurve is what tss-lib needs to parameterise a ceremony.
func (c Curve) ellipticCurve() (elliptic.Curve, error) {
	switch c {
	case CurveSecp256k1:
		return tsscommon.S256(), nil
	case CurveEd25519:
		return tsscommon.Edwards(), nil
	default:
		return nil, fmt.Errorf("unknown curve %q", c)
	}
}

// KeyShare is one party's output from a DKG, on either curve.
//
// A tagged union rather than an interface, because the two save-data types
// share no methods -- they are plain structs from different packages with
// different fields. The tag is what lets a share be sealed in Vault and
// read back years later by code that has to know which package to unmarshal
// it into. Without it, a stored share is an untyped blob of JSON that
// happens to parse as either.
type KeyShare struct {
	Curve Curve                           `json:"curve"`
	ECDSA *ecdsakeygen.LocalPartySaveData `json:"ecdsa,omitempty"`
	EdDSA *eddsakeygen.LocalPartySaveData `json:"eddsa,omitempty"`
}

// PublicKey returns the group public key and the address derived from it.
//
// The address derivation is per-chain rather than per-curve, so this
// returns the encodings a curve can produce and lets the caller pick. For
// secp256k1 that is the Ethereum address (Bitcoin derives its own from the
// public key, in mpc-signer/chains); for Ed25519 it is the base58 public
// key, which is what a Solana address is.
func (k *KeyShare) PublicKey() (publicKeyHex string, address string, err error) {
	switch k.Curve {
	case CurveSecp256k1:
		if k.ECDSA == nil || k.ECDSA.ECDSAPub == nil {
			return "", "", fmt.Errorf("secp256k1 share carries no public key")
		}
		pub := &ecdsa.PublicKey{
			Curve: crypto.S256(),
			X:     k.ECDSA.ECDSAPub.X(),
			Y:     k.ECDSA.ECDSAPub.Y(),
		}
		return hex.EncodeToString(crypto.FromECDSAPub(pub)), crypto.PubkeyToAddress(*pub).Hex(), nil

	case CurveEd25519:
		if k.EdDSA == nil || k.EdDSA.EDDSAPub == nil {
			return "", "", fmt.Errorf("ed25519 share carries no public key")
		}
		// A Solana address IS the 32-byte Ed25519 public key, base58
		// encoded. Not a hash of it, unlike every other chain here -- so
		// there is no truncation and the address is the key.
		//
		// Serialised by the curve library rather than by hand. The
		// encoding is not the Y coordinate's bytes: it is 32 bytes of
		// *little-endian* Y with the sign bit of X folded into the top bit
		// of the last byte. Writing big-endian Y here produced a
		// well-formed 32-byte address for a different account entirely,
		// and every signature verified against it failed -- which is how
		// this was found, and why the test verifies with crypto/ed25519
		// rather than trusting the derivation.
		pub := edwards.PublicKey{
			Curve: tsscommon.Edwards(),
			X:     k.EdDSA.EDDSAPub.X(),
			Y:     k.EdDSA.EDDSAPub.Y(),
		}
		encoded := pub.Serialize()
		return hex.EncodeToString(encoded), base58.Encode(encoded), nil

	default:
		return "", "", fmt.Errorf("unknown curve %q", k.Curve)
	}
}

// MarshalShare serialises a share for sealing, carrying its curve.
func MarshalShare(share *KeyShare) ([]byte, error) {
	return json.Marshal(share)
}

// UnmarshalShare reads a sealed share back.
//
// Refuses a share with no curve tag rather than guessing. Shares sealed
// before the tag existed are all secp256k1, but assuming that here would
// mean a mis-tagged share silently unmarshals into the wrong package's
// struct and produces a party that cannot sign -- discovered at signing
// time, on a key holding money.
func UnmarshalShare(raw []byte) (*KeyShare, error) {
	var share KeyShare
	if err := json.Unmarshal(raw, &share); err != nil {
		return nil, fmt.Errorf("unmarshal key share: %w", err)
	}
	if share.Curve == "" {
		return nil, fmt.Errorf("key share carries no curve tag; refusing to guess which curve it was generated on")
	}
	switch share.Curve {
	case CurveSecp256k1:
		if share.ECDSA == nil {
			return nil, fmt.Errorf("share is tagged secp256k1 but carries no secp256k1 data")
		}
	case CurveEd25519:
		if share.EdDSA == nil {
			return nil, fmt.Errorf("share is tagged ed25519 but carries no ed25519 data")
		}
	default:
		return nil, fmt.Errorf("share carries unknown curve %q", share.Curve)
	}
	return &share, nil
}
