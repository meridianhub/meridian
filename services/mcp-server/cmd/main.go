// Package main is the entry point for the MCP server.
// It supports stdio and SSE transports for Model Context Protocol communication.
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

	mcpauth "github.com/meridianhub/meridian/services/mcp-server/internal/auth"
	"github.com/meridianhub/meridian/services/mcp-server/internal/server"
	"github.com/meridianhub/meridian/services/mcp-server/internal/transport"
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

	cfg := server.Config{
		ServerName:    serverName,
		ServerVersion: Version,
	}

	switch transportMode {
	case "stdio":
		return runStdio(logger, cfg)
	case "sse":
		return runSSE(logger, cfg)
	default:
		return bootstrap.Permanent(fmt.Errorf("%w: %s (expected stdio or sse)", errUnknownTransport, transportMode))
	}
}

func runStdio(logger *slog.Logger, cfg server.Config) error {
	logger.Info("using stdio transport")

	tr := transport.NewStdioTransport(os.Stdin, os.Stdout)
	defer tr.Close()

	srv := server.New(tr, cfg, logger)

	// Wire tools, resources, and prompts onto the server.
	cleanup, err := wireServer(srv, logger)
	if err != nil {
		return fmt.Errorf("wire server: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

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

	return srv.Run(ctx)
}

const (
	httpReadHeaderTimeout = 10 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpWriteTimeout      = 0 // SSE requires no write timeout (long-lived streams)
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

func runSSE(logger *slog.Logger, cfg server.Config) error {
	port := env.GetEnvOrDefault("MCP_SSE_PORT", "8090")
	addr := fmt.Sprintf(":%s", port)

	logger.Info("using SSE transport", "address", addr)

	sseTr := transport.NewSSETransport(logger)
	defer sseTr.Close()

	srv := server.New(sseTr, cfg, logger)

	// Wire tools, resources, and prompts onto the server.
	cleanup, wireErr := wireServer(srv, logger)
	if wireErr != nil {
		return fmt.Errorf("wire server: %w", wireErr)
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Streamable HTTP transport (MCP spec 2025-03-26).
	// Shares the same MCPServer instance so tools/resources/prompts are identical.
	streamableHandler := transport.NewStreamableHTTPHandler(srv, logger)
	defer streamableHandler.Close()

	mux := http.NewServeMux()

	// OAuth 2.1 endpoints (optional — enabled via MCP_OAUTH_ENABLED=true).
	baseURL := env.GetEnvOrDefault("MCP_BASE_URL", fmt.Sprintf("http://localhost:%s", port))
	oauthCfg, oauthEnabled := buildOAuthConfig(baseURL)
	if oauthEnabled {
		logger.Info("OAuth 2.1 enabled",
			"authorization_url", oauthCfg.AuthorizationURL,
			"token_url", oauthCfg.TokenURL)

		store := mcpauth.NewCodeStore()
		defer store.Close()
		issuer := &passthroughIssuer{logger: logger}
		authzHandler := mcpauth.NewAuthorizationHandler(oauthCfg, store)
		tokenHandler := mcpauth.NewTokenHandler(oauthCfg, store, issuer)

		mux.Handle("/oauth/authorize", authzHandler)
		mux.Handle("/oauth/token", tokenHandler)

		meta := mcpauth.Metadata{
			AuthorizationURL: oauthCfg.AuthorizationURL,
			TokenURL:         oauthCfg.TokenURL,
		}

		// Use a background context for JWKS initialisation: srvCtx doesn't exist
		// yet at this point (it is created below). The JWKS provider uses its own
		// Close() to stop the background refresh goroutine on shutdown.
		validator, validatorCleanup, err := buildBearerValidator(context.Background(), logger)
		if err != nil {
			return fmt.Errorf("bearer validator: %w", err)
		}
		if validatorCleanup != nil {
			defer validatorCleanup()
		}
		bearerMW := mcpauth.NewBearerMiddleware(validator, meta)

		mux.Handle("/mcp", bearerMW.Handler(streamableHandler))
		mux.Handle("/sse", bearerMW.Handler(http.HandlerFunc(sseTr.HandleSSE)))
		mux.Handle("/message", bearerMW.Handler(http.HandlerFunc(sseTr.HandleMessage)))
	} else {
		mux.Handle("/mcp", streamableHandler)
		mux.HandleFunc("/sse", sseTr.HandleSSE)
		mux.HandleFunc("/message", sseTr.HandleMessage)
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}

	// Start MCP server loop in background
	serverErrors := bootstrap.ServerErrorChannel(2)
	srvCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := srv.Run(srvCtx); err != nil {
			serverErrors <- fmt.Errorf("mcp server: %w", err)
		}
	}()

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
	cancel()
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
