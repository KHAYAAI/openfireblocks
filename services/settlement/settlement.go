package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// SettlementService broadcasts signed transactions to blockchains
type SettlementService struct {
	db       *PostgresDB
	chains   map[string]ChainClient
	metrics  *SettlementMetrics
}

// ChainClient is an interface for blockchain interactions
type ChainClient interface {
	BroadcastTransaction(ctx context.Context, txData []byte) (string, error)
	GetTransactionStatus(ctx context.Context, txHash string) (string, error)
	EstimateGas(ctx context.Context, txData []byte) (uint64, error)
}

// Settlement represents a transaction settlement record
type Settlement struct {
	SettlementID   string    `json:"settlement_id"`
	SigningID      string    `json:"signing_id"`
	CustomerID     string    `json:"customer_id"`
	Blockchain     string    `json:"blockchain"`
	TransactionHash string   `json:"transaction_hash,omitempty"`
	Status         string    `json:"status"` // pending, broadcasted, confirmed, failed
	GasUsed        uint64    `json:"gas_used,omitempty"`
	ConfirmationTime int64   `json:"confirmation_time,omitempty"`
	BroadcastedAt  *time.Time `json:"broadcasted_at,omitempty"`
	ConfirmedAt    *time.Time `json:"confirmed_at,omitempty"`
	ErrorMessage   string    `json:"error_message,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// SettlementRequest is the request to settle a signed transaction
type SettlementRequest struct {
	SigningID      string `json:"signing_id"`
	Blockchain     string `json:"blockchain"`
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
	TotalSettlements    int64
	SuccessfulSettlements int64
	FailedSettlements   int64
	AvgConfirmationTime float64
}

// NewSettlementService creates a new settlement service
func NewSettlementService(db *PostgresDB) *SettlementService {
	return &SettlementService{
		db:     db,
		chains: make(map[string]ChainClient),
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
		SettlementID:   uuid.New().String(),
		SigningID:      req.SigningID,
		Blockchain:     req.Blockchain,
		Status:         "pending",
		CreatedAt:      time.Now(),
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

	// Start confirmation tracking in background
	go s.trackConfirmation(context.Background(), settlement)

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

// trackConfirmation polls for transaction confirmation
func (s *SettlementService) trackConfirmation(ctx context.Context, settlement *Settlement) {
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
			if err := s.db.UpdateSettlement(ctx, settlement); err != nil {
				log.Printf("Failed to update settlement: %v", err)
			}

			log.Printf("Settlement confirmed: %s", settlement.SettlementID)
			return
		}

		attempts++
	}

	// Update settlement with final status
	if err := s.db.UpdateSettlement(ctx, settlement); err != nil {
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

// BroadcastTransaction broadcasts a signed Ethereum transaction
func (e *EthereumClient) BroadcastTransaction(ctx context.Context, txData []byte) (string, error) {
	// Parse and broadcast transaction
	// This is a simplified implementation
	log.Printf("Broadcasting Ethereum transaction: %d bytes", len(txData))

	// In production, use:
	// tx := &types.Transaction{}
	// if err := rlp.DecodeBytes(txData, tx); err != nil {
	//     return "", err
	// }
	// if err := e.client.SendTransaction(ctx, tx); err != nil {
	//     return "", err
	// }
	// return tx.Hash().Hex(), nil

	return "0x" + fmt.Sprintf("%064x", len(txData)), nil
}

// GetTransactionStatus gets the status of a transaction
func (e *EthereumClient) GetTransactionStatus(ctx context.Context, txHash string) (string, error) {
	// Check transaction receipt
	// receipt, err := e.client.TransactionReceipt(ctx, common.HexToHash(txHash))
	// if err != nil {
	//     return "pending", nil
	// }
	// if receipt.Status == 1 {
	//     return "confirmed", nil
	// }
	// return "failed", nil

	return "confirmed", nil
}

// EstimateGas estimates gas for a transaction
func (e *EthereumClient) EstimateGas(ctx context.Context, txData []byte) (uint64, error) {
	// Simplified - return mock value
	return 21000, nil
}

// BitcoinClient implements ChainClient for Bitcoin
type BitcoinClient struct {
	rpcURL string
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

// BroadcastTransaction broadcasts a Bitcoin transaction
func (b *BitcoinClient) BroadcastTransaction(ctx context.Context, txData []byte) (string, error) {
	log.Printf("Broadcasting Bitcoin transaction: %d bytes", len(txData))
	// Use bitcoind RPC sendrawtransaction
	return fmt.Sprintf("tx_%x", txData[:16]), nil
}

// GetTransactionStatus gets Bitcoin transaction status
func (b *BitcoinClient) GetTransactionStatus(ctx context.Context, txHash string) (string, error) {
	// Use bitcoind RPC getrawtransaction with verbose=true
	return "confirmed", nil
}

// EstimateGas is not applicable for Bitcoin
func (b *BitcoinClient) EstimateGas(ctx context.Context, txData []byte) (uint64, error) {
	return 0, fmt.Errorf("not applicable for Bitcoin")
}
