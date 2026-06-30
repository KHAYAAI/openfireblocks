package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleRoundStart(t *testing.T) {
	ps := NewPartyServer(1)
	ps.tss = NewTSSWrapper(1, 7, 3)

	signal := RoundStartSignal{
		CeremonyID: "test-ceremony-1",
		RoundNum:   1,
		Action:     "start",
	}

	body, _ := json.Marshal(signal)
	req := httptest.NewRequest(http.MethodPost, "/round", bytes.NewReader(body))
	w := httptest.NewRecorder()

	ps.HandleRoundStart(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "acknowledged" {
		t.Errorf("expected acknowledged status, got %s", resp["status"])
	}
}

func TestHandleRoundDataRound1(t *testing.T) {
	ps := NewPartyServer(1)
	ps.tss = NewTSSWrapper(1, 7, 3)
	ps.state = &DKGPartyState{
		PartyID: 1,
		RoundNum: 1,
		Status: "round1",
	}

	req := httptest.NewRequest(http.MethodGet, "/round/1/data", nil)
	req = mux.SetURLVars(req, map[string]string{"round": "1"})
	w := httptest.NewRecorder()

	ps.HandleRoundData(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var roundData RoundData
	json.NewDecoder(w.Body).Decode(&roundData)
	if roundData.PartyID != 1 {
		t.Errorf("expected party ID 1, got %d", roundData.PartyID)
	}
	if roundData.RoundNum != 1 {
		t.Errorf("expected round 1, got %d", roundData.RoundNum)
	}
	if roundData.Commitments == "" {
		t.Error("expected non-empty commitments")
	}
}

func TestHandleBroadcastRoundData(t *testing.T) {
	ps := NewPartyServer(1)
	ps.tss = NewTSSWrapper(1, 7, 3)

	partyDataMap := map[int]*RoundData{
		2: {
			PartyID:     2,
			RoundNum:    1,
			Commitments: "test-commitments-2",
		},
		3: {
			PartyID:     3,
			RoundNum:    1,
			Commitments: "test-commitments-3",
		},
	}

	req := BroadcastRoundDataRequest{
		CeremonyID:   "test-ceremony-1",
		RoundNum:     1,
		PartyDataMap: partyDataMap,
	}

	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/round/1/broadcast", bytes.NewReader(body))
	w := httptest.NewRecorder()

	ps.HandleBroadcastRoundData(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Verify data was stored
	ps.roundDataMu.RLock()
	if len(ps.roundData) != 2 {
		t.Errorf("expected 2 round data entries, got %d", len(ps.roundData))
	}
	if ps.roundData[2].Commitments != "test-commitments-2" {
		t.Errorf("wrong commitments stored for party 2")
	}
	ps.roundDataMu.RUnlock()
}

func TestHandleComputeSignature(t *testing.T) {
	ps := NewPartyServer(1)
	ps.tss = NewTSSWrapper(1, 7, 3)

	// Simulate completed DKG
	ps.tss.keyShare = big.NewInt(12345)

	sigReq := ThresholdSignatureRequest{
		CeremonyID: "test-ceremony-1",
		Message:    "deadbeef",
		PartyIDs:   []int{1, 2, 3},
	}

	body, _ := json.Marshal(sigReq)
	req := httptest.NewRequest(http.MethodPost, "/sign", bytes.NewReader(body))
	w := httptest.NewRecorder()

	ps.HandleComputeSignature(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var sig PartialSignature
	json.NewDecoder(w.Body).Decode(&sig)
	if sig.PartyID != 1 {
		t.Errorf("expected party ID 1, got %d", sig.PartyID)
	}
	if sig.Signature == "" {
		t.Error("expected non-empty signature")
	}
}

func TestHandleHealth(t *testing.T) {
	ps := NewPartyServer(2)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	ps.HandleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var health map[string]string
	json.NewDecoder(w.Body).Decode(&health)
	if health["status"] != "healthy" {
		t.Errorf("expected healthy status, got %s", health["status"])
	}
	if health["partyId"] != "2" {
		t.Errorf("expected party ID 2, got %s", health["partyId"])
	}
}

func TestHandleInfo(t *testing.T) {
	ps := NewPartyServer(3)
	ps.state = &DKGPartyState{
		CeremonyID: "ceremony-123",
		PartyID:    3,
		Status:     "round2",
		RoundNum:   2,
	}

	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	w := httptest.NewRecorder()

	ps.HandleInfo(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var info map[string]interface{}
	json.NewDecoder(w.Body).Decode(&info)
	if info["partyId"] != float64(3) {
		t.Errorf("expected party ID 3, got %v", info["partyId"])
	}
	if info["ceremonyId"] != "ceremony-123" {
		t.Errorf("expected ceremony ID ceremony-123, got %v", info["ceremonyId"])
	}
}

func TestTSSWrapperRound1(t *testing.T) {
	wrapper := NewTSSWrapper(1, 7, 3)

	ctx := context.WithValue(context.Background(), "timestamp", time.Now())
	r1Out, err := wrapper.ExecuteRound1(ctx)
	if err != nil {
		t.Fatalf("ExecuteRound1 failed: %v", err)
	}

	if r1Out.Commitments == "" {
		t.Error("expected non-empty commitments")
	}
	if r1Out.PublicKey == "" {
		t.Error("expected non-empty public key")
	}
	if r1Out.DLProof == "" {
		t.Error("expected non-empty DL proof")
	}

	if wrapper.keyShare == nil {
		t.Error("key share should be set after round 1")
	}
}

func TestTSSWrapperComputePartialSignature(t *testing.T) {
	wrapper := NewTSSWrapper(1, 7, 3)

	// Simulate completed DKG
	wrapper.keyShare = big.NewInt(12345)

	ctx := context.Background()
	message := []byte("test message")
	partyIDs := []int{1, 2, 3}

	sig, err := wrapper.ComputePartialSignature(ctx, message, partyIDs)
	if err != nil {
		t.Fatalf("ComputePartialSignature failed: %v", err)
	}

	if sig == "" {
		t.Error("expected non-empty signature")
	}
}

func TestTSSWrapperCombineSignatures(t *testing.T) {
	wrapper := NewTSSWrapper(1, 7, 3)

	partialSigs := map[int]string{
		1: "1234567890abcdef",
		2: "fedcba0987654321",
		3: "aaaaaabbbbbbcccc",
	}

	combined, err := wrapper.CombinePartialSignatures(partialSigs, []int{1, 2, 3})
	if err != nil {
		t.Fatalf("CombinePartialSignatures failed: %v", err)
	}

	if combined == "" {
		t.Error("expected non-empty combined signature")
	}
}

// Add missing imports
import (
	"fmt"
	"math/big"
	"time"

	"github.com/gorilla/mux"
)
