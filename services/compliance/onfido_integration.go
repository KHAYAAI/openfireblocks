package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type OnfidoService struct {
	apiKey     string
	httpClient *http.Client
	db         *PostgresDB
}

type OnfidoApplicant struct {
	ID                 string   `json:"id"`
	CreatedAt          string   `json:"created_at"`
	Email              string   `json:"email"`
	FirstName          string   `json:"first_name"`
	LastName           string   `json:"last_name"`
	Address            *Address `json:"address"`
	CountryOfBirth     string   `json:"country_of_birth"`
	DateOfBirth        string   `json:"date_of_birth"`
	VerificationStatus string   `json:"verification_status"`
}

type Address struct {
	BuildingNumber string `json:"building_number"`
	Street         string `json:"street"`
	Town           string `json:"town"`
	PostalCode     string `json:"postal_code"`
	Country        string `json:"country"`
	Line1          string `json:"line1"`
	Line2          string `json:"line2"`
}

type OnfidoDocument struct {
	ID           string       `json:"id"`
	CreatedAt    string       `json:"created_at"`
	Type         string       `json:"type"` // passport, driving_licence, national_identity_card, visa
	SubType      string       `json:"subtype,omitempty"`
	Front        FileUpload   `json:"front,omitempty"`
	Back         FileUpload   `json:"back,omitempty"`
	PageMetadata PageMetadata `json:"page_metadata,omitempty"`
}

type FileUpload struct {
	DownloadHref string `json:"download_href"`
	FileType     string `json:"file_type"`
	FileSize     int64  `json:"file_size"`
	UploadedAt   string `json:"uploaded_at"`
}

type PageMetadata struct {
	PageNumber int `json:"page_number"`
}

type OnfidoLiveness struct {
	ID         string `json:"id"`
	CreatedAt  string `json:"created_at"`
	Status     string `json:"status"` // pending, complete
	GrantID    string `json:"grant_id"`
	DocumentID string `json:"document_id"`
}

type OnfidoCheck struct {
	ID              string         `json:"id"`
	CreatedAt       string         `json:"created_at"`
	ApplicantID     string         `json:"applicant_id"`
	Status          string         `json:"status"` // pending, in_progress, complete
	Result          string         `json:"result"` // clear, consider, unknown
	BuilderOutputID string         `json:"builder_output_id"`
	CheckType       string         `json:"check_type"`
	Reports         []OnfidoReport `json:"reports"`
}

type OnfidoReport struct {
	ID         string                 `json:"id"`
	CreatedAt  string                 `json:"created_at"`
	Name       string                 `json:"name"`
	Status     string                 `json:"status"` // pending, in_progress, complete
	Result     string                 `json:"result"` // clear, consider, unknown
	Breakdown  map[string]interface{} `json:"breakdown"`
	Properties map[string]interface{} `json:"properties"`
}

type OnfidoWebhook struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Enabled   bool   `json:"enabled"`
	Href      string `json:"href"`
	Payload   string `json:"payload"`
}

type OnfidoKYCVerification struct {
	VerificationID    string                 `json:"verification_id"`
	CustomerID        string                 `json:"customer_id"`
	OnfidoApplicantID string                 `json:"onfido_applicant_id"`
	OnfidoCheckID     string                 `json:"onfido_check_id"`
	Status            string                 `json:"status"`             // pending, verified, rejected, need_review
	VerificationLevel string                 `json:"verification_level"` // individual, business, institutional
	DocumentType      string                 `json:"document_type"`
	DocumentStatus    string                 `json:"document_status"`
	LivenessStatus    string                 `json:"liveness_status"`
	RiskAssessment    string                 `json:"risk_assessment"` // clear, consider, unknown
	CheckResult       string                 `json:"check_result"`
	VerifiedAt        *time.Time             `json:"verified_at"`
	ExpiresAt         *time.Time             `json:"expires_at"`
	Metadata          map[string]interface{} `json:"metadata"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

func NewOnfidoService(apiKey string, db *PostgresDB) *OnfidoService {
	return &OnfidoService{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		db: db,
	}
}

// CreateApplicant creates a new applicant in Onfido
func (s *OnfidoService) CreateApplicant(ctx context.Context, email, firstName, lastName string, address *Address) (*OnfidoApplicant, error) {
	payload := map[string]interface{}{
		"email":      email,
		"first_name": firstName,
		"last_name":  lastName,
	}

	if address != nil {
		payload["address"] = address
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.onfido.com/v3.5/applicants", io.NopCloser(io.Reader(bytes.NewReader(body))))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Token token=%s", s.apiKey))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create applicant: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Onfido error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var applicant OnfidoApplicant
	json.NewDecoder(resp.Body).Decode(&applicant)
	return &applicant, nil
}

// GetApplicant retrieves applicant details
func (s *OnfidoService) GetApplicant(ctx context.Context, applicantID string) (*OnfidoApplicant, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.onfido.com/v3.5/applicants/%s", applicantID), nil)
	req.Header.Set("Authorization", fmt.Sprintf("Token token=%s", s.apiKey))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get applicant: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Onfido error: %d", resp.StatusCode)
	}

	var applicant OnfidoApplicant
	json.NewDecoder(resp.Body).Decode(&applicant)
	return &applicant, nil
}

// GenerateSDKToken generates SDK token for document upload
func (s *OnfidoService) GenerateSDKToken(ctx context.Context, applicantID string) (string, error) {
	payload := map[string]interface{}{
		"applicant_id": applicantID,
		"referrer":     "https://app.openfireblocks.io",
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.onfido.com/v3.5/sdk_token", io.NopCloser(io.Reader(bytes.NewReader(body))))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Token token=%s", s.apiKey))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to generate SDK token: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Token string `json:"token"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Token, nil
}

