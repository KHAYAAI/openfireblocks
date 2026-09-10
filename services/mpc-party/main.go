package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// PartyServer represents an MPC DKG party service.
type PartyServer struct {
	partyID    int
	config     *PartyConfig
	tssManager *TSSPartyManager // real network-driven tss-lib DKG, see tss_party.go
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
	return &PartyServer{partyID: partyID}
}

// HandleHealth returns the party's health status.
//
// Liveness only, and deliberately not a function of ceremony state: a party
// whose last ceremony failed is still perfectly able to take part in the
// next one, and reporting it unhealthy would have kubelet restart a process
// that is working. It used to read the legacy DKG state for exactly that
// wrong answer.
func (ps *PartyServer) HandleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "healthy",
		"partyId": fmt.Sprintf("%d", ps.partyID),
	})
}

// HandleInfo returns information about the party.
func (ps *PartyServer) HandleInfo(w http.ResponseWriter, r *http.Request) {
	info := map[string]interface{}{
		"partyId": ps.partyID,
	}
	if ps.tssManager != nil {
		keygens, signings := ps.tssManager.CeremonyCounts()
		info["keygenCeremonies"] = keygens
		info["signingCeremonies"] = signings
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

	log.Printf("MPC party %d initialized", partyID)

	// Setup HTTP routes
	router := mux.NewRouter()

	// Real, network-driven tss-lib DKG -- see tss_party.go.
	//
	// This used to sit alongside a second set of endpoints (/round/*, /sign)
	// backed by TSSWrapper, a stand-in that produced signature-shaped output
	// with no cryptography behind it. Nothing drove them -- the workflows
	// call these -- but they were served on the same port by the same
	// binary, so anything that could reach a party could obtain a
	// "signature" that meant nothing. A placeholder that is merely unused is
	// still reachable; deleted rather than documented.
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
