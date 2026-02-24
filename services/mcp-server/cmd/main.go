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
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
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

	// For stdio, we run until stdin closes or we receive a signal.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	go func() {
		<-sigChan
		logger.Info("received shutdown signal")
		cancel()
	}()

	return srv.Run(ctx)
}

func runSSE(logger *slog.Logger, cfg server.Config) error {
	port := env.GetEnvOrDefault("MCP_SSE_PORT", "8090")
	addr := fmt.Sprintf(":%s", port)

	logger.Info("using SSE transport", "address", addr)

	sseTr := transport.NewSSETransport(logger)
	defer sseTr.Close()

	srv := server.New(sseTr, cfg, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", sseTr.HandleSSE)
	mux.HandleFunc("/message", sseTr.HandleMessage)

	httpServer := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// Start MCP server loop in background
	serverErrors := bootstrap.ServerErrorChannel(2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := srv.Run(ctx); err != nil {
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

	if err := bootstrap.WaitForShutdownSignal(sigChan, serverErrors, logger); err != nil {
		cancel()
		_ = bootstrap.GracefulShutdown(context.Background(), logger, httpServer)
		return err
	}

	cancel()
	return bootstrap.GracefulShutdown(context.Background(), logger, httpServer)
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
