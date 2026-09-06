package chains

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/btcsuite/btcutil/bech32"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/ripemd160" //nolint:staticcheck // Cosmos address derivation is defined in terms of RIPEMD160; there is no alternative.
)

// DefaultCosmosPrefix is the Bech32 human-readable part for the Cosmos Hub.
// Other Cosmos-SDK chains use their own (osmo, juno, ...), which is why the
// address helpers take a prefix rather than hardcoding this one.
const DefaultCosmosPrefix = "cosmos"

// CosmosSigner implements ChainSigner for Cosmos SDK chains (secp256k1).
//
// Address derivation follows the Cosmos SDK definition exactly:
//
//	bech32(prefix, RIPEMD160(SHA256(compressed_secp256k1_pubkey)))
//
// This is worth stating explicitly because the previous implementation of
// this file used Keccak256 (Ethereum's hash) and truncated to 20 bytes,
// which produces a well-formed but WRONG address -- funds sent to it would
// be unspendable. It also depended on the whole cosmos-sdk module for one
// type alias; bech32 from btcutil (already a dependency here) does the job
// without a multi-hundred-megabyte dependency tree that would additionally
// have forced a Go toolchain upgrade.
type CosmosSigner struct{}

// NewCosmosSigner creates a new Cosmos signer.
func NewCosmosSigner() ChainSigner {
	return &CosmosSigner{}
}

