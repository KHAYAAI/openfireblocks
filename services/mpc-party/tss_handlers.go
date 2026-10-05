package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"os"

	"github.com/gorilla/mux"
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

// postTSSEnvelope delivers one relayed protocol message to path, retrying
// on 503 (the receiving party's ceremony/signing session is registered but
// not yet ready -- still generating preParams for keygen, or the tiny
// window before runSigning constructs its localParty for signing; see
// ErrCeremonyNotReady / ErrSigningNotReady) rather than failing the whole
// operation over that startup race. Any other failure (network error,
// 4xx/5xx other than 503) is returned immediately. Shared by postTSSMessage
// (keygen) and postTSSSignMessage (signing) -- only the envelope type and
// URL path differ between them.
// PeerReadyTimeout is how long a sender keeps retrying a peer that answers
// 503 "registered but not yet ready".
//
// It MUST exceed preParamsGenTimeout (preparams.go): this is the time a
// party's peers will wait for it to become ready, and that is the longest
// it may take to become ready. Previously these were 60s and 120s
// respectively -- inverted -- so a party that used its full allowance was
// certain to be abandoned mid-ceremony. TestPreParamsTimeoutIsBelowPeerReadyTimeout
// pins the ordering.
//
// Raised alongside preParamsGenTimeout: a peer that is legitimately still
// searching for safe primes has to remain worth waiting for, and giving up
// on it aborts a ceremony that would otherwise have completed.
const PeerReadyTimeout = 6 * time.Minute

// retryInterval is how often to re-offer a message to a not-yet-ready peer.
const retryInterval = 250 * time.Millisecond

