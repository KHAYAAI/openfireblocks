package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// KYCStatus represents the status of a KYC verification
type KYCStatus string

const (
	KYCStatusPending  KYCStatus = "pending"
	KYCStatusVerified KYCStatus = "verified"
	KYCStatusRejected KYCStatus = "rejected"
	KYCStatusExpired  KYCStatus = "expired"
)

// AMLStatus represents the status of an AML check
type AMLStatus string

const (
	AMLStatusClean     AMLStatus = "clean"
	AMLStatusFlagged   AMLStatus = "flagged"
	AMLStatusBlocked   AMLStatus = "blocked"
	AMLStatusUnchecked AMLStatus = "unchecked"
)

// KYCProfile represents a customer's KYC verification profile
type KYCProfile struct {
	CustomerID        string            `json:"customer_id"`
	FullName          string            `json:"full_name"`
	Email             string            `json:"email"`
	PhoneNumber       string            `json:"phone_number"`
	Country           string            `json:"country"`
	DocumentType      string            `json:"document_type"`
	DocumentID        string            `json:"document_id"`
	DocumentHash      string            `json:"document_hash"`
	DateOfBirth       string            `json:"date_of_birth"`
	Status            KYCStatus         `json:"status"`
	RiskLevel         string            `json:"risk_level"` // low, medium, high
	VerifiedAt        time.Time         `json:"verified_at"`
	ExpiresAt         time.Time         `json:"expires_at"`
	VerificationNotes string            `json:"verification_notes"`
	Metadata          map[string]string `json:"metadata"`
}

// AMLCheckResult represents the result of an AML check
type AMLCheckResult struct {
	CustomerID         string    `json:"customer_id"`
	Address            string    `json:"address"`
	Status             AMLStatus `json:"status"`
	MatchedLists       []string  `json:"matched_lists"` // e.g., ["OFAC_SDN", "EU_SANCTIONS"]
	RiskScore          float32   `json:"risk_score"`    // 0.0 - 1.0
	CheckedAt          time.Time `json:"checked_at"`
	ExpiresAt          time.Time `json:"expires_at"`
	InvestigationNotes string    `json:"investigation_notes"`
}

// KYCValidator provides KYC verification functionality
type KYCValidator struct {
	kvStore KVStore
}

// NewKYCValidator creates a new KYC validator
func NewKYCValidator(kvStore KVStore) *KYCValidator {
	return &KYCValidator{kvStore: kvStore}
}

// VerifyCustomer performs KYC verification for a customer
func (k *KYCValidator) VerifyCustomer(ctx context.Context, profile *KYCProfile) (*KYCProfile, error) {
	if err := k.validateProfileData(profile); err != nil {
		return nil, fmt.Errorf("invalid profile data: %w", err)
	}

	// Compute document hash
	docHash := sha256.Sum256([]byte(profile.DocumentID + profile.DocumentType))
	profile.DocumentHash = fmt.Sprintf("%x", docHash)

	// Set verification timestamps
	profile.Status = KYCStatusVerified
	profile.VerifiedAt = time.Now()
	profile.ExpiresAt = time.Now().AddDate(1, 0, 0) // Expire in 1 year
	profile.RiskLevel = k.assessRiskLevel(profile)

	// Store in KV store
	key := fmt.Sprintf("kyc:customer:%s", profile.CustomerID)
	if err := k.kvStore.Set(ctx, key, profile); err != nil {
		return nil, fmt.Errorf("failed to store KYC profile: %w", err)
	}

	return profile, nil
}

// GetKYCProfile retrieves a customer's KYC profile
func (k *KYCValidator) GetKYCProfile(ctx context.Context, customerID string) (*KYCProfile, error) {
	key := fmt.Sprintf("kyc:customer:%s", customerID)
	var profile KYCProfile
	if err := k.kvStore.Get(ctx, key, &profile); err != nil {
		return nil, fmt.Errorf("KYC profile not found: %w", err)
	}

	// Check if profile has expired
	if time.Now().After(profile.ExpiresAt) {
		profile.Status = KYCStatusExpired
	}

	return &profile, nil
}

