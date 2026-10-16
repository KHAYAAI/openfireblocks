package chains

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcutil/base58"
	"github.com/decred/dcrd/dcrec/edwards/v2"
)

// SPL tokens (the original Token program only).
//
// A wallet does not hold tokens itself. Each (wallet, mint) pair has an
// associated token account at an address derived from both, and a transfer
// moves tokens between two such accounts. So sending a token means:
//
//  1. derive the sender's and the recipient's associated token accounts;
//  2. create the recipient's if it does not exist (the sender pays its rent);
//  3. TransferChecked, which states the mint and its decimals so a transfer
//     against the wrong mint, or at the wrong scale, fails on chain.
//
// Token-2022 mints are refused: they can carry transfer fees, transfer hooks
// and confidential balances, each of which changes what "send N tokens" means.

const (
	TokenProgramID                  = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	Token2022ProgramID              = "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb"
	AssociatedTokenAccountProgramID = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"

	// Account sizes of the Token program's two account types.
	SPLMintSize         = 82
	SPLTokenAccountSize = 165
)

// isOnCurve reports whether 32 bytes are a valid Ed25519 point. A program
// derived address must NOT be one, so nobody can hold its private key.
func isOnCurve(b []byte) bool {
	_, err := edwards.ParsePubKey(b)
	return err == nil
}

// CreateProgramAddress is Solana's create_program_address:
// sha256(seed_1 || ... || seed_n || program_id || "ProgramDerivedAddress"),
// refused if the result is on the curve.
func CreateProgramAddress(seeds [][]byte, programID string) (string, error) {
	program := base58.Decode(programID)
	if len(program) != 32 {
		return "", fmt.Errorf("program id %q is not a 32-byte address", programID)
	}
	if len(seeds) > 16 {
		return "", fmt.Errorf("at most 16 seeds")
	}
	h := sha256.New()
	for _, s := range seeds {
		if len(s) > 32 {
			return "", fmt.Errorf("a seed is at most 32 bytes")
		}
		h.Write(s)
	}
	h.Write(program)
	h.Write([]byte("ProgramDerivedAddress"))
	sum := h.Sum(nil)
	if isOnCurve(sum) {
		return "", fmt.Errorf("derived address is on the curve")
	}
	return base58.Encode(sum), nil
}

// FindProgramAddress is find_program_address: the first bump seed, counting
// down from 255, whose address is off the curve.
func FindProgramAddress(seeds [][]byte, programID string) (string, uint8, error) {
	for bump := 255; bump >= 0; bump-- {
		withBump := append(append([][]byte{}, seeds...), []byte{byte(bump)})
		addr, err := CreateProgramAddress(withBump, programID)
		if err == nil {
			return addr, uint8(bump), nil
		}
	}
	return "", 0, fmt.Errorf("no viable bump seed")
}

// AssociatedTokenAddress derives the associated token account of a wallet
// for a mint: seeds (wallet, token program, mint) under the ATA program.
func AssociatedTokenAddress(wallet, mint string) (string, error) {
	w, m, tp := base58.Decode(wallet), base58.Decode(mint), base58.Decode(TokenProgramID)
	if len(w) != 32 {
		return "", fmt.Errorf("wallet %q is not a 32-byte address", wallet)
	}
	if len(m) != 32 {
		return "", fmt.Errorf("mint %q is not a 32-byte address", mint)
	}
	addr, _, err := FindProgramAddress([][]byte{w, tp, m}, AssociatedTokenAccountProgramID)
	return addr, err
}

// SolanaTokenTransfer describes an SPL token transfer from the signing key's
// associated token account to the recipient wallet's.
type SolanaTokenTransfer struct {
	From            string // the signing key's wallet; also the fee payer
	To              string // the recipient WALLET (not its token account)
	Mint            string
	Amount          uint64 // in the mint's base units
	Decimals        uint8
	RecentBlockhash string
}

// SolanaTokenTransferPlan is what the message moves, for the caller to show
// and to check before anyone signs.
type SolanaTokenTransferPlan struct {
	Message            []byte
	SourceTokenAccount string
	DestTokenAccount   string
}

