package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/btcsuite/btcutil/base58"

	"forge-crypto/mpc-signer/chains"
)

// SPL token transfers. Same shape as native SOL: /solana/token/prepare says
// what to sign, the gateway runs the ceremony, and /solana/finalize (shared
// with SOL -- the key is the only signer and the fee payer either way) turns
// the signature into a transaction. See chains/solana_spl.go for what the
// message contains.

type solanaTokenPrepareRequest struct {
	PubKeyHex   string `json:"pubkey_hex"`
	Destination string `json:"destination"` // the recipient's WALLET address
	Mint        string `json:"mint"`
	Amount      string `json:"amount"` // base units, as a string
	// Decimals is what the caller's token registry says the mint has. It is
	// checked against the chain before anything is built: a disagreement
	// means the amount the approvers saw is not the amount that would move.
	Decimals *int `json:"decimals"`
}

type solanaTokenPrepareResponse struct {
	MessageHex           string `json:"message_hex"`
	From                 string `json:"from"`
	To                   string `json:"to"`
	Mint                 string `json:"mint"`
	SourceTokenAccount   string `json:"source_token_account"`
	DestTokenAccount     string `json:"destination_token_account"`
	CreatesTokenAccount  bool   `json:"creates_token_account"`
	Amount               string `json:"amount"`
	Decimals             int    `json:"decimals"`
	Fee                  string `json:"fee"`
	Rent                 string `json:"rent"`
	SolBalance           string `json:"sol_balance"`
	TokenBalance         string `json:"token_balance"`
	Blockhash            string `json:"blockhash"`
	LastValidBlockHeight uint64 `json:"last_valid_block_height"`
}

func badRequest(w http.ResponseWriter, format string, args ...interface{}) {
	writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": fmt.Sprintf(format, args...)})
}

func badGateway(w http.ResponseWriter, format string, args ...interface{}) {
	writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": fmt.Sprintf(format, args...)})
}

func noSolanaNode(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "no Solana node is configured (set SOLANA_RPC_URL)"})
}

// GET /solana/token/mint?mint= -- what the token registry verifies before it
// lists a mint: that it is a Token program mint, and its decimals.
func (s *server) handleSolanaTokenMint(w http.ResponseWriter, r *http.Request) {
	node := s.solanaNode()
	if node == nil {
		noSolanaNode(w)
		return
	}
	mint := r.URL.Query().Get("mint")
	if len(base58.Decode(mint)) != 32 {
		badRequest(w, "mint must be a base58 Solana address")
		return
	}
	m, err := node.SPLMintInfo(r.Context(), mint)
	if err != nil {
		// Not a mint (or a Token-2022 one) is the caller's input, not an outage.
		writeJSON(w, http.StatusUnprocessableEntity, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"mint": mint, "program": chains.TokenProgramID, "decimals": m.Decimals, "supply": strconv.FormatUint(m.Supply, 10),
	})
}

// GET /solana/token/balance?owner=&mint=
func (s *server) handleSolanaTokenBalance(w http.ResponseWriter, r *http.Request) {
	node := s.solanaNode()
	if node == nil {
		noSolanaNode(w)
		return
	}
	owner, mint := r.URL.Query().Get("owner"), r.URL.Query().Get("mint")
	if len(base58.Decode(owner)) != 32 || len(base58.Decode(mint)) != 32 {
		badRequest(w, "owner and mint must be base58 Solana addresses")
		return
	}
	ata, exists, acct, err := node.SPLTokenBalance(r.Context(), owner, mint)
	if err != nil {
		badGateway(w, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"owner": owner, "mint": mint, "token_account": ata, "exists": exists,
		"amount": strconv.FormatUint(acct.Amount, 10), "frozen": acct.State == 2,
	})
}

