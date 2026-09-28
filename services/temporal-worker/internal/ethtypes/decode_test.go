package ethtypes

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

// Reading back exactly what was written.
//
// The same golden vectors, used from the other direction: every signed
// transaction go-ethereum produced must decode to the fields it was built
// from. A decoder that reads the wrong index does not error -- it returns
// a plausible number from the neighbouring field, which is how a gas limit
// gets read out of a nonce.

func TestEveryVectorDecodesToTheFieldsItWasBuiltFrom(t *testing.T) {
	for _, v := range txVectors(t) {
		t.Run(v.Name, func(t *testing.T) {
			raw, err := hex.DecodeString(strings.TrimPrefix(v.Raw, "0x"))
			if err != nil {
				t.Fatalf("bad vector: %v", err)
			}
			info, err := DecodeSignedTransaction(raw)
			if err != nil {
				t.Fatalf("decoding: %v", err)
			}

			if info.Nonce != v.Nonce {
				t.Errorf("nonce %d, want %d", info.Nonce, v.Nonce)
			}
			if info.Gas != v.Gas {
				t.Errorf("gas %d, want %d", info.Gas, v.Gas)
			}
			if want := bigOrNil(v.Value); want != nil && info.Value.Cmp(want) != 0 {
				t.Errorf("value %s, want %s", info.Value, want)
			}
			if info.ChainID == nil || info.ChainID.Int64() != v.ChainID {
				t.Errorf("chain id %v, want %d", info.ChainID, v.ChainID)
			}

			wantType := byte(0)
			if v.Dynamic {
				wantType = DynamicFeeTxType
			}
			if info.Type != wantType {
				t.Errorf("type %#x, want %#x", info.Type, wantType)
			}

			if v.To == "" {
				if info.To != nil {
					t.Errorf("a contract creation decoded a recipient %x", info.To)
				}
			} else {
				if got := "0x" + hex.EncodeToString(info.To); !strings.EqualFold(got, v.To) {
					t.Errorf("recipient %s, want %s", got, v.To)
				}
			}

			if got := "0x" + hex.EncodeToString(info.Hash); !strings.EqualFold(got, v.TxHash) {
				t.Errorf("hash %s, want %s", got, v.TxHash)
			}
		})
	}
}

// The hash is the keccak of the broadcast bytes, so it must be derivable
// without understanding the transaction at all.
func TestTheHashNeedsNoDecoding(t *testing.T) {
	for _, v := range txVectors(t) {
		raw, _ := hex.DecodeString(strings.TrimPrefix(v.Raw, "0x"))
		if got := "0x" + hex.EncodeToString(TransactionHash(raw)); !strings.EqualFold(got, v.TxHash) {
			t.Errorf("%s: %s, want %s", v.Name, got, v.TxHash)
		}
	}
}

// -- malformed input --

func TestTrailingBytesAreRefused(t *testing.T) {
	v := txVectors(t)[0]
	raw, _ := hex.DecodeString(strings.TrimPrefix(v.Raw, "0x"))

	if _, err := DecodeSignedTransaction(append(raw, 0x01)); err == nil {
		t.Fatal("a transaction with a trailing byte was accepted; two concatenated " +
			"transactions would decode as the first")
	}
}

func TestTruncatedInputIsRefused(t *testing.T) {
	v := txVectors(t)[0]
	raw, _ := hex.DecodeString(strings.TrimPrefix(v.Raw, "0x"))

	for _, cut := range []int{1, len(raw) / 2, len(raw) - 1} {
		if _, err := DecodeSignedTransaction(raw[:cut]); err == nil {
			t.Errorf("a transaction truncated to %d bytes was accepted", cut)
		}
	}
}

func TestAnEmptyInputIsRefused(t *testing.T) {
	if _, err := DecodeSignedTransaction(nil); err == nil {
		t.Error("an empty transaction was accepted")
	}
}

func TestAnUnknownTransactionTypeIsRefused(t *testing.T) {
	// Type 0x03 is EIP-4844, which this platform does not produce.
	if _, err := DecodeSignedTransaction([]byte{0x03, 0xc0}); err == nil {
		t.Error("an unsupported transaction type was decoded anyway")
	}
}

// A length field with a leading zero encodes the same value as a shorter
// one, so the same transaction would have two encodings and two hashes.
func TestNonCanonicalLengthsAreRefused(t *testing.T) {
	// 0xb9 says "two length bytes follow"; 0x00 0x38 is 56 written
	// non-canonically.
	bad := append([]byte{0xf9, 0x00, 0x38}, make([]byte, 56)...)
	if _, err := DecodeSignedTransaction(bad); err == nil {
		t.Error("a non-canonical length was accepted")
	}
}

func TestAListWithTheWrongFieldCountIsRefused(t *testing.T) {
	// A well-formed three-element list is not a transaction.
	short := rlpList(concat(rlpUint(1), rlpUint(2), rlpUint(3)))
	if _, err := DecodeSignedTransaction(short); err == nil {
		t.Error("a three-field list was decoded as a transaction")
	}
}

// Round-trip through this package alone, for a transaction go-ethereum
// never saw.
func TestEncodeThenDecodeRoundTrips(t *testing.T) {
	to := make([]byte, 20)
	to[19] = 0xbb
	tx := &Transaction{
		ChainID: big.NewInt(424242), Nonce: 9, MaxFee: big.NewInt(77),
		Tip: big.NewInt(3), Gas: 123456, To: to, Value: big.NewInt(555),
		Data: []byte{0xde, 0xad},
	}
	sig := make([]byte, 65)
	for i := range sig[:64] {
		sig[i] = byte(i + 1)
	}

	// WithSignature recovers a sender, so pass no expectation.
	signed, err := tx.WithSignature(sig, "")
	if err != nil {
		t.Skipf("this contrived signature does not recover: %v", err)
	}
	info, err := DecodeSignedTransaction(signed.Raw)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if info.Nonce != 9 || info.Gas != 123456 || info.Value.Int64() != 555 {
		t.Errorf("round-trip lost fields: %+v", info)
	}
	if info.ChainID.Int64() != 424242 {
		t.Errorf("chain id %v", info.ChainID)
	}
}
