package activities

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"go.temporal.io/sdk/activity"

	"forge-crypto/temporal-worker/workflows"
)

// Real, network-driven tss-lib orchestration -- replaces the round-based
// placeholder (dkg_round.go's ExecuteDKGRound, driven round-by-round
// against mpc-party's /round/* endpoints, and RegisterParties, which
// calls a /register endpoint mpc-party has never actually had) and the
// previously-unimplemented SealKeyShares/RequestSignatures stubs
// (ceremony_activities.go, which correctly returned "not implemented"
// errors rather than fabricating success) with activities that actually
// drive mpc-party's real /tss/keygen/* and /tss/sign/* endpoints
// (services/mpc-party/tss_party.go, tss_signing.go).
//
// Architecturally simpler than the old round-based model: tss-lib
// protocol messages are relayed directly PARTY-TO-PARTY over HTTP now
// (see relayTSSMessage in mpc-party), not routed through this
// orchestrator one round at a time. This orchestrator's job shrinks to:
// tell every party to start (with an identical peers map), then poll for
// completion. Key-share sealing likewise now happens inside mpc-party
// itself as part of DKG completion (vault_seal.go) -- this orchestrator
// never sees or touches key material, only confirms every party reports
// sealed=true.

type tssKeygenStartBody struct {
	CeremonyID string            `json:"ceremony_id"`
	Threshold  int               `json:"threshold"`
	Peers      map[string]string `json:"peers"`
}

type tssKeygenStatusBody struct {
	Status    string `json:"status"`
	Error     string `json:"error"`
	PublicKey string `json:"public_key"`
	Address   string `json:"address"`
	Sealed    bool   `json:"sealed"`
}

type tssSignStartBody struct {
	SignID            string `json:"sign_id"`
	KeygenCeremonyID  string `json:"keygen_ceremony_id"`
	MessageHashHex    string `json:"message_hash_hex"`
	CommitteePartyIDs []int  `json:"committee_party_ids"`
}

type tssSignStatusBody struct {
	Status    string `json:"status"`
	Error     string `json:"error"`
	Signature string `json:"signature"`
}

