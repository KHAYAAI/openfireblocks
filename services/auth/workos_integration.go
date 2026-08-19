package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type WorkOSService struct {
	apiKey     string
	httpClient *http.Client
	authService *AuthService
}

type WorkOSUser struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
	EmailVerified bool   `json:"email_verified"`
	ProfilePhotoUrl string `json:"profile_photo_url"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

type WorkOSConnection struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ConnectionType string `json:"connection_type"` // "OAuthOIDC", "SAML"
	State         string `json:"state"` // "active", "inactive"
	CreatedAt     string `json:"created_at"`
}

type WorkOSOrganization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Domain string `json:"domain"`
	LogoUrl string `json:"logo_url"`
}

type WorkOSAuthorizationResponse struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

type WorkOSTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	IdToken     string `json:"id_token"`
}

func NewWorkOSService(apiKey string, authService *AuthService) *WorkOSService {
	return &WorkOSService{
		apiKey:      apiKey,
		authService: authService,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// GenerateAuthorizationURL generates OIDC authorization URL
func (s *WorkOSService) GenerateAuthorizationURL(clientID, redirectURI, state string) string {
	return fmt.Sprintf(
		"https://api.workos.com/sso/authorize?client_id=%s&redirect_uri=%s&response_type=code&state=%s",
		clientID,
		redirectURI,
		state,
	)
}

// ExchangeCodeForToken exchanges authorization code for access token
func (s *WorkOSService) ExchangeCodeForToken(ctx context.Context, code, clientID, clientSecret, redirectURI string) (*WorkOSTokenResponse, error) {
	payload := map[string]string{
		"client_id":     clientID,
		"client_secret": clientSecret,
		"code":          code,
		"grant_type":    "authorization_code",
		"redirect_uri":  redirectURI,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.workos.com/sso/token", nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(s.apiKey, "")
	req.Header.Set("Content-Type", "application/json")

	// Use form-encoded for token endpoint
	req.Body = io.NopCloser(io.Reader(bytes.NewReader(body)))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("WorkOS error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp WorkOSTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &tokenResp, nil
}

// GetUser retrieves user information from WorkOS
func (s *WorkOSService) GetUser(ctx context.Context, userID string) (*WorkOSUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://api.workos.com/user_management/users/%s", userID), nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(s.apiKey, "")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("WorkOS error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var user WorkOSUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("failed to decode user: %w", err)
	}

	return &user, nil
}

// ProvisionUser creates a new user in WorkOS
func (s *WorkOSService) ProvisionUser(ctx context.Context, email, firstName, lastName string) (*WorkOSUser, error) {
	payload := map[string]string{
		"email":      email,
		"first_name": firstName,
		"last_name":  lastName,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.workos.com/user_management/users", io.NopCloser(io.Reader(bytes.NewReader(body))))
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(s.apiKey, "")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to provision user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("WorkOS error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var user WorkOSUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("failed to decode user: %w", err)
	}

	return &user, nil
}

// SyncUserToLocal syncs WorkOS user to local database
func (s *WorkOSService) SyncUserToLocal(ctx context.Context, workosUser *WorkOSUser) (*User, error) {
	// Check if user already exists
	existingUser, _, err := s.authService.db.GetUserByEmail(ctx, workosUser.Email)
	if err == nil && existingUser != nil {
		// User exists, update last login
		now := time.Now()
		existingUser.LastLoginAt = &now
		s.authService.db.UpdateUser(ctx, existingUser)
		return existingUser, nil
	}

	// Create new user
	fullName := fmt.Sprintf("%s %s", workosUser.FirstName, workosUser.LastName)
	user := &User{
		UserID:       workosUser.ID,
		Email:        workosUser.Email,
		FullName:     fullName,
		Organization: "", // Will be set from connection/org
		Role:         "user",
		Status:       "active",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := s.authService.db.CreateUserWithSSO(ctx, user, workosUser.ID); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	return user, nil
}

// GetConnections retrieves SSO connections for an organization
func (s *WorkOSService) GetConnections(ctx context.Context, organizationID string) ([]*WorkOSConnection, error) {
	url := fmt.Sprintf("https://api.workos.com/sso/connections?organization_id=%s", organizationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(s.apiKey, "")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get connections: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("WorkOS error: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Data []*WorkOSConnection `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode connections: %w", err)
	}

	return result.Data, nil
}

// HTTP Handlers

func (s *WorkOSService) HandleSSOCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Extract code and state from query params
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		http.Error(w, "Missing code or state", http.StatusBadRequest)
		return
	}

	// Verify state (should be stored in session)
	sessionState, err := s.authService.db.GetSessionState(ctx, state)
	if err != nil {
		http.Error(w, "Invalid state", http.StatusUnauthorized)
		return
	}

	// Exchange code for token
	tokenResp, err := s.ExchangeCodeForToken(ctx, code, sessionState.ClientID, sessionState.ClientSecret, sessionState.RedirectURI)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to exchange code: %v", err), http.StatusInternalServerError)
		return
	}

	// TODO: Parse ID token (JWT) to get user claims
	// For now, we'll get user from WorkOS API

	// Sync user to local database
	workosUser, err := s.GetUser(ctx, "") // Extract user ID from token
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get user: %v", err), http.StatusInternalServerError)
		return
	}

	user, err := s.SyncUserToLocal(ctx, workosUser)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to sync user: %v", err), http.StatusInternalServerError)
		return
	}

	// Create login response
	loginResp, err := s.authService.createLoginSession(ctx, user, r.RemoteAddr, r.Header.Get("User-Agent"))
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create session: %v", err), http.StatusInternalServerError)
		return
	}

	// Return tokens in response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(loginResp)
}

func (s *WorkOSService) HandleInitiateSSOLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConnectionID string `json:"connection_id"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		RedirectURI  string `json:"redirect_uri"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Generate state
	state := generateToken()

	// Store state in session
	ctx := r.Context()
	s.authService.db.CreateSessionState(ctx, state, req.ClientID, req.ClientSecret, req.RedirectURI)

	// Generate authorization URL
	authURL := fmt.Sprintf(
		"https://api.workos.com/sso/authorize?connection_id=%s&client_id=%s&redirect_uri=%s&response_type=code&state=%s",
		req.ConnectionID,
		req.ClientID,
		req.RedirectURI,
		state,
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"authorization_url": authURL})
}

// Import missing dependencies
import "bytes"