// BuildSolanaTokenTransferMessage builds a two-instruction legacy message:
// the ATA program's CreateIdempotent for the recipient's token account (a
// no-op if it exists, so the message is right whether or not it does, and
// stays right if someone else creates it between prepare and send), then the
// Token program's TransferChecked.
func BuildSolanaTokenTransferMessage(t SolanaTokenTransfer) (*SolanaTokenTransferPlan, error) {
	if t.Amount == 0 {
		return nil, fmt.Errorf("amount must be greater than zero")
	}
	if t.From == t.To {
		return nil, fmt.Errorf("source and destination are the same wallet")
	}
	src, err := AssociatedTokenAddress(t.From, t.Mint)
	if err != nil {
		return nil, err
	}
	dst, err := AssociatedTokenAddress(t.To, t.Mint)
	if err != nil {
		return nil, err
	}

	// Token instruction 12 = TransferChecked: u8 tag, u64 LE amount, u8 decimals.
	data := make([]byte, 10)
	data[0] = 12
	binary.LittleEndian.PutUint64(data[1:9], t.Amount)
	data[9] = t.Decimals

	msg, err := NewSolanaSigner().(*SolanaSigner).BuildTransaction(context.Background(), &SolanaSignRequest{
		FeePayer:        t.From,
		RecentBlockhash: t.RecentBlockhash,
		Instructions: []SolanaInstruction{
			{
				// ATA instruction 1 = CreateIdempotent.
				ProgramID: AssociatedTokenAccountProgramID,
				Accounts: []SolanaAccountMeta{
					{Pubkey: t.From, IsSigner: true, IsWritable: true}, // funds the rent
					{Pubkey: dst, IsWritable: true},
					{Pubkey: t.To},
					{Pubkey: t.Mint},
					{Pubkey: SystemProgramID},
					{Pubkey: TokenProgramID},
				},
				Data: "01",
			},
			{
				ProgramID: TokenProgramID,
				Accounts: []SolanaAccountMeta{
					{Pubkey: src, IsWritable: true},
					{Pubkey: t.Mint},
					{Pubkey: dst, IsWritable: true},
					{Pubkey: t.From, IsSigner: true},
				},
				Data: hex.EncodeToString(data),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return &SolanaTokenTransferPlan{Message: msg, SourceTokenAccount: src, DestTokenAccount: dst}, nil
}

// SPLMint is the part of a mint account the platform reads.
type SPLMint struct {
	Supply      uint64
	Decimals    uint8
	Initialized bool
}

// ParseSPLMint reads a Token program mint account:
// mint_authority COption<Pubkey> (36) | supply u64 | decimals u8 | is_initialized u8 | freeze_authority (36).
func ParseSPLMint(data []byte) (*SPLMint, error) {
	if len(data) != SPLMintSize {
		return nil, fmt.Errorf("not a mint: account data is %d bytes, a mint is %d", len(data), SPLMintSize)
	}
	return &SPLMint{
		Supply:      binary.LittleEndian.Uint64(data[36:44]),
		Decimals:    data[44],
		Initialized: data[45] == 1,
	}, nil
}

// SPLTokenAccount is the part of a token account the platform reads.
type SPLTokenAccount struct {
	Mint   string
	Owner  string
	Amount uint64
	State  uint8 // 0 uninitialized, 1 initialized, 2 frozen
}

// ParseSPLTokenAccount reads a Token program token account:
// mint (32) | owner (32) | amount u64 | delegate (36) | state u8 | ...
func ParseSPLTokenAccount(data []byte) (*SPLTokenAccount, error) {
	if len(data) != SPLTokenAccountSize {
		return nil, fmt.Errorf("not a token account: account data is %d bytes, a token account is %d", len(data), SPLTokenAccountSize)
	}
	return &SPLTokenAccount{
		Mint:   base58.Encode(data[0:32]),
		Owner:  base58.Encode(data[32:64]),
		Amount: binary.LittleEndian.Uint64(data[64:72]),
		State:  data[108],
	}, nil
}
