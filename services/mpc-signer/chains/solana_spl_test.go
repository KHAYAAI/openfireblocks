package chains

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/btcsuite/btcutil/base58"
)

// Vectors from the reference libraries (@solana/spl-token 0.4.8
// getAssociatedTokenAddressSync, @solana/web3.js 1.95.3
// findProgramAddressSync). The first is spl-token's own test vector; the
// wallets after it are Keypair.fromSeed([i; 32]) for i = 1..4, against USDC.
func TestAssociatedTokenAddressMatchesTheReferenceLibrary(t *testing.T) {
	const usdc = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	cases := []struct{ wallet, mint, ata string }{
		{"B8UwBUUnKwCyKuGMbFKWaG7exYdDk2ozZrPg72NyVbfj", "7o36UsWR1JQLpZ9PE2gn9L4SQ69CNNiWAXd4Jt7rqz9Z", "DShWnroshVbeUp28oopA3Pu7oFPDBtC1DBmPECXXAQ9n"},
		{"AKnL4NNf3DGWZJS6cPknBuEGnVsV4A4m5tgebLHaRSZ9", usdc, "3wvJdyFnGvaMWpbq93NU91SggiVRveULUXL6iX5VZDGP"},
		{"9hSR6S7WPtxmTojgo6GG3k4yDPecgJY292j7xrsUGWBu", usdc, "ASZ2TDDNJG2n42TxAezqNNzwWipykHrENDKMCoLKgzup"},
		{"GyGKxMyg1p9SsHfm15MkNUu1u9TN2JtTspcdmrtGUdse", usdc, "DNDTCnZkNk358qDFZd9unHtnrc73SsXcpVWtwJJMrR4B"},
		{"EdmxWPmx2WH6WgFfTdu9xfkYf3k1g5wD1zccTVySEEh1", usdc, "FHPASu6WrzXmbm5NbAQy9BxwX5naUHKn5z8ycurGoSX"},
	}
	for _, c := range cases {
		got, err := AssociatedTokenAddress(c.wallet, c.mint)
		if err != nil {
			t.Fatalf("%s: %v", c.wallet, err)
		}
		if got != c.ata {
			t.Errorf("ATA(%s, %s) = %s, want %s", c.wallet, c.mint, got, c.ata)
		}
	}
}

func TestFindProgramAddressMatchesTheReferenceLibrary(t *testing.T) {
	addr, bump, err := FindProgramAddress([][]byte{[]byte("Talking"), []byte("Squirrels")}, "BPFLoaderUpgradeab1e11111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if addr != "HTqKuCuTUMwRJV4ceegG2CwYRxub4qjpj9DEg3nz1NGF" || bump != 255 {
		t.Fatalf("got %s bump %d", addr, bump)
	}
	// A derived address is never a valid public key, so no one holds its key.
	if isOnCurve(base58.Decode(addr)) {
		t.Fatal("a program address must be off the curve")
	}
	// An ordinary wallet is on the curve.
	if !isOnCurve(base58.Decode("B8UwBUUnKwCyKuGMbFKWaG7exYdDk2ozZrPg72NyVbfj")) {
		t.Fatal("a wallet key should be on the curve")
	}
}

