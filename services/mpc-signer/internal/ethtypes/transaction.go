package ethtypes

import (
	"fmt"
	"math/big"

	"forge-crypto/mpc-signer/internal/ethcrypto"
)

// DynamicFeeTxType is the EIP-2718 type byte for an EIP-1559 transaction.
//
// A legacy transaction has no type byte at all -- it is bare RLP, and its
// first byte is necessarily >= 0xc0 because it is a list. That is how a
// node tells the two apart, and it is why a typed transaction's envelope
// is 0x02 followed by RLP rather than RLP containing a 2.
const DynamicFeeTxType = 0x02

// Transaction is an unsigned Ethereum transaction.
//
// One struct for both forms rather than two, with GasPrice selecting the
// legacy path and the MaxFee/Tip pair selecting EIP-1559. A caller that
// sets neither or both is refused -- the two fee models are alternatives,
// and quietly preferring one would sign a transaction with a fee the
// caller did not intend.
type Transaction struct {
	ChainID  *big.Int
	Nonce    uint64
	Gas      uint64
	GasPrice *big.Int // legacy only
	MaxFee   *big.Int // EIP-1559 only
	Tip      *big.Int // EIP-1559 only
	// To is nil for contract creation, which RLP-encodes as the empty
	// string. A 20-byte zero address is a different thing entirely: it is
	// a transfer to an account nobody controls, and the money is gone.
	To    []byte
	Value *big.Int
	Data  []byte
}

// IsDynamic reports whether this is an EIP-1559 transaction.
func (t *Transaction) IsDynamic() bool {
	return t.MaxFee != nil || t.Tip != nil
}

func (t *Transaction) validate() error {
	if t.ChainID == nil || t.ChainID.Sign() <= 0 {
		// A missing chain id is what makes a signed transaction replayable
		// on every other EVM chain. EIP-155 exists precisely to bind a
		// signature to one chain, so there is no sensible default here.
		return fmt.Errorf("a chain id is required; without one the signature is replayable on any EVM chain")
	}
	legacy := t.GasPrice != nil
	dynamic := t.IsDynamic()
	if legacy && dynamic {
		return fmt.Errorf("supply either gasPrice (legacy) or maxFee and tip (EIP-1559), not both")
	}
	if !legacy && !dynamic {
		return fmt.Errorf("supply either gasPrice (legacy) or maxFee and tip (EIP-1559)")
	}
	if dynamic && (t.MaxFee == nil || t.Tip == nil) {
		return fmt.Errorf("EIP-1559 needs both maxFee and tip")
	}
	if t.To != nil && len(t.To) != 20 {
		return fmt.Errorf("a recipient address is 20 bytes, got %d", len(t.To))
	}
	if t.Value != nil && t.Value.Sign() < 0 {
		return fmt.Errorf("value cannot be negative")
	}
	return nil
}

// toField encodes the recipient: 20 bytes, or the empty string for a
// contract creation.
func (t *Transaction) toField() []byte {
	if t.To == nil {
		return rlpString(nil)
	}
	return rlpString(t.To)
}

// SigningHash is the digest that gets signed.
//
// Legacy uses EIP-155: the nine-field list where the last three are the
// chain id and two zeroes, standing in for the v, r, s that do not exist
// yet. Those trailing zeroes are not padding -- omitting them produces the
// pre-EIP-155 hash, which signs a transaction that is valid on every EVM
// chain simultaneously.
//
// EIP-1559 hashes the type byte followed by the nine-field list, with no
// such placeholders.
func (t *Transaction) SigningHash() ([]byte, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	if t.IsDynamic() {
		payload := rlpList(concat(
			rlpBig(t.ChainID),
			rlpUint(t.Nonce),
			rlpBig(t.Tip),
			rlpBig(t.MaxFee),
			rlpUint(t.Gas),
			t.toField(),
			rlpBig(t.Value),
			rlpString(t.Data),
			// An empty access list. Encoded as an empty list (0xc0), not
			// omitted: the field count is part of the format.
			rlpList(nil),
		))
		return ethcrypto.Keccak256(append([]byte{DynamicFeeTxType}, payload...)), nil
	}

	payload := rlpList(concat(
		rlpUint(t.Nonce),
		rlpBig(t.GasPrice),
		rlpUint(t.Gas),
		t.toField(),
		rlpBig(t.Value),
		rlpString(t.Data),
		rlpBig(t.ChainID),
		rlpUint(0),
		rlpUint(0),
	))
	return ethcrypto.Keccak256(payload), nil
}