// CreateCheck initiates a new verification check
func (s *OnfidoService) CreateCheck(ctx context.Context, applicantID string, checkType string) (*OnfidoCheck, error) {
	// Check type: identity_enhanced, document, facial_similarity_photo, liveness_photo, etc.
	payload := map[string]interface{}{
		"applicant_id": applicantID,
		"check_type":   checkType,
		"reports": []map[string]string{
			{"name": "identity_enhanced"},
			{"name": "document"},
		},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.onfido.com/v3.5/checks", io.NopCloser(io.Reader(bytes.NewReader(body))))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Token token=%s", s.apiKey))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create check: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Onfido error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var check OnfidoCheck
	json.NewDecoder(resp.Body).Decode(&check)
	return &check, nil
}

// GetCheck retrieves check details
func (s *OnfidoService) GetCheck(ctx context.Context, checkID string) (*OnfidoCheck, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.onfido.com/v3.5/checks/%s", checkID), nil)
	req.Header.Set("Authorization", fmt.Sprintf("Token token=%s", s.apiKey))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get check: %w", err)
	}
	defer resp.Body.Close()

	var check OnfidoCheck
	json.NewDecoder(resp.Body).Decode(&check)
	return &check, nil
}

// ProcessCheckResult processes Onfido check webhook
func (s *OnfidoService) ProcessCheckResult(ctx context.Context, check *OnfidoCheck, customerID string) (*OnfidoKYCVerification, error) {
	verificationID := uuid.New().String()

	// Determine verification status
	status := "need_review"
	if check.Result == "clear" {
		status = "verified"
	} else if check.Result == "consider" {
		status = "need_review"
	}

	now := time.Now()
	expiresAt := now.AddDate(1, 0, 0) // Expires in 1 year

	verification := &OnfidoKYCVerification{
		VerificationID:    verificationID,
		CustomerID:        customerID,
		OnfidoApplicantID: check.ApplicantID,
		OnfidoCheckID:     check.ID,
		Status:            status,
		CheckResult:       check.Result,
		CreatedAt:         now,
		UpdatedAt:         now,
		ExpiresAt:         &expiresAt,
	}

	// Parse reports
	for _, report := range check.Reports {
		if report.Name == "identity_enhanced" {
			if report.Result == "clear" {
				verification.DocumentStatus = "verified"
			}
		}
		if report.Name == "facial_similarity_photo" {
			if report.Result == "clear" {
				verification.LivenessStatus = "verified"
			}
		}
	}

	// Store in database
	if err := s.db.CreateOnfidoVerification(ctx, verification); err != nil {
		return nil, fmt.Errorf("failed to store verification: %w", err)
	}

	// Update the customer's denormalized current KYC status. The
	// verification record above (CreateOnfidoVerification) is the durable
	// source of truth and already succeeded, so a failure here is logged
	// rather than failing the whole webhook -- the underlying event is safe.
	var updateErr error
	if status == "verified" {
		updateErr = s.db.UpdateCustomerKYCStatus(ctx, customerID, "approved", now)
	} else if status == "need_review" {
		updateErr = s.db.UpdateCustomerKYCStatus(ctx, customerID, "under_review", time.Time{})
	}
	if updateErr != nil {
		log.Printf("failed to update customer %s KYC status: %v", customerID, updateErr)
	}

	return verification, nil
}

// RegisterWebhook registers webhook for Onfido events
func (s *OnfidoService) RegisterWebhook(ctx context.Context, webhookURL string) (*OnfidoWebhook, error) {
	payload := map[string]interface{}{
		"url":     webhookURL,
		"enabled": true,
		"payload": "full",
		"events":  []string{"check.completed", "report.completed"},
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.onfido.com/v3.5/webhooks", io.NopCloser(io.Reader(bytes.NewReader(body))))

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Token token=%s", s.apiKey))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to register webhook: %w", err)
	}
	defer resp.Body.Close()

	var webhook OnfidoWebhook
	json.NewDecoder(resp.Body).Decode(&webhook)
	return &webhook, nil
}

// HTTP Handlers

func (s *OnfidoService) HandleKYCStart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		FirstName string   `json:"first_name"`
		LastName  string   `json:"last_name"`
		Email     string   `json:"email"`
		Address   *Address `json:"address"`
	}

	json.NewDecoder(r.Body).Decode(&req)

	// Create Onfido applicant
	applicant, err := s.CreateApplicant(ctx, req.Email, req.FirstName, req.LastName, req.Address)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create applicant: %v", err), http.StatusInternalServerError)
		return
	}

	// Generate SDK token for document upload
	sdkToken, err := s.GenerateSDKToken(ctx, applicant.ID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate token: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"applicant_id": applicant.ID,
		"sdk_token":    sdkToken,
	})
}

func (s *OnfidoService) HandleKYCStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customerID := r.Header.Get("X-Customer-ID")
	if customerID == "" {
		http.Error(w, "X-Customer-ID header required", http.StatusBadRequest)
		return
	}

	verification, err := s.db.GetLatestOnfidoVerification(ctx, customerID)
	if err != nil {
		http.Error(w, "No verification found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(verification)
}