func postTSSEnvelope(client *http.Client, baseURL, path string, envelope interface{}) error {
	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal message envelope: %w", err)
	}

	deadline := time.Now().Add(PeerReadyTimeout)

	for {
		resp, err := client.Post(baseURL+path, "application/json", bytes.NewReader(body))
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

func postTSSMessage(client *http.Client, baseURL string, env tssMessageEnvelope) error {
	return postTSSEnvelope(client, baseURL, "/tss/keygen/message", env)
}

func postTSSSignMessage(client *http.Client, baseURL string, env tssSignMessageEnvelope) error {
	return postTSSEnvelope(client, baseURL, "/tss/sign/message", env)
}

// tssKeygenStartRequest is the body for POST /tss/keygen/start. peers must
// be identical (same party IDs and base URLs) across every party the
// ceremony initiator sends this to -- see deterministicPartyIDs in
// tss_party.go for why.
type tssKeygenStartRequest struct {
	CeremonyID string            `json:"ceremony_id"`
	Threshold  int               `json:"threshold"`
	Peers      map[string]string `json:"peers"` // partyId (as string, JSON object keys) -> base URL
	// Which curve to generate on. Absent means secp256k1, which is what
	// every key generated before this field existed is.
	Curve string `json:"curve,omitempty"`
	// The chain the key is for. When present it decides the curve, and the
	// address is written the way that chain writes it. A Curve that
	// disagrees with it is refused: the two were meant to say the same thing
	// and a mismatch means a caller is confused about what it is creating.
	Blockchain string `json:"blockchain,omitempty"`
	// A signature over this request from a key the platform's own hosts
	// cannot reach. Required only when this party has an authoriser
	// configured -- see authorizer.go.
	Authorization          string `json:"authorization,omitempty"`
	AuthorizationSignature string `json:"authorization_signature,omitempty"`
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

	// Defaulted to secp256k1 when absent, for the orchestrator that
	// predates the field. Every key generated before curves existed is
	// secp256k1, so this preserves the meaning of an old request rather
	// than guessing about a new one -- and an explicitly wrong curve is
	// still refused by StartKeygen.
	curve := Curve(req.Curve)
	if curve == "" {
		curve = CurveSecp256k1
	}

	if err := ps.requireAuthorization("keygen", req.CeremonyID, "",
		req.Authorization, req.AuthorizationSignature); err != nil {
		log.Printf("refused keygen %s: %v", req.CeremonyID, err)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if req.Blockchain != "" {
		want, err := CurveForChain(req.Blockchain)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if req.Curve != "" && Curve(req.Curve) != want {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf(
				"blockchain %q needs curve %s but the request asked for %s", req.Blockchain, want, req.Curve)})
			return
		}
	}
	var startErr error
	if req.Blockchain != "" {
		startErr = ps.tssManager.StartKeygenForChain(req.CeremonyID, req.Threshold, peers, req.Blockchain)
	} else {
		startErr = ps.tssManager.StartKeygen(req.CeremonyID, req.Threshold, peers, curve)
	}
	if err := startErr; err != nil {
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
	// The sender is whoever the client certificate says it is, not
	// whoever the body claims. See peer_identity.go.
	if err := authenticatePeer(r, env.FromPartyID); err != nil {
		log.Printf("rejected a keygen message for ceremony %s: %v (%s)",
			env.CeremonyID, err, peerCertificateSummary(r.TLS))
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
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

// tssSignMessageEnvelope is the wire format for relaying one tss-lib
// signing protocol message between mpc-party processes over HTTP --
// structurally identical to tssMessageEnvelope, kept as a separate type
// (SignID vs CeremonyID) so a caller can't accidentally cross-wire a
// keygen message into the signing endpoint or vice versa.
type tssSignMessageEnvelope struct {
	SignID      string `json:"sign_id"`
	FromPartyID int    `json:"from_party_id"`
	IsBroadcast bool   `json:"is_broadcast"`
	WireBytes   string `json:"wire_bytes"` // base64
}

// tssSignStartRequest is the body for POST /tss/sign/start. keygen_ceremony_id
// must reference a completed keygen ceremony this party actually took part
// in; committee_party_ids must be exactly threshold+1 party IDs (the
// keygen ceremony's own threshold), all of whom took part in that
// ceremony, sent identically to every committee member -- see StartSigning's
// doc comment in tss_signing.go for why re-deriving a fresh subset isn't safe.
type tssSignStartRequest struct {
	SignID            string `json:"sign_id"`
	KeygenCeremonyID  string `json:"keygen_ceremony_id"`
	MessageHashHex    string `json:"message_hash_hex"` // 32 bytes, hex-encoded
	CommitteePartyIDs []int  `json:"committee_party_ids"`
	// A signature over this request from a key the platform's own hosts
	// cannot reach. Required only when this party has an authoriser
	// configured -- see authorizer.go.
	Authorization          string `json:"authorization,omitempty"`
	AuthorizationSignature string `json:"authorization_signature,omitempty"`
}

func (ps *PartyServer) HandleTSSSignStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req tssSignStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.SignID == "" || req.KeygenCeremonyID == "" || len(req.CommitteePartyIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sign_id, keygen_ceremony_id, and committee_party_ids are required"})
		return
	}

	hash, err := hex.DecodeString(req.MessageHashHex)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message_hash_hex must be valid hex"})
		return
	}

	if err := ps.requireAuthorization("sign", req.SignID, req.MessageHashHex,
		req.Authorization, req.AuthorizationSignature); err != nil {
		log.Printf("refused signing %s: %v", req.SignID, err)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if err := ps.tssManager.StartSigning(req.SignID, req.KeygenCeremonyID, hash, req.CommitteePartyIDs); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (ps *PartyServer) HandleTSSSignMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var env tssSignMessageEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := authenticatePeer(r, env.FromPartyID); err != nil {
		log.Printf("rejected a signing message for session %s: %v (%s)",
			env.SignID, err, peerCertificateSummary(r.TLS))
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if err := ps.tssManager.HandleIncomingSignMessage(env); err != nil {
		if err == ErrSigningNotReady {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

func (ps *PartyServer) HandleTSSSignStatus(w http.ResponseWriter, r *http.Request) {
	signID := r.URL.Query().Get("sign_id")
	if signID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sign_id query param required"})
		return
	}
	status, err := ps.tssManager.GetSigningStatus(signID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// tssReshareMessageEnvelope is the wire format for relaying one tss-lib
// resharing message. Structurally the same as the keygen and signing
// envelopes and kept as its own type for the same reason: a refresh
// message delivered to the keygen endpoint would be fed to the wrong state
// machine, and the compiler should be the thing that prevents it.
type tssReshareMessageEnvelope struct {
	ReshareID   string `json:"reshare_id"`
	FromPartyID int    `json:"from_party_id"`
	IsBroadcast bool   `json:"is_broadcast"`
	WireBytes   string `json:"wire_bytes"` // base64
	// Which of the receiving node's two local parties this message is for,
	// and which of the sender's two it came from. Both are needed: a node
	// runs an old-committee and a new-committee party at once, and a
	// message delivered to the wrong one is never accounted for.
	ToOldCommittee   bool `json:"to_old_committee"`
	FromOldCommittee bool `json:"from_old_committee"`
}

func postReshareEnvelope(client *http.Client, baseURL string, env tssReshareMessageEnvelope) error {
	return postTSSEnvelope(client, baseURL, "/tss/reshare/message", env)
}

// tssReshareStartRequest is the body for POST /tss/reshare/start.
type tssReshareStartRequest struct {
	ReshareID        string         `json:"reshare_id"`
	SourceCeremonyID string         `json:"source_ceremony_id"`
	Peers            map[int]string `json:"peers"`
	// A signature over this request from a key the platform's own hosts
	// cannot reach. Required only when this party has an authoriser
	// configured -- see authorizer.go.
	Authorization          string `json:"authorization,omitempty"`
	AuthorizationSignature string `json:"authorization_signature,omitempty"`
}

// HandleTSSReshareStart begins a proactive key refresh.
func (ps *PartyServer) HandleTSSReshareStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req tssReshareStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.ReshareID == "" || req.SourceCeremonyID == "" {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "reshare_id and source_ceremony_id are required"})
		return
	}
	if err := ps.requireAuthorization("reshare", req.ReshareID, "",
		req.Authorization, req.AuthorizationSignature); err != nil {
		log.Printf("refused resharing %s: %v", req.ReshareID, err)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if err := ps.tssManager.StartResharing(req.ReshareID, req.SourceCeremonyID, req.Peers); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"reshare_id": req.ReshareID,
		"status":     "in_progress",
	})
}

// HandleTSSReshareMessage receives one relayed refresh message.
func (ps *PartyServer) HandleTSSReshareMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var env tssReshareMessageEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	// Same sender binding as keygen and signing: a refresh message is fed
	// to a state machine holding a live key, so who sent it matters at
	// least as much there as anywhere else.
	if err := authenticatePeer(r, env.FromPartyID); err != nil {
		log.Printf("rejected a resharing message for %s: %v (%s)",
			env.ReshareID, err, peerCertificateSummary(r.TLS))
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if err := ps.tssManager.HandleIncomingReshareMessage(env); err != nil {
		if err == ErrResharingNotReady {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "accepted"})
}

// HandleTSSReshareStatus reports how a refresh went.
func (ps *PartyServer) HandleTSSReshareStatus(w http.ResponseWriter, r *http.Request) {
	reshareID := mux.Vars(r)["reshareId"]
	status, err := ps.tssManager.GetResharingStatus(reshareID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// tssRestoreRequest is the body for POST /tss/keygen/restore.
type tssRestoreRequest struct {
	CeremonyID string         `json:"ceremony_id"`
	Peers      map[int]string `json:"peers"`
	// Authorised like any other ceremony operation: restoring a key into a
	// signing party is exactly as sensitive as generating one.
	Authorization          string `json:"authorization,omitempty"`
	AuthorizationSignature string `json:"authorization_signature,omitempty"`
}

// HandleTSSRestore reloads a completed ceremony from sealed material.
func (ps *PartyServer) HandleTSSRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req tssRestoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := ps.requireAuthorization("restore", req.CeremonyID, "",
		req.Authorization, req.AuthorizationSignature); err != nil {
		log.Printf("refused restore of %s: %v", req.CeremonyID, err)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if err := ps.tssManager.RestoreCeremony(req.CeremonyID, req.Peers); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	status, err := ps.tssManager.GetStatus(req.CeremonyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

type tssRetireRequest struct {
	CeremonyID             string `json:"ceremony_id"`
	Authorization          string `json:"authorization,omitempty"`
	AuthorizationSignature string `json:"authorization_signature,omitempty"`
}

// HandleTSSRetire permanently destroys this party's share for a ceremony:
// from memory, and from this party's own Vault.
//
// Authorised like every other ceremony operation -- destroying a share is
// irreversible, and on a key that still holds money it is the same as
// burning it. The co-signer decides whether that is intended.
func (ps *PartyServer) HandleTSSRetire(w http.ResponseWriter, r *http.Request) {
	var req tssRetireRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CeremonyID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request: ceremony_id is required"})
		return
	}
	if err := ps.requireAuthorization("retire", req.CeremonyID, "",
		req.Authorization, req.AuthorizationSignature); err != nil {
		log.Printf("refused retirement of %s: %v", req.CeremonyID, err)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}

	inMemory := ps.tssManager.Forget(req.CeremonyID)
	destroyed, err := RetireSealedShare(r.Context(), os.Getenv, ps.partyID, req.CeremonyID)
	if err != nil {
		// The share may still be in Vault. Say so: the caller must retry,
		// not record the retirement as done.
		log.Printf("retirement of %s: destroying the sealed share failed: %v", req.CeremonyID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("retired ceremony %s (in memory: %v, sealed share destroyed: %v)", req.CeremonyID, inMemory, destroyed)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ceremony_id":      req.CeremonyID,
		"party_id":         ps.partyID,
		"held_in_memory":   inMemory,
		"sealed_destroyed": destroyed,
	})
}
