package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// PolicyService manages signing policies and controls
type PolicyService struct {
	db *PostgresDB
}

// Policy defines signing restrictions and controls
type Policy struct {
	PolicyID    string           `json:"policy_id"`
	KeyID       string           `json:"key_id"`
	CustomerID  string           `json:"customer_id"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Status      string           `json:"status"` // active, inactive, archived
	Rules       []*PolicyRule    `json:"rules"`
	Approvals   *ApprovalConfig  `json:"approvals"`
	RateLimit   *RateLimitConfig `json:"rate_limit,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

// PolicyRule defines individual signing rules
type PolicyRule struct {
	RuleID      string     `json:"rule_id"`
	Type        string     `json:"type"` // amount_limit, whitelist, time_based, frequency
	Description string     `json:"description"`
	Enabled     bool       `json:"enabled"`
	Config      RuleConfig `json:"config"`
}

// RuleConfig is the configuration for a specific rule type
type RuleConfig struct {
	MaxAmount          *string  `json:"max_amount,omitempty"`           // For amount_limit
	WhitelistAddresses []string `json:"whitelist_addresses,omitempty"`  // For whitelist
	AllowedHours       []int    `json:"allowed_hours,omitempty"`        // For time_based (0-23)
	MaxRequestsPerDay  int      `json:"max_requests_per_day,omitempty"` // For frequency
	Blockchains        []string `json:"blockchains,omitempty"`          // For chain restrictions
}

// ApprovalConfig defines multi-approval requirements
type ApprovalConfig struct {
	Required       int               `json:"required"`
	Approvers      []*PolicyApprover `json:"approvers"`
	TimeoutMinutes int               `json:"timeout_minutes"`
}

// PolicyApprover is an authorized approver
type PolicyApprover struct {
	ApproverID string     `json:"approver_id"`
	Email      string     `json:"email"`
	Role       string     `json:"role"`
	Approved   bool       `json:"approved,omitempty"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
}

// RateLimitConfig defines rate limiting
type RateLimitConfig struct {
	MaxPerHour  int `json:"max_per_hour"`
	MaxPerDay   int `json:"max_per_day"`
	MaxPerMonth int `json:"max_per_month"`
}

// CreatePolicyRequest is the request to create a policy
type CreatePolicyRequest struct {
	KeyID       string           `json:"key_id"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Rules       []*PolicyRule    `json:"rules"`
	Approvals   *ApprovalConfig  `json:"approvals,omitempty"`
	RateLimit   *RateLimitConfig `json:"rate_limit,omitempty"`
}

// PolicyEvaluationRequest is used to evaluate a signing request against policies
type PolicyEvaluationRequest struct {
	KeyID              string     `json:"key_id"`
	DestinationAddress string     `json:"destination_address"`
	Amount             string     `json:"amount"`
	Blockchain         string     `json:"blockchain"`
	Timestamp          *time.Time `json:"timestamp,omitempty"`
}

// PolicyEvaluationResult contains the result of policy evaluation
type PolicyEvaluationResult struct {
	Allowed          bool            `json:"allowed"`
	RequiresApproval bool            `json:"requires_approval"`
	ApprovalConfig   *ApprovalConfig `json:"approval_config,omitempty"`
	ViolatedRules    []string        `json:"violated_rules,omitempty"`
	Reason           string          `json:"reason,omitempty"`
}

// NewPolicyService creates a new policy service
func NewPolicyService(db *PostgresDB) *PolicyService {
	return &PolicyService{
		db: db,
	}
}

// CreatePolicy creates a new signing policy
func (s *PolicyService) CreatePolicy(ctx context.Context, req *CreatePolicyRequest) (*Policy, error) {
	// Validate request
	if req.KeyID == "" || req.Name == "" {
		return nil, fmt.Errorf("key_id and name are required")
	}

	// Create policy
	policy := &Policy{
		PolicyID:    uuid.New().String(),
		KeyID:       req.KeyID,
		Name:        req.Name,
		Description: req.Description,
		Status:      "active",
		Rules:       req.Rules,
		Approvals:   req.Approvals,
		RateLimit:   req.RateLimit,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// Store policy
	if err := s.db.CreatePolicy(ctx, policy); err != nil {
		log.Printf("Failed to store policy: %v", err)
		return nil, fmt.Errorf("failed to create policy: %w", err)
	}

	log.Printf("Policy created: %s for key: %s", policy.PolicyID, req.KeyID)

	return policy, nil
}

// GetPolicy retrieves a policy by ID
func (s *PolicyService) GetPolicy(ctx context.Context, policyID string) (*Policy, error) {
	return s.db.GetPolicy(ctx, policyID)
}

// ListPoliciesByKey lists policies for a specific key
func (s *PolicyService) ListPoliciesByKey(ctx context.Context, keyID string) ([]*Policy, error) {
	return s.db.ListPoliciesByKey(ctx, keyID)
}

// UpdatePolicy updates an existing policy
func (s *PolicyService) UpdatePolicy(ctx context.Context, policyID string, updates *Policy) (*Policy, error) {
	policy, err := s.db.GetPolicy(ctx, policyID)
	if err != nil {
		return nil, fmt.Errorf("policy not found")
	}

	// Update fields
	if updates.Name != "" {
		policy.Name = updates.Name
	}
	if updates.Description != "" {
		policy.Description = updates.Description
	}
	if updates.Rules != nil {
		policy.Rules = updates.Rules
	}
	if updates.Approvals != nil {
		policy.Approvals = updates.Approvals
	}
	if updates.RateLimit != nil {
		policy.RateLimit = updates.RateLimit
	}

	policy.UpdatedAt = time.Now()

	// Store updates
	if err := s.db.UpdatePolicy(ctx, policy); err != nil {
		log.Printf("Failed to update policy: %v", err)
		return nil, fmt.Errorf("failed to update policy: %w", err)
	}

	return policy, nil
}

// EvaluatePolicy evaluates a signing request against all policies for a key
func (s *PolicyService) EvaluatePolicy(ctx context.Context, eval *PolicyEvaluationRequest) (*PolicyEvaluationResult, error) {
	// A database error here is not the same thing as "no policies
	// configured" -- the previous version treated them identically and
	// defaulted to Allowed: true on either, which means a transient DB
	// outage would silently disable every signing policy platform-wide
	// (fail-open on a security control) instead of blocking signing until
	// the policy check can actually run (fail-closed).
	policies, err := s.ListPoliciesByKey(ctx, eval.KeyID)
	if err != nil {
		log.Printf("Failed to get policies: %v", err)
		return nil, fmt.Errorf("policy evaluation unavailable: %w", err)
	}

	if len(policies) == 0 {
		return &PolicyEvaluationResult{Allowed: true}, nil
	}

	result := &PolicyEvaluationResult{
		Allowed:       true,
		ViolatedRules: []string{},
	}

	for _, policy := range policies {
		if policy.Status != "active" {
			continue
		}

		// Check each rule in the policy
		for _, rule := range policy.Rules {
			if !rule.Enabled {
				continue
			}

			if !s.evaluateRule(rule, eval) {
				result.Allowed = false
				result.ViolatedRules = append(result.ViolatedRules, rule.Description)
			}
		}

		// Check approval requirements
		if policy.Approvals != nil && policy.Approvals.Required > 0 {
			result.RequiresApproval = true
			result.ApprovalConfig = policy.Approvals
		}
	}

	if !result.Allowed {
		result.Reason = fmt.Sprintf("Signing blocked by policy rules: %v", result.ViolatedRules)
	}

	return result, nil
}

// evaluateRule checks if a signing request violates a specific rule
func (s *PolicyService) evaluateRule(rule *PolicyRule, eval *PolicyEvaluationRequest) bool {
	switch rule.Type {
	case "amount_limit":
		return s.checkAmountLimit(rule.Config, eval)
	case "whitelist":
		return s.checkWhitelist(rule.Config, eval)
	case "time_based":
		return s.checkTimeWindow(rule.Config)
	case "blockchain":
		return s.checkBlockchain(rule.Config, eval)
	default:
		return true
	}
}

// checkAmountLimit verifies amount against limit.
//
// Amounts are decimal strings (e.g. base-unit token amounts, which can
// exceed int64/float64 precision), so this compares them as big.Float
// rather than as strings. A lexicographic string comparison would have
// made "1000" <= "500" true (since '1' < '5'), letting a transaction twice
// the limit through as "under" it -- a real bypass on the one rule type
// whose entire purpose is enforcing a numeric ceiling.
func (s *PolicyService) checkAmountLimit(cfg RuleConfig, eval *PolicyEvaluationRequest) bool {
	if cfg.MaxAmount == nil {
		return true
	}

	amount, ok := new(big.Float).SetString(eval.Amount)
	if !ok {
		// Can't parse the amount at all: fail closed rather than silently
		// treat an unparseable value as passing the limit check.
		return false
	}
	max, ok := new(big.Float).SetString(*cfg.MaxAmount)
	if !ok {
		return false
	}

	return amount.Cmp(max) <= 0
}

// checkWhitelist verifies destination is in whitelist
func (s *PolicyService) checkWhitelist(cfg RuleConfig, eval *PolicyEvaluationRequest) bool {
	if len(cfg.WhitelistAddresses) == 0 {
		return true
	}

	for _, addr := range cfg.WhitelistAddresses {
		if addr == eval.DestinationAddress {
			return true
		}
	}

	return false
}

// checkTimeWindow verifies current time is in allowed window
func (s *PolicyService) checkTimeWindow(cfg RuleConfig) bool {
	if len(cfg.AllowedHours) == 0 {
		return true
	}

	currentHour := time.Now().Hour()
	for _, hour := range cfg.AllowedHours {
		if hour == currentHour {
			return true
		}
	}

	return false
}

// checkBlockchain verifies blockchain is allowed
func (s *PolicyService) checkBlockchain(cfg RuleConfig, eval *PolicyEvaluationRequest) bool {
	if len(cfg.Blockchains) == 0 {
		return true
	}

	for _, bc := range cfg.Blockchains {
		if bc == eval.Blockchain {
			return true
		}
	}

	return false
}

// ApprovePolicy marks a policy as approved by an approver
func (s *PolicyService) ApprovePolicy(ctx context.Context, policyID string, approverID string) error {
	policy, err := s.db.GetPolicy(ctx, policyID)
	if err != nil {
		return fmt.Errorf("policy not found")
	}

	if policy.Approvals == nil {
		return fmt.Errorf("policy does not require approvals")
	}

	// Mark approver as approved
	for _, approver := range policy.Approvals.Approvers {
		if approver.ApproverID == approverID {
			now := time.Now()
			approver.Approved = true
			approver.ApprovedAt = &now
			break
		}
	}

	policy.UpdatedAt = time.Now()

	// Store updates
	if err := s.db.UpdatePolicy(ctx, policy); err != nil {
		return fmt.Errorf("failed to update policy: %w", err)
	}

	log.Printf("Policy %s approved by %s", policyID, approverID)

	return nil
}

// HandleCreatePolicy is the HTTP handler for creating policies
func (s *PolicyService) HandleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreatePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	policy, err := s.CreatePolicy(ctx, &req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create policy: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(policy)
}

// HandleGetPolicy is the HTTP handler to get a policy
func (s *PolicyService) HandleGetPolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	policyID := r.URL.Query().Get("policy_id")

	if policyID == "" {
		http.Error(w, "policy_id required", http.StatusBadRequest)
		return
	}

	policy, err := s.GetPolicy(ctx, policyID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Not found: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(policy)
}

// HandleListPolicies is the HTTP handler to list policies for a key
func (s *PolicyService) HandleListPolicies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := r.URL.Query().Get("key_id")

	if keyID == "" {
		http.Error(w, "key_id required", http.StatusBadRequest)
		return
	}

	policies, err := s.ListPoliciesByKey(ctx, keyID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch policies: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(policies)
}

// HandleEvaluatePolicy is the HTTP handler to evaluate a signing request
func (s *PolicyService) HandleEvaluatePolicy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var eval PolicyEvaluationRequest
	if err := json.NewDecoder(r.Body).Decode(&eval); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	result, err := s.EvaluatePolicy(ctx, &eval)
	if err != nil {
		http.Error(w, fmt.Sprintf("Evaluation failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
