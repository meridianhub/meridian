// Package auth provides OAuth 2.1 authorization flow for the MCP server,
// enabling browser-based SSO login for MCP clients.
//
// Flow:
//  1. MCP client POSTs to /mcp, receives 401 with auth metadata
//  2. MCP client opens browser → authorization endpoint
//  3. User authenticates via Meridian SSO
//  4. Authorization code returned via redirect with PKCE challenge
//  5. Client exchanges code + PKCE verifier for JWT at token endpoint
//  6. Client retries /mcp with Bearer token
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
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

const (
	// authCodeTTL is how long an authorization code remains valid.
	authCodeTTL = 10 * time.Minute
	// authCodeBytes is the number of random bytes in a generated auth code.
	authCodeBytes = 32
	// storeEvictInterval is how often the CodeStore sweeps expired entries.
	storeEvictInterval = 5 * time.Minute
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
	// Token is the pre-signed JWT to return at token exchange time.
	// When set (by the OIDC callback flow), the TokenHandler returns this
	// token directly instead of calling the TokenIssuer.
	Token string
}

// CodeStore is a thread-safe in-memory store for authorization codes.
// Each code can be consumed exactly once and expires after authCodeTTL.
// A background sweep runs every storeEvictInterval to evict abandoned entries.
type CodeStore struct {
	mu        sync.Mutex
	codes     map[string]CodeEntry
	tokens    map[string]string // code → pre-signed JWT (set by OIDC flow)
	stop      chan struct{}
	closeOnce sync.Once
}

// NewCodeStore creates an empty CodeStore and starts the background eviction goroutine.
// Call [CodeStore.Close] to stop the eviction goroutine when the store is no longer needed.
func NewCodeStore() *CodeStore {
	s := &CodeStore{
		codes:  make(map[string]CodeEntry),
		tokens: make(map[string]string),
		stop:   make(chan struct{}),
	}
	go s.evictLoop()
	return s
}

// Close stops the background eviction goroutine. Safe to call multiple times.
func (s *CodeStore) Close() {
	s.closeOnce.Do(func() { close(s.stop) })
}

// evictLoop periodically removes entries that have passed authCodeTTL
// without being consumed. This bounds store size under load.
func (s *CodeStore) evictLoop() {
	ticker := time.NewTicker(storeEvictInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.evictExpired()
		case <-s.stop:
			return
		}
	}
}

func (s *CodeStore) evictExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for code, entry := range s.codes {
		if time.Since(entry.IssuedAt) > authCodeTTL {
			delete(s.codes, code)
			delete(s.tokens, code)
		}
	}
}

// Store saves an authorization code entry.
func (s *CodeStore) Store(code string, entry CodeEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[code] = entry
}

// StoreWithToken saves an authorization code entry alongside a pre-signed JWT.
// The token is returned directly by the TokenHandler during code exchange,
// bypassing the TokenIssuer. Used by the OIDC callback flow.
func (s *CodeStore) StoreWithToken(code string, entry CodeEntry, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[code] = entry
	s.tokens[code] = token
}

