package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"time"
)

// tssMessageEnvelope is the wire format for relaying one tss-lib protocol
// message between mpc-party processes over HTTP.
type tssMessageEnvelope struct {
	CeremonyID  string `json:"ceremony_id"`
	FromPartyID int    `json:"from_party_id"`
	IsBroadcast bool   `json:"is_broadcast"`
	WireBytes   string `json:"wire_bytes"` // base64
}

// postTSSMessage delivers one relayed protocol message, retrying on 503
// (the receiving party's ceremony is registered but not yet ready --
// still generating its own preParams, see ErrCeremonyNotReady) rather than
// failing the whole ceremony over that startup race. Any other failure
// (network error, 4xx/5xx other than 503) is returned immediately.
func postTSSMessage(client *http.Client, baseURL string, env tssMessageEnvelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("failed to marshal message envelope: %w", err)
	}

	const maxWait = 60 * time.Second
	const retryInterval = 250 * time.Millisecond
	deadline := time.Now().Add(maxWait)

	for {
		resp, err := client.Post(baseURL+"/tss/keygen/message", "application/json", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		if resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		if resp.StatusCode == http.StatusServiceUnavailable && time.Now().Before(deadline) {
			resp.Body.Close()
			time.Sleep(retryInterval)
			continue
		}
		dump, _ := httputil.DumpResponse(resp, true)
		resp.Body.Close()
		return fmt.Errorf("peer returned %d: %s", resp.StatusCode, string(dump))
	}
}

// tssKeygenStartRequest is the body for POST /tss/keygen/start. peers must
// be identical (same party IDs and base URLs) across every party the
// ceremony initiator sends this to -- see deterministicPartyIDs in
// tss_party.go for why.
type tssKeygenStartRequest struct {
	CeremonyID string            `json:"ceremony_id"`
	Threshold  int               `json:"threshold"`
	Peers      map[string]string `json:"peers"` // partyId (as string, JSON object keys) -> base URL
}

func (ps *PartyServer) HandleTSSKeygenStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req tssKeygenStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.CeremonyID == "" || req.Threshold <= 0 || len(req.Peers) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ceremony_id, threshold, and peers are required"})
		return
	}

	peers := make(map[int]string, len(req.Peers))
	for idStr, url := range req.Peers {
		var id int
		if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid peer id %q", idStr)})
			return
		}
		peers[id] = url
	}

	if err := ps.tssManager.StartKeygen(req.CeremonyID, req.Threshold, peers); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (ps *PartyServer) HandleTSSKeygenMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var env tssMessageEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := ps.tssManager.HandleIncomingMessage(env); err != nil {
		if err == ErrCeremonyNotReady {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func (ps *PartyServer) HandleTSSKeygenStatus(w http.ResponseWriter, r *http.Request) {
	ceremonyID := r.URL.Query().Get("ceremony_id")
	if ceremonyID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ceremony_id query param required"})
		return
	}
	status, err := ps.tssManager.GetStatus(ceremonyID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}
