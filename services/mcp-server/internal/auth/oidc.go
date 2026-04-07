//meridian:large-file -- OIDC handler + state store + helpers form a cohesive auth flow unit.

// Package auth provides the consent-based OAuth 2.1 flow for the MCP server.
//
// The MCP server acts as an OAuth 2.1 authorization server for MCP clients
// (e.g., Claude.ai). Authentication and consent are handled by the BFF:
//
//  1. MCP client POSTs to /mcp → receives 401 with auth metadata
//  2. MCP client opens browser → /oauth/authorize
//  3. /oauth/authorize stores PKCE state, redirects to UI consent page
//  4. User authenticates via the BFF (existing session or fresh login)
//  5. User approves MCP scopes; BFF issues a short-lived consent code
//  6. BFF redirects to /oauth/callback with consent code + state
//  7. MCP server consumes consent code, signs a Meridian JWT with tenant context
//  8. MCP server redirects to MCP client's redirect_uri with auth code
//  9. MCP client exchanges auth code for JWT at /oauth/token
//
// The JWT is signed with the same key as the BFF (shared JWT_SIGNING_KEY),
// so MCP-issued tokens are validated by the same JWKS endpoint. If the user
// is already authenticated via the UI, their BFF-issued JWT is also accepted.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
)

var (
	errOIDCConsentStoreRequired = errors.New("oidc handler: consent store is required")
	errOIDCStateStoreRequired   = errors.New("oidc handler: state store is required")
	errOIDCCodeStoreRequired    = errors.New("oidc handler: code store is required")
	errOIDCSignerRequired       = errors.New("oidc handler: JWT signer is required")
	errOIDCLoggerRequired       = errors.New("oidc handler: logger is required")
	errOIDCStateFull            = errors.New("oidc state store is full")
	errTenantRequired           = errors.New("tenant identification required: use a tenant subdomain or configure MCP_DEFAULT_TENANT_SLUG")
)

const (
	// oidcStateTTL is how long OIDC flow state remains valid.
	oidcStateTTL = 10 * time.Minute
	// oidcStateBytes is the number of random bytes in a state token.
	oidcStateBytes = 32
	// oidcStateEvictInterval is how often expired state entries are swept.
	oidcStateEvictInterval = 5 * time.Minute
	// oidcStateMaxEntries caps the number of in-flight OIDC authorizations
	// to prevent memory exhaustion from unauthenticated requests.
	oidcStateMaxEntries = 10_000

	// defaultTokenTTL is the default JWT token lifetime.
	defaultTokenTTL = time.Hour

	schemeHTTP  = "http"
	schemeHTTPS = "https"

	// Shared OAuth error messages used by both static and dynamic client validation.
	errMsgInvalidClientID     = "invalid client_id"
	errMsgRedirectURIMismatch = "redirect_uri does not match registered value"
	errMsgRedirectURIRequired = "redirect_uri is required for dynamic clients"
)

// OIDCFlowState holds the state for an in-progress OIDC authorization flow.
// It bridges the MCP client's OAuth request with the consent flow.
type OIDCFlowState struct {
	// MCP client's PKCE code challenge (from the original /oauth/authorize request).
	MCPCodeChallenge string
	// MCP client's OAuth client ID.
	MCPClientID string
	// MCP client's redirect URI (where to send the auth code after authentication).
	MCPRedirectURI string
	// MCP client's state parameter (forwarded back after authentication).
	MCPState string
	// TenantSlug extracted from the request subdomain.
	TenantSlug string
	// RequestedScopes are the OAuth scopes requested by the MCP client.
	RequestedScopes []string
	// IssuedAt is when this state was created.
	IssuedAt time.Time
}

// OIDCStateStore is a thread-safe in-memory store for OIDC flow state.
type OIDCStateStore struct {
	mu        sync.Mutex
	entries   map[string]OIDCFlowState
	stop      chan struct{}
	closeOnce sync.Once
}

// NewOIDCStateStore creates an empty state store and starts the background eviction goroutine.
func NewOIDCStateStore() *OIDCStateStore {
	s := &OIDCStateStore{
		entries: make(map[string]OIDCFlowState),
		stop:    make(chan struct{}),
	}
	go s.evictLoop()
	return s
}

