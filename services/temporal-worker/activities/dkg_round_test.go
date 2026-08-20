package activities

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecuteDKGRound_AllPartiesRespond tests successful DKG round with all parties responding.
func TestExecuteDKGRound_AllPartiesRespond(t *testing.T) {
	// Create mock party servers
	parties := make([]*httptest.Server, 3)
	partyIds := []int{0, 1, 2}

	for i, pid := range partyIds {
		pid := pid // capture for closure
		parties[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Mock party endpoint responses
			if r.Method == http.MethodGet && r.URL.Path == "/round/1/data" {
				data := RoundData{
					PartyID:     pid,
					RoundNum:    1,
					Commitments: "commitment_base64_" + string(rune(pid+'0')),
					DLProof:     "dlproof_" + string(rune(pid+'0')),
					PublicKey:   "0xpubkey" + string(rune(pid+'0')),
					Signature:   "0xsig" + string(rune(pid+'0')),
				}
				json.NewEncoder(w).Encode(data)
			}
		}))
	}
	defer func() {
		for _, p := range parties {
			p.Close()
		}
	}()

	coordinator := NewDKGRoundCoordinator()
	coordinator.endpointBase = func(partyId int) string { return parties[partyId].URL }
	ctx := context.Background()

	// Collect data from parties
	partyData, err := coordinator.CollectRoundData(ctx, "test-ceremony", 1, partyIds)
	require.NoError(t, err)

	// Verify all parties responded
	assert.Equal(t, len(partyIds), len(partyData))

	// Validate data
	errors := coordinator.ValidateRoundData(1, partyData)
	assert.Empty(t, errors, "expected no validation errors")

	// Verify party data
	for pid := range partyData {
		assert.NotNil(t, partyData[pid])
		assert.NotEmpty(t, partyData[pid].Commitments)
		assert.NotEmpty(t, partyData[pid].PublicKey)
	}
}

// TestValidateRoundData_MissingCommitments tests validation with missing commitments.
func TestValidateRoundData_MissingCommitments(t *testing.T) {
	coordinator := NewDKGRoundCoordinator()

	partyData := map[int]*RoundData{
		0: {
			PartyID:     0,
			RoundNum:    1,
			Commitments: "valid_commitments",
			PublicKey:   "0xpubkey0",
		},
		1: {
			PartyID:     1,
			RoundNum:    1,
			Commitments: "", // Missing!
			PublicKey:   "0xpubkey1",
		},
	}

	errors := coordinator.ValidateRoundData(1, partyData)
	assert.NotEmpty(t, errors)
	assert.Contains(t, errors[0], "party 1")
	assert.Contains(t, errors[0], "missing commitments")
}

// TestValidateRoundData_Round2Decommitment tests round 2 validation (decommitments required).
func TestValidateRoundData_Round2Decommitment(t *testing.T) {
	coordinator := NewDKGRoundCoordinator()

	partyData := map[int]*RoundData{
		0: {
			PartyID:     0,
			RoundNum:    2,
			Commitments: "commitments",
			DLProof:     "valid_proof",
			PublicKey:   "0xpubkey0",
		},
		1: {
			PartyID:     1,
			RoundNum:    2,
			Commitments: "commitments",
			DLProof:     "", // Missing decommitment proof!
			PublicKey:   "0xpubkey1",
		},
	}

	errors := coordinator.ValidateRoundData(2, partyData)
	assert.NotEmpty(t, errors)
	assert.Contains(t, errors[0], "missing decommitment proof")
}

// TestValidateRoundData_AllRounds tests validation rules for all 7 rounds.
func TestValidateRoundData_AllRounds(t *testing.T) {
	coordinator := NewDKGRoundCoordinator()

	for round := 1; round <= 7; round++ {
		partyData := map[int]*RoundData{
			0: {
				PartyID:     0,
				RoundNum:    round,
				Commitments: "commitments",
				DLProof:     "proof",
				PublicKey:   "0xpubkey0",
			},
			1: {
				PartyID:     1,
				RoundNum:    round,
				Commitments: "commitments",
				DLProof:     "proof",
				PublicKey:   "0xpubkey1",
			},
		}

		errors := coordinator.ValidateRoundData(round, partyData)
		assert.Empty(t, errors, "round %d should have no validation errors", round)
	}
}

// TestBroadcastRoundData_FanOut tests fan-out broadcast to all parties.
func TestBroadcastRoundData_FanOut(t *testing.T) {
	// Create mock party servers that track received broadcasts
	broadcastCount := make(map[int]int)
	parties := make([]*httptest.Server, 3)
	partyIds := []int{0, 1, 2}

	for i, pid := range partyIds {
		pid := pid
		parties[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && r.URL.Path == "/round/1/broadcast" {
				broadcastCount[pid]++
				w.WriteHeader(http.StatusOK)
			}
		}))
	}
	defer func() {
		for _, p := range parties {
			p.Close()
		}
	}()

	coordinator := NewDKGRoundCoordinator()
	coordinator.endpointBase = func(partyId int) string { return parties[partyId].URL }
	ctx := context.Background()

	partyData := map[int]*RoundData{
		0: {PartyID: 0, Commitments: "data0"},
		1: {PartyID: 1, Commitments: "data1"},
		2: {PartyID: 2, Commitments: "data2"},
	}

	err := coordinator.BroadcastRoundData(ctx, "test-ceremony", 1, partyData)
	assert.NoError(t, err)

	// Every target party receives one broadcast containing the other two
	// parties' data (fan-out excludes the target's own data).
	for _, pid := range partyIds {
		assert.Equal(t, 1, broadcastCount[pid], "party %d should receive exactly one broadcast", pid)
	}
}

// TestFetchPartyRoundData_Success tests fetching data from a single party.
func TestFetchPartyRoundData_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expectedData := RoundData{
			PartyID:     0,
			RoundNum:    1,
			Commitments: "test_commitments",
			DLProof:     "test_proof",
			PublicKey:   "0xtest_pubkey",
			Signature:   "0xtest_sig",
		}
		json.NewEncoder(w).Encode(expectedData)
	}))
	defer server.Close()

	coordinator := NewDKGRoundCoordinator()
	coordinator.endpointBase = func(partyId int) string { return server.URL }
	ctx := context.Background()

	data, err := coordinator.FetchPartyRoundData(ctx, "test-ceremony", 1, 0)
	require.NoError(t, err)
	assert.NotNil(t, data)
	assert.Equal(t, 0, data.PartyID)
}

// TestFetchPartyRoundData_Timeout tests timeout handling.
func TestFetchPartyRoundData_Timeout(t *testing.T) {
	// Create server that times out. Blocks on the request's own context
	// instead of `select {}`: with select{}, the handler goroutine never
	// returns even after the client gives up, and httptest.Server.Close()
	// (called via defer below) waits for every in-flight handler to
	// return -- so the whole test binary hung for the full 10-minute test
	// timeout instead of the 100ms this test is supposed to take.
	// Blocking on r.Context().Done() lets the handler return as soon as
	// the client's canceled context tears down the connection.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	coordinator := NewDKGRoundCoordinator()
	coordinator.endpointBase = func(partyId int) string { return server.URL }
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Should timeout
	_, err := coordinator.FetchPartyRoundData(ctx, "test-ceremony", 1, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context")
}
