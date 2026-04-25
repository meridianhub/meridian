package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/identity/connector"
	identitydomain "github.com/meridianhub/meridian/services/identity/domain"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

var (
	// ErrConnectorRequired is returned when no connector is provided to NewAuthHandler.
	ErrConnectorRequired = errors.New("auth handler: connector is required")
	// ErrSignerRequired is returned when no signer is provided to NewAuthHandler.
	ErrSignerRequired = errors.New("auth handler: signer is required")
	// ErrLoggerRequired is returned when no logger is provided to NewAuthHandler.
	ErrLoggerRequired = errors.New("auth handler: logger is required")
)

// AuthHandler handles BFF password authentication and JWKS serving.
// Password login bypasses Dex entirely — the backend validates credentials
// directly against the identity domain and signs its own JWT.
type AuthHandler struct {
	connector connector.PasswordConnector
	signer    *platformauth.JWTSigner
	tokenTTL  time.Duration
	logger    *slog.Logger
}

// AuthHandlerConfig holds configuration for creating an AuthHandler.
type AuthHandlerConfig struct {
	Connector connector.PasswordConnector
	Signer    *platformauth.JWTSigner
	TokenTTL  time.Duration // Defaults to 1 hour.
	Logger    *slog.Logger
}

// NewAuthHandler creates a handler for BFF password authentication.
// Returns an error if required dependencies (connector, signer, logger) are nil.
func NewAuthHandler(cfg AuthHandlerConfig) (*AuthHandler, error) {
	if cfg.Connector == nil {
		return nil, ErrConnectorRequired
	}
	if cfg.Signer == nil {
		return nil, ErrSignerRequired
	}
	if cfg.Logger == nil {
		return nil, ErrLoggerRequired
	}
	ttl := cfg.TokenTTL
	if ttl == 0 {
		ttl = time.Hour
	}
	return &AuthHandler{
		connector: cfg.Connector,
		signer:    cfg.Signer,
		tokenTTL:  ttl,
		logger:    cfg.Logger,
	}, nil
}

// loginRequest is the JSON body for POST /api/auth/login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// loginResponse is the JSON body returned on successful login.
type loginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// HandleLogin handles POST /api/auth/login.
// It resolves the tenant from the request (subdomain or header), validates
// credentials via the identity connector, and returns a signed JWT.
func (h *AuthHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 4KB to prevent memory exhaustion on this unauthenticated endpoint.
	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid request body",
		})
		return
	}

	if req.Email == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "email and password are required",
		})
		return
	}

	// Tenant is resolved by the tenant resolver middleware (from subdomain or X-Tenant-Slug header).
	ctx := r.Context()
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		h.logger.WarnContext(ctx, "auth: no tenant in context for login request",
			"host", r.Host)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "unable to determine tenant from request",
		})
		return
	}

	// Reject login attempts against tenants that are not yet operational. The
	// tenant resolver middleware allows /api/auth/login through during async
	// provisioning so this handler can return a JSON status-aware error
	// instead of HTML, distinguishing "still being set up" from "wrong
	// password" (which would otherwise confuse self-registered users).
	if status, ok := tenant.StatusFromContext(ctx); ok && status != "" && status != tenantStatusActive {
		h.handleNonActiveTenant(ctx, w, tenantID, status)
		return
	}

	// Validate credentials and sign JWT.
	tokenStr, identity, err := h.authenticateAndSign(ctx, tenantID, req)
	if err != nil {
		h.handleLoginError(ctx, w, tenantID, err)
		return
	}

	h.logger.InfoContext(ctx, "auth: login successful",
		"tenant_id", tenantID,
		"identity_id", identity.UserID)

	writeJSON(w, http.StatusOK, loginResponse{
		AccessToken: tokenStr,
		TokenType:   "Bearer",
		ExpiresIn:   int(h.tokenTTL.Seconds()),
	})
}

// authenticateAndSign validates credentials and returns a signed JWT token.
func (h *AuthHandler) authenticateAndSign(ctx context.Context, tenantID tenant.TenantID, req loginRequest) (string, connector.Identity, error) {
	identity, valid, err := h.connector.Login(ctx, nil, req.Email, req.Password)
	if err != nil {
		return "", connector.Identity{}, fmt.Errorf("login: %w", err)
	}
	if !valid {
		return "", connector.Identity{}, errInvalidCredentials
	}

	claims := connector.BuildClaims(identity, tenantID)
	if slug, ok := tenant.SlugFromContext(ctx); ok && slug != "" {
		claims[tenant.TenantSlugKey] = slug
	}
	if displayName, ok := tenant.DisplayNameFromContext(ctx); ok && displayName != "" {
		claims[tenant.TenantDisplayNameKey] = displayName
	}
	tokenStr, err := h.signer.SignClaims(claims, h.tokenTTL)
	if err != nil {
		return "", identity, fmt.Errorf("sign: %w", err)
	}

	return tokenStr, identity, nil
}

// errInvalidCredentials is a sentinel for invalid email/password.
var errInvalidCredentials = errors.New("invalid credentials")

// Tenant lifecycle status string constants matching services/tenant/domain.Status.
// Duplicated here as untyped strings to avoid importing the tenant domain package
// into the API gateway (which would invert the existing dependency direction).
const (
	tenantStatusActive              = "active"
	tenantStatusProvisioningPending = "provisioning_pending"
	tenantStatusProvisioning        = "provisioning"
	tenantStatusProvisioningFailed  = "provisioning_failed"
)

// handleNonActiveTenant responds to login attempts against tenants that are not
// yet (or no longer) operational, returning a status-specific JSON message so
// the SPA can render an actionable error to the user.
func (h *AuthHandler) handleNonActiveTenant(ctx context.Context, w http.ResponseWriter, tenantID tenant.TenantID, status string) {
	h.logger.InfoContext(ctx, "auth: login rejected - tenant not active",
		"tenant_id", tenantID,
		"tenant_status", status)

	switch status {
	case tenantStatusProvisioningPending, tenantStatusProvisioning:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":  "Your tenant is still being set up. Please wait a moment and try again.",
			"status": status,
		})
	case tenantStatusProvisioningFailed:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":  "Tenant provisioning failed. Please contact support.",
			"status": status,
		})
	default:
		// suspended, deprovisioned, or any future non-active status.
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error":  "This tenant is not currently available.",
			"status": status,
		})
	}
}

// handleLoginError maps authenticateAndSign errors to HTTP responses.
func (h *AuthHandler) handleLoginError(ctx context.Context, w http.ResponseWriter, tenantID tenant.TenantID, err error) {
	if errors.Is(err, errInvalidCredentials) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "invalid email or password",
		})
		return
	}
	if errors.Is(err, identitydomain.ErrEmailNotVerified) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "email address has not been verified",
		})
		return
	}
	if strings.HasPrefix(err.Error(), "sign:") {
		h.logger.ErrorContext(ctx, "auth: failed to sign token",
			"tenant_id", tenantID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to create session",
		})
		return
	}
	h.logger.ErrorContext(ctx, "auth: login error",
		"tenant_id", tenantID, "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": "authentication service error",
	})
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WithAuthHandler sets the BFF authentication handler for the server.
// When set, POST /api/auth/login and GET /api/auth/jwks are registered.
func WithAuthHandler(handler *AuthHandler) ServerOption {
	return func(s *Server) {
		s.authHandler = handler
	}
}
