package main

import (
	"time"
)

// Ceremony represents a distributed key generation ceremony.
type Ceremony struct {
	ID                 string                 `json:"id" db:"id"`
	CustomerID         string                 `json:"customer_id" db:"customer_id"`
	ChainID            string                 `json:"chain_id" db:"chain_id"`
	N                  int                    `json:"n" db:"n"`
	K                  int                    `json:"k" db:"k"`
	Status             string                 `json:"status" db:"status"`
	ThresholdAddress   string                 `json:"threshold_address" db:"threshold_address"`
	ThresholdPubKey    string                 `json:"threshold_public_key" db:"threshold_public_key"`
	CreatedAt          time.Time              `json:"created_at" db:"created_at"`
	StartedAt          *time.Time             `json:"started_at" db:"started_at"`
	CompletedAt        *time.Time             `json:"completed_at" db:"completed_at"`
	FailedAt           *time.Time             `json:"failed_at" db:"failed_at"`
	ErrorMessage       string                 `json:"error_message" db:"error_message"`
	Metadata           map[string]interface{} `json:"metadata" db:"metadata"`
	Parties            []CeremonyParty        `json:"parties,omitempty"`
	CurrentRound       int                    `json:"current_round"`
}

// CeremonyParty represents a participant in a ceremony.
type CeremonyParty struct {
	ID              string                 `json:"id" db:"id"`
	CeremonyID      string                 `json:"ceremony_id" db:"ceremony_id"`
	PartyID         int                    `json:"party_id" db:"party_id"`
	PartyEndpoint   string                 `json:"party_endpoint" db:"party_endpoint"`
	Status          string                 `json:"status" db:"status"`
	PublicKey       string                 `json:"public_key" db:"public_key"`
	KeyShareSealedAt *time.Time            `json:"key_share_sealed_at" db:"key_share_sealed_at"`
	JoinedAt        *time.Time             `json:"joined_at" db:"joined_at"`
	FailedAt        *time.Time             `json:"failed_at" db:"failed_at"`
	ErrorMessage    string                 `json:"error_message" db:"error_message"`
	Metadata        map[string]interface{} `json:"metadata" db:"metadata"`
}

// CeremonyRound represents a round in the DKG protocol (phases 1-7).
type CeremonyRound struct {
	ID         string     `json:"id" db:"id"`
	CeremonyID string     `json:"ceremony_id" db:"ceremony_id"`
	RoundNum   int        `json:"round_number" db:"round_number"`
	Status     string     `json:"status" db:"status"`
	StartedAt  *time.Time `json:"started_at" db:"started_at"`
	CompletedAt *time.Time `json:"completed_at" db:"completed_at"`
	FailedAt   *time.Time `json:"failed_at" db:"failed_at"`
	ErrorMsg   string     `json:"error_message" db:"error_message"`
}

// CreateCeremonyRequest is the request to initiate a new ceremony.
type CreateCeremonyRequest struct {
	ChainID      string   `json:"chain_id" binding:"required"`       // "ethereum", "bitcoin", "solana", "cosmos-hub"
	N            int      `json:"n" binding:"required,min=2,max=10"` // 2-10 parties
	K            int      `json:"k" binding:"required,min=1"`        // threshold: k+1 signatures needed
	PartyIDs     []int    `json:"party_ids" binding:"required,min=1"`
	PartyEndpoints []string `json:"party_endpoints" binding:"required"`
}

// CreateCeremonyResponse is the response after initiating a ceremony.
type CreateCeremonyResponse struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// SignWithThresholdRequest is the request to sign with a threshold key.
type SignWithThresholdRequest struct {
	CeremonyID     string   `json:"ceremony_id" binding:"required"`
	Message        string   `json:"message" binding:"required"`        // hex-encoded message
	PartyIDs       []int    `json:"party_ids" binding:"required,min=1"` // which parties to use for signing
}

// SignWithThresholdResponse is the response after threshold signing.
type SignWithThresholdResponse struct {
	RequestID     string `json:"request_id"`
	Signature     string `json:"signature"`       // 65-byte signature (R || S || V)
	SignedTx      string `json:"signed_tx"`       // optional: full signed transaction (RLP, script, etc.)
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
}

// CeremonyStatus enum
const (
	CeremonyPending     = "pending"
	CeremonyInProgress  = "in_progress"
	CeremonyRound1      = "round1"
	CeremonyRound2      = "round2"
	CeremonyRound3      = "round3"
	CeremonyRound4      = "round4"
	CeremonyRound5      = "round5"
	CeremonyRound6      = "round6"
	CeremonyRound7      = "round7"
	CeremonyCompleted   = "completed"
	CeremonyFailed      = "failed"
)

// PartyStatus enum
const (
	PartyPending   = "pending"
	PartyJoined    = "joined"
	PartyCommitted = "committed"
	PartyCompleted = "completed"
	PartyFailed    = "failed"
)

// SupportedChains lists all supported blockchains.
var SupportedChains = []string{
	"ethereum",
	"bitcoin",
	"solana",
	"cosmos-hub",
}

// ChainConfig holds chain-specific configuration.
type ChainConfig struct {
	ChainID      string
	Name         string
	Network      string // mainnet, testnet
	RPCEndpoint  string
	Confirmation int // number of blocks to wait for confirmation
}

// GetChainConfig returns configuration for a given chain.
func GetChainConfig(chainID string, network string) *ChainConfig {
	configs := map[string]map[string]*ChainConfig{
		"ethereum": {
			"mainnet": {ChainID: "ethereum", Name: "Ethereum", Network: "mainnet", RPCEndpoint: "https://eth-mainnet.g.alchemy.com", Confirmation: 12},
			"testnet": {ChainID: "ethereum", Name: "Ethereum Sepolia", Network: "testnet", RPCEndpoint: "https://eth-sepolia.g.alchemy.com", Confirmation: 2},
		},
		"bitcoin": {
			"mainnet": {ChainID: "bitcoin", Name: "Bitcoin", Network: "mainnet", Confirmation: 6},
			"testnet": {ChainID: "bitcoin", Name: "Bitcoin Testnet", Network: "testnet", Confirmation: 1},
		},
		"solana": {
			"mainnet": {ChainID: "solana", Name: "Solana Mainnet", Network: "mainnet", RPCEndpoint: "https://api.mainnet-beta.solana.com", Confirmation: 32},
			"testnet": {ChainID: "solana", Name: "Solana Devnet", Network: "testnet", RPCEndpoint: "https://api.devnet.solana.com", Confirmation: 32},
		},
		"cosmos-hub": {
			"mainnet": {ChainID: "cosmos-hub", Name: "Cosmos Hub", Network: "mainnet", RPCEndpoint: "https://cosmos-rpc.allthatnode.com:26657", Confirmation: 1},
			"testnet": {ChainID: "cosmos-hub", Name: "Cosmos Hub Testnet", Network: "testnet", RPCEndpoint: "https://rpc.testnet.cosmos.network:26657", Confirmation: 1},
		},
	}
	if chainConfigs, ok := configs[chainID]; ok {
		if cfg, ok := chainConfigs[network]; ok {
			return cfg
		}
	}
	return nil
}

// IsValidChain checks if a chain is supported.
func IsValidChain(chainID string) bool {
	for _, chain := range SupportedChains {
		if chain == chainID {
			return true
		}
	}
	return false
}

// IsValidThreshold checks if threshold parameters are valid.
func IsValidThreshold(n, k int) bool {
	return k >= 1 && k < n && n >= 2 && n <= 10
}
