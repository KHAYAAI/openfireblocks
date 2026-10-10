package chains

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	btcecdsa "github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"google.golang.org/protobuf/encoding/protowire"
)

// Cosmos SDK transactions in SIGN_MODE_DIRECT, hand-encoded.
//
// The SDK's own types would bring a dependency tree this module has so far
// refused (see cosmos.go), and the encoding needed to send tokens is small
// and stable. protowire, which ships with google.golang.org/protobuf, does
// the wire format; the field numbers below come from the cosmos-sdk protos
// (cosmos/tx/v1beta1/tx.proto, cosmos/bank/v1beta1/tx.proto,
// cosmos/base/v1beta1/coin.proto, cosmos/crypto/secp256k1/keys.proto):
//
//	TxRaw    { body_bytes=1 auth_info_bytes=2 signatures=3 }
//	TxBody   { messages=1 (Any) memo=2 timeout_height=3 }
//	AuthInfo { signer_infos=1 fee=2 }
//	SignerInfo { public_key=1 (Any) mode_info=2 sequence=3 }
//	ModeInfo { single=1 { mode=1 } }          SIGN_MODE_DIRECT = 1
//	Fee      { amount=1 (Coin) gas_limit=2 }
//	SignDoc  { body_bytes=1 auth_info_bytes=2 chain_id=3 account_number=4 }
//	MsgSend  { from_address=1 to_address=2 amount=3 (Coin) }
//	Coin     { denom=1 amount=2 }              PubKey { key=1 }
//
// Proto3 omits zero values, and the node rebuilds the SignDoc the same way,
// so an account_number or sequence of 0 must be ABSENT here, not written as
// 0 -- writing it would sign different bytes from the ones the node checks.

const (
	msgSendTypeURL      = "/cosmos.bank.v1beta1.MsgSend"
	secp256k1PubKeyType = "/cosmos.crypto.secp256k1.PubKey"
	signModeDirect      = 1
)

func appendBytesField(b []byte, num protowire.Number, v []byte) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, v)
}

func appendStringField(b []byte, num protowire.Number, s string) []byte {
	if s == "" {
		return b
	}
	return appendBytesField(b, num, []byte(s))
}