// Consume atomically retrieves and deletes an authorization code.
// Returns (entry, true) if the code exists and has not expired.
// Returns (zero, false) if the code is unknown or expired.
// If a pre-signed token was stored with StoreWithToken, the entry's Token
// field is populated.
func (s *CodeStore) Consume(code string) (CodeEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.codes[code]
	if !ok {
		return CodeEntry{}, false
	}

	// Retrieve pre-signed token if present.
	if token, hasToken := s.tokens[code]; hasToken {
		entry.Token = token
	}

	// Always delete (one-time use), even if expired.
	delete(s.codes, code)
	delete(s.tokens, code)

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
// Authorization Server Metadata — GET /.well-known/oauth-authorization-server
// -----------------------------------------------------------------------

// AuthorizationServerMetadata represents the OAuth 2.0 Authorization Server
// Metadata response per RFC 8414. MCP clients (e.g. Claude.ai) fetch this
// endpoint to discover how to authenticate.
type AuthorizationServerMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	RegistrationEndpoint              string   `json:"registration_endpoint,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
}

// NewMetadataHandler returns an http.HandlerFunc that serves the OAuth 2.0
// Authorization Server Metadata document (RFC 8414) at
// /.well-known/oauth-authorization-server.
//
// URLs are derived from the request's Host header at runtime so that
// tenant-scoped subdomains (e.g., acme.demo.meridianhub.cloud) receive
// metadata pointing back to their own origin.
func NewMetadataHandler(fallbackBaseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		base := baseURLFromRequest(r, fallbackBaseURL)
		meta := AuthorizationServerMetadata{
			Issuer:                            base,
			AuthorizationEndpoint:             base + "/oauth/authorize",
			TokenEndpoint:                     base + "/oauth/token",
			RegistrationEndpoint:              base + "/oauth/register",
			ResponseTypesSupported:            []string{"code"},
			GrantTypesSupported:               []string{"authorization_code"},
			CodeChallengeMethodsSupported:     []string{"S256"},
			TokenEndpointAuthMethodsSupported: []string{"none"},
		}

		body, err := json.Marshal(meta)
		if err != nil {
			http.Error(w, "internal error: failed to serialize metadata", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Vary", "Host, X-Forwarded-Host, X-Forwarded-Proto")
		_, _ = w.Write(body)
	}
}

// baseURLFromRequest derives the base URL (scheme + host) from the incoming
// request. It checks X-Forwarded-Host and X-Forwarded-Proto headers first
// (set by reverse proxies like Caddy), then falls back to r.Host.
// If the host cannot be determined, fallback is returned.
//
// Security: This function trusts X-Forwarded-Host/Proto headers. In production
// these are set by Caddy which overwrites (not appends) forwarded headers,
// preventing client spoofing. The MCP server must NOT be exposed directly
// to the internet without a reverse proxy that sanitizes these headers.
func baseURLFromRequest(r *http.Request, fallback string) string {
	host := firstCSV(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return fallback
	}

	scheme := firstCSV(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	return scheme + "://" + host
}

// firstCSV returns the first value from a potentially comma-separated header.
// In multi-hop proxy setups, X-Forwarded-Host and X-Forwarded-Proto may
// contain multiple values (e.g., "client-host, proxy-host"). The first
// value is the original client-facing value.
func firstCSV(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		return strings.TrimSpace(v[:i])
	}
	return strings.TrimSpace(v)
}

// -----------------------------------------------------------------------
// AuthorizationHandler — GET /oauth/authorize
// -----------------------------------------------------------------------

// AuthorizationHandler handles the OAuth 2.1 authorization endpoint.
// It validates the PKCE challenge, generates an authorization code,
// and redirects the client back to redirect_uri.
type AuthorizationHandler struct {
	cfg      OAuthConfig
	store    *CodeStore
	registry *ClientRegistry
	logger   *slog.Logger
}

// NewAuthorizationHandler creates a new AuthorizationHandler.
// If registry is non-nil, dynamically registered clients are accepted
// in addition to the static client ID from cfg.
func NewAuthorizationHandler(cfg OAuthConfig, store *CodeStore, registry *ClientRegistry) *AuthorizationHandler {
	return &AuthorizationHandler{
		cfg:      cfg,
		store:    store,
		registry: registry,
		logger:   slog.Default(),
	}
}

// resolveClient validates the client_id and redirect_uri combination.
// Returns the resolved redirect URI or an error description string.
func (h *AuthorizationHandler) resolveClient(clientID, redirectURI string) (string, string) {
	if clientID == h.cfg.ClientID {
		if redirectURI != "" && redirectURI != h.cfg.RedirectURI {
			return "", errMsgRedirectURIMismatch
		}
		// Always return the trusted configured URI, not the user-supplied value.
		return h.cfg.RedirectURI, ""
	}
	if h.registry != nil {
		client, ok := h.registry.Lookup(clientID)
		if !ok {
			return "", errMsgInvalidClientID
		}
		if redirectURI == "" {
			return "", errMsgRedirectURIRequired
		}
		// Return the registered URI from the trusted list, not user input.
		registered, matched := client.MatchRedirectURI(redirectURI)
		if !matched {
			return "", errMsgRedirectURIMismatch
		}
		return registered, ""
	}
	return "", errMsgInvalidClientID
}

// ServeHTTP implements http.Handler.
func (h *AuthorizationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// OAuth 2.1 authorization endpoints must only respond to GET.
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()

	redirectURI, errMsg := h.resolveClient(q.Get("client_id"), q.Get("redirect_uri"))
	if errMsg != "" {
		http.Error(w, errMsg, http.StatusBadRequest)
		return
	}

	// Defense-in-depth: validate the resolved redirect URI has a safe scheme.
	// resolveClient already validates against registered URIs, but this
	// explicit check prevents open-redirect if a malformed URI is registered.
	if !isAllowedRedirectURI(redirectURI) {
		h.logger.Error("unsafe redirect URI scheme", "uri", redirectURI)
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}

	clientID := q.Get("client_id")

	// Require response_type=code (only supported grant).
	if q.Get("response_type") != "code" {
		http.Error(w, "response_type must be 'code'", http.StatusBadRequest)
		return
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

	target, err := url.Parse(redirectURI)
	if err != nil {
		h.logger.Error("invalid registered redirect URI", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	params := target.Query()
	params.Set("code", code)
	if state := q.Get("state"); state != "" {
		params.Set("state", state)
	}
	target.RawQuery = params.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
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
	redirectURI := r.FormValue("redirect_uri")

	if code == "" || verifier == "" || clientID == "" {
		http.Error(w, "missing required parameters", http.StatusBadRequest)
		return
	}

	// Validate the authorization code and PKCE verifier.
	entry, errMsg := validateCodeExchange(h.store, code, verifier, clientID, redirectURI)
	if errMsg != "" {
		writeOAuthError(w, "invalid_grant", errMsg)
		return
	}

	// Use pre-signed token from OIDC flow if available, otherwise issue via TokenIssuer.
	token, err := h.resolveToken(entry, clientID)
	if err != nil {
		h.logger.Error("token issuer failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.logger.Info("access token issued", "client_id", clientID)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

// validateCodeExchange consumes the authorization code and validates client_id, redirect_uri, and PKCE.
// Returns the code entry on success, or an error message string on failure.
func validateCodeExchange(store *CodeStore, code, verifier, clientID, redirectURI string) (CodeEntry, string) {
	entry, ok := store.Consume(code)
	if !ok {
		return CodeEntry{}, "authorization code is invalid or expired"
	}
	if entry.ClientID != clientID {
		return CodeEntry{}, "client_id mismatch"
	}
	if redirectURI != "" && redirectURI != entry.RedirectURI {
		return CodeEntry{}, "redirect_uri mismatch"
	}
	if !verifyPKCE(verifier, entry.CodeChallenge) {
		return CodeEntry{}, "PKCE verification failed"
	}
	return entry, ""
}

// resolveToken returns a pre-signed token from the OIDC flow or issues a new one via the TokenIssuer.
func (h *TokenHandler) resolveToken(entry CodeEntry, clientID string) (string, error) {
	if entry.Token != "" {
		return entry.Token, nil
	}
	claims := map[string]any{
		"client_id": clientID,
		"iat":       time.Now().Unix(),
	}
	return h.issuer.Issue(claims)
}

// -----------------------------------------------------------------------
// BearerMiddleware — wraps MCP HTTP handlers
// -----------------------------------------------------------------------

// BearerMiddleware enforces Bearer token authentication on HTTP handlers.
// Unauthenticated requests receive a 401 with Metadata so MCP clients
// can start the OAuth flow. The metadata URLs are derived from the request's
// Host header so that tenant-scoped subdomains receive correct endpoints.
type BearerMiddleware struct {
	validator       BearerValidator
	fallbackBaseURL string
	logger          *slog.Logger
}

// NewBearerMiddleware creates a new BearerMiddleware. The fallbackBaseURL is
// used when the request's Host header cannot be determined.
func NewBearerMiddleware(validator BearerValidator, fallbackBaseURL string) *BearerMiddleware {
	return &BearerMiddleware{
		validator:       validator,
		fallbackBaseURL: fallbackBaseURL,
		logger:          slog.Default(),
	}
}

// Handler wraps an http.Handler, enforcing Bearer token authentication.
// When the validator implements ClaimsBearerValidator (JWKS mode), the
// authenticated tenant ID from the JWT's x-tenant-id claim is injected
// into the request context via tenant.WithTenant. This allows downstream
// tool handlers to make tenant-scoped gRPC calls without requiring the
// caller to pass a tenant_id parameter explicitly.
func (m *BearerMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := extractBearerFromHeader(r)
		if err != nil {
			m.writeAuthRequired(w, r)
			return
		}

		// If the validator supports claims extraction, use it to get tenant context.
		if claimsValidator, ok := m.validator.(ClaimsBearerValidator); ok {
			tenantID, validateErr := claimsValidator.ValidateBearerWithTenant(token)
			if validateErr != nil {
				m.logger.Debug("bearer token validation failed",
					"error", validateErr, "path", r.URL.Path)
				m.writeAuthRequired(w, r)
				return
			}
			if tenantID != "" {
				ctx := tenant.WithTenant(r.Context(), tenant.TenantID(tenantID))
				r = r.WithContext(ctx)
			}
		} else if err := m.validator.ValidateBearer(token); err != nil {
			m.logger.Debug("bearer token validation failed",
				"error", err, "path", r.URL.Path)
			m.writeAuthRequired(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// writeAuthRequired writes a 401 response with auth metadata derived from the request.
func (m *BearerMiddleware) writeAuthRequired(w http.ResponseWriter, r *http.Request) {
	base := baseURLFromRequest(r, m.fallbackBaseURL)
	meta := Metadata{
		AuthorizationURL: base + "/oauth/authorize",
		TokenURL:         base + "/oauth/token",
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="meridian-mcp"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Host, X-Forwarded-Host, X-Forwarded-Proto")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(meta)
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
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", ErrInvalidBearerToken
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", ErrInvalidBearerToken
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		return "", ErrInvalidBearerToken
	}
	return token, nil
}

// writeOAuthError writes an RFC 6749 §5.2 error response.
// Token endpoint errors use HTTP 400 Bad Request (not 401).
func writeOAuthError(w http.ResponseWriter, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}
