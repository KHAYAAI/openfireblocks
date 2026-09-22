package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// KYCAMLService handles KYC/AML verification for customers and transactions.
// Verifications are persisted through db, not held in memory -- an in-memory
// map here would silently diverge from the database and wouldn't survive a
// restart or be visible to any other instance of this service.
type KYCAMLService struct {
	db           *PostgresDB
	thirdparties map[string]ThirdPartyProvider
}

// ThirdPartyProvider is an interface for KYC/AML service providers.
type ThirdPartyProvider interface {
	VerifyCustomer(ctx context.Context, req *CustomerVerificationRequest) (*VerificationResult, error)
	VerifyTransaction(ctx context.Context, req *TransactionVerificationRequest) (*RiskAssessment, error)
	GetStatus(ctx context.Context, customerID string) (*ProviderStatus, error)
}

// KYCVerification represents a customer verification record.
type KYCVerification struct {
	VerificationID string                 `json:"verification_id"`
	CustomerID     string                 `json:"customer_id"`
	Status         string                 `json:"status"` // pending, approved, rejected, expired
	RiskLevel      string                 `json:"risk_level"`
	Provider       string                 `json:"provider"`
	VerifiedAt     *time.Time             `json:"verified_at,omitempty"`
	ExpiresAt      time.Time              `json:"expires_at"`
	Details        map[string]interface{} `json:"details,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
}

// CustomerVerificationRequest is the request for customer KYC verification.
type CustomerVerificationRequest struct {
	CustomerID  string `json:"customer_id"`
	FullName    string `json:"full_name"`
	Email       string `json:"email"`
	PhoneNumber string `json:"phone_number"`
	Country     string `json:"country"`
	IDDocType   string `json:"id_doc_type"` // passport, driver_license, national_id
	IDDocNumber string `json:"id_doc_number"`
	DateOfBirth string `json:"date_of_birth"` // YYYY-MM-DD
	Address     string `json:"address"`
	City        string `json:"city"`
	PostalCode  string `json:"postal_code"`
}

// VerificationResult is the result of customer verification.
type VerificationResult struct {
	Status       string                 `json:"status"` // approved, rejected, pending_review
	RiskLevel    string                 `json:"risk_level"`
	Confidence   float64                `json:"confidence"` // 0.0-1.0
	Details      map[string]interface{} `json:"details,omitempty"`
	Restrictions []string               `json:"restrictions,omitempty"`
	VerifiedAt   time.Time              `json:"verified_at"`
}

// TransactionVerificationRequest is the request for transaction verification.
type TransactionVerificationRequest struct {
	TransactionID   string `json:"transaction_id"`
	CustomerID      string `json:"customer_id"`
	Amount          string `json:"amount"` // in base units
	Currency        string `json:"currency"`
	FromAddress     string `json:"from_address"`
	ToAddress       string `json:"to_address"`
	Blockchain      string `json:"blockchain"`
	TransactionType string `json:"transaction_type"` // transfer, withdrawal, deposit
	Country         string `json:"country"`
}

// RiskAssessment represents the risk level of a transaction.
type RiskAssessment struct {
	TransactionID    string                 `json:"transaction_id"`
	RiskScore        float64                `json:"risk_score"` // 0.0-100.0
	RiskLevel        string                 `json:"risk_level"` // low, medium, high, critical
	Reasons          []string               `json:"reasons,omitempty"`
	RequiresApproval bool                   `json:"requires_approval"`
	Details          map[string]interface{} `json:"details,omitempty"`
	AssessedAt       time.Time              `json:"assessed_at"`
}

// ProviderStatus represents the status from a third-party provider.
type ProviderStatus struct {
	Provider  string    `json:"provider"`
	Status    string    `json:"status"`
	LastCheck time.Time `json:"last_check"`
	Latency   int       `json:"latency_ms"`
}

// NewKYCAMLService creates a new KYC/AML service.
func NewKYCAMLService(db *PostgresDB) *KYCAMLService {
	return &KYCAMLService{
		db:           db,
		thirdparties: make(map[string]ThirdPartyProvider),
	}
}

// RegisterProvider registers a third-party KYC/AML provider.
func (k *KYCAMLService) RegisterProvider(name string, provider ThirdPartyProvider) {
	k.thirdparties[name] = provider
	log.Printf("Registered KYC/AML provider: %s", name)
}

// VerifyCustomer verifies a customer through configured providers.
func (k *KYCAMLService) VerifyCustomer(ctx context.Context, req *CustomerVerificationRequest) (*KYCVerification, error) {
	verificationID := uuid.New().String()

	// Start verification with primary provider (assume first registered)
	var primaryProvider ThirdPartyProvider
	for _, p := range k.thirdparties {
		primaryProvider = p
		break
	}

	if primaryProvider == nil {
		return nil, fmt.Errorf("no KYC/AML provider configured")
	}

	// Verify customer
	result, err := primaryProvider.VerifyCustomer(ctx, req)
	if err != nil {
		log.Printf("KYC verification failed: %v", err)
		return nil, fmt.Errorf("verification failed: %w", err)
	}

	// Store verification result
	verification := &KYCVerification{
		VerificationID: verificationID,
		CustomerID:     req.CustomerID,
		Status:         result.Status,
		RiskLevel:      result.RiskLevel,
		Provider:       "primary",
		ExpiresAt:      time.Now().AddDate(1, 0, 0), // 1 year expiry
		Details:        result.Details,
		CreatedAt:      time.Now(),
	}

	if result.Status == "approved" {
		now := time.Now()
		verification.VerifiedAt = &now
	}

	// A verification that reports "approved" to the caller but fails to
	// persist would look unverified again on the next lookup or from any
	// other instance of this service -- for a compliance record, a failed
	// write must fail the whole call, not just log and continue.
	if err := k.db.StoreKYCVerification(ctx, verification); err != nil {
		return nil, fmt.Errorf("verification succeeded but failed to persist: %w", err)
	}

	return verification, nil
}

// GetVerificationStatus retrieves the current verification status.
func (k *KYCAMLService) GetVerificationStatus(ctx context.Context, customerID string) (*KYCVerification, error) {
	verification, err := k.db.GetLatestKYCVerification(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get verification: %w", err)
	}

	if verification != nil && time.Now().After(verification.ExpiresAt) {
		verification.Status = "expired"
	}

	return verification, nil
}

// VerifyTransaction assesses the risk level of a transaction.
func (k *KYCAMLService) VerifyTransaction(ctx context.Context, req *TransactionVerificationRequest) (*RiskAssessment, error) {
	// Get customer verification status first
	customerVerif, err := k.GetVerificationStatus(ctx, req.CustomerID)
	if err != nil {
		log.Printf("Failed to get customer verification: %v", err)
		return nil, fmt.Errorf("customer verification required")
	}

	if customerVerif == nil || customerVerif.Status != "approved" {
		return &RiskAssessment{
			TransactionID:    req.TransactionID,
			RiskScore:        100.0,
			RiskLevel:        "critical",
			Reasons:          []string{"Customer not verified"},
			RequiresApproval: true,
			AssessedAt:       time.Now(),
		}, nil
	}

	// Get provider for transaction verification
	var txProvider ThirdPartyProvider
	for _, p := range k.thirdparties {
		txProvider = p
		break
	}

	if txProvider == nil {
		return nil, fmt.Errorf("no transaction verification provider configured")
	}

	// Assess transaction risk
	assessment, err := txProvider.VerifyTransaction(ctx, req)
	if err != nil {
		log.Printf("Transaction verification failed: %v", err)
		return nil, fmt.Errorf("transaction verification failed: %w", err)
	}

	// Store assessment in database
	if err := k.db.StoreRiskAssessment(ctx, req.CustomerID, assessment); err != nil {
		log.Printf("Failed to store risk assessment: %v", err)
	}

	return assessment, nil
}

// ListRestrictedCountries returns a list of restricted/sanctioned countries.
func (k *KYCAMLService) ListRestrictedCountries(ctx context.Context) ([]string, error) {
	// Typically loaded from external compliance databases
	// This is a placeholder
	return []string{
		"KP", // North Korea
		"IR", // Iran
		"SY", // Syria
		"CU", // Cuba
	}, nil
}

// CheckSanctionsList checks if an address is on a sanctions list.
//
// No sanctions data source is wired up in this service (that lives in
// AMLChecker/OFACClient in aml_kyc.go, which has the same limitation --
// see the comment there). This deliberately returns an error rather than
// (true/false, nil): a stub that silently answered "false" would tell every
// caller "not sanctioned" for every address, which is a false negative on a
// control this platform's compliance posture depends on. Callers must treat
// an error here as "screening unavailable" and block, not proceed.
func (k *KYCAMLService) CheckSanctionsList(ctx context.Context, address, blockchain string) (bool, error) {
	return false, fmt.Errorf("sanctions screening not configured: no OFAC/EU/UN list provider is wired up for KYCAMLService")
}

// HandleKYCVerification is the HTTP handler for customer verification.
func (k *KYCAMLService) HandleKYCVerification(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CustomerVerificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	verification, err := k.VerifyCustomer(ctx, &req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Verification failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(verification)
}

// HandleTransactionVerification is the HTTP handler for transaction risk assessment.
func (k *KYCAMLService) HandleTransactionVerification(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TransactionVerificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	assessment, err := k.VerifyTransaction(ctx, &req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Assessment failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(assessment)
}

// HandleVerificationStatus is the HTTP handler to get verification status.
func (k *KYCAMLService) HandleVerificationStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID := r.URL.Query().Get("customer_id")

	if customerID == "" {
		http.Error(w, "customer_id required", http.StatusBadRequest)
		return
	}

	verification, err := k.GetVerificationStatus(ctx, customerID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get status: %v", err), http.StatusInternalServerError)
		return
	}

	if verification == nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(verification)
}
