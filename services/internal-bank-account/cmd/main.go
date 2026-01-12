// Package main is the entry point for the InternalBankAccount service.
package main

import (
	"log/slog"
	"os"
	"strings"
)

// Build information set via ldflags during compilation
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func main() {
	// Initialize structured logging with configurable log level
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting internal-bank-account service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	// TODO: Initialize and run the service
	// This will be implemented in subsequent tasks:
	// - Task 3: Define domain models
	// - Task 4: Create database schema
	// - Task 5: Implement persistence layer
	// - Task 6: Define gRPC API
	// - Task 7: Implement gRPC service

	logger.Info("service stopped gracefully")
}

// parseLogLevel converts a string log level to slog.Level.
func parseLogLevel(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
