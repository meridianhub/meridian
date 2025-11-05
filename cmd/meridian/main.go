// Package main is the entry point for the Meridian open banking ledger service.
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

	"github.com/meridianhub/meridian/internal/platform/audit"
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
		_, _ = fmt.Fprintf(w, "Meridian v%s (commit: %s, built: %s)\n", Version, Commit, BuildDate)
	})
}

// createServer creates an HTTP server with proper security timeouts
func createServer(port string) *http.Server {
	return &http.Server{
		Addr:              ":" + port,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
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

// getDBConnectionString returns database connection string from environment
func getDBConnectionString() string {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		// Default for local development (matches Tiltfile)
		// #nosec G101 -- Local development credential only, overridden by DATABASE_URL in production
		connStr = "postgres://meridian:meridian@localhost:26257/meridian?sslmode=disable"
	}
	return connStr
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
	log.Printf("Meridian v%s (commit: %s, built: %s)", Version, Commit, BuildDate)

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
	auditWorker := audit.NewAuditWorker(gormDB, logger)
	workerCtx, workerCancel := context.WithCancel(ctx)
	auditWorker.Start(workerCtx)
	log.Println("Audit worker started")

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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	log.Println("Shutdown complete")
}