// Close stops the background eviction goroutine.
func (s *OIDCStateStore) Close() {
	s.closeOnce.Do(func() { close(s.stop) })
}

func (s *OIDCStateStore) evictLoop() {
	ticker := time.NewTicker(oidcStateEvictInterval)
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

func (s *OIDCStateStore) evictExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.entries {
		if time.Since(entry.IssuedAt) > oidcStateTTL {
			delete(s.entries, key)
		}
	}
}

// Store saves an OIDC flow state entry and returns a key for retrieval.
// Returns errOIDCStateFull if the store has reached its capacity limit.
func (s *OIDCStateStore) Store(entry OIDCFlowState) (string, error) {
	key, err := generateRandomToken(oidcStateBytes)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) >= oidcStateMaxEntries {
		return "", errOIDCStateFull
	}
	entry.RequestedScopes = append([]string(nil), entry.RequestedScopes...)
	s.entries[key] = entry
	return key, nil
}

// Consume atomically retrieves and deletes an OIDC flow state entry.
func (s *OIDCStateStore) Consume(key string) (OIDCFlowState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		return OIDCFlowState{}, false
	}
	delete(s.entries, key)
	if time.Since(entry.IssuedAt) > oidcStateTTL {
		return OIDCFlowState{}, false
	}
	return entry, true
}

// Delete removes an OIDC flow state entry by key without returning it.
// This is used by the consent handler to clean up state on deny.
func (s *OIDCStateStore) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
}

// PeekInfoResult holds the fields returned by PeekInfo.
type PeekInfoResult struct {
	ClientID    string
	RedirectURI string
	Scopes      []string
	MCPState    string // Original MCP client state (not the internal key).
	TenantSlug  string
}

// PeekInfo returns selected fields from an OIDC flow state entry without
// consuming it. Expired entries are cleaned up and reported as not found.
func (s *OIDCStateStore) PeekInfo(key string) (PeekInfoResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.entries[key]
	if !exists {
		return PeekInfoResult{}, false
	}
	if time.Since(entry.IssuedAt) > oidcStateTTL {
		delete(s.entries, key)
		return PeekInfoResult{}, false
	}
	return PeekInfoResult{
		ClientID:    entry.MCPClientID,
		RedirectURI: entry.MCPRedirectURI,
		Scopes:      append([]string(nil), entry.RequestedScopes...),
		MCPState:    entry.MCPState,
		TenantSlug:  entry.TenantSlug,
	}, true
}

// OIDCHandler manages the consent-based OAuth 2.1 flow.
type OIDCHandler struct {
	oauthCfg          OAuthConfig
	stateStore        *OIDCStateStore
	codeStore         *CodeStore
	consentStore      ConsentCodeConsumer
	registry          *ClientRegistry
	signer            *platformauth.JWTSigner
	tokenTTL          time.Duration
	defaultTenantSlug string
	baseDomain        string
	baseURL           string
	logger            *slog.Logger
}

// OIDCHandlerConfig holds configuration for creating an OIDCHandler.
type OIDCHandlerConfig struct {
	OAuth      OAuthConfig
	StateStore *OIDCStateStore
	CodeStore  *CodeStore
	Registry   *ClientRegistry
	Signer     *platformauth.JWTSigner
	// ConsentStore is the shared consent code store used by both the BFF
	// (which issues consent codes after user approval) and the OIDC handler
	// (which consumes them in HandleCallback).
	ConsentStore ConsentCodeConsumer
	TokenTTL     time.Duration
	// DefaultTenantSlug is used when no tenant subdomain is present in the
	// request. In single-tenant deployments (e.g., demo), set this to the
	// tenant's slug so bare-domain requests work. When empty in multi-tenant
	// mode, bare-domain requests fail closed with HTTP 400.
	DefaultTenantSlug string
	// BaseURL is the public-facing base URL of the MCP server
	// (e.g., "https://demo.meridianhub.cloud"). Used to construct the consent
	// page redirect URL.
	BaseURL    string
	BaseDomain string
	Logger     *slog.Logger
}

