// Package main is the entry point for the Meridian open banking ledger service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// setupRoutes configures HTTP routes for the server
func setupRoutes() {
	// Health check endpoints
	http.HandleFunc("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "alive")
	})

	http.HandleFunc("/health/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ready")
	})

	http.HandleFunc("/health/startup", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "started")
	})

	// Root endpoint
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "Meridian v%s (commit: %s, built: %s)\n", Version, Commit, BuildDate)
	})
}

func main() {
	fmt.Printf("Meridian v%s (commit: %s, built: %s)\n", Version, Commit, BuildDate)

	// Get port from environment or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Setup routes
	setupRoutes()

	// Create server with proper timeouts (security best practice)
	server := &http.Server{
		Addr:              ":" + port,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

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

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
}
