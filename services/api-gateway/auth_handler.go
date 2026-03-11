package gateway

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/meridianhub/meridian/services/identity/connector"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	platformgateway "github.com/meridianhub/meridian/shared/platform/gateway"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// AuthHandler handles BFF password authentication and JWKS serving.
// Password login bypasses Dex entirely — the backend validates credentials
// directly against the identity domain and signs its own JWT.
type AuthHandler struct {
	connector      connector.PasswordConnector
	signer         *platformauth.JWTSigner
	tenantResolver *platformgateway.TenantResolverMiddleware
	tokenTTL       time.Duration
	logger         *slog.Logger
}

// AuthHandlerConfig holds configuration for creating an AuthHandler.
type AuthHandlerConfig struct {
	Connector      connector.PasswordConnector
	Signer         *platformauth.JWTSigner
	TenantResolver *platformgateway.TenantResolverMiddleware
	TokenTTL       time.Duration // Defaults to 1 hour.
	Logger         *slog.Logger
}

// NewAuthHandler creates a handler for BFF password authentication.
func NewAuthHandler(cfg AuthHandlerConfig) *AuthHandler {
	ttl := cfg.TokenTTL
	if ttl == 0 {
		ttl = time.Hour
	}
	return &AuthHandler{
		connector:      cfg.Connector,
		signer:         cfg.Signer,
		tenantResolver: cfg.TenantResolver,
		tokenTTL:       ttl,
		logger:         cfg.Logger,
	}
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

	// Resolve tenant from subdomain (or X-Tenant-Slug in dev mode).
	// The tenant resolver stores the resolved tenant ID in the request context.
	ctx := r.Context()
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		// Tenant wasn't resolved by middleware — try to resolve from request directly
		h.logger.WarnContext(ctx, "auth: no tenant in context for login request",
			"host", r.Host)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "unable to determine tenant from request",
		})
		return
	}

	// Validate credentials via the identity connector.
	identity, valid, err := h.connector.Login(ctx, nil, req.Email, req.Password)
	if err != nil {
		h.logger.ErrorContext(ctx, "auth: login error",
			"tenant_id", tenantID,
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "authentication service error",
		})
		return
	}
	if !valid {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "invalid email or password",
		})
		return
	}

	// Build claims and sign JWT.
	claims := connector.BuildClaims(identity, tenantID)
	tokenStr, err := h.signer.SignClaims(claims, h.tokenTTL)
	if err != nil {
		h.logger.ErrorContext(ctx, "auth: failed to sign token",
			"tenant_id", tenantID,
			"identity_id", identity.UserID,
			"error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to create session",
		})
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
