// Package auth provides OIDC integration for the MCP server's OAuth 2.1 flow.
//
// The MCP server acts as an OAuth 2.1 authorization server for MCP clients
// (e.g., Claude.ai). Authentication is delegated to Dex via standard OIDC:
//
//  1. MCP client POSTs to /mcp → receives 401 with auth metadata
//  2. MCP client opens browser → /oauth/authorize
//  3. /oauth/authorize stores PKCE state, redirects to Dex
//  4. User authenticates via Dex (Google, GitHub, password, etc.)
//  5. Dex redirects to /oauth/callback with authorization code
//  6. MCP server exchanges code for ID token, extracts email
//  7. MCP server signs a Meridian JWT with tenant context
//  8. MCP server redirects to MCP client's redirect_uri with auth code
//  9. MCP client exchanges auth code for JWT at /oauth/token
//
// The JWT is signed with the same key as the BFF (shared JWT_SIGNING_KEY),
// so MCP-issued tokens are validated by the same JWKS endpoint. If the user
// is already authenticated via the UI, their BFF-issued JWT is also accepted.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
)

var (
	errOIDCDexIssuerRequired  = errors.New("oidc handler: dex issuer URL is required")
	errOIDCClientIDRequired   = errors.New("oidc handler: client ID is required")
	errOIDCCallbackRequired   = errors.New("oidc handler: callback URL is required")
	errOIDCStateStoreRequired = errors.New("oidc handler: state store is required")
	errOIDCCodeStoreRequired  = errors.New("oidc handler: code store is required")
	errOIDCSignerRequired     = errors.New("oidc handler: JWT signer is required")
	errOIDCLoggerRequired     = errors.New("oidc handler: logger is required")
	errDexTokenError          = errors.New("dex token error")
	errDexBadStatus           = errors.New("dex token endpoint returned non-200 status")
	errDexEmptyIDToken        = errors.New("dex returned empty id_token")
	errOIDCStateFull          = errors.New("oidc state store is full")
	errInvalidJWTFormat       = errors.New("invalid JWT format")
	errEmailClaimMissing      = errors.New("email claim missing from ID token")
	errTenantRequired         = errors.New("tenant identification required: use a tenant subdomain or configure MCP_DEFAULT_TENANT_SLUG")
	errTenantResolutionFailed = errors.New("tenant slug resolution failed")
)

