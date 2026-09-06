package chains

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcutil/base58"
)

// SolanaSigner implements ChainSigner for Solana (Ed25519).
//
// Two things differ from every other chain here and are worth stating up
// front:
//
//  1. Solana signs the raw serialized message, NOT a hash of it. The
//     ChainSigner interface's parameter is named messageHash for the other
//     chains' benefit; for Solana it is the message bytes themselves (what
//     BuildTransaction returns).
//  2. Ed25519 has no public-key recovery, so RecoverAddress cannot work by
//     construction -- the address must be carried alongside the signature.
//     It returns an error rather than a fabricated value.
//
// base58 comes from btcutil (already a dependency of this module for
// Bitcoin) rather than adding another module for the same alphabet.
type SolanaSigner struct{}

// NewSolanaSigner creates a new Solana signer.
func NewSolanaSigner() ChainSigner {
	return &SolanaSigner{}
}

// solanaPrivateKey accepts either a 32-byte seed or a full 64-byte Ed25519
// private key, hex-encoded (with or without 0x) or base58-encoded, which is
// how Solana keys are usually handed around.
func solanaPrivateKey(privKey string) (ed25519.PrivateKey, error) {
	raw, err := hex.DecodeString(strip0x(privKey))
	if err != nil {
		if decoded := base58.Decode(privKey); len(decoded) > 0 {
			raw = decoded
		} else {
			return nil, fmt.Errorf("private key must be hex or base58 encoded")
		}
	}

	switch len(raw) {
	case ed25519.SeedSize: // 32
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize: // 64
		return ed25519.PrivateKey(raw), nil
	default:
		return nil, fmt.Errorf("private key must be %d or %d bytes, got %d",
			ed25519.SeedSize, ed25519.PrivateKeySize, len(raw))
	}
}

// SignMessage signs the serialized Solana message with Ed25519.
func (s *SolanaSigner) SignMessage(ctx context.Context, message []byte, privKey string) (*Signature, error) {
	if len(message) == 0 {
		return nil, fmt.Errorf("message is empty")
	}
	key, err := solanaPrivateKey(privKey)
	if err != nil {
		return nil, err
	}

	sig := ed25519.Sign(key, message)

	// Ed25519 signatures are 64 bytes, R||S, with no recovery byte -- V is
	// left zero rather than invented.
	return &Signature{
		R:              hex.EncodeToString(sig[:32]),
		S:              hex.EncodeToString(sig[32:]),
		V:              0,
		SignatureBytes: hex.EncodeToString(sig),
	}, nil
}

// VerifySignature verifies an Ed25519 signature against a base58 (Solana
// address) or hex public key.
func (s *SolanaSigner) VerifySignature(ctx context.Context, message []byte, signature *Signature, pubKey string) (bool, error) {
	if signature == nil {
		return false, fmt.Errorf("no signature provided")
	}

	pubKeyBytes, err := hex.DecodeString(strip0x(pubKey))
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		pubKeyBytes = base58.Decode(pubKey)
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		return false, fmt.Errorf("public key must be %d bytes, got %d", ed25519.PublicKeySize, len(pubKeyBytes))
	}

	sigBytes, err := hex.DecodeString(strip0x(signature.SignatureBytes))
	if err != nil {
		return false, fmt.Errorf("invalid signature: %w", err)
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return false, fmt.Errorf("signature must be %d bytes, got %d", ed25519.SignatureSize, len(sigBytes))
	}

	return ed25519.Verify(ed25519.PublicKey(pubKeyBytes), message, sigBytes), nil
}

// RecoverAddress cannot be implemented for Ed25519 -- see the type's doc
// comment. Returning an error is the honest answer, not a limitation to be
// papered over.
func (s *SolanaSigner) RecoverAddress(ctx context.Context, message []byte, signature *Signature) (string, error) {
	return "", fmt.Errorf("address recovery is not possible for Solana: Ed25519 signatures do not support public-key recovery, so the signer's address must be supplied alongside the signature")
}

