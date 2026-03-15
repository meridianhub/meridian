// Package main is the entry point for the MCP server.
// It supports stdio and streamable HTTP transports for Model Context Protocol communication.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpauth "github.com/meridianhub/meridian/services/mcp-server/internal/auth"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/env"
)

var errUnknownTransport = errors.New("unknown transport")

// Build information set via ldflags during compilation.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func main() {
	// Log to stderr: in stdio mode, stdout is the JSON-RPC wire protocol channel.
	// Logging to stdout would corrupt the protocol for MCP clients.
	logLevel := parseLogLevel(env.GetEnvOrDefault("LOG_LEVEL", ""))
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting mcp-server",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	if err := run(logger); err != nil {
		logger.Error("service failed", "error", err)
		os.Exit(1)
	}

	logger.Info("service stopped gracefully")
}

func run(logger *slog.Logger) error {
	transportMode := env.GetEnvOrDefault("MCP_TRANSPORT", "stdio")
	serverName := env.GetEnvOrDefault("MCP_SERVER_NAME", "meridian-mcp")

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: Version,
	}, &mcp.ServerOptions{
		Logger: logger,
	})

	// Wire tools, resources, and prompts onto the server.
	// cookbookFS is nil until the cookbook directory is embedded at build time.
	cleanup, err := wireServer(srv, logger, nil)
	if err != nil {
		return fmt.Errorf("wire server: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	switch transportMode {
	case "stdio":
		return runStdio(logger, srv)
	case "http":
		return runHTTP(logger, srv)
	default:
		return bootstrap.Permanent(fmt.Errorf("%w: %s (expected stdio or http)", errUnknownTransport, transportMode))
	}
}

func runStdio(logger *slog.Logger, srv *mcp.Server) error {
	logger.Info("using stdio transport")

	// For stdio, we run until stdin closes or we receive a signal.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	go func() {
		select {
		case <-sigChan:
			logger.Info("received shutdown signal")
			cancel()
		case <-ctx.Done():
		}
	}()

	return srv.Run(ctx, &mcp.StdioTransport{})
}

const (
	httpReadHeaderTimeout = 10 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpWriteTimeout      = 0 // Streamable HTTP may use long-lived responses
	httpIdleTimeout       = 120 * time.Second
	shutdownTimeout       = 30 * time.Second
)

// buildOAuthConfig reads OAuth configuration from environment variables.
// Returns a zero-value config and false when OAuth is not configured (MCP_OAUTH_ENABLED != "true").
func buildOAuthConfig(baseURL string) (mcpauth.OAuthConfig, bool) {
	if env.GetEnvOrDefault("MCP_OAUTH_ENABLED", "false") != "true" {
		return mcpauth.OAuthConfig{}, false
	}
	clientID := env.GetEnvOrDefault("MCP_OAUTH_CLIENT_ID", "meridian-mcp")
	return mcpauth.OAuthConfig{
		ClientID:         clientID,
		AuthorizationURL: baseURL + "/oauth/authorize",
		TokenURL:         baseURL + "/oauth/token",
		RedirectURI:      env.GetEnvOrDefault("MCP_OAUTH_REDIRECT_URI", baseURL+"/oauth/callback"),
	}, true
}

// buildBearerValidator constructs the BearerValidator to use for OAuth-protected endpoints.
//
// When MCP_JWKS_URL is set, it creates a JWKSBearerValidator that validates tokens
// using Dex public keys — suitable for production. When MCP_JWKS_URL is absent
// (development / CI), it falls back to the passthroughValidator.
//
// Returns the validator, an optional cleanup function, and an error. When MCP_JWKS_URL
// is explicitly configured but initialisation fails, an error is returned to prevent
// a fail-open auth path.
func buildBearerValidator(ctx context.Context, logger *slog.Logger) (mcpauth.BearerValidator, func(), error) {
	jwksURL := env.GetEnvOrDefault(mcpauth.EnvJWKSURL, "")
	if jwksURL == "" {
		logger.Warn("MCP_JWKS_URL not set — bearer token validation is disabled (passthrough mode)")
		return &passthroughValidator{logger: logger}, nil, nil
	}

	v, err := mcpauth.NewJWKSBearerValidator(ctx, jwksURL)
	if err != nil {
		return nil, nil, fmt.Errorf("JWKS bearer validator initialisation failed (fail-closed): %w", err)
	}

	logger.Info("JWKS bearer validation enabled", "jwks_url", jwksURL)
	return v, func() { _ = v.Close() }, nil
}

