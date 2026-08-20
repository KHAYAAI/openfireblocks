package workflows

// Ceremony workflow types for DKG orchestration.

// DKGCeremonyRequest is the workflow input for a distributed key generation ceremony.
type DKGCeremonyRequest struct {
	CustomerID     string   `json:"customerId"`
	CeremonyID     string   `json:"ceremonyId"`
	ChainID        string   `json:"chainId"` // "ethereum", "bitcoin", "solana", "cosmos-hub"
	N              int      `json:"n"`       // total parties
	K              int      `json:"k"`       // threshold (k+1 signatures needed)
	PartyIDs       []int    `json:"partyIds"`
	PartyEndpoints []string `json:"partyEndpoints"`
}

// DKGCeremonyResult is the workflow output.
type DKGCeremonyResult struct {
	CeremonyID       string `json:"ceremonyId"`
	Status           string `json:"status"` // completed | failed
	ThresholdAddress string `json:"thresholdAddress"`
	ThresholdPubKey  string `json:"thresholdPubKey"`
	Error            string `json:"error,omitempty"`
}

// DKGRoundRequest is passed to ExecuteDKGRound activity.
type DKGRoundRequest struct {
	CeremonyID string `json:"ceremonyId"`
	RoundNum   int    `json:"roundNum"`
	PartyIDs   []int  `json:"partyIds"`
}

// DKGRoundResult is returned by ExecuteDKGRound activity.
type DKGRoundResult struct {
	RoundNum         int            `json:"roundNum"`
	Status           string         `json:"status"` // completed | failed
	PartyCommitments map[int]string `json:"partyCommitments"`
	Error            string         `json:"error,omitempty"`
}

// RegisterPartiesRequest is passed to RegisterParties activity.
type RegisterPartiesRequest struct {
	CeremonyID     string
	PartyIDs       []int
	PartyEndpoints []string
}

// RegisterPartiesResult is returned by RegisterParties activity.
type RegisterPartiesResult struct {
	RegisteredCount int
	Error           string
}

// SealKeySharesRequest is passed to SealKeyShares activity.
type SealKeySharesRequest struct {
	CeremonyID string
	PartyIDs   []int
	CustomerID string
}

// SealKeySharesResult is returned by SealKeyShares activity.
type SealKeySharesResult struct {
	SealedCount int
	Error       string
}

// ThresholdSigningRequest is the workflow input for threshold signing.
type ThresholdSigningRequest struct {
	CeremonyID string `json:"ceremonyId"`
	Message    string `json:"message"`  // hex-encoded message
	PartyIDs   []int  `json:"partyIds"` // which k+1 parties to use
	ChainID    string `json:"chainId"`
}

// ThresholdSigningResult is the workflow output.
type ThresholdSigningResult struct {
	Signature string `json:"signature"`
	SignedTx  string `json:"signedTx,omitempty"`
	Status    string `json:"status"` // completed | failed
	Error     string `json:"error,omitempty"`
}

// RequestSignaturesRequest is passed to RequestSignatures activity.
type RequestSignaturesRequest struct {
	CeremonyID string
	Message    string
	PartyIDs   []int
}

// RequestSignaturesResult is returned by RequestSignatures activity.
type RequestSignaturesResult struct {
	Signature string
	Error     string
}
