package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/btcsuite/btcutil/base58"

	"forge-crypto/mpc-signer/chains"
)

// The Solana spend path, shaped like the Bitcoin one for the same reason:
// the gateway asks what to sign (/solana/prepare), runs a threshold ceremony
// against the party pods, and comes back with the signature to turn into a
// transaction (/solana/finalize). This service holds no share.
//
// Native SOL only. SPL tokens need associated-token-account derivation and
// creation, a different instruction set and a different fee shape; they are
// not offered rather than offered half-working.

type solanaPrepareRequest struct {
	// PubKeyHex is the threshold key's 32-byte Ed25519 public key. Its base58
	// form IS the address, so there is nothing to derive and nothing to
	// disagree about.
	PubKeyHex   string `json:"pubkey_hex"`
	Destination string `json:"destination"`
	// Lamports as a base-10 string; JSON numbers lose integer precision.
	Amount string `json:"amount"`
}

type solanaPrepareResponse struct {
	MessageHex           string `json:"message_hex"`
	From                 string `json:"from"`
	To                   string `json:"to"`
	Amount               string `json:"amount"`
	Fee                  string `json:"fee"`
	Balance              string `json:"balance"`
	Blockhash            string `json:"blockhash"`
	LastValidBlockHeight uint64 `json:"last_valid_block_height"`
}

type solanaFinalizeRequest struct {
	MessageHex   string `json:"message_hex"`
	SignatureHex string `json:"signature_hex"`
	Broadcast    bool   `json:"broadcast"`
}

type solanaFinalizeResponse struct {
	Signature   string `json:"signature"`
	RawTxBase64 string `json:"raw_tx_base64"`
	Broadcast   bool   `json:"broadcast"`
}

func (s *server) solanaNode() *chains.SolanaRPC {
	signer, ok := s.signerRouter.Signer("solana").(*chains.SolanaSigner)
	if !ok {
		return nil
	}
	return signer.Node()
}

func solanaAddressFromHex(pubKeyHex string) (string, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(pubKeyHex, "0x"))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return "", fmt.Errorf("pubkey_hex must be a %d-byte Ed25519 public key in hex", ed25519.PublicKeySize)
	}
	return chains.SolanaAddressFromPubKey(raw), nil
}

func (s *server) handleSolanaAddresses(w http.ResponseWriter, r *http.Request) {
	addr, err := solanaAddressFromHex(r.URL.Query().Get("pubkey"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"address": addr, "preferred": addr})
}

func (s *server) handleSolanaBalance(w http.ResponseWriter, r *http.Request) {
	node := s.solanaNode()
	if node == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "no Solana node is configured (set SOLANA_RPC_URL)"})
		return
	}
	addr := r.URL.Query().Get("address")
	if len(base58.Decode(addr)) != 32 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "address must be a base58 Solana address"})
		return
	}
	bal, err := node.Balance(r.Context(), addr)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"address": addr, "lamports": strconv.FormatUint(bal, 10), "asset": "SOL", "decimals": 9})
}

