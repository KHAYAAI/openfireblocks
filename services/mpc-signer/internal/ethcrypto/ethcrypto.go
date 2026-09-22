// Package ethcrypto derives Ethereum public-key encodings and addresses.
//
// It exists to remove github.com/ethereum/go-ethereum, the only copyleft
// dependency in this codebase. go-ethereum's library is LGPL-3.0, which
// permits commercial and proprietary use but requires that a recipient of
// a distributed binary can relink it against their own build of the
// library. Go links statically, so there is nothing to relink -- an
// awkward fit that is discharged by shipping source and not otherwise.
// See docs/LICENSING.md.
//
// What is replaced here is not cryptography. The curve arithmetic comes
// from decred/dcrd's secp256k1 (ISC) by way of btcec, which was already in
// this module's graph, and Keccak comes from golang.org/x/crypto (BSD-3),
// also already present. What go-ethereum was supplying was three pieces of
// encoding: how a public key becomes bytes, how those bytes become an
// address, and how that address is checksummed.
//
// Encoding is exactly where a quiet mistake is expensive. An address
// derived with the wrong hash function is a perfectly well-formed address
// for an account nobody controls, and it looks identical to a correct one.
// So every function here is pinned to golden vectors generated from
// go-ethereum itself (testdata_vectors.json), rather than to a second
// reading of the specification.
package ethcrypto

import (
	"crypto/elliptic"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2"
	"golang.org/x/crypto/sha3"
)

// S256 returns the secp256k1 curve.
//
// The same curve go-ethereum's crypto.S256() returned, and the same one
// tss-lib is already using for every ECDSA ceremony -- tss.S256() is this
// implementation. There was never a second curve involved.
func S256() elliptic.Curve {
	return btcec.S256()
}

// Keccak256 is Ethereum's hash.
//
// NewLegacyKeccak256, not New256. Ethereum uses the original Keccak
// submission, and SHA-3 as standardised differs from it in the padding
// rule. The two produce entirely different digests, so reaching for the
// obvious-looking sha3.New256 would yield addresses that are well-formed,
// stable, and wrong -- for accounts whose keys do not exist. The golden
// vectors are what stops that being a silent mistake.
func Keccak256(data ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, b := range data {
		h.Write(b)
	}
	return h.Sum(nil)
}

// MarshalPubkey returns the 65-byte uncompressed encoding: 0x04 || X || Y.
//
// Both coordinates are left-padded to 32 bytes. A big.Int drops leading
// zeroes, and a public key whose X happens to start with a zero byte would
// otherwise encode one byte short -- shifting every subsequent byte and
// producing a different, valid-looking key. FillBytes pads rather than
// truncating, and panics if the value does not fit, which is the correct
// response to a coordinate that is not on this curve.
func MarshalPubkey(x, y *big.Int) []byte {
	out := make([]byte, 65)
	out[0] = 4
	x.FillBytes(out[1:33])
	y.FillBytes(out[33:65])
	return out
}

// CompressPubkey returns the 33-byte encoding: a parity byte then X.
func CompressPubkey(x, y *big.Int) []byte {
	out := make([]byte, 33)
	out[0] = 2
	if y.Bit(0) == 1 {
		out[0] = 3
	}
	x.FillBytes(out[1:33])
	return out
}

// AddressBytes returns the raw 20-byte address for a public key.
//
// Keccak-256 of the 64 concatenated coordinate bytes -- note that the
// 0x04 prefix from the uncompressed encoding is NOT hashed -- keeping the
// last 20 bytes.
func AddressBytes(x, y *big.Int) []byte {
	return Keccak256(MarshalPubkey(x, y)[1:])[12:]
}

// Address returns the 0x-prefixed, EIP-55 checksummed address.
func Address(x, y *big.Int) string {
	return ChecksumAddress(AddressBytes(x, y))
}

// ChecksumAddress applies EIP-55 to a 20-byte address.
//
// The mixed case is a checksum, not decoration: each hex letter is
// uppercased when the corresponding nibble of the Keccak hash of the
// lowercase address is 8 or greater. It catches a mistyped address that
// would otherwise be a perfectly valid destination for somebody else's
// money -- which is why this is worth reproducing exactly rather than
// returning a lowercase address that would still "work" everywhere.
func ChecksumAddress(addr []byte) string {
	lower := hex.EncodeToString(addr)
	hash := hex.EncodeToString(Keccak256([]byte(lower)))

	out := make([]byte, 0, 42)
	out = append(out, '0', 'x')
	for i := 0; i < len(lower); i++ {
		c := lower[i]
		// Digits have no case to carry the checksum; only a-f do.
		if c >= 'a' && c <= 'f' && hash[i] >= '8' {
			c -= 32 // to upper
		}
		out = append(out, c)
	}
	return string(out)
}

// ParseAddress accepts a 0x-prefixed or bare hex address.
//
// Case-insensitive, and it does NOT verify an EIP-55 checksum. Rejecting a
// mixed-case address whose checksum fails would be the stricter choice and
// a change in behaviour from what this replaces; validating a checksum
// belongs at the edge where a customer's input arrives, not in a decoder
// used on values the platform generated itself.
func ParseAddress(s string) ([]byte, error) {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		s = s[2:]
	}
	if len(s) != 40 {
		return nil, fmt.Errorf("an address is 20 bytes of hex, got %d characters", len(s))
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("address is not hex: %w", err)
	}
	return raw, nil
}

// IsHexAddress reports whether s parses as an address.
func IsHexAddress(s string) bool {
	_, err := ParseAddress(s)
	return err == nil
}
