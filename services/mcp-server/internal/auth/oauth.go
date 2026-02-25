// Package auth provides OAuth 2.1 authorization flow for the MCP server,
// enabling browser-based SSO login for MCP clients.
//
// Flow:
//  1. MCP client connects to SSE endpoint, receives 401 with auth metadata
//  2. MCP client opens browser → authorization endpoint
//  3. User authenticates via Meridian SSO
//  4. Authorization code returned via redirect with PKCE challenge
//  5. Client exchanges code + PKCE verifier for JWT at token endpoint
//  6. Client reconnects to SSE with Bearer token
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// authCodeTTL is how long an authorization code remains valid.
	authCodeTTL = 10 * time.Minute
	// authCodeBytes is the number of random bytes in a generated auth code.
	authCodeBytes = 32
)

// ErrInvalidBearerToken is returned by BearerValidator when the token is
// missing, malformed, or fails validation.
var ErrInvalidBearerToken = errors.New("invalid bearer token")

// OAuthConfig holds configuration for the MCP server's OAuth 2.1 endpoints.
type OAuthConfig struct {
	// ClientID is the OAuth client identifier (e.g. "meridian-mcp").
	ClientID string
	// AuthorizationURL is the full URL of the /oauth/authorize endpoint.
	AuthorizationURL string
	// TokenURL is the full URL of the /oauth/token endpoint.
	TokenURL string
	// RedirectURI is the callback URI registered for this client.
	RedirectURI string
}

// Metadata is returned in 401 responses so MCP clients know where to
// start the authorization flow.
type Metadata struct {
	AuthorizationURL string `json:"authorization_url"`
	TokenURL         string `json:"token_url"`
}

// CodeEntry holds the state stored alongside an authorization code.
type CodeEntry struct {
	CodeChallenge string
	ClientID      string
	RedirectURI   string
	IssuedAt      time.Time
}

// CodeStore is a thread-safe in-memory store for authorization codes.
// Each code can be consumed exactly once and expires after authCodeTTL.
type CodeStore struct {
	mu    sync.Mutex
	codes map[string]CodeEntry
}

// NewCodeStore creates an empty CodeStore.
func NewCodeStore() *CodeStore {
	return &CodeStore{
		codes: make(map[string]CodeEntry),
	}
}

// Store saves an authorization code entry.
func (s *CodeStore) Store(code string, entry CodeEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[code] = entry
}

// Consume atomically retrieves and deletes an authorization code.
// Returns (entry, true) if the code exists and has not expired.
// Returns (zero, false) if the code is unknown or expired.
func (s *CodeStore) Consume(code string) (CodeEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.codes[code]
	if !ok {
		return CodeEntry{}, false
	}

	// Always delete (one-time use), even if expired.
	delete(s.codes, code)

	if time.Since(entry.IssuedAt) > authCodeTTL {
		return CodeEntry{}, false
	}

	return entry, true
}

// TokenIssuer issues access tokens from a set of claims.
// Implementations may sign a JWT locally or call an upstream IdP.
type TokenIssuer interface {
	Issue(claims map[string]any) (string, error)
}

// BearerValidator validates a raw Bearer token string extracted from an
// Authorization header.
type BearerValidator interface {
	ValidateBearer(token string) error
}

// -----------------------------------------------------------------------
// AuthorizationHandler — GET /oauth/authorize
// -----------------------------------------------------------------------

// AuthorizationHandler handles the OAuth 2.1 authorization endpoint.
// It validates the PKCE challenge, generates an authorization code,
// and redirects the client back to redirect_uri.
type AuthorizationHandler struct {
	cfg    OAuthConfig
	store  *CodeStore
	logger *slog.Logger
}

// NewAuthorizationHandler creates a new AuthorizationHandler.
func NewAuthorizationHandler(cfg OAuthConfig, store *CodeStore) *AuthorizationHandler {
	return &AuthorizationHandler{
		cfg:    cfg,
		store:  store,
		logger: slog.Default(),
	}
}

