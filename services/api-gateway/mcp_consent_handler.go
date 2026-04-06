package gateway

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/meridianhub/meridian/services/api-gateway/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// OIDCStatePeeker provides read-only access to OIDC flow state and the ability
// to delete entries. This interface decouples the consent handler from the
// mcp-server's internal OIDCStateStore, allowing the stores to be shared at
// wiring time.
type OIDCStatePeeker interface {
	// PeekInfo returns selected fields without consuming the entry.
	PeekInfo(key string) (clientID, redirectURI string, scopes []string, ok bool)
	// Delete removes an entry by key. Used for cleanup on deny.
	Delete(key string)
}

// MCPConsentHandler handles the BFF consent endpoint for MCP OAuth flows.
// It validates JWT auth, generates consent codes, and returns redirect URLs.
type MCPConsentHandler struct {
	consentStore   *ConsentCodeStore
	oidcStateStore OIDCStatePeeker
	logger         *slog.Logger
}

// MCPConsentHandlerConfig holds configuration for creating an MCPConsentHandler.
type MCPConsentHandlerConfig struct {
	ConsentStore   *ConsentCodeStore
	OIDCStateStore OIDCStatePeeker
	Logger         *slog.Logger
}

// NewMCPConsentHandler creates a new MCP consent handler.
func NewMCPConsentHandler(cfg MCPConsentHandlerConfig) *MCPConsentHandler {
	return &MCPConsentHandler{
		consentStore:   cfg.ConsentStore,
		oidcStateStore: cfg.OIDCStateStore,
		logger:         cfg.Logger,
	}
}

type mcpConsentRequest struct {
	MCPState string `json:"mcp_state"`
	ClientID string `json:"client_id"`
	Action   string `json:"action"`
}

type mcpConsentResponse struct {
	RedirectURL string `json:"redirect_url"`
}

type mcpConsentErrorResponse struct {
	Error string `json:"error"`
}

// HandleConsent handles POST /api/auth/mcp-consent.
// Requires JWT auth (via middleware). Validates the OIDC state, generates a
// consent code (approve) or error redirect (deny), and returns a redirect URL.
func (h *MCPConsentHandler) HandleConsent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)

	var req mcpConsentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, mcpConsentErrorResponse{Error: "invalid_json"})
		return
	}

	if req.MCPState == "" || req.ClientID == "" {
		writeJSON(w, http.StatusBadRequest, mcpConsentErrorResponse{Error: "missing_fields"})
		return
	}

	if req.Action != "approve" && req.Action != "deny" {
		writeJSON(w, http.StatusBadRequest, mcpConsentErrorResponse{Error: "invalid_action"})
		return
	}

	// Extract identity from JWT claims (set by auth middleware).
	ctx := r.Context()
	claims, ok := auth.GetClaimsFromContext(ctx)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, mcpConsentErrorResponse{Error: "unauthorized"})
		return
	}

	email := claims.Subject
	if email == "" {
		email = claims.Email
	}

	tenantID := claims.TenantID
	tenantSlug := ""
	if slug, slugOk := tenant.SlugFromContext(ctx); slugOk {
		tenantSlug = slug
	}

	// Peek OIDCStateStore to verify mcp_state exists and client_id matches.
	storedClientID, redirectURI, scopes, stateOk := h.oidcStateStore.PeekInfo(req.MCPState)
	if !stateOk {
		writeJSON(w, http.StatusBadRequest, mcpConsentErrorResponse{Error: "invalid_state"})
		return
	}

	if storedClientID != req.ClientID {
		h.logger.Warn("mcp-consent: client_id mismatch",
			"expected", storedClientID, "got", req.ClientID)
		writeJSON(w, http.StatusBadRequest, mcpConsentErrorResponse{Error: "client_mismatch"})
		return
	}

	if req.Action == "deny" {
		// Delete the state entry (cleanup) and build error redirect.
		h.oidcStateStore.Delete(req.MCPState)

		target, err := url.Parse(redirectURI)
		if err != nil {
			h.logger.Error("mcp-consent: failed to parse redirect URI", "error", err)
			writeJSON(w, http.StatusInternalServerError, mcpConsentErrorResponse{Error: "internal_error"})
			return
		}
		params := target.Query()
		params.Set("error", "access_denied")
		params.Set("state", req.MCPState)
		target.RawQuery = params.Encode()

		writeJSON(w, http.StatusOK, mcpConsentResponse{RedirectURL: target.String()})
		return
	}

	// Action == "approve": generate consent code and return redirect.
	code, err := h.consentStore.Store(ConsentCodeEntry{
		Email:          email,
		TenantID:       tenantID,
		TenantSlug:     tenantSlug,
		MCPState:       req.MCPState,
		ClientID:       req.ClientID,
		ApprovedScopes: scopes,
	})
	if err != nil {
		h.logger.Error("mcp-consent: failed to store consent code", "error", err)
		writeJSON(w, http.StatusInternalServerError, mcpConsentErrorResponse{Error: "internal_error"})
		return
	}

	redirectURL := "/oauth/callback?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(req.MCPState)

	h.logger.Info("mcp-consent: consent approved",
		"email", email, "client_id", req.ClientID)

	writeJSON(w, http.StatusOK, mcpConsentResponse{RedirectURL: redirectURL})
}

// WithMCPConsentHandler sets the MCP consent handler for the server.
// When set, POST /api/auth/mcp-consent is registered behind the full auth chain.
func WithMCPConsentHandler(handler *MCPConsentHandler) ServerOption {
	return func(s *Server) {
		s.mcpConsentHandler = handler
	}
}
