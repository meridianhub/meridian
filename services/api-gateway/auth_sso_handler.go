package gateway

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
	"time"

	"github.com/meridianhub/meridian/services/identity/connector"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

var (
	// ErrSSODexIssuerRequired is returned when no Dex issuer URL is provided.
	ErrSSODexIssuerRequired = errors.New("sso handler: dex issuer URL is required")
	// ErrSSOClientIDRequired is returned when no OAuth client ID is provided.
	ErrSSOClientIDRequired = errors.New("sso handler: client ID is required")
	// ErrSSOSignerRequired is returned when no signer is provided.
	ErrSSOSignerRequired = errors.New("sso handler: signer is required")
	// ErrSSOResolverRequired is returned when no identity resolver is provided.
	ErrSSOResolverRequired = errors.New("sso handler: identity resolver is required")
	// ErrSSOLoggerRequired is returned when no logger is provided.
	ErrSSOLoggerRequired = errors.New("sso handler: logger is required")
	// ErrDexTokenError is returned when Dex's token endpoint returns an error response.
	ErrDexTokenError = errors.New("dex token error")
	// ErrDexTokenStatus is returned when Dex's token endpoint returns a non-200 status.
	ErrDexTokenStatus = errors.New("dex token endpoint returned unexpected status")
	// ErrDexEmptyIDToken is returned when Dex returns an empty id_token.
	ErrDexEmptyIDToken = errors.New("dex returned empty id_token")
	// ErrInvalidJWTFormat is returned when the ID token is not a valid JWT.
	ErrInvalidJWTFormat = errors.New("invalid JWT format")
	// ErrEmailClaimMissing is returned when the ID token has no email claim.
	ErrEmailClaimMissing = errors.New("email claim missing from ID token")
	// ErrSSOCallbackURLRequired is returned when no callback URL is provided.
	ErrSSOCallbackURLRequired = errors.New("sso handler: callback URL is required")
	// ErrSSOCallbackURLInvalid is returned when the callback URL is not a valid absolute URL.
	ErrSSOCallbackURLInvalid = errors.New("sso handler: callback URL must be a valid absolute URL")
	// ErrSSODexIssuerInvalid is returned when the Dex issuer URL is not a valid absolute URL.
	ErrSSODexIssuerInvalid = errors.New("sso handler: dex issuer URL must be an absolute URL with scheme and host")
)

// IdentityResolver resolves an identity by email without password validation.
// This is a subset of the connector.Connector capabilities needed for SSO callback
// processing — after Dex authenticates the user, we look them up by email to
// populate Meridian-specific claims (roles, tenant membership).
type IdentityResolver interface {
	Resolve(ctx context.Context, email string) (connector.Identity, bool, error)
}

// SSOHandler handles the OAuth2/OIDC SSO flow via Dex as the BFF intermediary.
// The frontend never talks to Dex directly — all authorization and token exchange
// happens server-side through PKCE-protected endpoints.
type SSOHandler struct {
	dexIssuerURL string
	clientID     string
	callbackURL  string
	baseDomain   string
	stateStore   *StateStore
	signer       *platformauth.JWTSigner
	resolver     IdentityResolver
	tokenTTL     time.Duration
	logger       *slog.Logger
	httpClient   *http.Client // pluggable for tests
}

// SSOHandlerConfig holds configuration for creating an SSOHandler.
type SSOHandlerConfig struct {
	// DexIssuerURL is the base URL for the Dex OIDC provider (e.g., "https://demo.meridianhub.cloud/dex").
	DexIssuerURL string
	// ClientID is the OAuth2 client ID registered with Dex.
	ClientID string
	// CallbackURL is the absolute URL for the BFF callback endpoint
	// (e.g., "https://demo.meridianhub.cloud/api/auth/callback").
	CallbackURL string
	// BaseDomain is the base domain for subdomain-based tenant identification
	// (e.g., "demo.meridianhub.cloud"). When set, the SSO initiate redirect builds
	// a tenant-scoped Dex URL using the tenant slug as a subdomain prefix.
	BaseDomain string
	// Signer signs Meridian JWTs after SSO completes.
	Signer *platformauth.JWTSigner
	// Resolver looks up identities by email after SSO authentication.
	Resolver IdentityResolver
	// TokenTTL is the lifetime of issued JWTs. Defaults to 1 hour.
	TokenTTL time.Duration
	// Logger for structured logging. Required.
	Logger *slog.Logger
	// HTTPClient is used for the token exchange with Dex. Defaults to http.DefaultClient.
	HTTPClient *http.Client
	// StateStore is the PKCE state store. If nil, a default store is created.
	StateStore *StateStore
}

