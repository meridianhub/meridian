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

func main() {
	log.Printf("Meridian v%s (commit: %s, built: %s)", Version, Commit, BuildDate)

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

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
}