const (
	// oidcStateTTL is how long OIDC flow state remains valid.
	oidcStateTTL = 10 * time.Minute
	// oidcStateBytes is the number of random bytes in a state token.
	oidcStateBytes = 32
	// oidcCodeVerifierBytes is the number of random bytes for PKCE code verifier.
	oidcCodeVerifierBytes = 32
	// oidcMaxTokenResponseBytes limits the Dex token response body size.
	oidcMaxTokenResponseBytes = 64 * 1024
	// oidcHTTPTimeout is the timeout for Dex token exchange.
	oidcHTTPTimeout = 10 * time.Second
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

// OIDCConfig holds configuration for the Dex OIDC integration.
type OIDCConfig struct {
	// DexIssuerURL is the Dex issuer URL (e.g., "https://demo.meridianhub.cloud/dex").
	DexIssuerURL string
	// ClientID is the OAuth2 client ID registered with Dex.
	ClientID string
	// CallbackURL is the MCP server's OIDC callback URL
	// (e.g., "https://demo.meridianhub.cloud/oauth/callback").
	CallbackURL string
}

// OIDCFlowState holds the state for an in-progress OIDC authorization flow.
// It bridges the MCP client's OAuth request with the Dex OIDC flow.
type OIDCFlowState struct {
	// MCP client's PKCE code challenge (from the original /oauth/authorize request).
	MCPCodeChallenge string
	// MCP client's OAuth client ID.
	MCPClientID string
	// MCP client's redirect URI (where to send the auth code after authentication).
	MCPRedirectURI string
	// MCP client's state parameter (forwarded back after authentication).
	MCPState string
	// PKCE code verifier for the Dex authorization code exchange.
	DexCodeVerifier string
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

// PeekInfo returns selected fields from an OIDC flow state entry without
// consuming it. Expired entries are cleaned up and reported as not found.
func (s *OIDCStateStore) PeekInfo(key string) (clientID, redirectURI string, scopes []string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.entries[key]
	if !exists {
		return "", "", nil, false
	}
	if time.Since(entry.IssuedAt) > oidcStateTTL {
		delete(s.entries, key)
		return "", "", nil, false
	}
	scopes = append([]string(nil), entry.RequestedScopes...)
	return entry.MCPClientID, entry.MCPRedirectURI, scopes, true
}

// ConsentEntry holds the state stored alongside a consent code. This mirrors
// the ConsentCodeEntry from api-gateway but avoids a direct import dependency
// on that package (which pulls in otel transitive dependencies).
type ConsentEntry struct {
	Email          string
	TenantID       string
	TenantSlug     string
	MCPState       string
	ClientID       string
	ApprovedScopes []string
}

// ConsentCodeConsumer can consume a consent code exactly once. The canonical
// implementation is gateway.ConsentCodeStore.
type ConsentCodeConsumer interface {
	// Consume atomically retrieves and deletes a consent code.
	// Returns (entry, true) if the code exists and has not expired.
	Consume(code string) (ConsentEntry, bool)
}

// ConsentCodeConsumerFunc is an adapter to use ordinary functions as
// ConsentCodeConsumer implementations (see also: http.HandlerFunc pattern).
type ConsentCodeConsumerFunc func(code string) (ConsentEntry, bool)

// Consume calls f(code).
func (f ConsentCodeConsumerFunc) Consume(code string) (ConsentEntry, bool) { return f(code) }

// TenantSlugResolver resolves a tenant slug (e.g., "acme") to its canonical
// UUID. This ensures the x-tenant-id JWT claim contains a UUID consistent
// with BFF-issued tokens, not the raw slug string.
type TenantSlugResolver interface {
	ResolveSlug(ctx context.Context, slug string) (string, error)
}

// OIDCHandler manages the OIDC authorization flow with Dex.
type OIDCHandler struct {
	cfg               OIDCConfig
	oauthCfg          OAuthConfig
	stateStore        *OIDCStateStore
	codeStore         *CodeStore
	consentStore      ConsentCodeConsumer
	registry          *ClientRegistry
	signer            *platformauth.JWTSigner
	tenantResolver    TenantSlugResolver
	tokenTTL          time.Duration
	defaultTenantSlug string
	baseDomain        string
	baseURL           string
	dexAuthBaseURL    string // External Dex base URL for browser redirects (derived from BaseURL + DexIssuerURL path).
	httpClient        *http.Client
	logger            *slog.Logger
}

// OIDCHandlerConfig holds configuration for creating an OIDCHandler.
type OIDCHandlerConfig struct {
	OIDC       OIDCConfig
	OAuth      OAuthConfig
	StateStore *OIDCStateStore
	CodeStore  *CodeStore
	Registry   *ClientRegistry
	Signer     *platformauth.JWTSigner
	// ConsentStore is the shared consent code store used by both the BFF
	// (which issues consent codes after user approval) and the OIDC handler
	// (which consumes them in HandleCallback). When nil, HandleCallback
	// falls back to Dex token exchange (legacy flow).
	ConsentStore ConsentCodeConsumer
	// TenantResolver resolves tenant slugs to UUIDs for JWT claims.
	// When nil, the raw slug is used as-is (dev/test fallback).
	TenantResolver TenantSlugResolver
	TokenTTL       time.Duration
	// DefaultTenantSlug is used when no tenant subdomain is present in the
	// request. In single-tenant deployments (e.g., demo), set this to the
	// tenant's slug so bare-domain requests work. When empty in multi-tenant
	// mode, bare-domain requests fail closed with HTTP 400.
	DefaultTenantSlug string
	// BaseURL is the public-facing base URL of the MCP server
	// (e.g., "https://demo.meridianhub.cloud"). Used to construct browser-facing
	// Dex redirect URLs when DexIssuerURL is an internal Docker hostname.
	BaseURL    string
	BaseDomain string
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// NewOIDCHandler creates a handler for the OIDC-backed OAuth 2.1 flow.
func NewOIDCHandler(cfg OIDCHandlerConfig) (*OIDCHandler, error) {
	if cfg.OIDC.DexIssuerURL == "" {
		return nil, errOIDCDexIssuerRequired
	}
	// Parse and validate the Dex issuer URL scheme. HTTPS is required because
	// the OIDC callback trusts the Dex token response without signature
	// verification (server-to-server over TLS). HTTP is allowed for local
	// development (e.g., http://dex:5556/dex) but logged as a warning.
	issuerURL, err := url.Parse(cfg.OIDC.DexIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc handler: invalid dex issuer URL: %w", err)
	}
	if issuerURL.Scheme != schemeHTTPS && issuerURL.Scheme != schemeHTTP {
		return nil, fmt.Errorf("oidc handler: dex issuer URL must use http or https: %w", errOIDCDexIssuerRequired)
	}
	if cfg.OIDC.ClientID == "" {
		return nil, errOIDCClientIDRequired
	}
	if cfg.OIDC.CallbackURL == "" {
		return nil, errOIDCCallbackRequired
	}
	if cfg.StateStore == nil {
		return nil, errOIDCStateStoreRequired
	}
	if cfg.CodeStore == nil {
		return nil, errOIDCCodeStoreRequired
	}
	if cfg.Signer == nil {
		return nil, errOIDCSignerRequired
	}
	if cfg.Logger == nil {
		return nil, errOIDCLoggerRequired
	}

	if issuerURL.Scheme == schemeHTTP {
		cfg.Logger.Warn("oidc: Dex issuer URL uses HTTP — ID token integrity depends on network trust; use HTTPS in production",
			"issuer_url", cfg.OIDC.DexIssuerURL)
	}

	dexAuthBaseURL := resolveDexAuthBaseURL(cfg.BaseURL, issuerURL, cfg.OIDC.DexIssuerURL, cfg.Logger)

	ttl := cfg.TokenTTL
	if ttl == 0 {
		ttl = defaultTokenTTL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: oidcHTTPTimeout}
	}

	return &OIDCHandler{
		cfg:               cfg.OIDC,
		oauthCfg:          cfg.OAuth,
		stateStore:        cfg.StateStore,
		codeStore:         cfg.CodeStore,
		consentStore:      cfg.ConsentStore,
		registry:          cfg.Registry,
		signer:            cfg.Signer,
		tenantResolver:    cfg.TenantResolver,
		tokenTTL:          ttl,
		defaultTenantSlug: cfg.DefaultTenantSlug,
		baseDomain:        cfg.BaseDomain,
		baseURL:           cfg.BaseURL,
		dexAuthBaseURL:    dexAuthBaseURL,
		httpClient:        httpClient,
		logger:            cfg.Logger,
	}, nil
}

// resolveDexAuthBaseURL computes the browser-facing Dex base URL. When
// DexIssuerURL is an internal hostname (e.g., http://dex:5556/dex), browsers
// can't reach it. If publicBaseURL is set, we combine its scheme+host with
// the path from the internal issuer URL so the browser is redirected to the
// external reverse proxy instead.
func resolveDexAuthBaseURL(publicBaseURL string, issuerURL *url.URL, dexIssuerURL string, logger *slog.Logger) string {
	if publicBaseURL == "" {
		return dexIssuerURL
	}
	parsed, err := url.Parse(publicBaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return dexIssuerURL
	}
	external := *parsed
	external.Path = issuerURL.Path
	result := external.String()
	logger.Info("oidc: using external Dex URL for browser redirects",
		"dex_auth_base_url", result,
		"dex_internal_url", dexIssuerURL)
	return result
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

// writeDexRedirectError writes the appropriate HTTP error for a buildDexRedirect failure.
// errOIDCStateFull is transient backpressure and returns 503 with Retry-After: 30;
// all other errors return 500.
func (h *OIDCHandler) writeDexRedirectError(w http.ResponseWriter, err error) {
	if errors.Is(err, errOIDCStateFull) {
		h.logger.Warn("oidc: state store at capacity, rejecting new authorization request")
		w.Header().Set("Retry-After", "30")
		http.Error(w, "service temporarily unavailable, retry later", http.StatusServiceUnavailable)
		return
	}
	h.logger.Error("oidc: failed to build Dex redirect", "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

// ConsentInfoResponse is the JSON response returned by HandleConsentInfo.
type ConsentInfoResponse struct {
	ClientID    string   `json:"client_id"`
	ClientName  string   `json:"client_name"`
	RedirectURI string   `json:"redirect_uri"`
	Scopes      []string `json:"scopes"`
	IsDynamic   bool     `json:"is_dynamic"`
}

// HandleConsentInfo handles GET /oauth/consent-info. It returns metadata about
// the MCP client that initiated the authorization flow so the UI consent page
// can display it to the user before they approve or deny.
func (h *OIDCHandler) HandleConsentInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	clientID := q.Get("client_id")
	mcpState := q.Get("mcp_state")
	if clientID == "" || mcpState == "" {
		http.Error(w, "client_id and mcp_state are required", http.StatusBadRequest)
		return
	}

	stateClientID, redirectURI, scopes, ok := h.stateStore.PeekInfo(mcpState)
	if !ok {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	if stateClientID != clientID {
		http.Error(w, "client_id mismatch", http.StatusBadRequest)
		return
	}

	resp := ConsentInfoResponse{
		ClientID:    clientID,
		RedirectURI: redirectURI,
		Scopes:      scopes,
	}

	// Resolve client name: static client gets a fixed name, dynamic clients
	// use their registered client_name.
	if clientID == h.oauthCfg.ClientID {
		resp.ClientName = "Meridian CLI"
		resp.IsDynamic = false
	} else if h.registry != nil {
		if client, found := h.registry.Lookup(clientID); found {
			resp.IsDynamic = true
			resp.ClientName = client.ClientName
			if resp.ClientName == "" {
				resp.ClientName = "Unknown Application"
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleAuthorize handles GET /oauth/authorize from the MCP client.
// It stores the MCP client's PKCE state and redirects to Dex for authentication.
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
	requestedScopes := strings.Fields(strings.TrimSpace(q.Get("scope")))
	if len(requestedScopes) == 0 {
		requestedScopes = []string{"mcp:default"}
	}

	// When the consent store is configured, redirect to the UI consent page
	// instead of Dex. The consent page handles authentication + authorization
	// approval in one step.
	if h.consentStore != nil {
		redirectURL, err := h.buildConsentRedirect(challenge, clientID, redirectURI, q.Get("state"), tenantSlug, requestedScopes)
		if err != nil {
			h.writeDexRedirectError(w, err)
			return
		}
		h.logger.Info("oidc: redirecting to consent page",
			"tenant", tenantSlug,
			"client_id", clientID)
		http.Redirect(w, r, redirectURL, http.StatusFound)
		return
	}

	// Legacy path: redirect to Dex for authentication.
	redirectURL, err := h.buildDexRedirect(challenge, clientID, redirectURI, q.Get("state"), tenantSlug, requestedScopes)
	if err != nil {
		h.writeDexRedirectError(w, err)
		return
	}

	h.logger.Info("oidc: initiating Dex authorization",
		"tenant", tenantSlug,
		"client_id", clientID)

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// HandleCallback handles GET /oauth/callback.
// When the consent store is configured, it consumes a consent code issued by
// the BFF after user approval. Otherwise it falls back to Dex token exchange.
// In both cases it signs a Meridian JWT and redirects to the MCP client.
func (h *OIDCHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	stateKey := q.Get("state")
	code := q.Get("code")

	if h.consentStore != nil {
		h.handleConsentCallback(w, r, stateKey, code)
		return
	}
	h.handleDexCallback(w, r, stateKey, code)
}

// handleConsentCallback processes the consent-code callback path. The BFF
// issues a consent code after user approval; this method consumes it,
// cross-validates bindings, and issues the MCP authorization code.
func (h *OIDCHandler) handleConsentCallback(w http.ResponseWriter, r *http.Request, stateKey, code string) {
	if stateKey == "" || code == "" {
		http.Error(w, "missing state or code parameter", http.StatusBadRequest)
		return
	}

	consentEntry, ok := h.consentStore.Consume(code)
	if !ok {
		http.Error(w, "invalid or expired consent code", http.StatusBadRequest)
		return
	}

	flowState, ok := h.stateStore.Consume(stateKey)
	if !ok {
		http.Error(w, "invalid or expired state parameter", http.StatusBadRequest)
		return
	}

	if consentEntry.MCPState != stateKey || consentEntry.ClientID != flowState.MCPClientID {
		http.Error(w, "consent code binding mismatch", http.StatusBadRequest)
		return
	}

	if consentEntry.TenantSlug != flowState.TenantSlug {
		http.Error(w, "tenant mismatch", http.StatusBadRequest)
		return
	}

	if !isAllowedRedirectURI(flowState.MCPRedirectURI) {
		h.logger.Error("oidc: unsafe redirect URI scheme", "uri", flowState.MCPRedirectURI)
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}

	redirectURL, err := h.issueCodeAndRedirect(
		consentEntry.Email, consentEntry.TenantID, consentEntry.ApprovedScopes, flowState)
	if err != nil {
		h.logger.Error("oidc: failed to issue auth code", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// handleDexCallback processes the legacy Dex token-exchange callback path.
func (h *OIDCHandler) handleDexCallback(w http.ResponseWriter, r *http.Request, stateKey, code string) {
	ctx := r.Context()
	q := r.URL.Query()

	if errParam := q.Get("error"); errParam != "" {
		desc := q.Get("error_description")
		h.logger.Warn("oidc: Dex returned error",
			"error", errParam, "description", desc)
		http.Error(w, "authentication failed: "+errParam, http.StatusBadRequest)
		return
	}

	if stateKey == "" || code == "" {
		http.Error(w, "missing state or code parameter", http.StatusBadRequest)
		return
	}

	flowState, ok := h.stateStore.Consume(stateKey)
	if !ok {
		http.Error(w, "invalid or expired state parameter", http.StatusBadRequest)
		return
	}

	idToken, err := h.exchangeDexCode(ctx, code, flowState.DexCodeVerifier)
	if err != nil {
		h.logger.Error("oidc: Dex token exchange failed",
			"tenant", flowState.TenantSlug, "error", err)
		http.Error(w, "authentication token exchange failed", http.StatusBadGateway)
		return
	}

	email, err := extractEmailFromJWT(idToken)
	if err != nil {
		h.logger.Error("oidc: failed to extract email from ID token",
			"tenant", flowState.TenantSlug, "error", err)
		http.Error(w, "failed to process identity", http.StatusBadGateway)
		return
	}

	tenantID, err := h.resolveTenantID(ctx, flowState.TenantSlug)
	if err != nil {
		http.Error(w, errTenantResolutionFailed.Error(), http.StatusInternalServerError)
		return
	}

	if !isAllowedRedirectURI(flowState.MCPRedirectURI) {
		h.logger.Error("oidc: unsafe redirect URI scheme", "uri", flowState.MCPRedirectURI)
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}

	redirectURL, err := h.issueCodeAndRedirect(email, tenantID, flowState.RequestedScopes, flowState)
	if err != nil {
		h.logger.Error("oidc: failed to issue auth code", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// resolveTenantSlug extracts the tenant slug from the request host subdomain, falling back to the default.
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

// buildDexRedirect generates PKCE state, stores the OIDC flow state, and returns the Dex redirect URL.
func (h *OIDCHandler) buildDexRedirect(challenge, clientID, redirectURI, mcpState, tenantSlug string, requestedScopes []string) (string, error) {
	dexVerifier, err := generateRandomToken(oidcCodeVerifierBytes)
	if err != nil {
		return "", fmt.Errorf("generate code verifier: %w", err)
	}
	dexChallenge := computeS256Challenge(dexVerifier)

	stateKey, err := h.stateStore.Store(OIDCFlowState{
		MCPCodeChallenge: challenge,
		MCPClientID:      clientID,
		MCPRedirectURI:   redirectURI,
		MCPState:         mcpState,
		DexCodeVerifier:  dexVerifier,
		TenantSlug:       tenantSlug,
		RequestedScopes:  requestedScopes,
		IssuedAt:         time.Now(),
	})
	if err != nil {
		return "", fmt.Errorf("store state: %w", err)
	}

	dexAuthURL := BuildTenantScopedDexURL(h.dexAuthBaseURL, tenantSlug, h.baseDomain) + "/auth"
	params := url.Values{
		"client_id":             {h.cfg.ClientID},
		"redirect_uri":          {h.cfg.CallbackURL},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {stateKey},
		"code_challenge":        {dexChallenge},
		"code_challenge_method": {"S256"},
	}
	return dexAuthURL + "?" + params.Encode(), nil
}

// buildConsentRedirect stores OIDC flow state and returns the URL of the UI
// consent page. The consent page authenticates the user (via the existing BFF
// session) and asks them to approve the requested scopes before redirecting
// back to /oauth/callback with a consent code.
func (h *OIDCHandler) buildConsentRedirect(challenge, clientID, redirectURI, mcpState, tenantSlug string, requestedScopes []string) (string, error) {
	stateKey, err := h.stateStore.Store(OIDCFlowState{
		MCPCodeChallenge: challenge,
		MCPClientID:      clientID,
		MCPRedirectURI:   redirectURI,
		MCPState:         mcpState,
		TenantSlug:       tenantSlug,
		RequestedScopes:  requestedScopes,
		IssuedAt:         time.Now(),
	})
	if err != nil {
		return "", fmt.Errorf("store state: %w", err)
	}

	consentURL := h.buildConsentPageURL(tenantSlug, stateKey, clientID)
	return consentURL, nil
}

// buildConsentPageURL constructs the UI consent page URL with tenant subdomain.
func (h *OIDCHandler) buildConsentPageURL(tenantSlug, stateKey, clientID string) string {
	base := h.baseURL
	if base == "" {
		base = "https://" + h.baseDomain
	}

	// Insert tenant subdomain if base domain is configured.
	if h.baseDomain != "" && tenantSlug != "" {
		parsed, err := url.Parse(base)
		if err == nil {
			host := parsed.Hostname()
			port := parsed.Port()
			if host == h.baseDomain || strings.HasSuffix(host, "."+h.baseDomain) {
				parsed.Host = tenantSlug + "." + h.baseDomain
				if port != "" {
					parsed.Host = tenantSlug + "." + h.baseDomain + ":" + port
				}
				base = parsed.String()
			}
		}
	}

	params := url.Values{
		"mcp_state": {stateKey},
		"client_id": {clientID},
	}
	return base + "/auth/mcp-consent?" + params.Encode()
}

// resolveTenantID resolves a tenant slug to a tenant UUID via the tenant resolver.
func (h *OIDCHandler) resolveTenantID(ctx context.Context, tenantSlug string) (string, error) {
	tenantID := tenantSlug
	if h.tenantResolver != nil && tenantID != "" {
		resolved, resolveErr := h.tenantResolver.ResolveSlug(ctx, tenantID)
		if resolveErr != nil {
			h.logger.Error("oidc: tenant slug resolution failed",
				"tenant_slug", tenantID, "error", resolveErr)
			return "", resolveErr
		}
		tenantID = resolved
	}
	return tenantID, nil
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

// exchangeDexCode exchanges a Dex authorization code for an ID token.
func (h *OIDCHandler) exchangeDexCode(ctx context.Context, code, codeVerifier string) (string, error) {
	tokenURL := h.cfg.DexIssuerURL + "/token"
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {h.cfg.ClientID},
		"code":          {code},
		"redirect_uri":  {h.cfg.CallbackURL},
		"code_verifier": {codeVerifier},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token endpoint request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, oidcMaxTokenResponseBytes))
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	var tokenResp struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error,omitempty"`
		ErrDesc string `json:"error_description,omitempty"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.Error != "" {
		return "", fmt.Errorf("%w: %s: %s", errDexTokenError, tokenResp.Error, tokenResp.ErrDesc)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: status %d", errDexBadStatus, resp.StatusCode)
	}

	if tokenResp.IDToken == "" {
		return "", errDexEmptyIDToken
	}

	return tokenResp.IDToken, nil
}

// extractEmailFromJWT extracts the email claim from a JWT payload without
// signature verification (trusted server-to-server response from Dex).
func extractEmailFromJWT(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errInvalidJWTFormat
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode JWT payload: %w", err)
	}

	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parse JWT claims: %w", err)
	}

	if claims.Email == "" {
		return "", errEmailClaimMissing
	}

	return claims.Email, nil
}

// generateRandomToken generates a cryptographically random URL-safe token.
func generateRandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// computeS256Challenge computes the S256 PKCE code challenge from a verifier.
func computeS256Challenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// BuildTenantScopedDexURL inserts the tenant subdomain into the Dex base URL.
// Given "https://demo.meridianhub.cloud/dex" and tenant "acme" with baseDomain
// "demo.meridianhub.cloud", it returns "https://acme.demo.meridianhub.cloud/dex".
// When baseDomain is empty or the URL can't be parsed, the original URL is returned.
func BuildTenantScopedDexURL(dexBaseURL, tenantSlug, baseDomain string) string {
	if baseDomain == "" || tenantSlug == "" {
		return dexBaseURL
	}
	parsed, err := url.Parse(dexBaseURL)
	if err != nil {
		return dexBaseURL
	}
	// Only scope if the host matches or ends with the base domain.
	host := parsed.Hostname()
	if host != baseDomain && !strings.HasSuffix(host, "."+baseDomain) {
		return dexBaseURL
	}
	// Replace the host with tenant-scoped subdomain, preserving any port.
	port := parsed.Port()
	parsed.Host = tenantSlug + "." + baseDomain
	if port != "" {
		parsed.Host = tenantSlug + "." + baseDomain + ":" + port
	}
	return parsed.String()
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