// validateSSOConfig checks that all required fields are present and valid.
func validateSSOConfig(cfg SSOHandlerConfig) error {
	if cfg.DexIssuerURL == "" {
		return ErrSSODexIssuerRequired
	}
	issuerURL, err := url.Parse(cfg.DexIssuerURL)
	if err != nil || !issuerURL.IsAbs() || issuerURL.Host == "" {
		return fmt.Errorf("%w: %q", ErrSSODexIssuerInvalid, cfg.DexIssuerURL)
	}
	if cfg.CallbackURL == "" {
		return ErrSSOCallbackURLRequired
	}
	callbackURL, err := url.Parse(cfg.CallbackURL)
	if err != nil || !callbackURL.IsAbs() || callbackURL.Host == "" {
		return fmt.Errorf("%w: %q", ErrSSOCallbackURLInvalid, cfg.CallbackURL)
	}
	if cfg.ClientID == "" {
		return ErrSSOClientIDRequired
	}
	if cfg.Signer == nil {
		return ErrSSOSignerRequired
	}
	if cfg.Resolver == nil {
		return ErrSSOResolverRequired
	}
	if cfg.Logger == nil {
		return ErrSSOLoggerRequired
	}
	return nil
}

// NewSSOHandler creates a handler for BFF SSO authentication via Dex.
//
// Security: The token exchange with Dex relies on server-to-server TLS for
// integrity (ID token signature verification is skipped). In production, the
// DexIssuerURL MUST use HTTPS. A warning is logged if HTTP is configured
// (acceptable only in local development with trusted networks).
func NewSSOHandler(cfg SSOHandlerConfig) (*SSOHandler, error) {
	if err := validateSSOConfig(cfg); err != nil {
		return nil, err
	}
	issuerURL, _ := url.Parse(cfg.DexIssuerURL) // already validated
	if issuerURL.Scheme != "https" {
		cfg.Logger.Warn("sso handler: Dex issuer URL is not HTTPS — token exchange is not protected by TLS",
			"dex_issuer_url", cfg.DexIssuerURL)
	}
	ttl := cfg.TokenTTL
	if ttl == 0 {
		ttl = time.Hour
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	stateStore := cfg.StateStore
	if stateStore == nil {
		stateStore = NewStateStore(defaultStateTTL)
	}
	return &SSOHandler{
		dexIssuerURL: strings.TrimRight(cfg.DexIssuerURL, "/"),
		clientID:     cfg.ClientID,
		callbackURL:  cfg.CallbackURL,
		baseDomain:   cfg.BaseDomain,
		stateStore:   stateStore,
		signer:       cfg.Signer,
		resolver:     cfg.Resolver,
		tokenTTL:     ttl,
		logger:       cfg.Logger,
		httpClient:   httpClient,
	}, nil
}

// HandleInitiate handles GET /api/auth/sso/{connector_id}.
// It generates PKCE parameters, stores state, and redirects the user to Dex.
func (h *SSOHandler) HandleInitiate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	connectorID := r.PathValue("connector_id")
	if connectorID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "connector_id is required",
		})
		return
	}

	ctx := r.Context()
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		h.logger.WarnContext(ctx, "sso: no tenant in context for initiate",
			"host", r.Host)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "unable to determine tenant from request",
		})
		return
	}

	// Generate PKCE code verifier (43-128 chars of unreserved URI chars per RFC 7636).
	codeVerifier, err := generateCodeVerifier()
	if err != nil {
		h.logger.ErrorContext(ctx, "sso: failed to generate code verifier", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to initiate SSO",
		})
		return
	}

	codeChallenge := computeCodeChallenge(codeVerifier)

	returnURL := sanitizeReturnURL(r.URL.Query().Get("return_url"))

	stateKey, err := h.stateStore.Set(StateData{
		CodeVerifier: codeVerifier,
		TenantID:     tenantID,
		ReturnURL:    returnURL,
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "sso: failed to store state", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to initiate SSO",
		})
		return
	}

	// Build Dex authorization URL. When baseDomain is configured, the redirect
	// uses a tenant-scoped URL so that the Dex MeridianConnector receives tenant
	// context via the subdomain. Path-escape the connector ID to prevent
	// path traversal (e.g., "../../admin" → "%2E%2E%2Fadmin").
	authURL := h.buildDexAuthURL(tenantID, connectorID)
	params := url.Values{
		"client_id":             {h.clientID},
		"redirect_uri":          {h.callbackURL},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {stateKey},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}

	redirectURL := authURL + "?" + params.Encode()

	h.logger.InfoContext(ctx, "sso: initiating SSO flow",
		"tenant_id", tenantID,
		"connector_id", connectorID)

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// HandleCallback handles GET /api/auth/callback.
// It validates the state, exchanges the authorization code for tokens with Dex,
// resolves the user identity, and redirects with a Meridian JWT.
func (h *SSOHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()

	// Check for OAuth error from Dex.
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		h.logger.WarnContext(ctx, "sso: Dex returned error",
			"error", errParam,
			"description", desc)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "SSO authentication failed: " + errParam,
		})
		return
	}

	stateKey := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	if stateKey == "" || code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "missing state or code parameter",
		})
		return
	}

	// Retrieve and consume state (one-time use).
	stateData, ok := h.stateStore.Get(stateKey)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid or expired state parameter",
		})
		return
	}

	// Exchange authorization code for tokens with Dex.
	idToken, err := h.exchangeCode(ctx, code, stateData.CodeVerifier)
	if err != nil {
		h.logger.ErrorContext(ctx, "sso: token exchange failed",
			"tenant_id", stateData.TenantID,
			"error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "SSO token exchange failed",
		})
		return
	}

	// Parse the email from Dex's ID token (we trust the claims since we got them
	// directly from the token endpoint over a server-to-server connection).
	email, err := extractEmailFromIDToken(idToken)
	if err != nil {
		h.logger.ErrorContext(ctx, "sso: failed to extract email from ID token",
			"tenant_id", stateData.TenantID,
			"error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "failed to process SSO identity",
		})
		return
	}

	// Resolve the identity in Meridian's identity store to get roles and tenant context.
	tenantCtx := tenant.WithTenant(ctx, stateData.TenantID)
	identity, found, err := h.resolver.Resolve(tenantCtx, email)
	if err != nil {
		h.logger.ErrorContext(ctx, "sso: identity resolution error",
			"tenant_id", stateData.TenantID,
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to resolve identity",
		})
		return
	}
	if !found {
		h.logger.WarnContext(ctx, "sso: no matching identity for SSO email",
			"tenant_id", stateData.TenantID)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "no matching account for this SSO identity",
		})
		return
	}

	// Sign Meridian JWT.
	claims := connector.BuildClaims(identity, stateData.TenantID)
	tokenStr, err := h.signer.SignClaims(claims, h.tokenTTL)
	if err != nil {
		h.logger.ErrorContext(ctx, "sso: failed to sign token",
			"tenant_id", stateData.TenantID,
			"identity_id", identity.UserID,
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to create session",
		})
		return
	}

	h.logger.InfoContext(ctx, "sso: login successful",
		"tenant_id", stateData.TenantID,
		"identity_id", identity.UserID)

	// Redirect to frontend with token.
	returnURL := stateData.ReturnURL
	if returnURL == "" {
		returnURL = "/"
	}
	redirectURL, err := buildTokenRedirectURL(returnURL, tokenStr)
	if err != nil {
		// Fallback: return token as JSON if URL building fails.
		writeJSON(w, http.StatusOK, loginResponse{
			AccessToken: tokenStr,
			TokenType:   "Bearer",
			ExpiresIn:   int(h.tokenTTL.Seconds()),
		})
		return
	}

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// buildDexAuthURL constructs the Dex authorization URL. When baseDomain is
// configured, the URL includes the tenant slug as a subdomain prefix so that
// the MeridianConnector can resolve tenant context from the request host.
// Falls back to the bare dexIssuerURL when baseDomain is not set.
func (h *SSOHandler) buildDexAuthURL(tenantID tenant.TenantID, connectorID string) string {
	escapedConnector := url.PathEscape(connectorID)

	if h.baseDomain == "" {
		return h.dexIssuerURL + "/auth/" + escapedConnector
	}

	// Parse the Dex issuer URL to extract the scheme and path prefix.
	issuer, err := url.Parse(h.dexIssuerURL)
	if err != nil {
		// Already validated in constructor; fall back to bare URL.
		return h.dexIssuerURL + "/auth/" + escapedConnector
	}

	tenantSlug := strings.ToLower(tenantID.String())
	tenantHost := tenantSlug + "." + h.baseDomain
	// Preserve any explicit port from the Dex issuer URL (e.g., :5556 in dev).
	if port := issuer.Port(); port != "" {
		tenantHost = tenantHost + ":" + port
	}
	issuer.Host = tenantHost
	return issuer.String() + "/auth/" + escapedConnector
}

