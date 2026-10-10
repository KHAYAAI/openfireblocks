package ethtypes

import (
	"fmt"
	"math/big"

	"forge-crypto/mpc-signer/internal/ethcrypto"
)

// Reading a signed transaction back.
//
// Only as much as the platform actually needs: the gas limit a transaction
// committed to, its nonce, recipient and value, and its hash. Not a
// general RLP decoder, and deliberately not -- the settlement path decodes
// transactions this platform itself produced, and a permissive decoder
// that accepts things no encoder here emits is surface with no
// corresponding use.
//
// The hash needs no decoding at all. A transaction's identity is the
// Keccak hash of exactly the bytes broadcast, so it is computed from the
// bytes rather than reconstructed from parsed fields -- which also means
// it stays correct for any transaction type, including ones this package
// cannot otherwise read.

// SignedTxInfo is what can be read back out of a signed transaction.
type SignedTxInfo struct {
	Type    byte // 0 for legacy, 2 for EIP-1559
	Hash    []byte
	Nonce   uint64
	Gas     uint64
	Value   *big.Int
	To      []byte // nil for contract creation
	ChainID *big.Int
}

// TransactionHash is the identity of a signed transaction.
//
// Keccak of the broadcast bytes, whatever they encode. Kept separate from
// DecodeSignedTransaction because a caller that only needs the hash should
// not be able to fail on a transaction type this package does not parse.
func TransactionHash(raw []byte) []byte {
	return ethcrypto.Keccak256(raw)
}

// DecodeSignedTransaction reads the fields the platform uses.
func DecodeSignedTransaction(raw []byte) (*SignedTxInfo, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty transaction")
	}

	info := &SignedTxInfo{Hash: TransactionHash(raw)}
	body := raw

	// A legacy transaction is bare RLP, so its first byte is a list prefix
	// (>= 0xc0). Anything lower is an EIP-2718 type byte.
	if raw[0] < 0xc0 {
		info.Type = raw[0]
		if raw[0] != DynamicFeeTxType {
			return nil, fmt.Errorf("transaction type %#x is not one this platform produces", raw[0])
		}
		body = raw[1:]
	}

	items, err := splitList(body)
	if err != nil {
		return nil, err
	}

	// Field order differs between the two forms, and reading the wrong
	// index yields a plausible number rather than an error -- a gas limit
	// read out of the nonce slot, for instance.
	if info.Type == DynamicFeeTxType {
		// chainId, nonce, tip, maxFee, gas, to, value, data, accessList, v, r, s
		if len(items) != 12 {
			return nil, fmt.Errorf("an EIP-1559 transaction has 12 fields, got %d", len(items))
		}
		info.ChainID = new(big.Int).SetBytes(items[0])
		info.Nonce = beUint(items[1])
		info.Gas = beUint(items[4])
		info.To = addressOrNil(items[5])
		info.Value = new(big.Int).SetBytes(items[6])
		return info, nil
	}

	// nonce, gasPrice, gas, to, value, data, v, r, s
	if len(items) != 9 {
		return nil, fmt.Errorf("a legacy transaction has 9 fields, got %d", len(items))
	}
	info.Nonce = beUint(items[0])
	info.Gas = beUint(items[2])
	info.To = addressOrNil(items[3])
	info.Value = new(big.Int).SetBytes(items[4])
	// EIP-155 folds the chain id into v: v = recovery + 35 + 2*chainId.
	if v := new(big.Int).SetBytes(items[6]); v.Cmp(big.NewInt(35)) >= 0 {
		info.ChainID = new(big.Int).Rsh(new(big.Int).Sub(v, big.NewInt(35)), 1)
	}
	return info, nil
}

func addressOrNil(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

func beUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

// splitList returns the payloads of a list's top-level items.
//
// Strict about the outer length: trailing bytes after a well-formed list
// mean the input is not the single transaction it claims to be, and a
// decoder that reads the prefix and ignores the rest is how a caller ends
// up acting on the first of two concatenated transactions.
func splitList(b []byte) ([][]byte, error) {
	payload, rest, err := readItem(b, true)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%d trailing bytes after the transaction", len(rest))
	}

	var items [][]byte
	for len(payload) > 0 {
		// Either kind. Most fields are byte strings, but an EIP-1559
		// transaction's access list is a nested list -- demanding a string
		// here would reject every 1559 transaction, and it is the field
		// this decoder skips rather than reads.
		item, remaining, err := readAny(payload)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		payload = remaining
	}
	return items, nil
}

// readAny reads one RLP item of either kind.
func readAny(b []byte) (payload, rest []byte, err error) {
	if len(b) == 0 {
		return nil, nil, fmt.Errorf("unexpected end of input")
	}
	return readItem(b, b[0] >= 0xc0)
}

// readItem reads one RLP item, returning its payload and what follows.
//
// wantList refuses a string where a list was expected, and vice versa: the
// prefix ranges do not overlap, so confusing them is always a malformed
// input rather than a difference of interpretation.
func readItem(b []byte, wantList bool) (payload, rest []byte, err error) {
	if len(b) == 0 {
		return nil, nil, fmt.Errorf("unexpected end of input")
	}
	prefix := b[0]

	switch {
	case prefix < 0x80:
		// A single byte that is its own encoding.
		if wantList {
			return nil, nil, fmt.Errorf("expected a list, found a single byte")
		}
		return b[:1], b[1:], nil

	case prefix <= 0xb7: // short string
		if wantList {
			return nil, nil, fmt.Errorf("expected a list, found a string")
		}
		n := int(prefix - 0x80)
		if len(b) < 1+n {
			return nil, nil, fmt.Errorf("string runs past the end of the input")
		}
		return b[1 : 1+n], b[1+n:], nil

	case prefix <= 0xbf: // long string
		if wantList {
			return nil, nil, fmt.Errorf("expected a list, found a string")
		}
		return readLong(b, prefix-0xb7)

	case prefix <= 0xf7: // short list
		if !wantList {
			return nil, nil, fmt.Errorf("expected a string, found a list")
		}
		n := int(prefix - 0xc0)
		if len(b) < 1+n {
			return nil, nil, fmt.Errorf("list runs past the end of the input")
		}
		return b[1 : 1+n], b[1+n:], nil

	default: // long list
		if !wantList {
			return nil, nil, fmt.Errorf("expected a string, found a list")
		}
		return readLong(b, prefix-0xf7)
	}
}

func readLong(b []byte, lenOfLen byte) (payload, rest []byte, err error) {
	if lenOfLen == 0 || int(lenOfLen) > 8 {
		return nil, nil, fmt.Errorf("implausible length-of-length %d", lenOfLen)
	}
	if len(b) < 1+int(lenOfLen) {
		return nil, nil, fmt.Errorf("length field runs past the end of the input")
	}
	lengthBytes := b[1 : 1+int(lenOfLen)]
	if lengthBytes[0] == 0 {
		// A non-canonical encoding: the same value with a shorter length
		// field would be a different byte string for the same transaction,
		// and therefore a different hash.
		return nil, nil, fmt.Errorf("non-canonical length with a leading zero byte")
	}
	n := beUint(lengthBytes)
	if n <= 55 {
		return nil, nil, fmt.Errorf("non-canonical: %d bytes should use the short form", n)
	}
	start := 1 + uint64(lenOfLen)
	if uint64(len(b)) < start+n {
		return nil, nil, fmt.Errorf("item runs past the end of the input")
	}
	return b[start : start+n], b[start+n:], nil
}
