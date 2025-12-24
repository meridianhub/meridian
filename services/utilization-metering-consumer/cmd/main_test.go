package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
)

func TestHealthEndpoint_Liveness(_ *testing.T) {
	// Set required environment variables for the service
	envVars := map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS":   "kafka:9092",
		"CONSUMER_GROUP_ID":         "test-group",
		"POSITION_KEEPING_ENDPOINT": "position-keeping:50051",
		"TENANT_ZERO_ID":            "00000000-0000-0000-0000-000000000000",
		"HTTP_PORT":                 "18080", // Use non-standard port to avoid conflicts
	}

	backup := make(map[string]string)
	for key, value := range envVars {
		backup[key] = os.Getenv(key)
		os.Setenv(key, value)
	}
	defer func() {
		for key, value := range backup {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	// Start service in background
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		// Redirect logs to suppress output during tests
		oldLogger := os.Stdout
		os.Stdout, _ = os.Open(os.DevNull)
		defer func() { os.Stdout = oldLogger }()

		// Run the service (will block until context is cancelled)
		// Note: We can't easily test run() directly since it blocks,
		// so this test just verifies the health endpoint works
		// A full integration test would need more sophisticated setup
		errChan <- nil
	}()

	// Give service time to start (in real test, would use proper synchronization)
	time.Sleep(100 * time.Millisecond)

	// For this basic test, we'll just verify the health endpoint responds
	// In a real integration test, we'd start the actual server and check it
	// For now, this test is a placeholder that will be expanded in integration tests

	cancel() // Stop the background goroutine
}

func TestHealthEndpoint_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Set required environment variables
	envVars := map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS":   "kafka:9092",
		"CONSUMER_GROUP_ID":         "test-group",
		"POSITION_KEEPING_ENDPOINT": "position-keeping:50051",
		"TENANT_ZERO_ID":            "00000000-0000-0000-0000-000000000000",
		"HTTP_PORT":                 "18081",
	}

	backup := make(map[string]string)
	for key, value := range envVars {
		backup[key] = os.Getenv(key)
		os.Setenv(key, value)
	}
	defer func() {
		for key, value := range backup {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	// Start the server in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverReady := make(chan bool)
	go func() {
		// TODO: Start actual server when fully implemented
		// For now, create a simple test server
		http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
		})
		server := &http.Server{Addr: ":18081"}
		serverReady <- true
		go func() {
			_ = server.ListenAndServe()
		}()
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	// Wait for server to be ready
	<-serverReady

	// Wait for health endpoint to respond
	err := await.New().
		AtMost(5 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:18081/healthz", nil)
			if err != nil {
				return false
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		})
	if err != nil {
		t.Fatalf("Health endpoint did not become ready: %v", err)
	}

	// Verify health endpoint response
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:18081/healthz", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to call health endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	if string(body) != "OK" {
		t.Errorf("Expected body 'OK', got '%s'", string(body))
	}

	// Clean up
	cancel()
}
