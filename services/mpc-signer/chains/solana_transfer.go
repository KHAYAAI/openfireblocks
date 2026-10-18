package chains

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcutil/base58"
)

// SystemProgramID is the all-zero address, which base58-encodes to 32 ones.
const SystemProgramID = "11111111111111111111111111111111"

// SolanaRentExemptMinimumLamports is the balance an account with no data must
// hold to persist (the rent-exempt minimum for 0 bytes). A transfer that
// would leave an account holding less than this, but more than zero, is
// rejected by the runtime; so is one that funds a new account with less.
// It is a network parameter, so it is a constant here only because it has
// been 890,880 since rent exemption was introduced -- if a cluster changes
// it, the preflight simulation at the node is the backstop.
const SolanaRentExemptMinimumLamports uint64 = 890_880

// SolanaTransfer describes a native SOL transfer.
type SolanaTransfer struct {
	From            string // base58; also the fee payer
	To              string // base58
	Lamports        uint64
	RecentBlockhash string // base58
}

// BuildSolanaTransferMessage serialises the legacy message for a System
// Program transfer. The bytes returned are exactly what must be signed.
func BuildSolanaTransferMessage(t SolanaTransfer) ([]byte, error) {
	if t.Lamports == 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	if t.From == t.To {
		return nil, fmt.Errorf("source and destination are the same account")
	}
	// System Program instruction 2 = Transfer: u32 LE index, then u64 LE lamports.
	data := make([]byte, 12)
	binary.LittleEndian.PutUint32(data[0:4], 2)
	binary.LittleEndian.PutUint64(data[4:12], t.Lamports)

	return NewSolanaSigner().(*SolanaSigner).BuildTransaction(context.Background(), &SolanaSignRequest{
		FeePayer:        t.From,
		RecentBlockhash: t.RecentBlockhash,
		Instructions: []SolanaInstruction{{
			ProgramID: SystemProgramID,
			Accounts: []SolanaAccountMeta{
				{Pubkey: t.From, IsSigner: true, IsWritable: true},
				{Pubkey: t.To, IsSigner: false, IsWritable: true},
			},
			Data: hex.EncodeToString(data),
		}},
	})
}

// AssembleSolanaTransaction joins a message and its signature into the wire
// transaction: a compact-u16 signature count, the signatures, then the
// message. It first verifies the signature against the message's fee payer,
// so a signature from the wrong key, or over the wrong bytes, is refused here
// rather than at the node -- and rather than being relayed and paid for.
func AssembleSolanaTransaction(message, signature []byte) ([]byte, error) {
	if len(signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("signature must be %d bytes, got %d", ed25519.SignatureSize, len(signature))
	}
	payer, err := SolanaMessageFeePayer(message)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(payer, message, signature) {
		return nil, fmt.Errorf("the signature does not verify against the message's fee payer %s", base58.Encode(payer))
	}
	var tx bytes.Buffer
	writeCompactU16(&tx, 1)
	tx.Write(signature)
	tx.Write(message)
	return tx.Bytes(), nil
}

// SolanaMessageFeePayer reads the first account key out of a legacy message,
// which is the fee payer and, for a single-signature transfer, the signer.
func SolanaMessageFeePayer(message []byte) (ed25519.PublicKey, error) {
	if len(message) < 3 {
		return nil, fmt.Errorf("message is too short to be a Solana message")
	}
	if message[0] != 1 {
		return nil, fmt.Errorf("expected exactly one required signature, message declares %d", message[0])
	}
	n, read, err := readCompactU16(message[3:])
	if err != nil || n < 1 {
		return nil, fmt.Errorf("message has no account keys")
	}
	start := 3 + read
	if len(message) < start+32 {
		return nil, fmt.Errorf("message is truncated")
	}
	return ed25519.PublicKey(message[start : start+32]), nil
}

func readCompactU16(b []byte) (value, read int, err error) {
	for i := 0; i < 3 && i < len(b); i++ {
		value |= int(b[i]&0x7f) << (7 * i)
		if b[i]&0x80 == 0 {
			return value, i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("invalid compact-u16")
}

// SolanaSignatureID is the transaction id: the first signature, base58.
func SolanaSignatureID(signature []byte) string { return base58.Encode(signature) }
