package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AuthMiddleware struct {
	authService *AuthService
}

type RateLimitMiddleware struct {
	limiter *RateLimiter
}

type RequestLoggingMiddleware struct {
	logger *log.Logger
}

type CORSMiddleware struct {
	allowedOrigins []string
}

type SecurityHeadersMiddleware struct{}

type InputValidationMiddleware struct{}

// Auth Middleware
func (m *AuthMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract token from Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Missing authorization header", http.StatusUnauthorized)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "Invalid authorization header", http.StatusUnauthorized)
			return
		}

		tokenString := parts[1]

		// Verify token
		claims, err := m.authService.VerifyAccessToken(r.Context(), tokenString)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid token: %v", err), http.StatusUnauthorized)
			return
		}

		// Add claims to context
		ctx := context.WithValue(r.Context(), "claims", claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// API Key Middleware (alternative to JWT)
func (m *AuthMiddleware) APIKeyHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey == "" {
			http.Error(w, "Missing API key", http.StatusUnauthorized)
			return
		}

		// Verify API key
		key, err := m.authService.VerifyAPIKey(r.Context(), apiKey)
		if err != nil {
			http.Error(w, "Invalid API key", http.StatusUnauthorized)
			return
		}

		// Convert to claims for consistency
		claims := &JWTClaims{
			UserID:       key.UserID,
			Permissions:  []string{key.Scope + ":*"},
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			},
		}

		ctx := context.WithValue(r.Context(), "claims", claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Rate Limiting Middleware
func (m *RateLimitMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value("claims").(*JWTClaims)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		// Check rate limit
		allowed, retryAfter := m.limiter.Allow(claims.UserID)
		if !allowed {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter.Seconds()))
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Request Logging Middleware
func (m *RequestLoggingMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Get user ID from context if available
		userID := "anonymous"
		if claims, ok := r.Context().Value("claims").(*JWTClaims); ok {
			userID = claims.UserID
		}

		// Get IP address
		ipAddress := r.Header.Get("X-Forwarded-For")
		if ipAddress == "" {
			ipAddress = r.RemoteAddr
		}

		// Log request
		m.logger.Printf(
			"%s %s %s from %s user:%s user-agent:%s",
			r.Method,
			r.RequestURI,
			r.Proto,
			ipAddress,
			userID,
			r.Header.Get("User-Agent"),
		)

		// Wrap response writer to capture status code
		wrappedWriter := &ResponseWriter{ResponseWriter: w}

		// Call next handler
		next.ServeHTTP(wrappedWriter, r)

		// Log response
		m.logger.Printf(
			"Response: %d %s (%.3fs)",
			wrappedWriter.statusCode,
			http.StatusText(wrappedWriter.statusCode),
			time.Since(start).Seconds(),
		)
	})
}

type ResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *ResponseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *ResponseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// CORS Middleware
func (m *CORSMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Check if origin is allowed
		allowed := false
		for _, allowedOrigin := range m.allowedOrigins {
			if origin == allowedOrigin || allowedOrigin == "*" {
				allowed = true
				break
			}
		}

		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
			w.Header().Set("Access-Control-Max-Age", "3600")
		}

		// Handle preflight
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Security Headers Middleware
func (m *SecurityHeadersMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

		next.ServeHTTP(w, r)
	})
}

// Input Validation Middleware
func (m *InputValidationMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate content length
		if r.ContentLength > 10*1024*1024 { // 10MB limit
			http.Error(w, "Payload too large", http.StatusRequestEntityTooLarge)
			return
		}

		// Validate content type
		contentType := r.Header.Get("Content-Type")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if contentType == "" {
				http.Error(w, "Content-Type header required", http.StatusBadRequest)
				return
			}

			if !strings.Contains(contentType, "application/json") {
				http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// Rate Limiter using token bucket algorithm
type RateLimiter struct {
	limits map[string]*TokenBucket
}

type TokenBucket struct {
	tokens    float64
	maxTokens float64
	refillRate float64
	lastRefill time.Time
}

func NewRateLimiter(requestsPerMinute int) *RateLimiter {
	return &RateLimiter{
		limits: make(map[string]*TokenBucket),
	}
}

func (rl *RateLimiter) Allow(userID string) (bool, time.Duration) {
	bucket, exists := rl.limits[userID]
	if !exists {
		bucket = &TokenBucket{
			tokens:     float64(100), // 100 requests per minute default
			maxTokens:  float64(100),
			refillRate: 100.0 / 60.0, // tokens per second
			lastRefill: time.Now(),
		}
		rl.limits[userID] = bucket
	}

	// Refill tokens
	now := time.Now()
	timePassed := now.Sub(bucket.lastRefill).Seconds()
	bucket.tokens = min(bucket.maxTokens, bucket.tokens + (bucket.refillRate * timePassed))
	bucket.lastRefill = now

	// Check if request allowed
	if bucket.tokens >= 1.0 {
		bucket.tokens -= 1.0
		return true, 0
	}

	// Calculate retry after
	tokensNeeded := 1.0 - bucket.tokens
	retryAfter := time.Duration((tokensNeeded / bucket.refillRate) * float64(time.Second))
	return false, retryAfter
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// Permission checking middleware
func RequirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := r.Context().Value("claims").(*JWTClaims)
			if !ok {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Check permission
			hasPermission := false
			for _, perm := range claims.Permissions {
				if perm == permission || strings.HasSuffix(perm, ":*") {
					hasPermission = true
					break
				}
			}

			if !hasPermission {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
