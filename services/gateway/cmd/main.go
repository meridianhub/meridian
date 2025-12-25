// Package main is the entry point for the Gateway service.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/meridianhub/meridian/services/gateway/internal/config"
	"github.com/meridianhub/meridian/services/gateway/internal/server"
)

// Build information set via ldflags during compilation.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func main() {
	// Initialize structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting gateway service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	// Run the service
	if err := run(logger); err != nil {
		logger.Error("service failed", "error", err)
		os.Exit(1)
	}

	logger.Info("service stopped gracefully")
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	// Load configuration
	cfg, err := config.LoadFromEnv()
	if err != nil {
		return err
	}

	logger.Info("configuration loaded",
		"port", cfg.Port,
		"base_domain", cfg.BaseDomain,
		"backends", len(cfg.Backends))

	// Log backend routes
	for _, backend := range cfg.Backends {
		logger.Info("backend route configured",
			"name", backend.Name,
			"path_prefix", backend.PathPrefix,
			"target", backend.Target)
	}

	// Create and start server
	srv, err := server.New(cfg, logger)
	if err != nil {
		return err
	}

	if err := srv.Start(ctx); err != nil {
		return err
	}

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	logger.Info("received signal", "signal", sig)

	// Graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return srv.Shutdown(shutdownCtx)
}
