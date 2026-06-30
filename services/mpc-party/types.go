package main

import (
	"time"
)

// PartyConfig represents the configuration for a DKG party.
type PartyConfig struct {
	PartyID      int    `json:"partyId"`
	CeremonyID   string `json:"ceremonyId"`
	N            int    `json:"n"`           // Total parties
	K            int    `json:"k"`           // Threshold (k+1 signatures needed)
	ChainID      string `json:"chainId"`    // Blockchain (ethereum, bitcoin, solana, cosmos-hub)
	VaultURL     string `json:"vaultUrl"`
	VaultToken   string `json:"vaultToken"`
	PartyEndpointsMap map[int]string `json:"partyEndpointsMap"` // Mapping of partyId -> endpoint URL
}

// RoundStartSignal is sent by the orchestrator to start a round.
type RoundStartSignal struct {
	CeremonyID string `json:"ceremonyId"`
	RoundNum   int    `json:"roundNum"`
	Action     string `json:"action"`
}

// RoundData represents cryptographic data produced by a party for a round.
type RoundData struct {
	PartyID     int    `json:"partyId"`
	RoundNum    int    `json:"roundNum"`
	Commitments string `json:"commitments"` // Base64-encoded commitments from Round 1
	DLProof     string `json:"dlProof"`     // Discrete log proof (base64)
	PublicKey   string `json:"publicKey"`   // Party's secp256k1 public key (hex)
	Signature   string `json:"signature"`   // Proof of possession (hex)
}

// BroadcastRoundDataRequest is sent to broadcast data from all parties for a round.
type BroadcastRoundDataRequest struct {
	CeremonyID string                `json:"ceremonyId"`
	RoundNum   int                   `json:"roundNum"`
	PartyDataMap map[int]*RoundData   `json:"partyDataMap"` // Map of partyId -> RoundData
}

// KeyShareData represents a party's key share after DKG completes.
type KeyShareData struct {
	CeremonyID   string `json:"ceremonyId"`
	PartyID      int    `json:"partyId"`
	KeyShare     string `json:"keyShare"`     // Encrypted key share (hex)
	Commitment   string `json:"commitment"`   // Party's commitment to the share (hex)
	PublicKey    string `json:"publicKey"`    // The shared public key after DKG (hex)
	ChainAddress string `json:"chainAddress"` // Derived address on the target blockchain
	CreatedAt    time.Time `json:"createdAt"`
}

// DKGPartyState tracks the state of a party in the DKG ceremony.
type DKGPartyState struct {
	CeremonyID       string
	PartyID          int
	Config           *PartyConfig
	Status           string // pending, round1, round2, ..., round7, completed, failed
	RoundNum         int    // Current round
	Commitments      string // Party's commitments (filled in Round 1)
	DLProof          string // Discrete log proof
	PublicKey        string // secp256k1 public key
	KeyShare         string // Key share (only after Round 7)
	ReceivedRoundDataMap map[int]*RoundData // Data received from other parties
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ThresholdSignatureRequest is a request to sign with the threshold key share.
type ThresholdSignatureRequest struct {
	CeremonyID string   `json:"ceremonyId"`
	Message    string   `json:"message"`     // hex-encoded message to sign
	PartyIDs   []int    `json:"partyIds"`   // Which parties to use for signing
}

// PartialSignature is a partial signature contributed by this party.
type PartialSignature struct {
	PartyID      int    `json:"partyId"`
	Signature    string `json:"signature"` // Partial signature (hex)
	CeremonyID   string `json:"ceremonyId"`
	RoundNum     int    `json:"roundNum"`
}

// ThresholdSignatureResult is the combined threshold signature.
type ThresholdSignatureResult struct {
	CeremonyID     string `json:"ceremonyId"`
	FinalSignature string `json:"finalSignature"` // Combined k-of-n signature (hex)
	PublicKey      string `json:"publicKey"`      // The shared public key
	Message        string `json:"message"`        // Original message signed
	ChainID        string `json:"chainId"`
}
