package ethcrypto

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2"
	btcecdsa "github.com/btcsuite/btcd/btcec/v2/ecdsa"
)

// Signing, recovery and verification over secp256k1.
//
// The arithmetic is btcec's, which is decred's secp256k1 underneath -- the
// same implementation tss-lib already uses for every threshold ceremony in
// this platform. What go-ethereum contributed was a byte layout, and the
// layout is where the two libraries disagree in a way that silently
// produces wrong answers rather than errors.
//
// Ethereum orders a compact signature [R || S || V] with V in {0, 1}.
// btcec orders it [V+27 || R || S], with a further +4 when the recovered
// key should be compressed. Feeding one format to the other's parser does
// not fail: it reads R where V is and recovers a well-formed public key
// for an account nobody controls. That is the reason every function here
// is pinned to vectors produced by go-ethereum rather than to a reading of
// either library's documentation.

// SignatureLength is [R || S || V].
const SignatureLength = 65

// Sign produces a 65-byte Ethereum signature over a 32-byte digest.
//
// Deterministic: btcec derives the nonce per RFC 6979, so the same key and
// digest always yield the same signature. That is what makes the golden
// vectors possible, and it is also the property that matters for an audit
// trail -- two signatures over the same authorised transaction are equal,
// so a duplicate is visible as a duplicate.
func Sign(digest []byte, priv *ecdsa.PrivateKey) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("a secp256k1 signature is over a 32-byte digest, got %d bytes", len(digest))
	}
	key, _ := btcec.PrivKeyFromBytes(priv.D.FillBytes(make([]byte, 32)))

	// btcec returns [V+27(+4 if compressed) || R || S].
	compact := btcecdsa.SignCompact(key, digest, false)
	if len(compact) != SignatureLength {
		return nil, fmt.Errorf("btcec returned a %d-byte compact signature, expected %d",
			len(compact), SignatureLength)
	}

	sig := make([]byte, SignatureLength)
	copy(sig, compact[1:65])
	// Back to Ethereum's ordering and to a 0/1 recovery id.
	sig[64] = compact[0] - 27
	return sig, nil
}

// SigToPub recovers the signing public key from an Ethereum signature.
func SigToPub(digest, sig []byte) (x, y *big.Int, err error) {
	if len(digest) != 32 {
		return nil, nil, fmt.Errorf("a digest is 32 bytes, got %d", len(digest))
	}
	if len(sig) != SignatureLength {
		return nil, nil, fmt.Errorf("a signature is %d bytes, got %d", SignatureLength, len(sig))
	}
	v := sig[64]
	// Tolerate the EIP-155-era 27/28 spelling as well as 0/1. Both appear
	// in the wild, and refusing one would reject signatures this platform
	// itself produced under an older convention.
	if v >= 27 {
		v -= 27
	}
	if v > 1 {
		return nil, nil, fmt.Errorf("recovery id %d is not 0 or 1", sig[64])
	}

	compact := make([]byte, SignatureLength)
	compact[0] = v + 27
	copy(compact[1:], sig[:64])

	pub, _, err := btcecdsa.RecoverCompact(compact, digest)
	if err != nil {
		return nil, nil, fmt.Errorf("recovering the public key: %w", err)
	}
	return pub.X(), pub.Y(), nil
}

// RecoverAddress returns the checksummed address that signed a digest.
func RecoverAddress(digest, sig []byte) (string, error) {
	x, y, err := SigToPub(digest, sig)
	if err != nil {
		return "", err
	}
	return Address(x, y), nil
}

// VerifySignature checks a 64-byte [R || S] signature against a public key.
//
// 64 bytes, not 65: the recovery byte is not part of the signature being
// verified, and go-ethereum's equivalent required it to be absent. A
// caller holding a 65-byte signature passes sig[:64].
func VerifySignature(pubkey, digest, sig []byte) bool {
	if len(sig) != 64 || len(digest) != 32 {
		return false
	}
	pub, err := btcec.ParsePubKey(pubkey)
	if err != nil {
		return false
	}
	var r, s btcec.ModNScalar
	if overflow := r.SetByteSlice(sig[:32]); overflow {
		return false
	}
	if overflow := s.SetByteSlice(sig[32:]); overflow {
		return false
	}
	// Reject a malleable high-S signature, as go-ethereum's verifier does.
	// Both S and N-S satisfy the equation, so accepting either means one
	// authorised transaction has two valid signatures and two hashes.
	if s.IsOverHalfOrder() {
		return false
	}
	return btcecdsa.NewSignature(&r, &s).Verify(digest, pub)
}

// HexToECDSA parses a 32-byte hex private key.
func HexToECDSA(s string) (*ecdsa.PrivateKey, error) {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("private key is not hex: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("a secp256k1 private key is 32 bytes, got %d", len(raw))
	}
	d := new(big.Int).SetBytes(raw)
	// Zero and anything at or above the group order are not private keys.
	// btcec would clamp rather than refuse, which turns an invalid key into
	// a valid one for a different account.
	if d.Sign() == 0 || d.Cmp(S256().Params().N) >= 0 {
		return nil, fmt.Errorf("private key is not in the range [1, n-1]")
	}
	return privFromD(d), nil
}

// GenerateKey returns a new random private key.
func GenerateKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(S256(), rand.Reader)
}

// FromECDSA returns the 32-byte private scalar, left-padded.
func FromECDSA(priv *ecdsa.PrivateKey) []byte {
	if priv == nil {
		return nil
	}
	return priv.D.FillBytes(make([]byte, 32))
}

// FromECDSAPub returns the 65-byte uncompressed public key.
func FromECDSAPub(pub *ecdsa.PublicKey) []byte {
	if pub == nil || pub.X == nil || pub.Y == nil {
		return nil
	}
	return MarshalPubkey(pub.X, pub.Y)
}

// PubkeyToAddress returns the checksummed address for a public key.
func PubkeyToAddress(pub ecdsa.PublicKey) string {
	return Address(pub.X, pub.Y)
}

func privFromD(d *big.Int) *ecdsa.PrivateKey {
	priv := new(ecdsa.PrivateKey)
	priv.Curve = S256()
	priv.D = d
	priv.X, priv.Y = S256().ScalarBaseMult(d.Bytes())
	return priv
}