// passthroughValidator accepts every token without verification.
// Used when MCP_JWKS_URL is not configured — development and CI only.
// For production deployments, set MCP_JWKS_URL to enable JWKS validation.
type passthroughValidator struct {
	logger *slog.Logger
}

func (p *passthroughValidator) ValidateBearer(_ string) error {
	p.logger.Warn("bearer token validation skipped — set MCP_JWKS_URL for production")
	return nil
}

// passthroughIssuer echoes a fixed opaque token.
// Replace with a real JWT signer for production use.
type passthroughIssuer struct {
	logger *slog.Logger
}

func (p *passthroughIssuer) Issue(claims map[string]any) (string, error) {
	p.logger.Warn("token issuer is a passthrough — configure a real JWT issuer for production")
	// In production this would sign a JWT. For now, issue a structured opaque token.
	return fmt.Sprintf("mcp-issued-%v", claims["client_id"]), nil
}

func runHTTP(logger *slog.Logger, srv *mcp.Server) error {
	port := env.GetEnvOrDefault("MCP_PORT", "8090")
	addr := fmt.Sprintf(":%s", port)

	logger.Info("using streamable HTTP transport", "address", addr)

	// The SDK's StreamableHTTPHandler creates sessions and transports internally.
	streamableHandler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return srv
	}, nil)

	mux := http.NewServeMux()

	// OAuth 2.1 endpoints (optional — enabled via MCP_OAUTH_ENABLED=true).
	baseURL := env.GetEnvOrDefault("MCP_BASE_URL", fmt.Sprintf("http://localhost:%s", port))
	oauthCfg, oauthEnabled := buildOAuthConfig(baseURL)
	if oauthEnabled {
		logger.Info("OAuth 2.1 enabled",
			"authorization_url", oauthCfg.AuthorizationURL,
			"token_url", oauthCfg.TokenURL)

		codeStore := mcpauth.NewCodeStore()
		defer codeStore.Close()

		// Dynamic client registration (RFC 7591) — MCP clients like Claude.ai
		// register themselves before starting the OAuth flow.
		clientRegistry := mcpauth.NewClientRegistry()
		defer clientRegistry.Close()
		regHandler := mcpauth.NewRegistrationHandler(clientRegistry, logger)
		mux.Handle("/oauth/register", regHandler)

		// Token endpoint uses passthrough issuer as fallback; the OIDC flow
		// stores pre-signed JWTs directly via StoreWithToken.
		issuer := &passthroughIssuer{logger: logger}
		tokenHandler := mcpauth.NewTokenHandler(oauthCfg, codeStore, issuer)
		mux.Handle("/oauth/token", tokenHandler)

		// RFC 8414 OAuth Authorization Server Metadata — required by MCP clients
		// (e.g. Claude.ai) to discover auth endpoints before connecting.
		mux.HandleFunc("/.well-known/oauth-authorization-server", mcpauth.NewMetadataHandler(baseURL, oauthCfg))

		meta := mcpauth.Metadata{
			AuthorizationURL: oauthCfg.AuthorizationURL,
			TokenURL:         oauthCfg.TokenURL,
		}

		baseDomain := env.GetEnvOrDefault("MCP_BASE_DOMAIN", "")

		// OIDC-backed authorization flow: /oauth/authorize → Dex → /oauth/callback.
		dexIssuerURL := env.GetEnvOrDefault("MCP_DEX_ISSUER_URL", "")
		dexClientID := env.GetEnvOrDefault("MCP_DEX_CLIENT_ID", "meridian-service")
		dexCallbackURL := env.GetEnvOrDefault("MCP_DEX_CALLBACK_URL", baseURL+"/oauth/callback")

		if dexIssuerURL != "" {
			// Build JWT signer with the same key as BFF for session sharing.
			signer, err := platformauth.NewJWTSigner(platformauth.JWTSignerConfig{
				PrivateKeyPEM: env.GetEnvOrDefault("JWT_SIGNING_KEY", ""),
				KeyID:         env.GetEnvOrDefault("JWT_SIGNING_KEY_ID", "meridian-1"),
				Issuer:        env.GetEnvOrDefault("JWT_SIGNING_ISSUER", "meridian"),
			})
			if err != nil {
				return fmt.Errorf("jwt signer: %w", err)
			}

			oidcStateStore := mcpauth.NewOIDCStateStore()
			defer oidcStateStore.Close()

			defaultTenantSlug := env.GetEnvOrDefault("MCP_DEFAULT_TENANT_SLUG", "")

			oidcHandler, err := mcpauth.NewOIDCHandler(mcpauth.OIDCHandlerConfig{
				OIDC: mcpauth.OIDCConfig{
					DexIssuerURL: dexIssuerURL,
					ClientID:     dexClientID,
					CallbackURL:  dexCallbackURL,
				},
				OAuth:             oauthCfg,
				StateStore:        oidcStateStore,
				CodeStore:         codeStore,
				Registry:          clientRegistry,
				Signer:            signer,
				DefaultTenantSlug: defaultTenantSlug,
				BaseURL:           baseURL,
				BaseDomain:        baseDomain,
				Logger:            logger,
			})
			if err != nil {
				return fmt.Errorf("oidc handler: %w", err)
			}

			mux.HandleFunc("/oauth/authorize", oidcHandler.HandleAuthorize)
			mux.HandleFunc("/oauth/callback", oidcHandler.HandleCallback)

			logger.Info("OIDC-backed OAuth enabled",
				"dex_issuer", dexIssuerURL,
				"callback", dexCallbackURL)
		} else {
			// No Dex configured — use the direct authorization handler (dev/CI only).
			logger.Warn("MCP_DEX_ISSUER_URL not set — /oauth/authorize issues codes without authentication")
			authzHandler := mcpauth.NewAuthorizationHandler(oauthCfg, codeStore, clientRegistry)
			mux.Handle("/oauth/authorize", authzHandler)
		}

		// Bearer token validation on the /mcp endpoint.
		validator, validatorCleanup, err := buildBearerValidator(context.Background(), logger)
		if err != nil {
			return fmt.Errorf("bearer validator: %w", err)
		}
		if validatorCleanup != nil {
			defer validatorCleanup()
		}
		bearerMW := mcpauth.NewBearerMiddleware(validator, meta)

		// Subdomain-to-tenant validation: ensures the request's subdomain
		// matches the authenticated user's tenant from the JWT.
		subdomainMW := mcpauth.NewTenantSubdomainMiddleware(baseDomain, logger)

		mcpHandler := bearerMW.Handler(streamableHandler)

		// If the validator supports claims extraction (JWKS mode), wrap with
		// subdomain validation. The passthrough validator in dev mode does not
		// implement ClaimsBearerValidator, so subdomain checks are skipped.
		if claimsValidator, ok := validator.(mcpauth.ClaimsBearerValidator); ok {
			mcpHandler = subdomainMW.Handler(claimsValidator, meta, mcpHandler)
		}

		mux.Handle("/mcp", mcpHandler)
	} else {
		mux.Handle("/mcp", streamableHandler)
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}

	// Start HTTP server
	serverErrors := bootstrap.ServerErrorChannel(1)

	go func() {
		logger.Info("HTTP server starting", "address", addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("http server: %w", err)
		}
	}()

	// Wait for signal or error
	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	serverErr := bootstrap.WaitForShutdownSignal(sigChan, serverErrors, logger)

	// Create the shutdown context AFTER the signal/error arrives so the full
	// timeout window is available for graceful drain.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()

	_ = bootstrap.GracefulShutdown(shutdownCtx, logger, httpServer)
	return serverErr
}

// parseLogLevel converts a string log level to slog.Level.
func parseLogLevel(levelStr string) slog.Level {
	switch strings.ToLower(levelStr) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
