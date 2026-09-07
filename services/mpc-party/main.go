package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// PartyServer represents an MPC DKG party service.
type PartyServer struct {
	partyID     int
	config      *PartyConfig
	state       *DKGPartyState
	tss         *TSSWrapper
	tssManager  *TSSPartyManager // real network-driven tss-lib DKG, see tss_party.go
	stateMu     sync.RWMutex
	roundDataMu sync.RWMutex
	roundData   map[int]*RoundData
}

// Prometheus metrics
var (
	roundDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "dkg_round_duration_seconds",
			Help: "Duration of DKG rounds in seconds",
		},
		[]string{"round", "party"},
	)
	roundTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dkg_round_total",
			Help: "Total number of DKG rounds executed",
		},
		[]string{"round", "status"},
	)
	signatureTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "threshold_signature_total",
			Help: "Total number of threshold signatures computed",
		},
		[]string{"status"},
	)
)

func init() {
	prometheus.MustRegister(roundDuration, roundTotal, signatureTotal)
}

// NewPartyServer creates a new DKG party server.
func NewPartyServer(partyID int) *PartyServer {
	return &PartyServer{
		partyID:   partyID,
		roundData: make(map[int]*RoundData),
	}
}

// HandleRoundStart handles the signal to start a new DKG round.
func (ps *PartyServer) HandleRoundStart(w http.ResponseWriter, r *http.Request) {
	var signal RoundStartSignal

	if err := json.NewDecoder(r.Body).Decode(&signal); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	log.Printf("party %d received round start signal for round %d", ps.partyID, signal.RoundNum)

	ps.stateMu.Lock()
	if ps.state == nil {
		ps.state = &DKGPartyState{
			PartyID:              ps.partyID,
			ReceivedRoundDataMap: make(map[int]*RoundData),
			CreatedAt:            time.Now(),
		}
	}
	ps.state.CeremonyID = signal.CeremonyID
	ps.state.RoundNum = signal.RoundNum
	ps.state.Status = fmt.Sprintf("round%d", signal.RoundNum)
	ps.state.UpdatedAt = time.Now()
	ps.stateMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]string{"status": "acknowledged"})
}

// HandleRoundData handles requests to retrieve this party's round data.
func (ps *PartyServer) HandleRoundData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	roundNum := mux.Vars(r)["round"]

	round, err := strconv.Atoi(roundNum)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid round number"})
		return
	}

	log.Printf("party %d providing round %d data", ps.partyID, round)

	timer := prometheus.NewTimer(roundDuration.WithLabelValues(roundNum, fmt.Sprintf("party_%d", ps.partyID)))
	defer timer.ObserveDuration()

	ps.stateMu.RLock()
	defer ps.stateMu.RUnlock()

	var roundData *RoundData

	// Execute the appropriate DKG round
	switch round {
	case 1:
		// Round 1: Generate commitments
		r1Out, err := ps.tss.ExecuteRound1(ctx)
		if err != nil {
			roundTotal.WithLabelValues(roundNum, "failed").Inc()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		roundData = &RoundData{
			PartyID:     ps.partyID,
			RoundNum:    round,
			Commitments: r1Out.Commitments,
			DLProof:     r1Out.DLProof,
			PublicKey:   r1Out.PublicKey,
			Signature:   r1Out.PublicKey,
		}

	case 2:
		// Round 2: Generate decommitments (shares)
		ps.roundDataMu.RLock()
		receivedCommitments := make(map[int]string)
		for partyID, data := range ps.roundData {
			if data.Commitments != "" {
				receivedCommitments[partyID] = data.Commitments
			}
		}
		ps.roundDataMu.RUnlock()

		r2Out, err := ps.tss.ExecuteRound2(ctx, receivedCommitments)
		if err != nil {
			roundTotal.WithLabelValues(roundNum, "failed").Inc()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		roundData = &RoundData{
			PartyID:     ps.partyID,
			RoundNum:    round,
			Commitments: r2Out.Decommitments,
			DLProof:     r2Out.ProofOfShare,
			PublicKey:   "",
			Signature:   "",
		}

	case 3, 4, 5, 6, 7:
		// Rounds 3-7: Validation and finalization
		ps.roundDataMu.RLock()
		receivedData := make(map[int]*RoundData)
		for partyID, data := range ps.roundData {
			receivedData[partyID] = data
		}
		ps.roundDataMu.RUnlock()

		keyShare, err := ps.tss.ExecuteRound3to7(ctx, receivedData)
		if err != nil {
			roundTotal.WithLabelValues(roundNum, "failed").Inc()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		// In final round, return the key share data
		roundData = &RoundData{
			PartyID:     ps.partyID,
			RoundNum:    round,
			Commitments: keyShare.Commitment,
			DLProof:     "",
			PublicKey:   keyShare.PublicKey,
			Signature:   keyShare.KeyShare,
		}

	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid round"})
		return
	}

	roundTotal.WithLabelValues(roundNum, "success").Inc()
	writeJSON(w, http.StatusOK, roundData)
}

// HandleBroadcastRoundData handles receiving round data from other parties (fan-in).
func (ps *PartyServer) HandleBroadcastRoundData(w http.ResponseWriter, r *http.Request) {
	var req BroadcastRoundDataRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	log.Printf("party %d received broadcast of %d parties' data for round %d",
		ps.partyID, len(req.PartyDataMap), req.RoundNum)

	// Store received data for use in next rounds
	ps.roundDataMu.Lock()
	for partyID, data := range req.PartyDataMap {
		if data != nil {
			ps.roundData[partyID] = data
		}
	}
	ps.roundDataMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]string{"status": "stored"})
}

