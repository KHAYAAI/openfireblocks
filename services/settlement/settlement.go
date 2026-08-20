package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/google/uuid"
)

// SettlementService broadcasts signed transactions to blockchains
type SettlementService struct {
	db      *PostgresDB
	chains  map[string]ChainClient
	metrics *SettlementMetrics
}

// ChainClient is an interface for blockchain interactions
type ChainClient interface {
	BroadcastTransaction(ctx context.Context, txData []byte) (string, error)
	GetTransactionStatus(ctx context.Context, txHash string) (string, error)
	EstimateGas(ctx context.Context, txData []byte) (uint64, error)
}

// Settlement represents a transaction settlement record
type Settlement struct {
	SettlementID     string     `json:"settlement_id"`
	SigningID        string     `json:"signing_id"`
	CustomerID       string     `json:"customer_id"`
	Blockchain       string     `json:"blockchain"`
	TransactionHash  string     `json:"transaction_hash,omitempty"`
	Status           string     `json:"status"` // pending, broadcasted, confirmed, failed
	GasUsed          uint64     `json:"gas_used,omitempty"`
	ConfirmationTime int64      `json:"confirmation_time,omitempty"`
	BroadcastedAt    *time.Time `json:"broadcasted_at,omitempty"`
	ConfirmedAt      *time.Time `json:"confirmed_at,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// SettlementRequest is the request to settle a signed transaction
type SettlementRequest struct {
	SigningID         string `json:"signing_id"`
	Blockchain        string `json:"blockchain"`
	SignedTransaction string `json:"signed_transaction"`
}

// SettlementResponse is the result of settlement
type SettlementResponse struct {
	SettlementID    string `json:"settlement_id"`
	Status          string `json:"status"`
	TransactionHash string `json:"transaction_hash,omitempty"`
	Error           string `json:"error,omitempty"`
}

// SettlementMetrics tracks settlement performance
type SettlementMetrics struct {
	TotalSettlements      int64
	SuccessfulSettlements int64
	FailedSettlements     int64
	AvgConfirmationTime   float64
}

// NewSettlementService creates a new settlement service
func NewSettlementService(db *PostgresDB) *SettlementService {
	return &SettlementService{
		db:      db,
		chains:  make(map[string]ChainClient),
		metrics: &SettlementMetrics{},
	}
}

// RegisterChain registers a blockchain client
func (s *SettlementService) RegisterChain(blockchain string, client ChainClient) error {
	if client == nil {
		return fmt.Errorf("chain client cannot be nil")
	}
	s.chains[blockchain] = client
	log.Printf("Registered chain client: %s", blockchain)
	return nil
}

// Settle broadcasts a signed transaction to the blockchain
func (s *SettlementService) Settle(ctx context.Context, req *SettlementRequest) (*Settlement, error) {
	// Validate request
	if req.SigningID == "" || req.Blockchain == "" || req.SignedTransaction == "" {
		return nil, fmt.Errorf("invalid settlement request")
	}

	// Get blockchain client
	client, exists := s.chains[req.Blockchain]
	if !exists {
		return nil, fmt.Errorf("unsupported blockchain: %s", req.Blockchain)
	}

	// Create settlement record
	settlement := &Settlement{
		SettlementID: uuid.New().String(),
		SigningID:    req.SigningID,
		Blockchain:   req.Blockchain,
		Status:       "pending",
		CreatedAt:    time.Now(),
	}

	// Estimate gas (if applicable)
	if req.Blockchain == "ethereum" || req.Blockchain == "polygon" {
		gasEstimate, err := client.EstimateGas(ctx, []byte(req.SignedTransaction))
		if err != nil {
			log.Printf("Failed to estimate gas: %v", err)
		} else {
			settlement.GasUsed = gasEstimate
		}
	}

	// Broadcast transaction
	txHash, err := client.BroadcastTransaction(ctx, []byte(req.SignedTransaction))
	if err != nil {
		settlement.Status = "failed"
		settlement.ErrorMessage = err.Error()
		s.metrics.FailedSettlements++

		log.Printf("Failed to broadcast transaction: %v", err)

		// Store failed settlement
		if storeErr := s.db.CreateSettlement(ctx, settlement); storeErr != nil {
			log.Printf("Failed to store settlement: %v", storeErr)
		}

		return settlement, fmt.Errorf("broadcast failed: %w", err)
	}

	// Update settlement with transaction hash
	settlement.TransactionHash = txHash
	settlement.Status = "broadcasted"
	now := time.Now()
	settlement.BroadcastedAt = &now

	// Store settlement
	if err := s.db.CreateSettlement(ctx, settlement); err != nil {
		log.Printf("Failed to store settlement: %v", err)
	}

	s.metrics.TotalSettlements++
	s.metrics.SuccessfulSettlements++

	log.Printf("Settlement broadcast: %s (tx: %s)", settlement.SettlementID, txHash)

	// Start confirmation tracking in background. Passed by value (a copy),
	// not the same *Settlement being returned below: the HTTP handler
	// JSON-encodes that pointer immediately after this call returns, and
	// trackConfirmation used to mutate the exact same struct concurrently
	// with no synchronization -- a real data race (visible under
	// `go test -race`) between the response encoder reading the struct's
	// fields and this goroutine writing to them.
	go s.trackConfirmation(context.Background(), *settlement)

	return settlement, nil
}

// GetSettlement retrieves a settlement by ID
func (s *SettlementService) GetSettlement(ctx context.Context, settlementID string) (*Settlement, error) {
	return s.db.GetSettlement(ctx, settlementID)
}

// ListSettlements lists settlements for a customer
func (s *SettlementService) ListSettlements(ctx context.Context, customerID string) ([]*Settlement, error) {
	return s.db.ListSettlements(ctx, customerID)
}

// trackConfirmation polls for transaction confirmation. Takes settlement by
// value: this runs in its own goroutine, started right before the caller
// returns and JSON-encodes its own *Settlement, so mutating a shared
// pointer here would race with that encode.
func (s *SettlementService) trackConfirmation(ctx context.Context, settlement Settlement) {
	client, exists := s.chains[settlement.Blockchain]
	if !exists {
		log.Printf("Chain client not found for %s", settlement.Blockchain)
		return
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	maxAttempts := 120 // 10 minutes max
	attempts := 0

	for range ticker.C {
		if attempts >= maxAttempts {
			settlement.Status = "timeout"
			break
		}

		status, err := client.GetTransactionStatus(ctx, settlement.TransactionHash)
		if err != nil {
			log.Printf("Failed to get transaction status: %v", err)
			attempts++
			continue
		}

		if status == "confirmed" {
			settlement.Status = "confirmed"
			now := time.Now()
			settlement.ConfirmedAt = &now

			// Calculate confirmation time in seconds
			if settlement.BroadcastedAt != nil {
				confirmationTime := settlement.ConfirmedAt.Sub(*settlement.BroadcastedAt).Seconds()
				settlement.ConfirmationTime = int64(confirmationTime)

				// Update metrics
				currentAvg := s.metrics.AvgConfirmationTime
				s.metrics.AvgConfirmationTime = (currentAvg + confirmationTime) / 2
			}

			// Update settlement in database
			if err := s.db.UpdateSettlement(ctx, &settlement); err != nil {
				log.Printf("Failed to update settlement: %v", err)
			}

			log.Printf("Settlement confirmed: %s", settlement.SettlementID)
			return
		}

		attempts++
	}

	// Update settlement with final status
	if err := s.db.UpdateSettlement(ctx, &settlement); err != nil {
		log.Printf("Failed to update settlement: %v", err)
	}
}

// HandleSettle is the HTTP handler for settling transactions
func (s *SettlementService) HandleSettle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SettlementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	settlement, err := s.Settle(ctx, &req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Settlement failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(&SettlementResponse{
		SettlementID:    settlement.SettlementID,
		Status:          settlement.Status,
		TransactionHash: settlement.TransactionHash,
		Error:           settlement.ErrorMessage,
	})
}

// HandleGetSettlement is the HTTP handler to get settlement status
func (s *SettlementService) HandleGetSettlement(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settlementID := r.URL.Query().Get("settlement_id")

	if settlementID == "" {
		http.Error(w, "settlement_id required", http.StatusBadRequest)
		return
	}

	settlement, err := s.GetSettlement(ctx, settlementID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Not found: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settlement)
}

// EthereumClient implements ChainClient for Ethereum
type EthereumClient struct {
	rpcURL string
	client *ethclient.Client
}

// NewEthereumClient creates a new Ethereum client
func NewEthereumClient(rpcURL string) (*EthereumClient, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Ethereum: %w", err)
	}

	return &EthereumClient{
		rpcURL: rpcURL,
		client: client,
	}, nil
}

// BroadcastTransaction decodes and broadcasts a signed Ethereum transaction.
//
// This used to unconditionally return "0x" + hex(len(txData)) -- the byte
// length of the input dressed up as a transaction hash -- and never called
// the Ethereum node at all. Every settlement would have reported success
// with a hash that could never correspond to a real transaction, while
// nothing was ever broadcast. For the layer whose entire job is moving real
// funds, that is the single most dangerous stub in this codebase: it
// doesn't just fail to work, it actively lies that money moved.
func (e *EthereumClient) BroadcastTransaction(ctx context.Context, txData []byte) (string, error) {
	tx := &types.Transaction{}
	if err := tx.UnmarshalBinary(txData); err != nil {
		return "", fmt.Errorf("failed to decode signed transaction: %w", err)
	}

	if err := e.client.SendTransaction(ctx, tx); err != nil {
		return "", fmt.Errorf("failed to broadcast transaction: %w", err)
	}

	log.Printf("broadcast Ethereum transaction %s (%d bytes)", tx.Hash().Hex(), len(txData))
	return tx.Hash().Hex(), nil
}

// GetTransactionStatus checks a transaction's on-chain receipt. Used to
// unconditionally return "confirmed" for any hash, including ones that were
// never broadcast -- combined with the BroadcastTransaction stub above, the
// confirmation-tracking loop in trackConfirmation would mark every
// settlement "confirmed" within one 5-second tick regardless of whether
// anything real happened on-chain.
func (e *EthereumClient) GetTransactionStatus(ctx context.Context, txHash string) (string, error) {
	receipt, err := e.client.TransactionReceipt(ctx, common.HexToHash(txHash))
	if err != nil {
		if errors.Is(err, ethereum.NotFound) {
			return "pending", nil // mined but not yet indexed, or still in the mempool
		}
		return "", fmt.Errorf("failed to get transaction receipt: %w", err)
	}
	if receipt.Status == types.ReceiptStatusSuccessful {
		return "confirmed", nil
	}
	return "failed", nil
}

// EstimateGas returns the gas limit the transaction already committed to at
// signing time. txData here is a fully signed transaction (Settle calls
// this after signing, not before), so there is nothing left to estimate --
// re-querying the node would only ask it to guess at a number the
// transaction itself already fixes. The previous implementation returned a
// hardcoded 21000 (the base cost of a plain ETH transfer) for every
// transaction regardless of whether it was a transfer, a contract call, or
// anything else.
func (e *EthereumClient) EstimateGas(ctx context.Context, txData []byte) (uint64, error) {
	tx := &types.Transaction{}
	if err := tx.UnmarshalBinary(txData); err != nil {
		return 0, fmt.Errorf("failed to decode signed transaction: %w", err)
	}
	return tx.Gas(), nil
}

// BitcoinClient implements ChainClient for Bitcoin
type BitcoinClient struct {
	rpcURL  string
	rpcUser string
	rpcPass string
}

// NewBitcoinClient creates a new Bitcoin client
func NewBitcoinClient(rpcURL, rpcUser, rpcPass string) *BitcoinClient {
	return &BitcoinClient{
		rpcURL:  rpcURL,
		rpcUser: rpcUser,
		rpcPass: rpcPass,
	}
}

// rpcCall makes a JSON-RPC 2.0 call against bitcoind, per the documented
// Bitcoin Core RPC API (stable across versions, unlike a specific vendor's
// undocumented API -- see BroadcastTransaction/GetTransactionStatus below,
// which used to fabricate a hash/status instead of calling bitcoind at all).
func (b *BitcoinClient) rpcCall(ctx context.Context, method string, params []interface{}, result interface{}) error {
	reqBody, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "1.0",
		"id":      "openfireblocks",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal RPC request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.rpcURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("failed to build RPC request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(b.rpcUser, b.rpcPass)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("bitcoind RPC request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read RPC response: %w", err)
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return fmt.Errorf("failed to decode RPC response: %w", err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("bitcoind RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if result != nil {
		if err := json.Unmarshal(rpcResp.Result, result); err != nil {
			return fmt.Errorf("failed to decode RPC result: %w", err)
		}
	}
	return nil
}

// BroadcastTransaction submits a raw signed transaction via bitcoind's
// sendrawtransaction. Used to return fmt.Sprintf("tx_%x", txData[:16]) --
// the first 16 bytes of the input relabeled as a transaction id -- without
// ever contacting bitcoind. Same failure mode as the pre-fix
// EthereumClient: reports success for a transaction that was never
// actually submitted to the network.
func (b *BitcoinClient) BroadcastTransaction(ctx context.Context, txData []byte) (string, error) {
	var txid string
	if err := b.rpcCall(ctx, "sendrawtransaction", []interface{}{fmt.Sprintf("%x", txData)}, &txid); err != nil {
		return "", fmt.Errorf("failed to broadcast transaction: %w", err)
	}
	log.Printf("broadcast Bitcoin transaction %s (%d bytes)", txid, len(txData))
	return txid, nil
}

// GetTransactionStatus checks confirmation count via getrawtransaction
// (verbose). Used to unconditionally return "confirmed" for any hash.
func (b *BitcoinClient) GetTransactionStatus(ctx context.Context, txHash string) (string, error) {
	var tx struct {
		Confirmations int `json:"confirmations"`
	}
	if err := b.rpcCall(ctx, "getrawtransaction", []interface{}{txHash, true}, &tx); err != nil {
		return "", fmt.Errorf("failed to get transaction: %w", err)
	}
	if tx.Confirmations >= 6 {
		return "confirmed", nil
	}
	return "pending", nil
}

// EstimateGas is not applicable for Bitcoin (there is no gas concept; fees
// are sat/vByte and already fixed once a transaction is signed).
func (b *BitcoinClient) EstimateGas(ctx context.Context, txData []byte) (uint64, error) {
	return 0, fmt.Errorf("not applicable for Bitcoin")
}