// dexTokenResponse is the JSON response from Dex's token endpoint.
type dexTokenResponse struct {
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error,omitempty"`
	ErrorDesc   string `json:"error_description,omitempty"`
}

// exchangeCode exchanges an authorization code for tokens at the Dex token endpoint.
func (h *SSOHandler) exchangeCode(ctx context.Context, code, codeVerifier string) (string, error) {
	tokenURL := h.dexIssuerURL + "/token"
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {h.clientID},
		"code":          {code},
		"redirect_uri":  {h.callbackURL},
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	var tokenResp dexTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.Error != "" {
		return "", fmt.Errorf("%w: %s: %s", ErrDexTokenError, tokenResp.Error, tokenResp.ErrorDesc)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: %d", ErrDexTokenStatus, resp.StatusCode)
	}

	if tokenResp.IDToken == "" {
		return "", ErrDexEmptyIDToken
	}

	return tokenResp.IDToken, nil
}

// extractEmailFromIDToken decodes the JWT payload (without verification — we trust
// the server-to-server response from Dex) and extracts the email claim.
func extractEmailFromIDToken(idToken string) (string, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", ErrInvalidJWTFormat
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
		return "", ErrEmailClaimMissing
	}

	return claims.Email, nil
}

// generateCodeVerifier generates a PKCE code verifier per RFC 7636.
// Returns a 43-character base64url-encoded random string (32 bytes of entropy).
func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// computeCodeChallenge computes the S256 PKCE code challenge from a code verifier.
func computeCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// sanitizeReturnURL validates that the return URL is a safe relative path.
// This prevents open redirect attacks where an attacker crafts a return_url
// pointing to an external domain to steal the JWT from the URL fragment.
//
// Only paths starting with "/" (relative to the origin) are accepted.
// Absolute URLs, protocol-relative URLs (//evil.com), and malformed input
// are rejected and replaced with "/".
func sanitizeReturnURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	// Reject protocol-relative URLs like "//evil.com/steal"
	if strings.HasPrefix(raw, "//") {
		return "/"
	}
	// Must be a relative path starting with "/"
	if !strings.HasPrefix(raw, "/") {
		return "/"
	}
	return raw
}

// buildTokenRedirectURL appends the access token as a fragment to the return URL.
// Using a fragment (#) ensures the token is not sent to the server in subsequent requests.
func buildTokenRedirectURL(returnURL, token string) (string, error) {
	u, err := url.Parse(returnURL)
	if err != nil {
		return "", err
	}
	u.Fragment = "access_token=" + token
	return u.String(), nil
}

// WithSSOHandler sets the BFF SSO handler for the server.
// When set, GET /api/auth/sso/{connector_id} and GET /api/auth/callback are registered.
func WithSSOHandler(handler *SSOHandler) ServerOption {
	return func(s *Server) {
		s.ssoHandler = handler
	}
}
