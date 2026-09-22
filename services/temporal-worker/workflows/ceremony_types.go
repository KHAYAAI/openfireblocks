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

// ThresholdSigningRequest is the workflow input for threshold signing.
type ThresholdSigningRequest struct {
	CeremonyID     string   `json:"ceremonyId"`     // the keygen ceremony that produced the key to sign with
	Message        string   `json:"message"`        // hex-encoded 32-byte message hash
	PartyIDs       []int    `json:"partyIds"`       // which k+1 parties make up the signing committee
	PartyEndpoints []string `json:"partyEndpoints"` // parallel to PartyIDs -- where to reach each committee member
	ChainID        string   `json:"chainId"`
}

// ThresholdSigningResult is the workflow output.
type ThresholdSigningResult struct {
	Signature string `json:"signature"`
	SignedTx  string `json:"signedTx,omitempty"`
	Status    string `json:"status"` // completed | failed
	Error     string `json:"error,omitempty"`
}
