package chains

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/mr-tron/base58"
	"golang.org/x/crypto/ed25519"
)

// SolanaSigner implements ChainSigner for Solana (Ed25519 signing).
// Note: Solana uses Ed25519 instead of secp256k1, so this is crypto-different from Bitcoin/Ethereum.
type SolanaSigner struct{}

// NewSolanaSigner creates a new Solana signer.
func NewSolanaSigner() ChainSigner {
	return &SolanaSigner{}
}

// SignMessage signs a message for Solana using Ed25519.
func (s *SolanaSigner) SignMessage(ctx context.Context, messageHash []byte, privKeyHex string) (*Signature, error) {
	if len(privKeyHex) >= 2 && privKeyHex[:2] == "0x" {
		privKeyHex = privKeyHex[2:]
	}

	// Parse private key (32 bytes for Ed25519)
	privKeyBytes, err := hex.DecodeString(privKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	if len(privKeyBytes) != 32 {
		return nil, fmt.Errorf("Ed25519 private key must be 32 bytes, got %d", len(privKeyBytes))
	}

	privKey := ed25519.PrivateKey(privKeyBytes)
	pubKey := privKey.Public().(ed25519.PublicKey)

	// Sign message using Ed25519
	signature := ed25519.Sign(privKey, messageHash)

	return &Signature{
		SignatureBytes: base64.StdEncoding.EncodeToString(signature),
		// Also include hex for compatibility
		R: hex.EncodeToString(signature[:32]),
		S: hex.EncodeToString(signature[32:]),
	}, nil
}

// VerifySignature verifies a Solana Ed25519 signature.
func (s *SolanaSigner) VerifySignature(ctx context.Context, messageHash []byte, signature *Signature, pubKeyBase58 string) (bool, error) {
	// Solana public keys are base58-encoded
	pubKeyBytes, err := base58.Decode(pubKeyBase58)
	if err != nil {
		return false, fmt.Errorf("invalid public key encoding: %w", err)
	}

	if len(pubKeyBytes) != 32 {
		return false, fmt.Errorf("Ed25519 public key must be 32 bytes, got %d", len(pubKeyBytes))
	}

	// Decode signature (base64 or hex)
	var sigBytes []byte
	if signature.SignatureBytes != "" {
		var decodeErr error
		sigBytes, decodeErr = base64.StdEncoding.DecodeString(signature.SignatureBytes)
		if decodeErr != nil {
			// Try hex
			sigBytes, decodeErr = hex.DecodeString(signature.SignatureBytes)
			if decodeErr != nil {
				return false, fmt.Errorf("invalid signature encoding: %w", decodeErr)
			}
		}
	}

	if len(sigBytes) != 64 {
		return false, fmt.Errorf("Ed25519 signature must be 64 bytes, got %d", len(sigBytes))
	}

	pubKey := ed25519.PublicKey(pubKeyBytes)
	valid := ed25519.Verify(pubKey, messageHash, sigBytes)
	return valid, nil
}

// RecoverAddress recovers the Solana address (base58-encoded public key).
func (s *SolanaSigner) RecoverAddress(ctx context.Context, messageHash []byte, signature *Signature) (string, error) {
	// In Solana, the address IS the base58-encoded public key
	// For recovery, we'd need the public key, not the signature
	return "", fmt.Errorf("address recovery not applicable for Solana (address is the public key)")
}

// BuildTransaction builds a Solana transaction message ready for signing.
// Message format: version || header || accountKeys || recentBlockhash || instructions
func (s *SolanaSigner) BuildTransaction(ctx context.Context, txData interface{}) ([]byte, error) {
	req, ok := txData.(*SolanaSignRequest)
	if !ok {
		return nil, fmt.Errorf("invalid transaction type for Solana")
	}

	// Build message in Solana format
	var message bytes.Buffer

	// Version byte (0 = legacy transaction)
	message.WriteByte(0)

	// Message header (3 bytes)
	// - required_signatures (1 byte): number of signing accounts
	// - readonly_signed_accounts (1 byte): number of readonly signed accounts
	// - readonly_unsigned_accounts (1 byte): number of readonly unsigned accounts
	message.WriteByte(1)  // 1 signer (fee payer)
	message.WriteByte(0)  // 0 readonly signed
	message.WriteByte(0)  // 0 readonly unsigned

	// Account keys (variable length)
	message.WriteByte(byte(len(req.Instructions))) // Simple: num instructions as num accounts (simplified)
	for _, instr := range req.Instructions {
		if accounts, ok := instr["accounts"].([]string); ok {
			for _, account := range accounts {
				if acc, err := base58.Decode(account); err == nil {
					message.Write(acc)
				}
			}
		}
	}

	// Recent blockhash (32 bytes)
	if bhBytes, err := base58.Decode(req.RecentBlockhash); err == nil {
		if len(bhBytes) != 32 {
			// Pad or truncate to 32 bytes
			bhBytes = make([]byte, 32)
		}
		message.Write(bhBytes)
	}

	// Instructions (simplified)
	message.WriteByte(byte(len(req.Instructions)))
	for _, instr := range req.Instructions {
		// Program ID
		if program, ok := instr["program"].(string); ok {
			if progBytes, err := base58.Decode(program); err == nil {
				message.Write(progBytes)
			}
		}
		// Instruction data
		if data, ok := instr["data"].(string); ok {
			if dataBytes, err := hex.DecodeString(data); err == nil {
				message.WriteByte(byte(len(dataBytes)))
				message.Write(dataBytes)
			}
		}
	}

	return message.Bytes(), nil
}

// BroadcastTransaction broadcasts a signed Solana transaction.
func (s *SolanaSigner) BroadcastTransaction(ctx context.Context, signedTx []byte) (string, error) {
	// TODO: Implement broadcast via Solana RPC
	// Example:
	//   client := rpc.New("https://api.mainnet-beta.solana.com")
	//   sig, err := client.SendTransaction(ctx, signedTx)
	//   return sig.String(), err
	return "", fmt.Errorf("broadcasting not yet implemented for Solana")
}

// RecoverPublicKey recovers the Ed25519 public key from a message and signature.
// Note: Ed25519 signatures do not support key recovery like ECDSA does.
// The public key must be provided separately.
func RecoverPublicKeyFromSignature(message, signature []byte) (ed25519.PublicKey, error) {
	return nil, fmt.Errorf("Ed25519 does not support key recovery from signatures")
}

// DeriveAddress derives a Solana address from a public key.
func DeriveAddress(pubKey ed25519.PublicKey) string {
	return base58.Encode(pubKey)
}
