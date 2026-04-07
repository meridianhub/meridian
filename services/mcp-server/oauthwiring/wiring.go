// Package oauthwiring provides a public factory for creating MCP OAuth 2.1
// components needed by the unified binary. The internal auth package handles
// the implementation details; this package exposes only the wiring surface.
package oauthwiring

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	mcpauth "github.com/meridianhub/meridian/services/mcp-server/internal/auth"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
)

// ConsentEntry holds the state stored alongside a consent code.
// Matches the internal mcpauth.ConsentEntry type.
type ConsentEntry = mcpauth.ConsentEntry

// ConsentCodeConsumer can consume a consent code exactly once.
type ConsentCodeConsumer = mcpauth.ConsentCodeConsumer

// PeekInfoResult holds metadata about an in-flight OIDC authorization.
type PeekInfoResult = mcpauth.PeekInfoResult

// OIDCStateStore is the OIDC flow state store.
type OIDCStateStore = mcpauth.OIDCStateStore

// Config holds configuration for the MCP OAuth 2.1 wiring.
type Config struct {
	Signer            *platformauth.JWTSigner
	BaseDomain        string
	BaseURL           string
	ClientID          string
	RedirectURI       string
	TokenTTL          time.Duration
	DefaultTenantSlug string
	Logger            *slog.Logger
}

// Endpoints holds the HTTP handlers and stores produced by wiring.
type Endpoints struct {
	// HTTP handlers for mounting on the gateway.
	Authorize   http.Handler
	Callback    http.Handler
	Token       http.Handler
	ConsentInfo http.Handler
	Metadata    http.HandlerFunc
	Register    http.Handler

	// StateStore is the shared OIDCStateStore, exposed so the BFF consent
	// handler can peek and delete entries via its OIDCStatePeeker interface.
	StateStore *OIDCStateStore
}

// noopTokenIssuer is a fallback for the token endpoint. In the consent-based
// OIDC flow, tokens are pre-signed via StoreWithToken.
type noopTokenIssuer struct{}

func (n *noopTokenIssuer) Issue(claims map[string]any) (string, error) {
	return fmt.Sprintf("mcp-fallback-%v", claims["client_id"]), nil
}

// Wire creates shared stores and MCP OAuth 2.1 handlers.
// The consentConsumer bridges the BFF-side consent store to the OIDC handler.
// Returns Endpoints for mounting, a cleanup function, and any error.
func Wire(cfg Config, consentConsumer ConsentCodeConsumer) (*Endpoints, func(), error) {
	stateStore := mcpauth.NewOIDCStateStore()
	codeStore := mcpauth.NewCodeStore()
	clientRegistry := mcpauth.NewClientRegistry()

	oauthCfg := mcpauth.OAuthConfig{
		ClientID:         cfg.ClientID,
		AuthorizationURL: cfg.BaseURL + "/oauth/authorize",
		TokenURL:         cfg.BaseURL + "/oauth/token",
		RedirectURI:      cfg.RedirectURI,
	}

	oidcHandler, err := mcpauth.NewOIDCHandler(mcpauth.OIDCHandlerConfig{
		OAuth:             oauthCfg,
		StateStore:        stateStore,
		CodeStore:         codeStore,
		Registry:          clientRegistry,
		Signer:            cfg.Signer,
		ConsentStore:      consentConsumer,
		TokenTTL:          cfg.TokenTTL,
		DefaultTenantSlug: cfg.DefaultTenantSlug,
		BaseURL:           cfg.BaseURL,
		BaseDomain:        cfg.BaseDomain,
		Logger:            cfg.Logger,
	})
	if err != nil {
		stateStore.Close()
		codeStore.Close()
		clientRegistry.Close()
		return nil, nil, fmt.Errorf("create OIDC handler: %w", err)
	}

	tokenHandler := mcpauth.NewTokenHandler(oauthCfg, codeStore, &noopTokenIssuer{})
	regHandler := mcpauth.NewRegistrationHandler(clientRegistry, cfg.Logger)
	metadataHandler := mcpauth.NewMetadataHandler(cfg.BaseURL)

	cleanup := func() {
		stateStore.Close()
		codeStore.Close()
		clientRegistry.Close()
	}

	return &Endpoints{
		Authorize:   http.HandlerFunc(oidcHandler.HandleAuthorize),
		Callback:    http.HandlerFunc(oidcHandler.HandleCallback),
		Token:       tokenHandler,
		ConsentInfo: http.HandlerFunc(oidcHandler.HandleConsentInfo),
		Metadata:    metadataHandler,
		Register:    regHandler,
		StateStore:  stateStore,
	}, cleanup, nil
}
