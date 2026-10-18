// Package keys separates "who holds the signing key" from "what gets
// signed".
//
// Until this existed, every signer in this service was handed a raw hex
// private key and parsed it itself: MPCSigner at startup, and each of the
// Ethereum, Bitcoin and Cosmos chain signers on every call. That made the
// key's location a property of every signing function, and it meant the
// key could only ever be somewhere a Go string could hold it -- process
// memory, one parse away from a heap dump.
//
// KeySigner is the one operation all of them actually needed: sign a
// 32-byte secp256k1 digest and hand back a recoverable signature. What
// sits behind it is now a deployment choice. RawKeySigner keeps today's
// behaviour exactly. The PKCS#11 signer in this package (built with
// -tags pkcs11) keeps the key inside a hardware security module, where
// the signing happens and from which the key cannot be exported.
//
// What this does not change, and must not be read as changing: the
// threshold signing in services/mpc-party. A tss-lib ceremony does
// arithmetic on its share in every round, which PKCS#11 has no operation
// for. See docs/engineering/PKCS11-HSM-SIGNING.md for why "the MPC shares
// live in the HSM" is a different project from this one.
package keys

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"

	"forge-crypto/mpc-signer/internal/ethcrypto"
)

// KeySigner signs secp256k1 digests with a key it need not reveal.
type KeySigner interface {
	// PublicKey returns the 65-byte uncompressed SEC1 encoding,
	// 0x04 || X || Y. Every chain derives its address from this.
	PublicKey() []byte

	// SignDigest signs a 32-byte digest and returns 65 bytes in
	// Ethereum's layout, [R || S || V], with S in the lower half of the
	// group order and V in {0, 1}.
	//
	// That one layout, fixed here, is what lets each chain re-encode the
	// result into its own convention without caring where the signature
	// came from: Ethereum uses it as is, Bitcoin reorders it to
	// [V+31 || R || S], Cosmos keeps the first 64 bytes. A backend that
	// cannot produce it directly -- PKCS#11 returns bare R || S with
	// whatever S the token chose -- normalises through
	// RecoverableSignature, which is the only place that logic lives.
	SignDigest(ctx context.Context, digest []byte) ([]byte, error)

	// Describe names where the key is, for logs and GET /info. Never
	// includes key material.
	Describe() string
}

// Address returns the EIP-55 Ethereum address for a KeySigner's key.
func Address(k KeySigner) (string, error) {
	x, y, err := coordinates(k.PublicKey())
	if err != nil {
		return "", err
	}
	return ethcrypto.Address(x, y), nil
}

// CompressedPublicKey returns the 33-byte SEC1 compressed form, which is
// what Bitcoin addresses and Cosmos account keys are derived from.
func CompressedPublicKey(k KeySigner) ([]byte, error) {
	x, y, err := coordinates(k.PublicKey())
	if err != nil {
		return nil, err
	}
	return ethcrypto.CompressPubkey(x, y), nil
}

func coordinates(pub []byte) (x, y *big.Int, err error) {
	if len(pub) != 65 || pub[0] != 4 {
		return nil, nil, fmt.Errorf("a public key must be 65 bytes of uncompressed SEC1 (0x04 || X || Y), got %d bytes", len(pub))
	}
	// Parsed through btcec rather than sliced, so a point that is not on
	// secp256k1 is refused here instead of producing an address for a
	// key that cannot exist.
	parsed, err := btcec.ParsePubKey(pub)
	if err != nil {
		return nil, nil, fmt.Errorf("the public key is not a point on secp256k1: %w", err)
	}
	return parsed.X(), parsed.Y(), nil
}

// ---------------------------------------------------------------------------
// The software key -- today's behaviour, behind the interface
// ---------------------------------------------------------------------------

// RawKeySigner holds a private key in process memory.
//
// Exactly what every signer in this service did before this package
// existed, and still the right default for development, testnets and any
// deployment whose threat model does not require hardware. It is also the
// reference the hardware path is tested against: for the same key the two
// must give the same address, and each one's signatures must verify under
// the other's public key. Not byte-identical signatures -- this one is
// RFC 6979 deterministic, while PKCS#11 CKM_ECDSA on most tokens draws a
// random nonce. Both are valid, low-S, and recover to the same sender.
type RawKeySigner struct {
	priv *ecdsa.PrivateKey
	pub  []byte
}