// HandleComputeSignature handles requests to compute partial signatures.
func (ps *PartyServer) HandleComputeSignature(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req ThresholdSignatureRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	log.Printf("party %d computing partial signature for ceremony %s", ps.partyID, req.CeremonyID)

	// Decode message
	messageBytes := make([]byte, len(req.Message)/2)
	if _, err := fmt.Sscanf(req.Message, "%x", &messageBytes); err != nil {
		signatureTotal.WithLabelValues("invalid").Inc()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid message format"})
		return
	}

	// Compute partial signature using Lagrange interpolation
	partialSig, err := ps.tss.ComputePartialSignature(ctx, messageBytes, req.PartyIDs)
	if err != nil {
		signatureTotal.WithLabelValues("failed").Inc()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	signatureTotal.WithLabelValues("success").Inc()
	writeJSON(w, http.StatusOK, PartialSignature{
		PartyID:    ps.partyID,
		Signature:  partialSig,
		CeremonyID: req.CeremonyID,
		RoundNum:   0,
	})
}

// HandleHealth returns the party's health status.
func (ps *PartyServer) HandleHealth(w http.ResponseWriter, r *http.Request) {
	ps.stateMu.RLock()
	status := "healthy"
	if ps.state != nil && ps.state.Status == "failed" {
		status = "unhealthy"
	}
	ps.stateMu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  status,
		"partyId": fmt.Sprintf("%d", ps.partyID),
	})
}

