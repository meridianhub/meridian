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

// OIDCHandler manages the OIDC authorization flow with Dex.
type OIDCHandler struct {
	cfg        OIDCConfig
	oauthCfg   OAuthConfig
	stateStore *OIDCStateStore
	codeStore  *CodeStore
	signer     *platformauth.JWTSigner
	tokenTTL   time.Duration
	baseDomain string
	httpClient *http.Client
	logger     *slog.Logger
}

// OIDCHandlerConfig holds configuration for creating an OIDCHandler.
type OIDCHandlerConfig struct {
	OIDC       OIDCConfig
	OAuth      OAuthConfig
	StateStore *OIDCStateStore
	CodeStore  *CodeStore
	Signer     *platformauth.JWTSigner
	TokenTTL   time.Duration
	BaseDomain string
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// NewOIDCHandler creates a handler for the OIDC-backed OAuth 2.1 flow.
func NewOIDCHandler(cfg OIDCHandlerConfig) (*OIDCHandler, error) {
	if cfg.OIDC.DexIssuerURL == "" {
		return nil, errOIDCDexIssuerRequired
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

	ttl := cfg.TokenTTL
	if ttl == 0 {
		ttl = defaultTokenTTL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: oidcHTTPTimeout}
	}

	return &OIDCHandler{
		cfg:        cfg.OIDC,
		oauthCfg:   cfg.OAuth,
		stateStore: cfg.StateStore,
		codeStore:  cfg.CodeStore,
		signer:     cfg.Signer,
		tokenTTL:   ttl,
		baseDomain: cfg.BaseDomain,
		httpClient: httpClient,
		logger:     cfg.Logger,
	}, nil
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
	if clientID != h.oauthCfg.ClientID {
		http.Error(w, "invalid client_id", http.StatusBadRequest)
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

	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" {
		http.Error(w, "redirect_uri is required", http.StatusBadRequest)
		return
	}

	// Validate redirect_uri scheme to prevent open-redirect attacks.
	// MCP clients provide dynamic callback URIs, so we validate the scheme
	// rather than matching a single registered URI: HTTPS is required for
	// production; HTTP is allowed only for localhost (development).
	if !isAllowedRedirectURI(redirectURI) {
		http.Error(w, "redirect_uri must use https (or http://localhost for development)", http.StatusBadRequest)
		return
	}

	// Extract tenant slug from request subdomain.
	tenantSlug := extractSubdomain(r.Host, h.baseDomain)

	// Generate PKCE code verifier for the Dex exchange.
	dexVerifier, err := generateRandomToken(oidcCodeVerifierBytes)
	if err != nil {
		h.logger.Error("oidc: failed to generate code verifier", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	dexChallenge := computeS256Challenge(dexVerifier)

	// Store state bridging MCP client request ↔ Dex OIDC flow.
	stateKey, err := h.stateStore.Store(OIDCFlowState{
		MCPCodeChallenge: challenge,
		MCPClientID:      clientID,
		MCPRedirectURI:   redirectURI,
		MCPState:         q.Get("state"),
		DexCodeVerifier:  dexVerifier,
		TenantSlug:       tenantSlug,
		IssuedAt:         time.Now(),
	})
	if err != nil {
		h.logger.Error("oidc: failed to store state", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// Redirect to Dex authorization endpoint.
	// Use the "local" connector for password-based auth, or omit connector_id
	// to let Dex show its connector selection page.
	dexAuthURL := h.cfg.DexIssuerURL + "/auth"
	params := url.Values{
		"client_id":             {h.cfg.ClientID},
		"redirect_uri":          {h.cfg.CallbackURL},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {stateKey},
		"code_challenge":        {dexChallenge},
		"code_challenge_method": {"S256"},
	}

	redirectURL := dexAuthURL + "?" + params.Encode()

	h.logger.Info("oidc: initiating Dex authorization",
		"tenant", tenantSlug,
		"client_id", clientID)

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// HandleCallback handles GET /oauth/callback from Dex.
// It exchanges the Dex authorization code for an ID token, extracts the email,
// signs a Meridian JWT, and redirects back to the MCP client's redirect_uri.
func (h *OIDCHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	q := r.URL.Query()

	// Check for Dex error.
	if errParam := q.Get("error"); errParam != "" {
		desc := q.Get("error_description")
		h.logger.Warn("oidc: Dex returned error",
			"error", errParam, "description", desc)
		http.Error(w, "authentication failed: "+errParam, http.StatusBadRequest)
		return
	}

	stateKey := q.Get("state")
	code := q.Get("code")
	if stateKey == "" || code == "" {
		http.Error(w, "missing state or code parameter", http.StatusBadRequest)
		return
	}

	// Retrieve and consume state (one-time use).
	flowState, ok := h.stateStore.Consume(stateKey)
	if !ok {
		http.Error(w, "invalid or expired state parameter", http.StatusBadRequest)
		return
	}

	// Exchange Dex authorization code for tokens.
	idToken, err := h.exchangeDexCode(ctx, code, flowState.DexCodeVerifier)
	if err != nil {
		h.logger.Error("oidc: Dex token exchange failed",
			"tenant", flowState.TenantSlug, "error", err)
		http.Error(w, "authentication token exchange failed", http.StatusBadGateway)
		return
	}

	// Extract email from Dex ID token (trusted server-to-server response).
	email, err := extractEmailFromJWT(idToken)
	if err != nil {
		h.logger.Error("oidc: failed to extract email from ID token",
			"tenant", flowState.TenantSlug, "error", err)
		http.Error(w, "failed to process identity", http.StatusBadGateway)
		return
	}

	// Sign Meridian JWT with tenant context.
	claims := map[string]interface{}{
		"sub":         email, // Use email as subject until identity resolution is wired
		"email":       email,
		"x-tenant-id": flowState.TenantSlug,
	}
	tokenStr, err := h.signer.SignClaims(claims, h.tokenTTL)
	if err != nil {
		h.logger.Error("oidc: failed to sign JWT",
			"tenant", flowState.TenantSlug, "error", err)
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	// Generate MCP authorization code and store it with the JWT.
	mcpCode, err := generateCode()
	if err != nil {
		h.logger.Error("oidc: failed to generate auth code", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.codeStore.StoreWithToken(mcpCode, CodeEntry{
		CodeChallenge: flowState.MCPCodeChallenge,
		ClientID:      flowState.MCPClientID,
		RedirectURI:   flowState.MCPRedirectURI,
		IssuedAt:      time.Now(),
	}, tokenStr)

	h.logger.Info("oidc: authentication successful",
		"tenant", flowState.TenantSlug)

	// Redirect to MCP client's redirect_uri with the authorization code.
	target, err := url.Parse(flowState.MCPRedirectURI)
	if err != nil {
		h.logger.Error("oidc: invalid redirect URI", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	params := target.Query()
	params.Set("code", mcpCode)
	if flowState.MCPState != "" {
		params.Set("state", flowState.MCPState)
	}
	target.RawQuery = params.Encode()

	http.Redirect(w, r, target.String(), http.StatusFound)
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

// isAllowedRedirectURI validates that a redirect URI is safe to redirect to.
// HTTPS is required for production; HTTP is allowed only for localhost (development).
func isAllowedRedirectURI(uri string) bool {
	parsed, err := url.Parse(uri)
	if err != nil {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme == "http" {
		host := parsed.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	}
	return false
}