// SignMessage signs a 32-byte message hash with secp256k1.
//
// The hash a Cosmos chain expects is SHA256 of the canonical JSON SignDoc
// (see BuildTransaction), not Keccak256. go-ethereum's crypto.Sign is used
// purely as a secp256k1 implementation here -- the curve is identical; only
// the hashing and encoding around it differ between the two ecosystems.
func (c *CosmosSigner) SignMessage(ctx context.Context, messageHash []byte, privKeyHex string) (*Signature, error) {
	if len(messageHash) != 32 {
		return nil, fmt.Errorf("message hash must be 32 bytes, got %d", len(messageHash))
	}
	privKeyHex = strip0x(privKeyHex)

	privKey, err := crypto.HexToECDSA(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	signature, err := crypto.Sign(messageHash, privKey)
	if err != nil {
		return nil, fmt.Errorf("signing failed: %w", err)
	}

	// Cosmos wire format is the 64-byte R||S; the recovery byte is kept in
	// V for RecoverAddress's benefit but is not part of what a Cosmos chain
	// receives.
	return &Signature{
		R:              hex.EncodeToString(signature[:32]),
		S:              hex.EncodeToString(signature[32:64]),
		V:              signature[64],
		SignatureBytes: hex.EncodeToString(signature),
	}, nil
}

// VerifySignature verifies a signature against a hex-encoded public key.
func (c *CosmosSigner) VerifySignature(ctx context.Context, messageHash []byte, signature *Signature, pubKeyHex string) (bool, error) {
	if signature == nil {
		return false, fmt.Errorf("no signature provided")
	}
	pubKeyBytes, err := hex.DecodeString(strip0x(pubKeyHex))
	if err != nil {
		return false, fmt.Errorf("invalid public key: %w", err)
	}
	sigBytes, err := hex.DecodeString(strip0x(signature.SignatureBytes))
	if err != nil {
		return false, fmt.Errorf("invalid signature: %w", err)
	}
	if len(sigBytes) < 64 {
		return false, fmt.Errorf("signature must be at least 64 bytes, got %d", len(sigBytes))
	}
	return crypto.VerifySignature(pubKeyBytes, messageHash, sigBytes[:64]), nil
}

// RecoverAddress recovers the bech32 Cosmos address of the signer.
func (c *CosmosSigner) RecoverAddress(ctx context.Context, messageHash []byte, signature *Signature) (string, error) {
	if signature == nil {
		return "", fmt.Errorf("no signature provided")
	}
	sigBytes, err := hex.DecodeString(strip0x(signature.SignatureBytes))
	if err != nil {
		return "", fmt.Errorf("invalid signature: %w", err)
	}
	if len(sigBytes) != 65 {
		return "", fmt.Errorf("signature must be 65 bytes to recover, got %d", len(sigBytes))
	}

	pubKey, err := crypto.SigToPub(messageHash, sigBytes)
	if err != nil {
		return "", fmt.Errorf("recovery failed: %w", err)
	}

	return CosmosAddressFromPubKey(crypto.CompressPubkey(pubKey), DefaultCosmosPrefix)
}

// CosmosAddressFromPubKey derives a bech32 Cosmos address from a COMPRESSED
// (33-byte) secp256k1 public key: bech32(prefix, RIPEMD160(SHA256(pubkey))).
func CosmosAddressFromPubKey(compressedPubKey []byte, prefix string) (string, error) {
	if len(compressedPubKey) != 33 {
		return "", fmt.Errorf("expected a 33-byte compressed public key, got %d bytes", len(compressedPubKey))
	}
	if prefix == "" {
		prefix = DefaultCosmosPrefix
	}

	sha := sha256.Sum256(compressedPubKey)
	hasher := ripemd160.New()
	if _, err := hasher.Write(sha[:]); err != nil {
		return "", fmt.Errorf("ripemd160 failed: %w", err)
	}
	addressBytes := hasher.Sum(nil) // 20 bytes

	// bech32 payload is 5-bit groups, not raw bytes.
	converted, err := bech32.ConvertBits(addressBytes, 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("bech32 conversion failed: %w", err)
	}
	addr, err := bech32.Encode(prefix, converted)
	if err != nil {
		return "", fmt.Errorf("bech32 encoding failed: %w", err)
	}
	return addr, nil
}

// BuildTransaction produces the bytes a Cosmos chain expects to be signed:
// SHA256 over the canonical (sorted-key, no-whitespace) JSON SignDoc of the
// legacy amino StdSignDoc shape.
//
// Scope, stated plainly: this implements the legacy amino JSON sign mode
// (SIGN_MODE_LEGACY_AMINO_JSON), which Cosmos SDK chains still accept and
// which can be produced without the SDK's protobuf machinery. It does NOT
// implement SIGN_MODE_DIRECT (protobuf SignDoc), which would require the
// cosmos-sdk dependency this file deliberately avoids. A caller needing
// SIGN_MODE_DIRECT must build that SignDoc itself and pass the resulting
// hash to SignMessage.
func (c *CosmosSigner) BuildTransaction(ctx context.Context, txData interface{}) ([]byte, error) {
	req, ok := txData.(*CosmosSignRequest)
	if !ok {
		return nil, fmt.Errorf("invalid transaction type for Cosmos")
	}
	if req.ChainID == "" {
		return nil, fmt.Errorf("chain_id is required")
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("transaction must have at least one message")
	}

	// encoding/json sorts map keys, which is what amino JSON requires.
	signDoc := map[string]interface{}{
		"account_number": fmt.Sprintf("%d", req.AccountNum),
		"chain_id":       req.ChainID,
		"fee": map[string]interface{}{
			"amount": req.Fee.Amount,
			"gas":    req.Fee.Gas,
		},
		"memo":     "",
		"msgs":     req.Messages,
		"sequence": fmt.Sprintf("%d", req.Sequence),
	}
	if req.Timeout > 0 {
		signDoc["timeout_height"] = fmt.Sprintf("%d", req.Timeout)
	}

	encoded, err := json.Marshal(signDoc)
	if err != nil {
		return nil, fmt.Errorf("failed to encode SignDoc: %w", err)
	}

	hash := sha256.Sum256(encoded)
	return hash[:], nil
}

// BroadcastTransaction is not implemented: it needs a Cosmos RPC/LCD
// endpoint this service is not configured with. Failing loudly beats a stub
// that claims success.
func (c *CosmosSigner) BroadcastTransaction(ctx context.Context, signedTx []byte) (string, error) {
	return "", fmt.Errorf("broadcasting not implemented for Cosmos: no RPC endpoint is configured")
}

func strip0x(s string) string {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}