// NewRawKeySigner wraps an existing private key.
func NewRawKeySigner(priv *ecdsa.PrivateKey) *RawKeySigner {
	return &RawKeySigner{
		priv: priv,
		pub:  ethcrypto.MarshalPubkey(priv.PublicKey.X, priv.PublicKey.Y),
	}
}

// RawKeySignerFromHex parses a 32-byte hex private key, with or without
// a 0x prefix.
func RawKeySignerFromHex(privKeyHex string) (*RawKeySigner, error) {
	priv, err := ethcrypto.HexToECDSA(strings.TrimPrefix(strings.TrimPrefix(privKeyHex, "0x"), "0X"))
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	return NewRawKeySigner(priv), nil
}

func (r *RawKeySigner) PublicKey() []byte { return append([]byte(nil), r.pub...) }

func (r *RawKeySigner) SignDigest(_ context.Context, digest []byte) ([]byte, error) {
	return ethcrypto.Sign(digest, r.priv)
}

func (r *RawKeySigner) Describe() string {
	return "software key in process memory"
}

// ---------------------------------------------------------------------------
// Normalising a bare signature
// ---------------------------------------------------------------------------

// RecoverableSignature turns a bare 64-byte R || S into the [R || S || V]
// layout KeySigner promises, given the digest and the public key that
// should have produced it.
//
// Two things a hardware signer does not give you, and both matter:
//
//   - Low S. ECDSA is malleable: if (R, S) verifies, so does (R, N-S).
//     Ethereum (EIP-2) and Bitcoin (BIP-62/146) both reject the high form,
//     so one authorised transaction must have exactly one signature. An
//     HSM implementing plain ECDSA picks whichever S its arithmetic lands
//     on -- about half the time, the one every node on the network will
//     refuse.
//   - The recovery id. ECDSA has two candidate public keys for any
//     signature; V says which. Ethereum needs it to recover the sender,
//     and PKCS#11 has no field to report it in. It is recovered here by
//     trying both and keeping the one that yields the expected key.
//
// The second of those is also a check, and a valuable one. If neither
// candidate recovers the expected public key, the token signed with a key
// other than the one it reported -- a wrong key label, a rotated key, a
// misconfigured slot. That is refused rather than returned, because the
// alternative is a well-formed signature for an address nobody expects,
// broadcast to a chain that will accept it.
func RecoverableSignature(digest, rs, expectedPub []byte) ([]byte, error) {
	if len(digest) != 32 {
		return nil, fmt.Errorf("a digest is 32 bytes, got %d", len(digest))
	}
	if len(rs) != 64 {
		return nil, fmt.Errorf("a bare secp256k1 signature is 64 bytes (R || S), got %d", len(rs))
	}

	var r, s btcec.ModNScalar
	if overflow := r.SetByteSlice(rs[:32]); overflow || r.IsZero() {
		return nil, fmt.Errorf("R is zero or not reduced modulo the group order")
	}
	if overflow := s.SetByteSlice(rs[32:]); overflow || s.IsZero() {
		return nil, fmt.Errorf("S is zero or not reduced modulo the group order")
	}
	if s.IsOverHalfOrder() {
		s.Negate()
	}

	sig := make([]byte, 65)
	rBytes, sBytes := r.Bytes(), s.Bytes()
	copy(sig[0:32], rBytes[:])
	copy(sig[32:64], sBytes[:])

	for v := byte(0); v <= 1; v++ {
		sig[64] = v
		x, y, err := ethcrypto.SigToPub(digest, sig)
		if err != nil {
			continue
		}
		if bytes.Equal(ethcrypto.MarshalPubkey(x, y), expectedPub) {
			// And verify outright. Recovery succeeding is strong
			// evidence, not proof; the verifier is the same one every
			// consumer of this signature will run.
			if !ethcrypto.VerifySignature(expectedPub, digest, sig[:64]) {
				return nil, fmt.Errorf("the signature recovers to the expected key but does not verify")
			}
			return sig, nil
		}
	}
	return nil, fmt.Errorf("the signature does not recover to the expected public key under either " +
		"recovery id; the signer used a different key from the one it reports")
}
