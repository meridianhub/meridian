package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	identitypersistence "github.com/meridianhub/meridian/services/identity/adapters/persistence"
	identityconnector "github.com/meridianhub/meridian/services/identity/connector"
	identitydex "github.com/meridianhub/meridian/services/identity/dex"
	"github.com/meridianhub/meridian/services/mcp-server/oauthwiring"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/env"

	"gorm.io/gorm"
)

// wireEmbeddedDex creates the embedded Dex OIDC server and returns a gateway
// ServerOption that mounts its HTTP handler at /dex/*. When DEX_ISSUER is not
// set, returns a no-op option (Dex is opt-in).
func wireEmbeddedDex(ctx context.Context, identityDB *gorm.DB, logger *slog.Logger) (gateway.ServerOption, error) {
	noopOption := func(*gateway.Server) {}

	issuer := os.Getenv("DEX_ISSUER")
	if issuer == "" {
		logger.Info("DEX_ISSUER not set, embedded Dex disabled")
		return noopOption, nil
	}

	baseDomain := env.GetEnvOrDefault("DEX_BASE_DOMAIN", env.GetEnvOrDefault("BASE_DOMAIN", "localhost"))

	// Create identity repository and connector.
	repo := identitypersistence.NewRepository(identityDB)
	conn, err := identityconnector.New(repo, logger)
	if err != nil {
		return nil, fmt.Errorf("dex connector: %w", err)
	}

	// Build client list: default demo client + MCP OAuth callback.
	clients := []identitydex.ClientConfig{identitydex.DefaultDemoClient(baseDomain)}

	// Create embedded Dex.
	embedded, err := identitydex.New(ctx, identitydex.Config{
		Issuer:    issuer,
		Connector: conn,
		Logger:    logger,
		Clients:   clients,
	})
	if err != nil {
		return nil, fmt.Errorf("embedded dex: %w", err)
	}

	// Start the Dex OIDC HTTP server (creates signing keys, mounts endpoints).
	if err := embedded.StartServer(ctx, issuer, true); err != nil {
		return nil, fmt.Errorf("dex server start: %w", err)
	}

	logger.Info("embedded Dex wired", "issuer", issuer, "base_domain", baseDomain)
	return gateway.WithDexHandler(embedded.Handler()), nil
}

// wireBFFAuth creates the BFF auth handler for direct password login.
// The handler validates credentials via the identity connector and signs
// JWTs with Meridian's own RSA key. This bypasses Dex for password auth.
//
// Environment variables:
//   - JWT_SIGNING_KEY: RSA private key in PEM format (auto-generated if unset)
//   - JWT_SIGNING_KEY_ID: kid header value (default: "meridian-1")
//   - JWT_SIGNING_ISSUER: iss claim value (default: "meridian")
//   - JWT_TOKEN_TTL: token lifetime (default: "1h")
func wireBFFAuth(identityDB *gorm.DB, logger *slog.Logger) (*platformauth.JWTSigner, []gateway.ServerOption) {
	signer, err := platformauth.NewJWTSigner(platformauth.JWTSignerConfig{
		PrivateKeyFile: os.Getenv("JWT_SIGNING_KEY_FILE"),
		PrivateKeyPEM:  os.Getenv("JWT_SIGNING_KEY"),
		KeyID:          env.GetEnvOrDefault("JWT_SIGNING_KEY_ID", "meridian-1"),
		Issuer:         env.GetEnvOrDefault("JWT_SIGNING_ISSUER", "meridian"),
	})
	if err != nil {
		logger.Error("failed to create JWT signer, BFF auth disabled", "error", err)
		return nil, nil
	}

	identityRepo := identitypersistence.NewRepository(identityDB)
	conn, err := identityconnector.New(identityRepo, logger)
	if err != nil {
		logger.Error("failed to create identity connector, BFF auth disabled", "error", err)
		return nil, nil
	}

	tokenTTL := env.GetEnvAsDuration("JWT_TOKEN_TTL", time.Hour)

	handler, err := gateway.NewAuthHandler(gateway.AuthHandlerConfig{
		Connector: conn,
		Signer:    signer,
		TokenTTL:  tokenTTL,
		Logger:    logger,
	})
	if err != nil {
		logger.Error("failed to create auth handler, BFF auth disabled", "error", err)
		return nil, nil
	}

	logger.Info("BFF auth handler initialized",
		"issuer", signer.Issuer(),
		"key_id", signer.KeyID(),
		"token_ttl", tokenTTL)

	return signer, []gateway.ServerOption{gateway.WithAuthHandler(handler)}
}