// validateProfileData validates KYC profile data
func (k *KYCValidator) validateProfileData(profile *KYCProfile) error {
	if profile.CustomerID == "" {
		return fmt.Errorf("customer_id is required")
	}
	if profile.FullName == "" {
		return fmt.Errorf("full_name is required")
	}
	if !isValidEmail(profile.Email) {
		return fmt.Errorf("invalid email format")
	}
	if profile.Country == "" {
		return fmt.Errorf("country is required")
	}
	if profile.DocumentType == "" {
		return fmt.Errorf("document_type is required")
	}
	if profile.DocumentID == "" {
		return fmt.Errorf("document_id is required")
	}
	if profile.DateOfBirth == "" {
		return fmt.Errorf("date_of_birth is required")
	}

	// Validate age (must be 18+)
	dob, err := time.Parse("2006-01-02", profile.DateOfBirth)
	if err != nil {
		return fmt.Errorf("invalid date_of_birth format")
	}
	age := time.Since(dob).Hours() / 24 / 365.25
	if age < 18 {
		return fmt.Errorf("customer must be at least 18 years old")
	}

	return nil
}

// assessRiskLevel assesses the customer's risk level based on profile
func (k *KYCValidator) assessRiskLevel(profile *KYCProfile) string {
	// High-risk countries and document types
	highRiskCountries := map[string]bool{
		"KP": true, // North Korea
		"IR": true, // Iran
		"SY": true, // Syria
	}

	if highRiskCountries[profile.Country] {
		return "high"
	}

	// Risk assessment based on document type
	riskDocuments := map[string]bool{
		"passport":       false, // Low risk
		"national_id":    false,
		"driver_license": true, // Medium risk
	}

	if riskDocuments[strings.ToLower(profile.DocumentType)] {
		return "medium"
	}

	return "low"
}

// AMLChecker provides AML screening functionality
type AMLChecker struct {
	db           *PostgresDB
	kvStore      KVStore
	ofacClient   *OFACClient
	sanctionsTTL time.Duration
}

// NewAMLChecker creates a new AML checker
func NewAMLChecker(db *PostgresDB, kvStore KVStore, ofacClient *OFACClient) *AMLChecker {
	return &AMLChecker{
		db:           db,
		kvStore:      kvStore,
		ofacClient:   ofacClient,
		sanctionsTTL: 24 * time.Hour, // Cache sanctions lists for 24 hours
	}
}

// CheckAddress performs AML screening on a blockchain address
func (a *AMLChecker) CheckAddress(ctx context.Context, customerID, address, chainID string) (*AMLCheckResult, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("aml:check:%s:%s:%s", customerID, address, chainID)
	var cachedResult AMLCheckResult
	if err := a.kvStore.Get(ctx, cacheKey, &cachedResult); err == nil {
		if time.Now().Before(cachedResult.ExpiresAt) {
			return &cachedResult, nil
		}
	}

	// Perform AML check
	result := &AMLCheckResult{
		CustomerID: customerID,
		Address:    address,
		CheckedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(a.sanctionsTTL),
	}

	// Check against OFAC SDN list
	sdnMatches, err := a.ofacClient.CheckSDN(ctx, address)
	if err != nil {
		return nil, fmt.Errorf("OFAC SDN check failed: %w", err)
	}

	if len(sdnMatches) > 0 {
		result.Status = AMLStatusBlocked
		result.MatchedLists = append(result.MatchedLists, "OFAC_SDN")
		result.RiskScore = 1.0
		result.InvestigationNotes = fmt.Sprintf("Address matches %d OFAC SDN entries", len(sdnMatches))
	} else {
		result.Status = AMLStatusClean
		result.RiskScore = 0.0
	}

	// Store result in cache
	if err := a.kvStore.Set(ctx, cacheKey, result); err != nil {
		// Log error but don't fail the check
		fmt.Printf("Failed to cache AML result: %v\n", err)
	}

	// Also store in audit trail
	auditKey := fmt.Sprintf("aml:audit:%s:%d", customerID, time.Now().Unix())
	_ = a.kvStore.Set(ctx, auditKey, result)

	return result, nil
}

