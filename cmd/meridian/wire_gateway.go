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
