// Package main is the entry point for the audit-worker service.
// The audit-worker processes audit log entries from the outbox table,
// moving them to the audit log with retry logic and metrics collection.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Note: fmt.Fprintf is used for HTTP responses, log.Printf for application lifecycle logging

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

// getDBConnectionString returns database connection string from environment.
// DATABASE_URL is required - the service will fail fast if not provided.
func getDBConnectionString() string {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}
	return connStr
}

// getAuditSchema returns the audit schema from environment.
// Per ADR-0020, each service should run its own embedded worker.
// The AUDIT_SCHEMA environment variable must be set to specify which schema to process.
func getAuditSchema() string {
	schema := os.Getenv("AUDIT_SCHEMA")
	if schema == "" {
		log.Fatal("AUDIT_SCHEMA environment variable is required")
	}
	return schema
}

// setupDatabase initializes the database connection with GORM
func setupDatabase(_ context.Context) (*gorm.DB, error) {
	connStr := getDBConnectionString()

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
	log.Printf("audit-worker v%s (commit: %s, built: %s)", Version, Commit, BuildDate)

	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Initialize database connection
	ctx := context.Background()
	gormDB, err := setupDatabase(ctx)
	if err != nil {
		log.Fatalf("Failed to setup database: %v", err)
	}
	log.Println("Database connection established")

	// Start audit worker
	// The AUDIT_SCHEMA env var specifies which audit schema this worker processes.
	schema := getAuditSchema()
	auditWorker := audit.NewAuditWorker(gormDB, schema, logger)
	workerCtx, workerCancel := context.WithCancel(ctx)
	auditWorker.Start(workerCtx)
	log.Printf("Audit worker started for schema: %s", schema)

	// Get port from environment or use default
	port := getPort()

	// Setup routes on default mux
	setupRoutes(http.DefaultServeMux)

	// Create server with proper timeouts (security best practice)
	server := createServer(port)

	// Start server in background
	go func() {
		log.Printf("Starting HTTP server on :%s\n", port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down server...")

	// Cancel audit worker context
	workerCancel()

	// Stop audit worker gracefully
	log.Println("Stopping audit worker...")
	auditWorker.Stop()
	log.Println("Audit worker stopped")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaults.DefaultGracefulShutdown)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	log.Println("Shutdown complete")
}