// HandleInfo returns information about the party.
func (ps *PartyServer) HandleInfo(w http.ResponseWriter, r *http.Request) {
	ps.stateMu.RLock()
	defer ps.stateMu.RUnlock()

	info := map[string]interface{}{
		"partyId": ps.partyID,
	}
	if ps.state != nil {
		info["ceremonyId"] = ps.state.CeremonyID
		info["status"] = ps.state.Status
		info["roundNum"] = ps.state.RoundNum
	}

	writeJSON(w, http.StatusOK, info)
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	// Get party ID and configuration from environment
	partyIDStr := getenv("PARTY_ID", "1")
	partyID, err := strconv.Atoi(partyIDStr)
	if err != nil {
		log.Fatalf("invalid PARTY_ID: %v", err)
	}

	// Initialize party server
	ps := NewPartyServer(partyID)
	ps.tss = NewTSSWrapper(partyID, 7, 3) // Default: 7 parties, threshold 3

	relayTransport, relayMTLSEnabled, err := mtlsTransportFromEnv()
	if err != nil {
		log.Fatalf("mTLS configuration error: %v", err)
	}
	relayClient := &http.Client{Timeout: 30 * time.Second}
	if relayMTLSEnabled {
		relayClient.Transport = relayTransport
	}
	ps.tssManager = NewTSSPartyManager(partyID, relayClient)

	// Initialize state
	ps.state = &DKGPartyState{
		PartyID:              partyID,
		Status:               "pending",
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
		ReceivedRoundDataMap: make(map[int]*RoundData),
	}

	log.Printf("MPC party %d initialized", partyID)

	// Setup HTTP routes
	router := mux.NewRouter()

	// DKG endpoints
	router.HandleFunc("/round", ps.HandleRoundStart).Methods(http.MethodPost)
	router.HandleFunc("/round/{round}/data", ps.HandleRoundData).Methods(http.MethodGet)
	router.HandleFunc("/round/{round}/broadcast", ps.HandleBroadcastRoundData).Methods(http.MethodPost)

	// Signing endpoints
	router.HandleFunc("/sign", ps.HandleComputeSignature).Methods(http.MethodPost)

	// Real, network-driven tss-lib DKG (see tss_party.go) -- distinct from
	// the /round/* endpoints above, which remain the non-cryptographic
	// placeholder documented in tss_wrapper.go until temporal-worker's
	// DKGRoundCoordinator is rewired to drive this protocol instead.
	router.HandleFunc("/tss/keygen/start", ps.HandleTSSKeygenStart).Methods(http.MethodPost)
	router.HandleFunc("/tss/keygen/message", ps.HandleTSSKeygenMessage).Methods(http.MethodPost)
	router.HandleFunc("/tss/keygen/status", ps.HandleTSSKeygenStatus).Methods(http.MethodGet)

	// Real, network-driven tss-lib threshold SIGNING (see tss_signing.go) --
	// the counterpart to /tss/keygen/* above. Always references a
	// completed keygen ceremony by ID.
	router.HandleFunc("/tss/sign/start", ps.HandleTSSSignStart).Methods(http.MethodPost)
	router.HandleFunc("/tss/sign/message", ps.HandleTSSSignMessage).Methods(http.MethodPost)
	router.HandleFunc("/tss/sign/status", ps.HandleTSSSignStatus).Methods(http.MethodGet)

	// Health and info
	router.HandleFunc("/health", ps.HandleHealth).Methods(http.MethodGet)
	router.HandleFunc("/info", ps.HandleInfo).Methods(http.MethodGet)

	// Metrics
	router.Handle("/metrics", promhttp.Handler()).Methods(http.MethodGet)

	// Start server
	addr := ":" + getenv("PORT", "7000")
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	tlsConfig, mtlsEnabled, err := serverTLSConfigFromEnv()
	if err != nil {
		log.Fatalf("mTLS configuration error: %v", err)
	}
	if mtlsEnabled {
		// A separate plaintext listener carrying ONLY /health.
		//
		// Without it, turning mTLS on makes this pod permanently
		// unrunnable. The main port becomes HTTPS with
		// RequireAndVerifyClientCert, and a Kubernetes httpGet probe
		// speaks plaintext HTTP and presents no client certificate, so
		// every liveness probe fails the handshake ("client sent an HTTP
		// request to an HTTPS server") and kubelet kills the container --
		// forever. A security control that guarantees an outage does not
		// get switched on.
		//
		// Probing over HTTPS instead would not help: kubelet has no client
		// certificate to present, and RequireAndVerifyClientCert is the
		// property worth keeping.
		//
		// This listener exposes liveness only -- no ceremony endpoints, no
		// key material, no /info, no /metrics -- so it is not a way around
		// mTLS for anything that matters.
		go serveHealthPlaintext(ps, partyID)

		srv.TLSConfig = tlsConfig
		log.Printf("MPC party %d listening on %s (mTLS: client certs required)", partyID, addr)
		log.Fatal(srv.ListenAndServeTLS("", "")) // certs already loaded into TLSConfig
	}

	log.Printf("MPC party %d listening on %s (mTLS disabled: %s/%s/%s not all set)",
		partyID, addr, envMTLSCertFile, envMTLSKeyFile, envMTLSCAFile)
	log.Fatal(srv.ListenAndServe())
}

// envHealthPort names the plaintext health-only listener's port. Empty
// disables it; it is only started when mTLS is on, since without mTLS the
// main port already serves /health in plaintext.
const envHealthPort = "HEALTH_PORT"

// serveHealthPlaintext runs a listener whose only route is GET /health.
//
// See the call site for why this exists. Deliberately minimal: a separate
// mux rather than the main router, so no ceremony or introspection endpoint
// can ever be reachable without a client certificate by accident.
func serveHealthPlaintext(ps *PartyServer, partyID int) {
	port := getenv(envHealthPort, "")
	if port == "" {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ps.HandleHealth(w, r)
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("MPC party %d serving plaintext /health on :%s (probes only)", partyID, port)
	if err := srv.ListenAndServe(); err != nil {
		log.Printf("plaintext health listener stopped: %v", err)
	}
}