// POST /solana/token/prepare
func (s *server) handleSolanaTokenPrepare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req solanaTokenPrepareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "invalid request body")
		return
	}
	node := s.solanaNode()
	if node == nil {
		noSolanaNode(w)
		return
	}
	from, err := solanaAddressFromHex(req.PubKeyHex)
	if err != nil {
		badRequest(w, "%v", err)
		return
	}
	if len(base58.Decode(req.Destination)) != 32 {
		badRequest(w, "destination must be a base58 Solana wallet address")
		return
	}
	if len(base58.Decode(req.Mint)) != 32 {
		badRequest(w, "mint must be a base58 Solana address")
		return
	}
	amount, err := strconv.ParseUint(req.Amount, 10, 64)
	if err != nil || amount == 0 {
		badRequest(w, "amount must be a positive integer number of base units, as a string")
		return
	}
	if req.Decimals == nil {
		badRequest(w, "decimals is required (from the token registry)")
		return
	}

	mint, err := node.SPLMintInfo(ctx, req.Mint)
	if err != nil {
		badRequest(w, "%v", err)
		return
	}
	if int(mint.Decimals) != *req.Decimals {
		badRequest(w, "the mint has %d decimals on chain but the registry says %d; fix the registry entry before sending", mint.Decimals, *req.Decimals)
		return
	}

	// The destination must be a wallet. Sending to someone's token account
	// (rather than their wallet) would derive a token account OF that token
	// account, which nobody can spend from.
	dest, err := node.AccountInfo(ctx, req.Destination)
	if err != nil {
		badGateway(w, "could not read the destination: %v", err)
		return
	}
	if dest.Exists && (dest.Owner == chains.TokenProgramID || dest.Owner == chains.Token2022ProgramID) {
		badRequest(w, "destination %s is a token account or mint, not a wallet; send to the recipient's wallet address", req.Destination)
		return
	}

	srcATA, srcExists, src, err := node.SPLTokenBalance(ctx, from, req.Mint)
	if err != nil {
		badGateway(w, "could not read the source token account: %v", err)
		return
	}
	if !srcExists || src.Amount < amount {
		have := uint64(0)
		if src != nil {
			have = src.Amount
		}
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":         fmt.Sprintf("insufficient token balance: need %d, have %d (token account %s)", amount, have, srcATA),
			"token_balance": strconv.FormatUint(have, 10),
		})
		return
	}
	if src.State == 2 {
		badRequest(w, "the source token account %s is frozen by the mint's freeze authority", srcATA)
		return
	}

	_, dstExists, dst, err := node.SPLTokenBalance(ctx, req.Destination, req.Mint)
	if err != nil {
		badGateway(w, "could not read the destination token account: %v", err)
		return
	}
	if dstExists && dst.State == 2 {
		badRequest(w, "the recipient's token account is frozen by the mint's freeze authority")
		return
	}

	blockhash, lastValid, err := node.LatestBlockhash(ctx)
	if err != nil {
		badGateway(w, "could not fetch a recent blockhash: %v", err)
		return
	}
	plan, err := chains.BuildSolanaTokenTransferMessage(chains.SolanaTokenTransfer{
		From: from, To: req.Destination, Mint: req.Mint, Amount: amount, Decimals: mint.Decimals, RecentBlockhash: blockhash,
	})
	if err != nil {
		badRequest(w, "%v", err)
		return
	}
	fee, err := node.FeeForMessage(ctx, plan.Message)
	if err != nil {
		badGateway(w, "could not price the transaction: %v", err)
		return
	}
	var rent uint64
	if !dstExists {
		if rent, err = node.MinimumBalanceForRentExemption(ctx, chains.SPLTokenAccountSize); err != nil {
			badGateway(w, "could not read the rent for a new token account: %v", err)
			return
		}
	}
	sol, err := node.Balance(ctx, from)
	if err != nil {
		badGateway(w, "could not read the SOL balance: %v", err)
		return
	}
	// The key pays the fee and, for a recipient with no token account yet,
	// that account's rent -- in SOL. Checked now, before a ceremony.
	if need := fee + rent; sol < need {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":       fmt.Sprintf("insufficient SOL for fees: need %d lamports (fee %d + new token account rent %d), have %d", need, fee, rent, sol),
			"sol_balance": strconv.FormatUint(sol, 10),
		})
		return
	}
	if rest := sol - fee - rent; rest > 0 && rest < chains.SolanaRentExemptMinimumLamports {
		badRequest(w, "paying the fees would leave %d lamports, below the %d rent-exempt minimum; add SOL to the key first", rest, chains.SolanaRentExemptMinimumLamports)
		return
	}

	writeJSON(w, http.StatusOK, solanaTokenPrepareResponse{
		MessageHex: hex.EncodeToString(plan.Message), From: from, To: req.Destination, Mint: req.Mint,
		SourceTokenAccount: plan.SourceTokenAccount, DestTokenAccount: plan.DestTokenAccount, CreatesTokenAccount: !dstExists,
		Amount: strconv.FormatUint(amount, 10), Decimals: int(mint.Decimals),
		Fee: strconv.FormatUint(fee, 10), Rent: strconv.FormatUint(rent, 10),
		SolBalance: strconv.FormatUint(sol, 10), TokenBalance: strconv.FormatUint(src.Amount, 10),
		Blockhash: blockhash, LastValidBlockHeight: lastValid,
	})
}