func TestTokenTransferMessageSaysExactlyWhatItMoves(t *testing.T) {
	from, to := "AKnL4NNf3DGWZJS6cPknBuEGnVsV4A4m5tgebLHaRSZ9", "9hSR6S7WPtxmTojgo6GG3k4yDPecgJY292j7xrsUGWBu"
	mint := "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	plan, err := BuildSolanaTokenTransferMessage(SolanaTokenTransfer{
		From: from, To: to, Mint: mint, Amount: 2_500_000, Decimals: 6,
		RecentBlockhash: base58.Encode(bytes.Repeat([]byte{7}, 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceTokenAccount != "3wvJdyFnGvaMWpbq93NU91SggiVRveULUXL6iX5VZDGP" || plan.DestTokenAccount != "ASZ2TDDNJG2n42TxAezqNNzwWipykHrENDKMCoLKgzup" {
		t.Fatalf("token accounts %s -> %s", plan.SourceTokenAccount, plan.DestTokenAccount)
	}
	m := plan.Message
	// One signer (the key, which also pays), no readonly signers, and five
	// readonly accounts: recipient wallet, mint, System, Token, ATA programs.
	if m[0] != 1 || m[1] != 0 || m[2] != 5 {
		t.Fatalf("header = %v, want [1 0 5]", m[:3])
	}
	if m[3] != 8 {
		t.Fatalf("account count = %d, want 8", m[3])
	}
	keys := make([]string, 8)
	for i := range keys {
		keys[i] = base58.Encode(m[4+32*i : 36+32*i])
	}
	want := []string{from, plan.DestTokenAccount, plan.SourceTokenAccount, to, mint, SystemProgramID, TokenProgramID, AssociatedTokenAccountProgramID}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("account %d = %s, want %s", i, keys[i], want[i])
		}
	}
	p := 4 + 32*8 + 32
	if m[p] != 2 {
		t.Fatalf("instruction count = %d, want 2", m[p])
	}
	p++
	// CreateIdempotent: program 7; accounts payer, dest ATA, wallet, mint, system, token; data [1].
	wantCreate := []byte{7, 6, 0, 1, 3, 4, 5, 6, 1, 1}
	if !bytes.Equal(m[p:p+len(wantCreate)], wantCreate) {
		t.Fatalf("create instruction = %v, want %v", m[p:p+len(wantCreate)], wantCreate)
	}
	p += len(wantCreate)
	// TransferChecked: program 6; accounts source, mint, dest, owner; data [12, amount, decimals].
	if !bytes.Equal(m[p:p+6], []byte{6, 4, 2, 4, 1, 0}) {
		t.Fatalf("transfer accounts = %v", m[p:p+6])
	}
	p += 6
	if m[p] != 10 || m[p+1] != 12 || binary.LittleEndian.Uint64(m[p+2:p+10]) != 2_500_000 || m[p+10] != 6 {
		t.Fatalf("transfer data = %v", m[p:p+11])
	}
	if len(m) != p+11 {
		t.Fatalf("%d trailing bytes", len(m)-p-11)
	}
	payer, err := SolanaMessageFeePayer(m)
	if err != nil || base58.Encode(payer) != from {
		t.Fatalf("fee payer = %v, %v", payer, err)
	}
}

func TestTokenTransferRefusesWhatCannotBeRight(t *testing.T) {
	bh := base58.Encode(bytes.Repeat([]byte{7}, 32))
	a, b := "AKnL4NNf3DGWZJS6cPknBuEGnVsV4A4m5tgebLHaRSZ9", "9hSR6S7WPtxmTojgo6GG3k4yDPecgJY292j7xrsUGWBu"
	mint := "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	for name, tr := range map[string]SolanaTokenTransfer{
		"zero amount": {From: a, To: b, Mint: mint, Amount: 0, Decimals: 6, RecentBlockhash: bh},
		"to self":     {From: a, To: a, Mint: mint, Amount: 1, Decimals: 6, RecentBlockhash: bh},
		"bad mint":    {From: a, To: b, Mint: "nope", Amount: 1, Decimals: 6, RecentBlockhash: bh},
		"bad wallet":  {From: a, To: "0OIl", Mint: mint, Amount: 1, Decimals: 6, RecentBlockhash: bh},
	} {
		if _, err := BuildSolanaTokenTransferMessage(tr); err == nil {
			t.Errorf("%s: built a message, want refusal", name)
		}
	}
}

func TestParsesMintAndTokenAccounts(t *testing.T) {
	mintData := make([]byte, SPLMintSize)
	binary.LittleEndian.PutUint64(mintData[36:44], 1_000_000_000)
	mintData[44], mintData[45] = 6, 1
	mint, err := ParseSPLMint(mintData)
	if err != nil || mint.Decimals != 6 || !mint.Initialized || mint.Supply != 1_000_000_000 {
		t.Fatalf("mint = %+v, %v", mint, err)
	}
	if _, err := ParseSPLMint(make([]byte, SPLTokenAccountSize)); err == nil {
		t.Fatal("a token account parsed as a mint")
	}

	acct := make([]byte, SPLTokenAccountSize)
	copy(acct[0:32], base58.Decode("EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"))
	copy(acct[32:64], base58.Decode("AKnL4NNf3DGWZJS6cPknBuEGnVsV4A4m5tgebLHaRSZ9"))
	binary.LittleEndian.PutUint64(acct[64:72], 42)
	acct[108] = 1
	ta, err := ParseSPLTokenAccount(acct)
	if err != nil || ta.Amount != 42 || ta.State != 1 || ta.Owner != "AKnL4NNf3DGWZJS6cPknBuEGnVsV4A4m5tgebLHaRSZ9" {
		t.Fatalf("token account = %+v, %v", ta, err)
	}
	if _, err := ParseSPLTokenAccount(mintData); err == nil {
		t.Fatal("a mint parsed as a token account")
	}
}
