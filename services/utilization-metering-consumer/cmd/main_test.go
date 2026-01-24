package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
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

	// This is a placeholder test that will be expanded with full integration tests.
	// The actual service startup is tested in TestHealthEndpoint_Integration.
	// This test just verifies environment variable handling and basic setup.
}

func TestCreateHTTPServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	readiness := &readinessState{}
	readinessMu := &sync.RWMutex{}

	server := createHTTPServer("18082", readiness, readinessMu, logger)

	// Verify server configuration
	if server.Addr != ":18082" {
		t.Errorf("Expected Addr ':18082', got '%s'", server.Addr)
	}

	if server.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("Expected ReadHeaderTimeout 10s, got %v", server.ReadHeaderTimeout)
	}

	if server.WriteTimeout != 30*time.Second {
		t.Errorf("Expected WriteTimeout 30s, got %v", server.WriteTimeout)
	}

	if server.IdleTimeout != 120*time.Second {
		t.Errorf("Expected IdleTimeout 120s, got %v", server.IdleTimeout)
	}

	// Verify handler is set
	if server.Handler == nil {
		t.Error("Expected Handler to be set, got nil")
	}
}

func TestHealthEndpoint_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Initialize logger for test
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create readiness state
	readiness := &readinessState{}
	readinessMu := &sync.RWMutex{}

	// Create HTTP server using extracted function
	httpServer := createHTTPServer("18081", readiness, readinessMu, logger)

	// Start server in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErrors := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

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

	// Test 1: Verify liveness endpoint
	testEndpoint(t, ctx, "http://localhost:18081/healthz", http.StatusOK, "OK")

	// Test 2: Verify readiness endpoint (should be NOT_READY initially)
	testEndpoint(t, ctx, "http://localhost:18081/ready", http.StatusServiceUnavailable, "NOT_READY")

	// Test 3: Mark consumer as ready and verify readiness probe
	readinessMu.Lock()
	readiness.consumerInitialized = true
	readinessMu.Unlock()

	testEndpoint(t, ctx, "http://localhost:18081/ready", http.StatusOK, "READY")

	// Test 4: Verify metrics endpoint returns Prometheus format
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:18081/metrics", nil)
	if err != nil {
		t.Fatalf("Failed to create metrics request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to call metrics endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Metrics endpoint: expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read metrics response: %v", err)
	}

	// Verify Prometheus format (contains # HELP and # TYPE comments)
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "# HELP") || !strings.Contains(bodyStr, "# TYPE") {
		t.Errorf("Metrics endpoint did not return Prometheus format")
	}

	// Verify no server errors occurred
	select {
	case err := <-serverErrors:
		t.Fatalf("Server error occurred: %v", err)
	default:
		// No errors, test passed
	}
}

// testEndpoint is a helper function to test HTTP endpoints.
func testEndpoint(t *testing.T, ctx context.Context, url string, expectedStatus int, expectedBody string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("Failed to create request for %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to call %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != expectedStatus {
		t.Errorf("%s: expected status %d, got %d", url, expectedStatus, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body from %s: %v", url, err)
	}

	if string(body) != expectedBody {
		t.Errorf("%s: expected body '%s', got '%s'", url, expectedBody, string(body))
	}
}