// ServeHTTP implements http.Handler.
func (h *AuthorizationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	clientID := q.Get("client_id")
	if clientID != h.cfg.ClientID {
		http.Error(w, "invalid client_id", http.StatusBadRequest)
		return
	}

	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" {
		redirectURI = h.cfg.RedirectURI
	}

	challenge := q.Get("code_challenge")
	if challenge == "" {
		http.Error(w, "code_challenge is required (PKCE S256)", http.StatusBadRequest)
		return
	}

	method := q.Get("code_challenge_method")
	if method != "S256" {
		http.Error(w, "only code_challenge_method=S256 is supported", http.StatusBadRequest)
		return
	}

	// Generate a cryptographically random authorization code.
	code, err := generateCode()
	if err != nil {
		h.logger.Error("failed to generate authorization code", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.store.Store(code, CodeEntry{
		CodeChallenge: challenge,
		ClientID:      clientID,
		RedirectURI:   redirectURI,
		IssuedAt:      time.Now(),
	})

	h.logger.Info("authorization code issued", "client_id", clientID)

	// Build redirect URL.
	state := q.Get("state")
	redirectTarget := fmt.Sprintf("%s?code=%s", redirectURI, code)
	if state != "" {
		redirectTarget += "&state=" + state
	}

	http.Redirect(w, r, redirectTarget, http.StatusFound)
}

// -----------------------------------------------------------------------
// TokenHandler — POST /oauth/token
// -----------------------------------------------------------------------

// TokenHandler handles the OAuth 2.1 token endpoint.
// It validates the authorization code and PKCE verifier, then issues a JWT.
type TokenHandler struct {
	cfg    OAuthConfig
	store  *CodeStore
	issuer TokenIssuer
	logger *slog.Logger
}

// NewTokenHandler creates a new TokenHandler.
func NewTokenHandler(cfg OAuthConfig, store *CodeStore, issuer TokenIssuer) *TokenHandler {
	return &TokenHandler{
		cfg:    cfg,
		store:  store,
		issuer: issuer,
		logger: slog.Default(),
	}
}

// ServeHTTP implements http.Handler.
func (h *TokenHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}

	grantType := r.FormValue("grant_type")
	if grantType != "authorization_code" {
		http.Error(w, "unsupported grant_type", http.StatusBadRequest)
		return
	}

	code := r.FormValue("code")
	verifier := r.FormValue("code_verifier")
	clientID := r.FormValue("client_id")

	if code == "" || verifier == "" || clientID == "" {
		http.Error(w, "missing required parameters", http.StatusBadRequest)
		return
	}

	// Consume the code (one-time use, TTL check).
	entry, ok := h.store.Consume(code)
	if !ok {
		writeOAuthError(w, "invalid_grant", "authorization code is invalid or expired")
		return
	}

	// Validate client_id matches what was authorized.
	if entry.ClientID != clientID {
		writeOAuthError(w, "invalid_grant", "client_id mismatch")
		return
	}

	// Verify PKCE: SHA256(verifier) must equal stored challenge.
	if !verifyPKCE(verifier, entry.CodeChallenge) {
		writeOAuthError(w, "invalid_grant", "PKCE verification failed")
		return
	}

	// Issue token via the configured issuer.
	claims := map[string]any{
		"client_id": clientID,
		"iat":       time.Now().Unix(),
	}
	token, err := h.issuer.Issue(claims)
	if err != nil {
		h.logger.Error("token issuer failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Info("access token issued", "client_id", clientID)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

// -----------------------------------------------------------------------
// BearerMiddleware — wraps SSE/message handlers
// -----------------------------------------------------------------------

// BearerMiddleware enforces Bearer token authentication on HTTP handlers.
// Unauthenticated requests receive a 401 with Metadata so MCP clients
// can start the OAuth flow.
type BearerMiddleware struct {
	validator BearerValidator
	meta      Metadata
	logger    *slog.Logger
}

// NewBearerMiddleware creates a new BearerMiddleware.
func NewBearerMiddleware(validator BearerValidator, meta Metadata) *BearerMiddleware {
	return &BearerMiddleware{
		validator: validator,
		meta:      meta,
		logger:    slog.Default(),
	}
}

// Handler wraps an http.Handler, enforcing Bearer token authentication.
func (m *BearerMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := extractBearerFromHeader(r)
		if err != nil {
			m.writeAuthRequired(w)
			return
		}

		if err := m.validator.ValidateBearer(token); err != nil {
			m.logger.Debug("bearer token validation failed",
				"error", err, "path", r.URL.Path)
			m.writeAuthRequired(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// writeAuthRequired writes a 401 response with auth metadata.
func (m *BearerMiddleware) writeAuthRequired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="meridian-mcp"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(m.meta)
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

// generateCode returns a cryptographically random, URL-safe authorization code.
func generateCode() (string, error) {
	b := make([]byte, authCodeBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate auth code: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// verifyPKCE checks that SHA256(verifier) == challenge (base64url-encoded).
func verifyPKCE(verifier, challenge string) bool {
	h := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(h[:])
	return computed == challenge
}

// extractBearerFromHeader extracts the raw token from an Authorization: Bearer header.
func extractBearerFromHeader(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", ErrInvalidBearerToken
	}
	if !strings.HasPrefix(auth, "Bearer ") {
		return "", ErrInvalidBearerToken
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" {
		return "", ErrInvalidBearerToken
	}
	return token, nil
}

// writeOAuthError writes a 401 with an RFC 6749 error response.
func writeOAuthError(w http.ResponseWriter, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}