// NewOIDCHandler creates a handler for the consent-based OAuth 2.1 flow.
func NewOIDCHandler(cfg OIDCHandlerConfig) (*OIDCHandler, error) {
	if cfg.StateStore == nil {
		return nil, errOIDCStateStoreRequired
	}
	if cfg.CodeStore == nil {
		return nil, errOIDCCodeStoreRequired
	}
	if cfg.ConsentStore == nil {
		return nil, errOIDCConsentStoreRequired
	}
	if cfg.Signer == nil {
		return nil, errOIDCSignerRequired
	}
	if cfg.Logger == nil {
		return nil, errOIDCLoggerRequired
	}

	ttl := cfg.TokenTTL
	if ttl == 0 {
		ttl = defaultTokenTTL
	}

	return &OIDCHandler{
		oauthCfg:          cfg.OAuth,
		stateStore:        cfg.StateStore,
		codeStore:         cfg.CodeStore,
		consentStore:      cfg.ConsentStore,
		registry:          cfg.Registry,
		signer:            cfg.Signer,
		tokenTTL:          ttl,
		defaultTenantSlug: cfg.DefaultTenantSlug,
		baseDomain:        cfg.BaseDomain,
		baseURL:           cfg.BaseURL,
		logger:            cfg.Logger,
	}, nil
}

