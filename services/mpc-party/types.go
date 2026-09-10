package main

import (
	"time"
)

// PartyConfig represents the configuration for a DKG party.
type PartyConfig struct {
	PartyID           int            `json:"partyId"`
	CeremonyID        string         `json:"ceremonyId"`
	N                 int            `json:"n"`       // Total parties
	K                 int            `json:"k"`       // Threshold (k+1 signatures needed)
	ChainID           string         `json:"chainId"` // Blockchain (ethereum, bitcoin, solana, cosmos-hub)
	VaultURL          string         `json:"vaultUrl"`
	VaultToken        string         `json:"vaultToken"`
	PartyEndpointsMap map[int]string `json:"partyEndpointsMap"` // Mapping of partyId -> endpoint URL
}

// KeyShareData represents a party's key share after DKG completes.
type KeyShareData struct {
	CeremonyID   string    `json:"ceremonyId"`
	PartyID      int       `json:"partyId"`
	KeyShare     string    `json:"keyShare"`     // Encrypted key share (hex)
	Commitment   string    `json:"commitment"`   // Party's commitment to the share (hex)
	PublicKey    string    `json:"publicKey"`    // The shared public key after DKG (hex)
	ChainAddress string    `json:"chainAddress"` // Derived address on the target blockchain
	CreatedAt    time.Time `json:"createdAt"`
}
