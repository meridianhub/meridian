// Package main is the entry point for the audit-worker service.
// The audit-worker processes audit log entries from the outbox table,
// moving them to the audit log with retry logic and metrics collection.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// setupRoutes configures HTTP routes for the server
func setupRoutes(mux *http.ServeMux) {
	// Health check endpoints
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "alive")
	})

	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ready")
	})

	mux.HandleFunc("/health/startup", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "started")
	})

	// Root endpoint
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "audit-worker v%s (commit: %s, built: %s)\n", Version, Commit, BuildDate)
	})

	// Prometheus metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())
}

// createServer creates an HTTP server with proper security timeouts
func createServer(port string) *http.Server {
	return &http.Server{
		Addr:              ":" + port,
		ReadHeaderTimeout: defaults.DefaultHTTPReadHeaderTimeout,
		ReadTimeout:       defaults.DefaultHTTPReadTimeout,
		WriteTimeout:      defaults.DefaultHTTPWriteTimeout,
		IdleTimeout:       2 * defaults.DefaultHTTPIdleTimeout, // Extended for long-running worker
	}
}

// getPort returns the port from environment or default value
func getPort() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return port
}

// Static errors for configuration validation.
var (
	ErrDatabaseURLRequired = errors.New("DATABASE_URL environment variable is required")
	ErrAuditSchemaRequired = errors.New("AUDIT_SCHEMA environment variable is required")
)

// getDBConnectionString returns database connection string from environment.
// DATABASE_URL is required - returns an error if not provided.
func getDBConnectionString() (string, error) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		return "", ErrDatabaseURLRequired
	}
	return connStr, nil
}

// getAuditSchema returns the audit schema from environment.
// Per ADR-0020, each service should run its own embedded worker.
// The AUDIT_SCHEMA environment variable must be set to specify which schema to process.
func getAuditSchema() (string, error) {
	schema := os.Getenv("AUDIT_SCHEMA")
	if schema == "" {
		return "", ErrAuditSchemaRequired
	}
	return schema, nil
}

// setupDatabase initializes the database connection with GORM
func setupDatabase(_ context.Context) (*gorm.DB, error) {
	connStr, err := getDBConnectionString()
	if err != nil {
		return nil, bootstrap.Permanent(err)
	}

	// Initialize GORM with PostgreSQL driver
	gormDB, err := gorm.Open(postgres.Open(connStr), &gorm.Config{
		// Disable default transaction for performance
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GORM: %w", err)
	}

	// Configure connection pool settings
	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database instance: %w", err)
	}

	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(1 * time.Hour)
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)

	return gormDB, nil
}

func main() {
	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting audit-worker service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	// Run the service with retry for transient startup errors
	if err := bootstrap.RunWithRetry(
		func() error { return run(logger) },
		bootstrap.WithRetryLogger(logger),
	); err != nil {
		logger.Error("service failed to start", "error", err)
		os.Exit(1)
	}

	logger.Info("service stopped gracefully")
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	// Validate audit schema (permanent config error)
	schema, err := getAuditSchema()
	if err != nil {
		return bootstrap.Permanent(err)
	}

	// Initialize database connection
	gormDB, err := setupDatabase(ctx)
	if err != nil {
		return fmt.Errorf("failed to setup database: %w", err)
	}
	logger.Info("database connection established")

	// Start audit worker
	auditWorker := audit.NewAuditWorker(gormDB, schema, logger)
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	auditWorker.Start(workerCtx)
	logger.Info("audit worker started", "schema", schema)

	// Setup and start HTTP server
	port := getPort()
	mux := http.NewServeMux()
	setupRoutes(mux)
	server := createServer(port)
	server.Handler = mux

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server", "port", port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	return awaitShutdown(logger, server, auditWorker, workerCancel, serverErrors)
}

// awaitShutdown waits for an interrupt signal or server error, then performs
// graceful shutdown of the audit worker and HTTP server.
func awaitShutdown(logger *slog.Logger, server *http.Server, auditWorker *audit.Worker, workerCancel context.CancelFunc, serverErrors <-chan error) error {
	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	var runErr error
	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		logger.Error("HTTP server error", "error", err)
		runErr = err
	}

	logger.Info("shutting down...")
	workerCancel()

	logger.Info("stopping audit worker...")
	auditWorker.Stop()
	logger.Info("audit worker stopped")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaults.DefaultGracefulShutdown)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", "error", err)
	}

	return runErr
}