// validateAuthorizeClient validates the client_id and redirect_uri parameters
// for an authorize request. Returns the resolved redirect URI or an error message.
func (h *OIDCHandler) validateAuthorizeClient(clientID, redirectURI string) (string, string) {
	if clientID == h.oauthCfg.ClientID {
		if redirectURI == "" {
			return h.oauthCfg.RedirectURI, ""
		}
		if redirectURI != h.oauthCfg.RedirectURI {
			return "", errMsgRedirectURIMismatch
		}
		// Always return the trusted configured URI, not the user-supplied value.
		return h.oauthCfg.RedirectURI, ""
	}

	if h.registry == nil {
		return "", errMsgInvalidClientID
	}
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

// writeRedirectError writes the appropriate HTTP error for a redirect build failure.
// errOIDCStateFull is transient backpressure and returns 503 with Retry-After: 30;
// all other errors return 500.
func (h *OIDCHandler) writeRedirectError(w http.ResponseWriter, err error) {
	if errors.Is(err, errOIDCStateFull) {
		h.logger.Warn("oidc: state store at capacity, rejecting new authorization request")
		w.Header().Set("Retry-After", "30")
		http.Error(w, "service temporarily unavailable, retry later", http.StatusServiceUnavailable)
		return
	}
	h.logger.Error("oidc: failed to build consent redirect", "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// HandleAuthorize handles GET /oauth/authorize from the MCP client.
// It stores the MCP client's PKCE state and redirects to the UI consent page.
func (h *OIDCHandler) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()

	clientID := q.Get("client_id")
	redirectURI, errMsg := h.validateAuthorizeClient(clientID, q.Get("redirect_uri"))
	if errMsg != "" {
		http.Error(w, errMsg, http.StatusBadRequest)
		return
	}

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

	// Validate redirect_uri scheme to prevent open-redirect attacks.
	// HTTPS is required for production; HTTP is allowed only for localhost.
	if !isAllowedRedirectURI(redirectURI) {
		http.Error(w, "redirect_uri must use https (or http://localhost for development)", http.StatusBadRequest)
		return
	}

	// Extract tenant slug from request subdomain, falling back to default.
	tenantSlug, err := h.resolveTenantSlug(r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Capture requested OAuth scopes from the MCP client (space-delimited per RFC 6749).
	// Only allow scopes with the "mcp:" prefix to prevent arbitrary scope injection.
	requestedScopes := filterAllowedScopes(strings.Fields(strings.TrimSpace(q.Get("scope"))))
	if len(requestedScopes) == 0 {
		requestedScopes = []string{"mcp:default"}
	}

	redirectURL, err := h.buildConsentRedirect(challenge, clientID, redirectURI, q.Get("state"), tenantSlug, requestedScopes)
	if err != nil {
		h.writeRedirectError(w, err)
		return
	}
	h.logger.Info("oidc: redirecting to consent page",
		"tenant", tenantSlug,
		"client_id", clientID)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// HandleCallback handles GET /oauth/callback.
// It consumes the consent code issued by the BFF after user approval,
// signs a Meridian JWT, and redirects to the MCP client.
func (h *OIDCHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	h.handleConsentCallback(w, r, q.Get("state"), q.Get("code"))
}

// resolveTenantSlug extracts the tenant slug from the request host subdomain,
// falling back to the default.
func (h *OIDCHandler) resolveTenantSlug(host string) (string, error) {
	tenantSlug := extractSubdomain(host, h.baseDomain)
	if tenantSlug == "" && h.defaultTenantSlug != "" {
		tenantSlug = h.defaultTenantSlug
		h.logger.Debug("oidc: using default tenant slug", "slug", tenantSlug)
	}
	if tenantSlug == "" {
		h.logger.Warn("oidc: no tenant subdomain and no default configured - fail closed")
		return "", errTenantRequired
	}
	return tenantSlug, nil
}

// issueCodeAndRedirect signs a Meridian JWT, generates an MCP authorization code, stores it, and returns the redirect URL.
func (h *OIDCHandler) issueCodeAndRedirect(email, tenantID string, scopes []string, flowState OIDCFlowState) (string, error) {
	claims := map[string]interface{}{
		"sub":         email,
		"email":       email,
		"x-tenant-id": tenantID,
		"scopes":      scopes,
	}
	tokenStr, err := h.signer.SignClaims(claims, h.tokenTTL)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	mcpCode, err := generateCode()
	if err != nil {
		return "", fmt.Errorf("generate auth code: %w", err)
	}

	h.codeStore.StoreWithToken(mcpCode, CodeEntry{
		CodeChallenge: flowState.MCPCodeChallenge,
		ClientID:      flowState.MCPClientID,
		RedirectURI:   flowState.MCPRedirectURI,
		IssuedAt:      time.Now(),
	}, tokenStr)

	h.logger.Info("oidc: authentication successful",
		"tenant", flowState.TenantSlug)

	return buildAuthRedirect(flowState.MCPRedirectURI, mcpCode, flowState.MCPState)
}

// buildAuthRedirect constructs the redirect URL with authorization code and optional state.
func buildAuthRedirect(redirectURI, code, state string) (string, error) {
	target, err := url.Parse(redirectURI)
	if err != nil {
		return "", err
	}
	params := target.Query()
	params.Set("code", code)
	if state != "" {
		params.Set("state", state)
	}
	target.RawQuery = params.Encode()
	return target.String(), nil
}

// generateRandomToken generates a cryptographically random URL-safe token.
func generateRandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// isAllowedRedirectURI validates that a redirect URI is safe to redirect to.
// HTTPS is required for production; HTTP is allowed only for localhost (development).
// Rejects opaque or hostless forms (e.g., "https:evil.com", "https:///cb")
// that could bypass scheme-only validation due to URL parsing differences.
func isAllowedRedirectURI(uri string) bool {
	parsed, err := url.Parse(uri)
	if err != nil {
		return false
	}
	// Reject opaque URIs (e.g., "https:evil.com") and empty-host URIs
	// (e.g., "https:///cb") which browsers may resolve unexpectedly.
	if parsed.Opaque != "" || parsed.Hostname() == "" {
		return false
	}
	if parsed.Scheme == schemeHTTPS {
		return true
	}
	if parsed.Scheme == schemeHTTP {
		host := parsed.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	}
	return false
}

// filterAllowedScopes returns only scopes with the "mcp:" prefix,
// preventing clients from injecting arbitrary scope strings into signed JWTs.
func filterAllowedScopes(scopes []string) []string {
	var allowed []string
	for _, s := range scopes {
		if strings.HasPrefix(s, "mcp:") {
			allowed = append(allowed, s)
		}
	}
	return allowed
}