// ExecuteRealDKG drives a real DKG ceremony across req.PartyIDs at
// req.PartyEndpoints via mpc-party's /tss/keygen/* endpoints, replacing
// the 7-round placeholder loop entirely -- tss-lib's real protocol has no
// fixed round count exposed at this layer; the parties themselves handle
// however many rounds their internal state machine needs and relay
// messages to each other directly.
func (a *Activities) ExecuteRealDKG(ctx context.Context, req workflows.DKGCeremonyRequest) (*workflows.DKGCeremonyResult, error) {
	logger := activity.GetLogger(ctx)

	if len(req.PartyIDs) != len(req.PartyEndpoints) {
		return nil, fmt.Errorf("partyIds (%d) and partyEndpoints (%d) length mismatch", len(req.PartyIDs), len(req.PartyEndpoints))
	}
	if len(req.PartyIDs) == 0 {
		return nil, fmt.Errorf("no parties specified")
	}

	peers := make(map[string]string, len(req.PartyIDs))
	endpointByParty := make(map[int]string, len(req.PartyIDs))
	for i, id := range req.PartyIDs {
		peers[strconv.Itoa(id)] = req.PartyEndpoints[i]
		endpointByParty[id] = req.PartyEndpoints[i]
	}

	payload, err := json.Marshal(tssKeygenStartBody{
		CeremonyID: req.CeremonyID,
		Threshold:  req.K,
		Peers:      peers,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal keygen start request: %w", err)
	}

	// Every party must accept the start request before we move to
	// polling. tss-lib's protocol needs every party actively
	// participating -- if even one is unreachable, the ceremony can never
	// complete, so fail fast rather than polling something that will
	// hang forever waiting on a missing member.
	for _, id := range req.PartyIDs {
		if err := postJSONExpect2xx(ctx, a.httpClient, endpointByParty[id]+"/tss/keygen/start", payload); err != nil {
			logger.Error("failed to start keygen on party", "partyId", id, "error", err)
			return &workflows.DKGCeremonyResult{CeremonyID: req.CeremonyID, Status: "failed", Error: fmt.Sprintf("party %d: %v", id, err)}, nil
		}
	}
	logger.Info("real DKG started on all parties", "ceremonyId", req.CeremonyID, "parties", req.PartyIDs)

	const pollInterval = 500 * time.Millisecond
	for {
		allDone := true
		var address, pubKey string
		sealedCount := 0

		for _, id := range req.PartyIDs {
			status, err := getTSSStatus[tssKeygenStatusBody](ctx, a.httpClient, endpointByParty[id]+"/tss/keygen/status?ceremony_id="+req.CeremonyID)
			if err != nil {
				return &workflows.DKGCeremonyResult{CeremonyID: req.CeremonyID, Status: "failed", Error: fmt.Sprintf("party %d status check: %v", id, err)}, nil
			}
			switch status.Status {
			case "failed":
				return &workflows.DKGCeremonyResult{CeremonyID: req.CeremonyID, Status: "failed", Error: fmt.Sprintf("party %d: %s", id, status.Error)}, nil
			case "completed":
				address, pubKey = status.Address, status.PublicKey
				if status.Sealed {
					sealedCount++
				}
			default:
				allDone = false
			}
		}

		if allDone {
			logger.Info("real DKG completed", "ceremonyId", req.CeremonyID, "address", address, "sealedCount", sealedCount, "totalParties", len(req.PartyIDs))
			if sealedCount < len(req.PartyIDs) {
				logger.Warn("DKG completed but not every party reports its share sealed in Vault", "sealed", sealedCount, "total", len(req.PartyIDs))
			}
			return &workflows.DKGCeremonyResult{
				CeremonyID:       req.CeremonyID,
				Status:           "completed",
				ThresholdAddress: address,
				ThresholdPubKey:  pubKey,
			}, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// ExecuteRealSigning drives a real threshold signing ceremony across
// req.PartyIDs (the committee) via mpc-party's /tss/sign/* endpoints,
// replacing RequestSignatures' "not implemented" stub. signID is derived
// deterministically from the ceremony ID and message hash (not randomly
// generated here) so a Temporal workflow replay produces the identical
// activity input every time, as Temporal requires.
func (a *Activities) ExecuteRealSigning(ctx context.Context, req workflows.ThresholdSigningRequest) (*workflows.ThresholdSigningResult, error) {
	logger := activity.GetLogger(ctx)

	if len(req.PartyIDs) != len(req.PartyEndpoints) {
		return nil, fmt.Errorf("partyIds (%d) and partyEndpoints (%d) length mismatch", len(req.PartyIDs), len(req.PartyEndpoints))
	}
	if len(req.PartyIDs) == 0 {
		return nil, fmt.Errorf("no signing committee specified")
	}

	messageHash, err := hex.DecodeString(req.Message)
	if err != nil {
		return nil, fmt.Errorf("message must be hex-encoded: %w", err)
	}
	if len(messageHash) != 32 {
		return nil, fmt.Errorf("message hash must be 32 bytes, got %d", len(messageHash))
	}

	signIDSum := sha256.Sum256(append([]byte(req.CeremonyID+":"), messageHash...))
	signID := req.CeremonyID + "-sign-" + hex.EncodeToString(signIDSum[:8])

	endpointByParty := make(map[int]string, len(req.PartyIDs))
	for i, id := range req.PartyIDs {
		endpointByParty[id] = req.PartyEndpoints[i]
	}

	payload, err := json.Marshal(tssSignStartBody{
		SignID:            signID,
		KeygenCeremonyID:  req.CeremonyID,
		MessageHashHex:    req.Message,
		CommitteePartyIDs: req.PartyIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal sign start request: %w", err)
	}

	for _, id := range req.PartyIDs {
		if err := postJSONExpect2xx(ctx, a.httpClient, endpointByParty[id]+"/tss/sign/start", payload); err != nil {
			logger.Error("failed to start signing on party", "partyId", id, "error", err)
			return &workflows.ThresholdSigningResult{Status: "failed", Error: fmt.Sprintf("party %d: %v", id, err)}, nil
		}
	}
	logger.Info("real signing started on committee", "signId", signID, "committee", req.PartyIDs)

	const pollInterval = 250 * time.Millisecond
	for {
		allDone := true
		var signature string

		for _, id := range req.PartyIDs {
			status, err := getTSSStatus[tssSignStatusBody](ctx, a.httpClient, endpointByParty[id]+"/tss/sign/status?sign_id="+signID)
			if err != nil {
				return &workflows.ThresholdSigningResult{Status: "failed", Error: fmt.Sprintf("party %d status check: %v", id, err)}, nil
			}
			switch status.Status {
			case "failed":
				return &workflows.ThresholdSigningResult{Status: "failed", Error: fmt.Sprintf("party %d: %s", id, status.Error)}, nil
			case "completed":
				if signature != "" && signature != status.Signature {
					return &workflows.ThresholdSigningResult{Status: "failed", Error: fmt.Sprintf("party %d produced a different signature than the rest of the committee -- signing did not converge", id)}, nil
				}
				signature = status.Signature
			default:
				allDone = false
			}
		}

		if allDone {
			logger.Info("real signing completed", "signId", signID)
			return &workflows.ThresholdSigningResult{Status: "completed", Signature: signature}, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func postJSONExpect2xx(ctx context.Context, client *http.Client, url string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func getTSSStatus[T any](ctx context.Context, client *http.Client, url string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return &out, nil
}
