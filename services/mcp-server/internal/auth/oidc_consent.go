package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	errConsentBaseNotConfigured = errors.New("consent redirect base is not configured")
	errConsentBaseURLInvalid    = errors.New("invalid consent redirect base URL: missing scheme or host")
)

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

	// Defense-in-depth: approved scopes must be a subset of the originally
	// requested scopes. Reject if the consent issuer returns broader access.
	if !scopesSubset(consentEntry.ApprovedScopes, flowState.RequestedScopes) {
		h.logger.Error("oidc: approved scopes exceed requested scopes",
			"approved", consentEntry.ApprovedScopes,
			"requested", flowState.RequestedScopes)
		http.Error(w, "approved scopes exceed requested scopes", http.StatusBadRequest)
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

// scopesSubset returns true if every element of approved is contained in requested.
func scopesSubset(approved, requested []string) bool {
	allowed := make(map[string]struct{}, len(requested))
	for _, s := range requested {
		allowed[s] = struct{}{}
	}
	for _, s := range approved {
		if _, ok := allowed[s]; !ok {
			return false
		}
	}
	return true
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

	consentURL, err := h.buildConsentPageURL(tenantSlug, stateKey, clientID)
	if err != nil {
		return "", fmt.Errorf("build consent URL: %w", err)
	}
	return consentURL, nil
}

// buildConsentPageURL constructs the UI consent page URL with tenant subdomain.
func (h *OIDCHandler) buildConsentPageURL(tenantSlug, stateKey, clientID string) (string, error) {
	base := h.baseURL
	if base == "" {
		if h.baseDomain == "" {
			return "", errConsentBaseNotConfigured
		}
		base = "https://" + h.baseDomain
	}

	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errConsentBaseURLInvalid
	}

	// Insert tenant subdomain if base domain is configured.
	if h.baseDomain != "" && tenantSlug != "" {
		host := parsed.Hostname()
		port := parsed.Port()
		if host == h.baseDomain || strings.HasSuffix(host, "."+h.baseDomain) {
			parsed.Host = tenantSlug + "." + h.baseDomain
			if port != "" {
				parsed.Host = tenantSlug + "." + h.baseDomain + ":" + port
			}
		}
	}

	params := url.Values{
		"mcp_state": {stateKey},
		"client_id": {clientID},
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/auth/mcp-consent"
	parsed.RawQuery = params.Encode()
	return parsed.String(), nil
}