// CheckTransaction performs AML screening on a transaction
func (a *AMLChecker) CheckTransaction(ctx context.Context, customerID, fromAddress, toAddress, chainID string, amount float64) (*AMLCheckResult, error) {
	// Check recipient address for sanctions
	result, err := a.CheckAddress(ctx, customerID, toAddress, chainID)
	if err != nil {
		return nil, err
	}

	// Additional checks for suspicious patterns
	if amount > 100000 { // Large transaction threshold
		result.RiskScore += 0.1
	}

	// Structuring check (multiple small transactions)
	// Would typically check transaction history here
	suspiciousCount, err := a.countSuspiciousTransactions(ctx, customerID)
	if err == nil && suspiciousCount > 5 {
		result.RiskScore += 0.2
		if result.MatchedLists == nil {
			result.MatchedLists = []string{}
		}
		result.MatchedLists = append(result.MatchedLists, "SUSPICIOUS_PATTERN")
	}

	// Cap risk score at 1.0
	if result.RiskScore > 1.0 {
		result.RiskScore = 1.0
	}

	// Update status based on risk score
	if result.RiskScore > 0.7 {
		result.Status = AMLStatusBlocked
	} else if result.RiskScore > 0.3 {
		result.Status = AMLStatusFlagged
	}

	return result, nil
}

// suspiciousTransactionWindow is the structuring-detection lookback: many
// small transactions clustered in a short window is a classic attempt to
// stay under a per-transaction reporting threshold.
const suspiciousTransactionWindow = 24 * time.Hour

// countSuspiciousTransactions counts a customer's signing requests in the
// last suspiciousTransactionWindow, used by CheckTransaction as a structuring
// signal (many small transactions in a short window).
func (a *AMLChecker) countSuspiciousTransactions(ctx context.Context, customerID string) (int, error) {
	return a.db.CountRecentSigningRequests(ctx, customerID, time.Now().Add(-suspiciousTransactionWindow))
}

// OFACClient provides integration with OFAC sanctions lists
type OFACClient struct {
	baseURL string
	apiKey  string
	cache   map[string][]string
	kvStore KVStore
}

// NewOFACClient creates a new OFAC client
func NewOFACClient(baseURL, apiKey string, kvStore KVStore) *OFACClient {
	return &OFACClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		cache:   make(map[string][]string),
		kvStore: kvStore,
	}
}

// CheckSDN checks if an address matches OFAC SDN (Specially Designated Nationals) list.
//
// If no screening provider is configured (baseURL/apiKey empty), this
// returns an error rather than an empty match list. An empty result here
// means "not sanctioned" to every caller (see CheckAddress, which sets
// AMLStatusClean on a nil error with zero matches) -- silently returning
// that for every address just because no real provider is wired up would
// make every AML check pass by default, the opposite of fail-safe for a
// sanctions control. Wire in a real screening provider (e.g. Chainalysis,
// ComplyAdvantage, TRM Labs) before deploying this to handle real funds.
func (o *OFACClient) CheckSDN(ctx context.Context, address string) ([]string, error) {
	if o.baseURL == "" || o.apiKey == "" {
		return nil, fmt.Errorf("OFAC/sanctions screening not configured: no provider baseURL/apiKey set")
	}

	// Check cache first so repeated screens of the same address within the
	// TTL don't re-hit the (rate-limited, billed-per-call) provider.
	if matches, exists := o.cache[address]; exists {
		return matches, nil
	}

	// TODO: call the configured screening provider's API here. Left
	// unimplemented rather than guessing at a request/response shape for a
	// vendor this codebase hasn't picked yet -- a fabricated call against an
	// unverified contract would be exactly the kind of code that looks done
	// but silently isn't.
	return nil, fmt.Errorf("OFAC/sanctions screening provider configured but CheckSDN is not yet implemented")
}

// Utility functions

// isValidEmail validates an email address format
func isValidEmail(email string) bool {
	pattern := `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`
	re := regexp.MustCompile(pattern)
	return re.MatchString(email)
}

// KVStore defines the interface for key-value storage
type KVStore interface {
	Get(ctx context.Context, key string, value interface{}) error
	Set(ctx context.Context, key string, value interface{}) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}
