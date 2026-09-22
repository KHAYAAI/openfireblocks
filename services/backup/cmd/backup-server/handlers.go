package main

import (
	"encoding/json"
	"net/http"

	backup "forge-crypto/backup"
)

type backupServer struct {
	manager *backup.BackupManager
	storage *backup.FilesystemBackupStorage
	dr      *backup.DisasterRecoveryCoordinator
}

func (s *backupServer) handleBackupFull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	meta, err := s.manager.ExecuteFullBackup(r.Context(), "local")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.storage.SaveMetadata(meta); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "backup succeeded but saving metadata failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (s *backupServer) handleBackupIncremental(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		PreviousBackupID string `json:"previous_backup_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PreviousBackupID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "previous_backup_id is required"})
		return
	}

	all, err := s.storage.ListBackups(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var previous *backup.BackupMetadata
	for i := range all {
		if all[i].ID == req.PreviousBackupID {
			previous = &all[i]
			break
		}
	}
	if previous == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "previous backup not found"})
		return
	}

	meta, err := s.manager.ExecuteIncrementalBackup(r.Context(), "local", previous)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := s.storage.SaveMetadata(meta); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "backup succeeded but saving metadata failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (s *backupServer) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		RestorePointID string `json:"restore_point_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RestorePointID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "restore_point_id is required"})
		return
	}

	points, err := s.manager.ListRestorePoints(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var target *backup.RestorePoint
	for i := range points {
		if points[i].ID == req.RestorePointID {
			target = &points[i]
			break
		}
	}
	if target == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "restore point not found"})
		return
	}

	if err := s.manager.RestoreFromPoint(r.Context(), *target); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored", "restore_point_id": req.RestorePointID})
}

func (s *backupServer) handleRestorePoints(w http.ResponseWriter, r *http.Request) {
	points, err := s.manager.ListRestorePoints(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, points)
}

func (s *backupServer) handleCreateDRPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var plan backup.DisasterRecoveryPlan
	if err := json.NewDecoder(r.Body).Decode(&plan); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := s.dr.CreateDRPlan(r.Context(), &plan); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, plan)
}

func (s *backupServer) handleTestDR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PlanID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "plan_id is required"})
		return
	}

	op, err := s.dr.TestDisasterRecovery(r.Context(), req.PlanID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// TestDisasterRecovery genuinely reports per-component failure for
	// postgres/vault/api-gateway/temporal promotion -- those steps aren't
	// wired to real infrastructure (see disaster_recovery.go's doc
	// comment on failoverPostgres etc.), so a caller should expect and
	// handle a non-"completed" status here, not treat it as a bug in this
	// handler.
	writeJSON(w, http.StatusOK, op)
}

func (s *backupServer) handleInitiateFailover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PlanID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "plan_id is required"})
		return
	}
	op, err := s.dr.InitiateFailover(r.Context(), req.PlanID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Runs async (see InitiateFailover's doc comment) -- poll
	// /dr/failover/status?operation_id=... for the real per-component
	// outcome, which will honestly report "failed" for postgres/vault/
	// api-gateway/temporal (none are wired to real infrastructure yet).
	writeJSON(w, http.StatusAccepted, op)
}

func (s *backupServer) handleFailoverStatus(w http.ResponseWriter, r *http.Request) {
	operationID := r.URL.Query().Get("operation_id")
	if operationID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "operation_id query param required"})
		return
	}
	op, err := s.dr.GetFailoverOperation(r.Context(), operationID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