// BuildTransaction serializes a legacy Solana message ready for signing.
//
// Format (legacy, unversioned -- a v0 message would carry a 0x80-prefixed
// version byte, which this deliberately does not emit):
//
//	header:            numRequiredSignatures, numReadonlySigned, numReadonlyUnsigned
//	account_keys:      compact-u16 length, then 32 bytes each
//	recent_blockhash:  32 bytes
//	instructions:      compact-u16 length, then each:
//	                     program_id_index (u8)
//	                     compact-u16 account index count, then indices (u8 each)
//	                     compact-u16 data length, then data
//
// Account keys are ordered as the runtime requires -- writable signers,
// readonly signers, writable non-signers, readonly non-signers -- with the
// fee payer forced first. Program ids are included as readonly non-signers.
func (s *SolanaSigner) BuildTransaction(ctx context.Context, txData interface{}) ([]byte, error) {
	req, ok := txData.(*SolanaSignRequest)
	if !ok {
		return nil, fmt.Errorf("invalid transaction type for Solana")
	}
	if req.FeePayer == "" {
		return nil, fmt.Errorf("fee_payer is required")
	}
	if len(req.Instructions) == 0 {
		return nil, fmt.Errorf("transaction must have at least one instruction")
	}

	blockhash := base58.Decode(req.RecentBlockhash)
	if len(blockhash) != 32 {
		return nil, fmt.Errorf("recent_blockhash must decode to 32 bytes, got %d", len(blockhash))
	}

	// Collect every account, merging duplicates by taking the strongest
	// privilege seen (a key that is a signer anywhere is a signer; likewise
	// writable).
	type accountInfo struct {
		key        []byte
		isSigner   bool
		isWritable bool
	}
	order := []string{}
	accounts := map[string]*accountInfo{}

	upsert := func(pubkey string, isSigner, isWritable bool) error {
		decoded := base58.Decode(pubkey)
		if len(decoded) != 32 {
			return fmt.Errorf("account %q must decode to 32 bytes, got %d", pubkey, len(decoded))
		}
		if existing, ok := accounts[pubkey]; ok {
			existing.isSigner = existing.isSigner || isSigner
			existing.isWritable = existing.isWritable || isWritable
			return nil
		}
		accounts[pubkey] = &accountInfo{key: decoded, isSigner: isSigner, isWritable: isWritable}
		order = append(order, pubkey)
		return nil
	}

	// The fee payer is always the first account, and is always a writable
	// signer (it pays).
	if err := upsert(req.FeePayer, true, true); err != nil {
		return nil, fmt.Errorf("invalid fee_payer: %w", err)
	}
	for i, instr := range req.Instructions {
		if instr.ProgramID == "" {
			return nil, fmt.Errorf("instruction %d has no program_id", i)
		}
		for _, acc := range instr.Accounts {
			if err := upsert(acc.Pubkey, acc.IsSigner, acc.IsWritable); err != nil {
				return nil, fmt.Errorf("instruction %d: %w", i, err)
			}
		}
	}
	// Program ids are readonly non-signers, added after the instruction
	// accounts so they sort last.
	for i, instr := range req.Instructions {
		if err := upsert(instr.ProgramID, false, false); err != nil {
			return nil, fmt.Errorf("instruction %d program_id: %w", i, err)
		}
	}

	// Stable ordering by privilege class, preserving insertion order within
	// each class (which keeps the fee payer first).
	var writableSigners, readonlySigners, writableOthers, readonlyOthers []string
	for _, key := range order {
		info := accounts[key]
		switch {
		case info.isSigner && info.isWritable:
			writableSigners = append(writableSigners, key)
		case info.isSigner:
			readonlySigners = append(readonlySigners, key)
		case info.isWritable:
			writableOthers = append(writableOthers, key)
		default:
			readonlyOthers = append(readonlyOthers, key)
		}
	}
	ordered := append(append(append(append([]string{}, writableSigners...), readonlySigners...), writableOthers...), readonlyOthers...)

	indexOf := map[string]int{}
	for i, key := range ordered {
		indexOf[key] = i
	}

	var msg bytes.Buffer
	msg.WriteByte(byte(len(writableSigners) + len(readonlySigners))) // numRequiredSignatures
	msg.WriteByte(byte(len(readonlySigners)))                        // numReadonlySignedAccounts
	msg.WriteByte(byte(len(readonlyOthers)))                         // numReadonlyUnsignedAccounts

	writeCompactU16(&msg, len(ordered))
	for _, key := range ordered {
		msg.Write(accounts[key].key)
	}

	msg.Write(blockhash)

	writeCompactU16(&msg, len(req.Instructions))
	for i, instr := range req.Instructions {
		msg.WriteByte(byte(indexOf[instr.ProgramID]))

		writeCompactU16(&msg, len(instr.Accounts))
		for _, acc := range instr.Accounts {
			msg.WriteByte(byte(indexOf[acc.Pubkey]))
		}

		data, err := hex.DecodeString(strip0x(instr.Data))
		if err != nil {
			return nil, fmt.Errorf("instruction %d has invalid hex data: %w", i, err)
		}
		writeCompactU16(&msg, len(data))
		msg.Write(data)
	}

	return msg.Bytes(), nil
}

// BroadcastTransaction is not implemented: it needs a Solana RPC endpoint
// this service is not configured with.
func (s *SolanaSigner) BroadcastTransaction(ctx context.Context, signedTx []byte) (string, error) {
	return "", fmt.Errorf("broadcasting not implemented for Solana: no RPC endpoint is configured")
}

// writeCompactU16 writes Solana's shortvec length prefix: 7 bits per byte,
// high bit set while more bytes follow.
func writeCompactU16(buf *bytes.Buffer, value int) {
	v := uint16(value)
	for {
		elem := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			buf.WriteByte(elem)
			return
		}
		buf.WriteByte(elem | 0x80)
	}
}

// SolanaAddressFromPubKey returns the base58 address for an Ed25519 public
// key -- in Solana the address IS the public key.
func SolanaAddressFromPubKey(pubKey ed25519.PublicKey) string {
	return base58.Encode(pubKey)
}