// SignedTransaction is a transaction with its signature attached.
type SignedTransaction struct {
	Raw  []byte // the bytes to broadcast
	Hash []byte // keccak of those bytes, the transaction's identity
	From string // recovered, not asserted
}

// WithSignature attaches a 65-byte [R || S || V] signature and returns the
// broadcastable transaction.
//
// The signature is not trusted. The sender is recovered from it and
// compared against expectedFrom, because a signing ceremony can return a
// well-formed signature belonging to a different key -- and handing that
// back would give a customer a valid transaction spending from an address
// they do not control. Cheap to check, and the failure it catches is
// otherwise silent.
func (t *Transaction) WithSignature(sig []byte, expectedFrom string) (*SignedTransaction, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	if len(sig) != ethcrypto.SignatureLength {
		return nil, fmt.Errorf("a signature is %d bytes, got %d", ethcrypto.SignatureLength, len(sig))
	}

	digest, err := t.SigningHash()
	if err != nil {
		return nil, err
	}
	from, err := ethcrypto.RecoverAddress(digest, sig)
	if err != nil {
		return nil, fmt.Errorf("the signature does not recover to any public key: %w", err)
	}
	if expectedFrom != "" && !equalAddress(from, expectedFrom) {
		return nil, fmt.Errorf(
			"the signature recovers to %s but this key is %s; refusing to return a "+
				"transaction that spends from an address the customer does not control",
			from, expectedFrom)
	}

	r := new(big.Int).SetBytes(sig[0:32])
	s := new(big.Int).SetBytes(sig[32:64])
	recovery := sig[64]
	if recovery >= 27 {
		recovery -= 27
	}

	var raw []byte
	if t.IsDynamic() {
		// EIP-1559 carries the recovery id bare as yParity.
		payload := rlpList(concat(
			rlpBig(t.ChainID),
			rlpUint(t.Nonce),
			rlpBig(t.Tip),
			rlpBig(t.MaxFee),
			rlpUint(t.Gas),
			t.toField(),
			rlpBig(t.Value),
			rlpString(t.Data),
			rlpList(nil),
			rlpUint(uint64(recovery)),
			rlpBig(r),
			rlpBig(s),
		))
		raw = append([]byte{DynamicFeeTxType}, payload...)
	} else {
		// EIP-155 folds the chain id into v: v = recovery + 35 + 2*chainId.
		// This is what binds a legacy signature to one chain, and getting
		// the arithmetic wrong yields a transaction every node rejects --
		// or worse, one that a different chain accepts.
		v := new(big.Int).SetUint64(uint64(recovery) + 35)
		v.Add(v, new(big.Int).Mul(t.ChainID, big.NewInt(2)))

		raw = rlpList(concat(
			rlpUint(t.Nonce),
			rlpBig(t.GasPrice),
			rlpUint(t.Gas),
			t.toField(),
			rlpBig(t.Value),
			rlpString(t.Data),
			rlpBig(v),
			rlpBig(r),
			rlpBig(s),
		))
	}

	return &SignedTransaction{
		Raw:  raw,
		Hash: ethcrypto.Keccak256(raw),
		From: from,
	}, nil
}

// equalAddress compares two addresses ignoring EIP-55 case.
func equalAddress(a, b string) bool {
	ab, err := ethcrypto.ParseAddress(a)
	if err != nil {
		return false
	}
	bb, err := ethcrypto.ParseAddress(b)
	if err != nil {
		return false
	}
	if len(ab) != len(bb) {
		return false
	}
	for i := range ab {
		if ab[i] != bb[i] {
			return false
		}
	}
	return true
}