// wireMCPOAuth creates shared in-memory stores and wires the consent-based OAuth
// 2.1 flow for the unified binary. The BFF consent handler and the MCP OIDC handler
// share the same ConsentCodeStore and OIDCStateStore, enabling the full flow:
//
//	MCP client -> /oauth/authorize -> consent page -> POST /api/auth/mcp-consent
//	           -> /oauth/callback -> /oauth/token -> MCP client has JWT
//
// Returns gateway ServerOptions to mount both the consent handler and OAuth endpoints,
// plus a cleanup function to stop background goroutines.
// Returns (nil, noop) when MCP OAuth is not enabled or the signer is unavailable.
func wireMCPOAuth(signer *platformauth.JWTSigner, logger *slog.Logger) ([]gateway.ServerOption, func()) {
	noop := func() {}

	if signer == nil {
		logger.Info("MCP OAuth disabled: no JWT signer available")
		return nil, noop
	}

	if env.GetEnvOrDefault("MCP_OAUTH_ENABLED", "false") != "true" {
		logger.Info("MCP OAuth disabled: MCP_OAUTH_ENABLED not set")
		return nil, noop
	}

	baseDomain := env.GetEnvOrDefault("BASE_DOMAIN", "localhost")
	baseURL := env.GetEnvOrDefault("MCP_BASE_URL", "")
	clientID := env.GetEnvOrDefault("MCP_OAUTH_CLIENT_ID", "meridian-mcp")
	redirectURI := env.GetEnvOrDefault("MCP_OAUTH_REDIRECT_URI", baseURL+"/oauth/callback")
	tokenTTL := env.GetEnvAsDuration("JWT_TOKEN_TTL", time.Hour)

	// Create the BFF-side consent code store.
	consentStore := gateway.NewConsentCodeStore()

	// Adapter: gateway.ConsentCodeStore -> oauthwiring.ConsentCodeConsumer
	consentConsumer := &consentStoreAdapter{store: consentStore}

	// Wire MCP OAuth endpoints via the public oauthwiring package.
	endpoints, mcpCleanup, err := oauthwiring.Wire(oauthwiring.Config{
		Signer:            signer,
		BaseDomain:        baseDomain,
		BaseURL:           baseURL,
		ClientID:          clientID,
		RedirectURI:       redirectURI,
		TokenTTL:          tokenTTL,
		DefaultTenantSlug: env.GetEnvOrDefault("MCP_DEFAULT_TENANT_SLUG", ""),
		Logger:            logger.With("component", "mcp-oauth"),
	}, consentConsumer)
	if err != nil {
		logger.Error("failed to wire MCP OAuth, MCP OAuth disabled", "error", err)
		consentStore.Close()
		return nil, noop
	}

	// Adapter: oauthwiring.OIDCStateStore -> gateway.OIDCStatePeeker
	oidcPeeker := &oidcStatePeekerAdapter{store: endpoints.StateStore}

	consentHandler := gateway.NewMCPConsentHandler(gateway.MCPConsentHandlerConfig{
		ConsentStore:   consentStore,
		OIDCStateStore: oidcPeeker,
		Logger:         logger.With("component", "mcp-consent"),
	})

	cleanup := func() {
		consentStore.Close()
		if mcpCleanup != nil {
			mcpCleanup()
		}
	}

	opts := []gateway.ServerOption{
		gateway.WithMCPConsentHandler(consentHandler),
		gateway.WithMCPOAuthEndpoints(&gateway.MCPOAuthEndpoints{
			Authorize:   endpoints.Authorize,
			Callback:    endpoints.Callback,
			Token:       endpoints.Token,
			ConsentInfo: endpoints.ConsentInfo,
			Metadata:    endpoints.Metadata,
			Register:    endpoints.Register,
		}),
	}

	logger.Info("MCP OAuth wired with shared stores",
		"base_domain", baseDomain,
		"base_url", baseURL,
		"client_id", clientID)

	return opts, cleanup
}

// ─── MCP OAuth Adapters ─────────────────────────────────────────────────────

// consentStoreAdapter adapts gateway.ConsentCodeStore to oauthwiring.ConsentCodeConsumer.
// The BFF writes consent codes via the gateway store; the MCP OIDC handler consumes
// them via this adapter.
type consentStoreAdapter struct {
	store *gateway.ConsentCodeStore
}

func (a *consentStoreAdapter) Consume(code string) (oauthwiring.ConsentEntry, bool) {
	entry, ok := a.store.Consume(code)
	if !ok {
		return oauthwiring.ConsentEntry{}, false
	}
	return oauthwiring.ConsentEntry{
		Email:          entry.Email,
		TenantID:       entry.TenantID,
		TenantSlug:     entry.TenantSlug,
		MCPState:       entry.MCPState,
		ClientID:       entry.ClientID,
		ApprovedScopes: entry.ApprovedScopes,
	}, true
}

// oidcStatePeekerAdapter adapts oauthwiring.OIDCStateStore to gateway.OIDCStatePeeker.
// The MCP OIDC handler stores flow state; the BFF consent handler peeks and deletes
// entries via this adapter.
type oidcStatePeekerAdapter struct {
	store *oauthwiring.OIDCStateStore
}

func (a *oidcStatePeekerAdapter) PeekInfo(key string) (gateway.OIDCStatePeekResult, bool) {
	info, ok := a.store.PeekInfo(key)
	if !ok {
		return gateway.OIDCStatePeekResult{}, false
	}
	return gateway.OIDCStatePeekResult{
		ClientID:    info.ClientID,
		RedirectURI: info.RedirectURI,
		Scopes:      info.Scopes,
		MCPState:    info.MCPState,
		TenantSlug:  info.TenantSlug,
	}, true
}

func (a *oidcStatePeekerAdapter) Delete(key string) {
	a.store.Delete(key)
}