func appendVarintField(b []byte, num protowire.Number, v uint64) []byte {
	if v == 0 {
		return b
	}
	b = protowire.AppendTag(b, num, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

func encodeCoin(denom, amount string) []byte {
	return appendStringField(appendStringField(nil, 1, denom), 2, amount)
}

func encodeAny(typeURL string, value []byte) []byte {
	return appendBytesField(appendStringField(nil, 1, typeURL), 2, value)
}

// CosmosSend describes a bank MsgSend of one denomination.
type CosmosSend struct {
	ChainID       string
	AccountNumber uint64
	Sequence      uint64
	From          string // bech32
	To            string // bech32
	Denom         string
	Amount        string // base units, base-10
	FeeDenom      string
	FeeAmount     string
	GasLimit      uint64
	Memo          string
	// PubKey is the signer's secp256k1 public key, 33 or 65 bytes.
	PubKey []byte
}

// CosmosSigningPlan is what a threshold ceremony needs and what finalize
// needs back.
type CosmosSigningPlan struct {
	BodyBytesHex     string `json:"body_bytes_hex"`
	AuthInfoBytesHex string `json:"auth_info_bytes_hex"`
	ChainID          string `json:"chain_id"`
	AccountNumber    uint64 `json:"account_number"`
	// DigestHex is SHA-256 of the SignDoc: the 32 bytes to be signed.
	DigestHex string `json:"digest_hex"`
}

func validateCosmosAmount(label, v string) error {
	n, ok := new(big.Int).SetString(v, 10)
	if !ok || n.Sign() <= 0 || strings.TrimLeft(v, "0") != v {
		return fmt.Errorf("%s must be a positive base-10 integer without leading zeros, got %q", label, v)
	}
	return nil
}

// PlanCosmosSend builds the SIGN_MODE_DIRECT transaction for a send and the
// digest to threshold-sign.
func PlanCosmosSend(s CosmosSend) (*CosmosSigningPlan, error) {
	if s.ChainID == "" {
		return nil, fmt.Errorf("chain_id is required")
	}
	if s.From == "" || s.To == "" || s.Denom == "" || s.FeeDenom == "" {
		return nil, fmt.Errorf("from, to, denom and fee denom are required")
	}
	if s.From == s.To {
		return nil, fmt.Errorf("source and destination are the same account")
	}
	if err := validateCosmosAmount("amount", s.Amount); err != nil {
		return nil, err
	}
	if err := validateCosmosAmount("fee", s.FeeAmount); err != nil {
		return nil, err
	}
	if s.GasLimit == 0 {
		return nil, fmt.Errorf("gas limit is required")
	}
	pub, err := btcec.ParsePubKey(s.PubKey)
	if err != nil {
		return nil, fmt.Errorf("public key is not a secp256k1 key: %w", err)
	}

	msg := appendStringField(nil, 1, s.From)
	msg = appendStringField(msg, 2, s.To)
	msg = appendBytesField(msg, 3, encodeCoin(s.Denom, s.Amount))
	body := appendBytesField(nil, 1, encodeAny(msgSendTypeURL, msg))
	body = appendStringField(body, 2, s.Memo)

	pubAny := encodeAny(secp256k1PubKeyType, appendBytesField(nil, 1, pub.SerializeCompressed()))
	single := appendVarintField(nil, 1, signModeDirect)
	modeInfo := appendBytesField(nil, 1, single)
	signerInfo := appendBytesField(nil, 1, pubAny)
	signerInfo = appendBytesField(signerInfo, 2, modeInfo)
	signerInfo = appendVarintField(signerInfo, 3, s.Sequence)
	fee := appendBytesField(nil, 1, encodeCoin(s.FeeDenom, s.FeeAmount))
	fee = appendVarintField(fee, 2, s.GasLimit)
	authInfo := appendBytesField(nil, 1, signerInfo)
	authInfo = appendBytesField(authInfo, 2, fee)

	digest := cosmosSignDocDigest(body, authInfo, s.ChainID, s.AccountNumber)
	return &CosmosSigningPlan{
		BodyBytesHex:     hex.EncodeToString(body),
		AuthInfoBytesHex: hex.EncodeToString(authInfo),
		ChainID:          s.ChainID,
		AccountNumber:    s.AccountNumber,
		DigestHex:        hex.EncodeToString(digest),
	}, nil
}

func cosmosSignDocDigest(body, authInfo []byte, chainID string, accountNumber uint64) []byte {
	doc := appendBytesField(nil, 1, body)
	doc = appendBytesField(doc, 2, authInfo)
	doc = appendStringField(doc, 3, chainID)
	doc = appendVarintField(doc, 4, accountNumber)
	sum := sha256.Sum256(doc)
	return sum[:]
}

// CosmosTx is an assembled, signed transaction.
type CosmosTx struct {
	Raw  []byte // the TxRaw bytes to broadcast
	Hash string // upper-case hex SHA-256 of Raw: the id every Cosmos explorer shows
}

// AssembleCosmosTx turns a plan and a threshold signature into a TxRaw.
//
// The signature is checked against the plan's digest and the public key
// carried in the transaction before anything is returned, so a signature from
// the wrong key or over different bytes is refused here and not relayed. It
// is also normalised to low-S: the SDK rejects the high-S twin of every
// signature as malleable, and a threshold ceremony returns either with equal
// probability.
func AssembleCosmosTx(plan *CosmosSigningPlan, r, s []byte, pubKey []byte) (*CosmosTx, error) {
	if plan == nil {
		return nil, fmt.Errorf("no signing plan supplied")
	}
	body, err := hex.DecodeString(plan.BodyBytesHex)
	if err != nil {
		return nil, fmt.Errorf("body_bytes_hex: %w", err)
	}
	authInfo, err := hex.DecodeString(plan.AuthInfoBytesHex)
	if err != nil {
		return nil, fmt.Errorf("auth_info_bytes_hex: %w", err)
	}
	if len(r) != 32 || len(s) != 32 {
		return nil, fmt.Errorf("r and s must each be 32 bytes, got %d and %d", len(r), len(s))
	}
	pub, err := btcec.ParsePubKey(pubKey)
	if err != nil {
		return nil, fmt.Errorf("public key is not a secp256k1 key: %w", err)
	}

	// The digest is recomputed, never taken from the plan: the plan crossed
	// the gateway, and a digest that does not match its own bytes would sign
	// something other than what was shown.
	digest := cosmosSignDocDigest(body, authInfo, plan.ChainID, plan.AccountNumber)

	var rs, ss btcec.ModNScalar
	if rs.SetByteSlice(r) || ss.SetByteSlice(s) || rs.IsZero() || ss.IsZero() {
		return nil, fmt.Errorf("r or s is out of range")
	}
	if ss.IsOverHalfOrder() {
		ss.Negate()
	}
	if !btcecdsa.NewSignature(&rs, &ss).Verify(digest, pub) {
		return nil, fmt.Errorf("the signature does not verify against the transaction's signer key")
	}

	rb, sb := rs.Bytes(), ss.Bytes()
	raw := appendBytesField(nil, 1, body)
	raw = appendBytesField(raw, 2, authInfo)
	raw = appendBytesField(raw, 3, append(rb[:], sb[:]...))
	sum := sha256.Sum256(raw)
	return &CosmosTx{Raw: raw, Hash: strings.ToUpper(hex.EncodeToString(sum[:]))}, nil
}