func (s *server) handleSolanaPrepare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req solanaPrepareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}
	node := s.solanaNode()
	if node == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "no Solana node is configured (set SOLANA_RPC_URL)"})
		return
	}
	from, err := solanaAddressFromHex(req.PubKeyHex)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	if len(base58.Decode(req.Destination)) != 32 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "destination must be a base58 Solana address"})
		return
	}
	amount, err := strconv.ParseUint(req.Amount, 10, 64)
	if err != nil || amount == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "amount must be a positive integer number of lamports, as a string"})
		return
	}

	balance, err := node.Balance(ctx, from)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": fmt.Sprintf("could not read the balance: %v", err)})
		return
	}
	destBalance, err := node.Balance(ctx, req.Destination)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": fmt.Sprintf("could not read the destination: %v", err)})
		return
	}
	blockhash, lastValid, err := node.LatestBlockhash(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": fmt.Sprintf("could not get a recent blockhash: %v", err)})
		return
	}
	message, err := chains.BuildSolanaTransferMessage(chains.SolanaTransfer{From: from, To: req.Destination, Lamports: amount, RecentBlockhash: blockhash})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	fee, err := node.FeeForMessage(ctx, message)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": fmt.Sprintf("could not price the transaction: %v", err)})
		return
	}

	// Refused here rather than discovered at the node, and before a
	// ceremony: each of these fails after the parties have already signed.
	need := amount + fee
	if need < amount || balance < need {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error":   fmt.Sprintf("insufficient balance: need %d lamports (amount %d + fee %d), have %d", need, amount, fee, balance),
			"balance": balance,
		})
		return
	}
	if rest := balance - need; rest > 0 && rest < chains.SolanaRentExemptMinimumLamports {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": fmt.Sprintf("the transfer would leave %d lamports, below the %d rent-exempt minimum; send the whole balance less the fee, or leave more",
				rest, chains.SolanaRentExemptMinimumLamports),
			"balance": balance,
		})
		return
	}
	if destBalance == 0 && amount < chains.SolanaRentExemptMinimumLamports {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": fmt.Sprintf("the destination does not exist yet and %d lamports is below the %d needed to create it",
				amount, chains.SolanaRentExemptMinimumLamports),
		})
		return
	}

	writeJSON(w, http.StatusOK, solanaPrepareResponse{
		MessageHex: hex.EncodeToString(message), From: from, To: req.Destination,
		Amount: strconv.FormatUint(amount, 10), Fee: strconv.FormatUint(fee, 10), Balance: strconv.FormatUint(balance, 10),
		Blockhash: blockhash, LastValidBlockHeight: lastValid,
	})
}

func (s *server) handleSolanaFinalize(w http.ResponseWriter, r *http.Request) {
	var req solanaFinalizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid request body"})
		return
	}
	message, err := hex.DecodeString(strings.TrimPrefix(req.MessageHex, "0x"))
	if err != nil || len(message) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "message_hex is not valid hex"})
		return
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(req.SignatureHex, "0x"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "signature_hex is not valid hex"})
		return
	}
	// Verifies the signature against the message's fee payer first.
	tx, err := chains.AssembleSolanaTransaction(message, sig)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": err.Error()})
		return
	}
	resp := solanaFinalizeResponse{Signature: chains.SolanaSignatureID(sig), RawTxBase64: base64.StdEncoding.EncodeToString(tx)}
	if !req.Broadcast {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	node := s.solanaNode()
	if node == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "no Solana node is configured (set SOLANA_RPC_URL)",
			"signature": resp.Signature, "raw_tx_base64": resp.RawTxBase64})
		return
	}
	id, err := node.SendTransaction(r.Context(), tx)
	if err != nil {
		// 502: it assembled and verified here and the node refused it. The
		// bytes come back so a relay can be retried without a new ceremony --
		// until the blockhash expires, which prepare reported.
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": err.Error(), "signature": resp.Signature, "raw_tx_base64": resp.RawTxBase64})
		return
	}
	resp.Broadcast = true
	resp.Signature = id
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleSolanaStatus(w http.ResponseWriter, r *http.Request) {
	node := s.solanaNode()
	if node == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{"error": "no Solana node is configured (set SOLANA_RPC_URL)"})
		return
	}
	sig := r.URL.Query().Get("signature")
	if len(base58.Decode(sig)) != 64 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "signature must be a base58 Solana transaction signature"})
		return
	}
	st, err := node.SignatureStatus(r.Context(), sig)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{"error": err.Error()})
		return
	}
	out := map[string]interface{}{"found": st.Found, "slot": st.Slot, "confirmation_status": st.ConfirmationStatus}
	if st.Err != nil {
		out["err"] = st.Err
	}
	// What it moved, for reconciliation: only worth a second call once the
	// transaction has landed and succeeded.
	if st.Found && st.Err == nil {
		if tr, err := node.Transfer(r.Context(), sig); err == nil && tr != nil {
			out["transfer"] = tr
		}
	}
	writeJSON(w, http.StatusOK, out)
}
