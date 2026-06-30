package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// main.go wires the MPC signer's HTTP API.
//
// Endpoints:
//   POST /sign    -> sign an Ethereum transaction, audit-log the lifecycle
//   GET  /address -> return the shared signer address (useful for funding on testnet)
//   GET  /health  -> liveness/readiness probe
//
// The audit logger is best-effort: if immudb is unreachable at startup the
// service still boots and serves /sign, recording a warning per request. This
// keeps the Phase 0 happy path working even when immudb is slow to come up.

type server struct {
	signer *MPCSigner
	audit  *AuditLogger // may be nil if immudb was unavailable at startup
}

func (s *server) log(ctx context.Context, event AuditEvent) uint64 {
	if s.audit == nil {
		auditWriteTotal.WithLabelValues("skipped").Inc()
		return 0
	}
	id, err := s.audit.LogEvent(ctx, event)
	if err != nil {
		auditWriteTotal.WithLabelValues("failed").Inc()
		log.Printf("warning: audit log failed for %s (%s): %v", event.RequestID, event.Type, err)
		return 0
	}
	auditWriteTotal.WithLabelValues("success").Inc()
	return id
}

func (s *server) handleSign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	requestID := uuid.NewString()

	timer := prometheus.NewTimer(signDuration)
	defer timer.ObserveDuration()

	var req SignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		signTotal.WithLabelValues("invalid").Inc()
		s.log(ctx, AuditEvent{Type: "SIGN_REQUEST_INVALID", RequestID: requestID, Message: err.Error(), Status: "failed"})
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body", RequestID: requestID})
		return
	}

	s.log(ctx, AuditEvent{
		Type:      "SIGN_REQUEST_RECEIVED",
		RequestID: requestID,
		Message:   fmt.Sprintf("to=%s value=%s nonce=%d data_len=%d", req.To, req.Value, req.Nonce, len(req.Data)),
		Status:    "pending",
	})

	signed, err := s.signer.SignTransaction(ctx, &req)
	if err != nil {
		signTotal.WithLabelValues("failed").Inc()
		s.log(ctx, AuditEvent{Type: "SIGN_FAILED", RequestID: requestID, Message: err.Error(), Status: "failed"})
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: err.Error(), RequestID: requestID})
		return
	}
	signTotal.WithLabelValues("success").Inc()

	auditID := s.log(ctx, AuditEvent{
		Type:      "SIGN_SUCCESS",
		RequestID: requestID,
		Signature: signed.Signature,
		Hash:      signed.Hash,
		From:      signed.From,
		Status:    "signed",
	})

	writeJSON(w, http.StatusOK, SignResponse{
		RequestID:  requestID,
		SignedTx:   signed.RawTx,
		TxHash:     signed.Hash,
		From:       signed.From,
		Status:     "signed",
		AuditLogID: auditID,
	})
}

func (s *server) handleAddress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"address": s.signer.Address()})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	// Resolve the signing key from Vault (preferred), env, or generate ephemeral.
	keyHex, err := ResolveSigningKey(context.Background(), os.Getenv)
	if err != nil {
		log.Fatalf("failed to resolve signing key: %v", err)
	}

	signer, err := NewMPCSigner(keyHex)
	if err != nil {
		log.Fatalf("failed to init MPC signer: %v", err)
	}
	log.Printf("MPC signer address: %s", signer.Address())

	// immudb is best-effort at startup so a slow ledger doesn't block signing.
	var audit *AuditLogger
	immudbURL := getenv("IMMUDB_URL", "localhost:3322")
	audit, err = NewAuditLogger(immudbURL, os.Getenv("IMMUDB_USER"), os.Getenv("IMMUDB_PASSWORD"))
	if err != nil {
		log.Printf("warning: immudb audit logger unavailable (%v); continuing without immutable ledger", err)
		audit = nil
	} else {
		defer audit.Close()
		log.Printf("connected to immudb at %s", immudbURL)
	}

	s := &server{signer: signer, audit: audit}

	router := mux.NewRouter()
	router.HandleFunc("/sign", s.handleSign).Methods(http.MethodPost)
	router.HandleFunc("/address", s.handleAddress).Methods(http.MethodGet)
	router.HandleFunc("/health", handleHealth).Methods(http.MethodGet)
	router.Handle("/metrics", promhttp.Handler()).Methods(http.MethodGet)

	addr := ":" + getenv("PORT", "8080")
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("MPC signer listening on %s", addr)
	log.Fatal(srv.ListenAndServe())
}
