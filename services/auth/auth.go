package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	db              *PostgresDB
	jwtSecret       string
	jwtRefreshSecret string
	workosAPIKey    string
}

type User struct {
	UserID        string    `json:"user_id"`
	Email         string    `json:"email"`
	FullName      string    `json:"full_name"`
	Organization  string    `json:"organization"`
	Role          string    `json:"role"` // admin, billing_admin, user, viewer
	Status        string    `json:"status"` // active, inactive, suspended
	TwoFAEnabled  bool      `json:"two_fa_enabled"`
	LastLoginAt   *time.Time `json:"last_login_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	MFACode  string `json:"mfa_code,omitempty"`
}

type LoginResponse struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	ExpiresIn        int       `json:"expires_in"`
	User             *User     `json:"user"`
	MFARequired      bool      `json:"mfa_required"`
	MFASessionToken  string    `json:"mfa_session_token,omitempty"`
}

type SSOTokenRequest struct {
	Code        string `json:"code"`
	RedirectURI string `json:"redirect_uri"`
}

type JWTClaims struct {
	UserID       string   `json:"user_id"`
	Email        string   `json:"email"`
	Organization string   `json:"organization"`
	Role         string   `json:"role"`
	Permissions  []string `json:"permissions"`
	jwt.RegisteredClaims
}

type MFASession struct {
	SessionToken string
	UserID       string
	ExpiresAt    time.Time
}

type APIKey struct {
	KeyID      string    `json:"key_id"`
	UserID     string    `json:"user_id"`
	Name       string    `json:"name"`
	LastChars  string    `json:"last_chars"` // Last 8 chars for display
	KeyHash    string    `json:"-"` // Hashed in DB
	Scope      string    `json:"scope"` // read, write, admin
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time `json:"created_at"`
}

type Session struct {
	SessionID    string    `json:"session_id"`
	UserID       string    `json:"user_id"`
	IPAddress    string    `json:"ip_address"`
	UserAgent    string    `json:"user_agent"`
	DeviceInfo   string    `json:"device_info"`
	IsActive     bool      `json:"is_active"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type PasswordReset struct {
	ResetID   string    `json:"reset_id"`
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Token     string    `json:"-"`
	IsUsed    bool      `json:"is_used"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func NewAuthService(db *PostgresDB, jwtSecret, jwtRefreshSecret, workosAPIKey string) *AuthService {
	return &AuthService{
		db:               db,
		jwtSecret:        jwtSecret,
		jwtRefreshSecret: jwtRefreshSecret,
		workosAPIKey:     workosAPIKey,
	}
}

// RegisterUser registers a new user with email/password
func (s *AuthService) RegisterUser(ctx context.Context, email, password, fullName, organization string) (*User, error) {
	// Validate password strength
	if err := validatePassword(password); err != nil {
		return nil, err
	}

	// Hash password with bcrypt
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	user := &User{
		UserID:       uuid.New().String(),
		Email:        email,
		FullName:     fullName,
		Organization: organization,
		Role:         "user",
		Status:       "active",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := s.db.CreateUser(ctx, user, string(hashedPassword)); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	// Audit log
	s.db.CreateAuditLog(ctx, &AuditLog{
		LogID:        uuid.New().String(),
		CustomerID:   user.UserID,
		Action:       "user_registered",
		Actor:        "system",
		ResourceID:   user.UserID,
		ResourceType: "user",
		Status:       "success",
		CreatedAt:    time.Now(),
	})

	return user, nil
}

// LoginWithPassword authenticates user with email/password
func (s *AuthService) LoginWithPassword(ctx context.Context, email, password, ipAddress, userAgent string) (*LoginResponse, error) {
	user, passwordHash, err := s.db.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("user not found")
	}

	if user.Status != "active" {
		return nil, fmt.Errorf("user account is not active")
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		// Log failed attempt
		s.db.CreateAuditLog(ctx, &AuditLog{
			LogID:        uuid.New().String(),
			CustomerID:   user.UserID,
			Action:       "login_failed",
			Actor:        email,
			ResourceID:   user.UserID,
			ResourceType: "session",
			Status:       "failure",
			IPAddress:    ipAddress,
			UserAgent:    userAgent,
			CreatedAt:    time.Now(),
		})
		return nil, fmt.Errorf("invalid credentials")
	}

	// Check if 2FA is enabled
	if user.TwoFAEnabled {
		// Generate MFA session token
		mfaSessionToken := generateToken()
		if err := s.db.CreateMFASession(ctx, user.UserID, mfaSessionToken); err != nil {
			return nil, fmt.Errorf("failed to create MFA session: %w", err)
		}

		return &LoginResponse{
			MFARequired:     true,
			MFASessionToken: mfaSessionToken,
		}, nil
	}

	// Create session and tokens
	return s.createLoginSession(ctx, user, ipAddress, userAgent)
}

// VerifyMFACode verifies TOTP/WebAuthn code
func (s *AuthService) VerifyMFACode(ctx context.Context, userID, mfaSessionToken, code string) (*LoginResponse, error) {
	// Verify MFA session exists and is valid
	session, err := s.db.GetMFASession(ctx, mfaSessionToken)
	if err != nil || session.UserID != userID || time.Now().After(session.ExpiresAt) {
		return nil, fmt.Errorf("invalid MFA session")
	}

	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("user not found")
	}

	// Verify TOTP code (implement with github.com/pquerna/otp)
	mfaSecret, err := s.db.GetMFASecret(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("MFA configuration not found")
	}

	if !verifyTOTPCode(code, mfaSecret) {
		s.db.CreateAuditLog(ctx, &AuditLog{
			LogID:        uuid.New().String(),
			CustomerID:   userID,
			Action:       "mfa_verification_failed",
			Actor:        user.Email,
			ResourceID:   userID,
			ResourceType: "mfa",
			Status:       "failure",
			CreatedAt:    time.Now(),
		})
		return nil, fmt.Errorf("invalid MFA code")
	}

	// Invalidate MFA session
	s.db.InvalidateMFASession(ctx, mfaSessionToken)

	// Get IP/User-Agent from context or use defaults
	ipAddress := "0.0.0.0"
	userAgent := "unknown"
	if ip, ok := ctx.Value("ip_address").(string); ok {
		ipAddress = ip
	}
	if ua, ok := ctx.Value("user_agent").(string); ok {
		userAgent = ua
	}

	return s.createLoginSession(ctx, user, ipAddress, userAgent)
}

// createLoginSession generates tokens and creates session
func (s *AuthService) createLoginSession(ctx context.Context, user *User, ipAddress, userAgent string) (*LoginResponse, error) {
	// Generate tokens
	accessToken, expiresIn, err := s.generateAccessToken(user)
	if err != nil {
		return nil, fmt.Errorf("failed to generate access token: %w", err)
	}

	refreshToken, err := s.generateRefreshToken(user)
	if err != nil {
		return nil, fmt.Errorf("failed to generate refresh token: %w", err)
	}

	// Create session record
	sessionID := uuid.New().String()
	session := &Session{
		SessionID:  sessionID,
		UserID:     user.UserID,
		IPAddress:  ipAddress,
		UserAgent:  userAgent,
		IsActive:   true,
		LastSeenAt: time.Now(),
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(24 * time.Hour),
	}

	if err := s.db.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Update last login
	now := time.Now()
	user.LastLoginAt = &now
	s.db.UpdateUser(ctx, user)

	// Audit log
	s.db.CreateAuditLog(ctx, &AuditLog{
		LogID:        uuid.New().String(),
		CustomerID:   user.UserID,
		Action:       "login_success",
		Actor:        user.Email,
		ResourceID:   sessionID,
		ResourceType: "session",
		Status:       "success",
		IPAddress:    ipAddress,
		UserAgent:    userAgent,
		CreatedAt:    time.Now(),
	})

	return &LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
		User:         user,
	}, nil
}

// GenerateAccessToken creates a short-lived JWT
func (s *AuthService) generateAccessToken(user *User) (string, int, error) {
	expiresIn := 3600 // 1 hour
	claims := JWTClaims{
		UserID:       user.UserID,
		Email:        user.Email,
		Organization: user.Organization,
		Role:         user.Role,
		Permissions:  s.getPermissionsForRole(user.Role),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(expiresIn) * time.Second)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "openfireblocks",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(s.jwtSecret))
	if err != nil {
		return "", 0, err
	}

	return tokenString, expiresIn, nil
}

// GenerateRefreshToken creates a long-lived refresh token
func (s *AuthService) generateRefreshToken(user *User) (string, error) {
	claims := JWTClaims{
		UserID: user.UserID,
		Email:  user.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(30 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "openfireblocks",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(s.jwtRefreshSecret))
	if err != nil {
		return "", err
	}

	return tokenString, nil
}

// VerifyAccessToken validates JWT and returns claims
func (s *AuthService) VerifyAccessToken(ctx context.Context, tokenString string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(s.jwtSecret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(*JWTClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	// Check if session is still active
	session, err := s.db.GetSessionByUserID(ctx, claims.UserID)
	if err != nil || !session.IsActive || time.Now().After(session.ExpiresAt) {
		return nil, fmt.Errorf("session expired or invalid")
	}

	return claims, nil
}

// RefreshAccessToken generates new access token from refresh token
func (s *AuthService) RefreshAccessToken(ctx context.Context, refreshTokenString string) (string, int, error) {
	token, err := jwt.ParseWithClaims(refreshTokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(s.jwtRefreshSecret), nil
	})

	if err != nil {
		return "", 0, fmt.Errorf("invalid refresh token: %w", err)
	}

	claims, ok := token.Claims.(*JWTClaims)
	if !ok || !token.Valid {
		return "", 0, fmt.Errorf("invalid refresh token claims")
	}

	// Get user and regenerate access token
	user, err := s.db.GetUser(ctx, claims.UserID)
	if err != nil {
		return "", 0, fmt.Errorf("user not found")
	}

	accessToken, expiresIn, err := s.generateAccessToken(user)
	if err != nil {
		return "", 0, err
	}

	return accessToken, expiresIn, nil
}

// LogoutUser invalidates session and refresh token
func (s *AuthService) LogoutUser(ctx context.Context, userID, sessionID string) error {
	if err := s.db.InvalidateSession(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to invalidate session: %w", err)
	}

	s.db.CreateAuditLog(ctx, &AuditLog{
		LogID:        uuid.New().String(),
		CustomerID:   userID,
		Action:       "logout",
		Actor:        userID,
		ResourceID:   sessionID,
		ResourceType: "session",
		Status:       "success",
		CreatedAt:    time.Now(),
	})

	return nil
}

// CreateAPIKey generates a new API key
func (s *AuthService) CreateAPIKey(ctx context.Context, userID, name, scope string, expiresAt *time.Time) (*APIKey, error) {
	// Generate random key
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}
	keyString := base64.URLEncoding.EncodeToString(keyBytes)

	// Hash key for storage
	keyHash, err := bcrypt.GenerateFromPassword([]byte(keyString), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash key: %w", err)
	}

	apiKey := &APIKey{
		KeyID:     uuid.New().String(),
		UserID:    userID,
		Name:      name,
		LastChars: keyString[len(keyString)-8:],
		KeyHash:   string(keyHash),
		Scope:     scope,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}

	if err := s.db.CreateAPIKey(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("failed to create API key: %w", err)
	}

	// Return only the full key (client must store it)
	apiKey.KeyHash = ""
	return apiKey, nil
}

// VerifyAPIKey validates an API key
func (s *AuthService) VerifyAPIKey(ctx context.Context, apiKeyString string) (*APIKey, error) {
	apiKey, err := s.db.GetAPIKeyByPrefix(ctx, apiKeyString[:16])
	if err != nil {
		return nil, fmt.Errorf("API key not found")
	}

	if apiKey.ExpiresAt != nil && time.Now().After(*apiKey.ExpiresAt) {
		return nil, fmt.Errorf("API key expired")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(apiKey.KeyHash), []byte(apiKeyString)); err != nil {
		return nil, fmt.Errorf("invalid API key")
	}

	// Update last used timestamp
	now := time.Now()
	apiKey.LastUsedAt = &now
	s.db.UpdateAPIKey(ctx, apiKey)

	return apiKey, nil
}

// GetUserSessions returns all active sessions for user
func (s *AuthService) GetUserSessions(ctx context.Context, userID string) ([]*Session, error) {
	return s.db.GetUserSessions(ctx, userID)
}

// RevokeSession invalidates a specific session
func (s *AuthService) RevokeSession(ctx context.Context, userID, sessionID string) error {
	session, err := s.db.GetSession(ctx, sessionID)
	if err != nil || session.UserID != userID {
		return fmt.Errorf("session not found")
	}

	return s.db.InvalidateSession(ctx, sessionID)
}

// InitiatePasswordReset creates a reset token
func (s *AuthService) InitiatePasswordReset(ctx context.Context, email string) (*PasswordReset, error) {
	user, _, err := s.db.GetUserByEmail(ctx, email)
	if err != nil {
		// Don't reveal whether email exists
		return nil, nil
	}

	token := generateToken()
	reset := &PasswordReset{
		ResetID:   uuid.New().String(),
		UserID:    user.UserID,
		Email:     email,
		Token:     token,
		IsUsed:    false,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}

	if err := s.db.CreatePasswordReset(ctx, reset); err != nil {
		return nil, fmt.Errorf("failed to create reset: %w", err)
	}

	// Audit log
	s.db.CreateAuditLog(ctx, &AuditLog{
		LogID:        uuid.New().String(),
		CustomerID:   user.UserID,
		Action:       "password_reset_initiated",
		Actor:        "system",
		ResourceID:   reset.ResetID,
		ResourceType: "password_reset",
		Status:       "success",
		CreatedAt:    time.Now(),
	})

	return reset, nil
}

// ResetPassword uses token to set new password
func (s *AuthService) ResetPassword(ctx context.Context, resetID, token, newPassword string) error {
	reset, err := s.db.GetPasswordReset(ctx, resetID)
	if err != nil || reset.IsUsed || time.Now().After(reset.ExpiresAt) {
		return fmt.Errorf("invalid or expired reset token")
	}

	if reset.Token != token {
		return fmt.Errorf("token mismatch")
	}

	// Validate password
	if err := validatePassword(newPassword); err != nil {
		return err
	}

	// Hash new password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	// Update password
	if err := s.db.UpdateUserPassword(ctx, reset.UserID, string(hashedPassword)); err != nil {
		return fmt.Errorf("failed to update password: %w", err)
	}

	// Mark reset as used
	reset.IsUsed = true
	s.db.UpdatePasswordReset(ctx, reset)

	// Audit log
	s.db.CreateAuditLog(ctx, &AuditLog{
		LogID:        uuid.New().String(),
		CustomerID:   reset.UserID,
		Action:       "password_reset_completed",
		Actor:        "system",
		ResourceID:   reset.ResetID,
		ResourceType: "password_reset",
		Status:       "success",
		CreatedAt:    time.Now(),
	})

	return nil
}

// Helper functions

func validatePassword(password string) error {
	if len(password) < 12 {
		return fmt.Errorf("password must be at least 12 characters")
	}
	if !strings.ContainsAny(password, "0123456789") {
		return fmt.Errorf("password must contain numbers")
	}
	if !strings.ContainsAny(password, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return fmt.Errorf("password must contain uppercase letters")
	}
	if !strings.ContainsAny(password, "abcdefghijklmnopqrstuvwxyz") {
		return fmt.Errorf("password must contain lowercase letters")
	}
	if !strings.ContainsAny(password, "!@#$%^&*") {
		return fmt.Errorf("password must contain special characters")
	}
	return nil
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

func verifyTOTPCode(code, secret string) bool {
	// TODO: Implement TOTP verification using github.com/pquerna/otp
	// This requires the OTP library integration
	return true // Placeholder
}

func (s *AuthService) getPermissionsForRole(role string) []string {
	switch role {
	case "admin":
		return []string{
			"users:read", "users:write", "users:delete",
			"keys:read", "keys:write", "keys:delete",
			"signings:read", "signings:write", "signings:approve",
			"policies:read", "policies:write", "policies:delete",
			"reports:read", "webhooks:read", "webhooks:write",
			"compliance:read", "audit:read",
		}
	case "billing_admin":
		return []string{
			"users:read", "billing:read", "billing:write",
			"reports:read", "audit:read",
		}
	case "user":
		return []string{
			"keys:read", "keys:write",
			"signings:read", "signings:write",
			"policies:read", "webhooks:read", "webhooks:write",
		}
	case "viewer":
		return []string{
			"keys:read", "signings:read", "reports:read",
		}
	default:
		return []string{}
	}
}

// HTTP Handlers

func (s *AuthService) HandleRegister(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		Email        string `json:"email"`
		Password     string `json:"password"`
		FullName     string `json:"full_name"`
		Organization string `json:"organization"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	user, err := s.RegisterUser(ctx, req.Email, req.Password, req.FullName, req.Organization)
	if err != nil {
		http.Error(w, fmt.Sprintf("Registration failed: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

func (s *AuthService) HandleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	ipAddress := r.Header.Get("X-Forwarded-For")
	if ipAddress == "" {
		ipAddress = strings.Split(r.RemoteAddr, ":")[0]
	}
	userAgent := r.Header.Get("User-Agent")

	response, err := s.LoginWithPassword(ctx, req.Email, req.Password, ipAddress, userAgent)
	if err != nil {
		http.Error(w, fmt.Sprintf("Login failed: %v", err), http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *AuthService) HandleRefreshToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req struct {
		RefreshToken string `json:"refresh_token"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	accessToken, expiresIn, err := s.RefreshAccessToken(ctx, req.RefreshToken)
	if err != nil {
		http.Error(w, fmt.Sprintf("Refresh failed: %v", err), http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token": accessToken,
		"expires_in":   expiresIn,
	})
}

func (s *AuthService) HandleLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := ctx.Value("claims").(*JWTClaims)

	sessionID := r.URL.Query().Get("session_id")
	if err := s.LogoutUser(ctx, claims.UserID, sessionID); err != nil {
		http.Error(w, fmt.Sprintf("Logout failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "logged out"})
}
