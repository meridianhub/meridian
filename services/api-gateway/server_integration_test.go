package gateway

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// httpGet performs an HTTP GET request with context.
func httpGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

// getAvailablePort finds an available TCP port on localhost.
func getAvailablePort(ctx context.Context, t *testing.T) int {
	t.Helper()
	lc := &net.ListenConfig{}
	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	err = listener.Close()
	require.NoError(t, err)
	return port
}

// TestIntegration_HealthEndpoints starts an actual HTTP server and verifies
// health endpoints respond correctly to real HTTP requests.
func TestIntegration_HealthEndpoints(t *testing.T) {
	ctx := context.Background()

	// Find an available port
	port := getAvailablePort(ctx, t)

	// Create server
	config := &Config{
		Port:        port,
		BaseDomain:  "api.test.io",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	// Start server in background
	serverCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	serverReady := make(chan struct{})
	serverErr := make(chan error, 1)
	go func() {
		close(serverReady)
		if err := server.Start(serverCtx); err != nil {
			serverErr <- err
		}
	}()

	<-serverReady

	// Wait for server to be ready using await package
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	err := await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			resp, err := httpGet(ctx, baseURL+"/health")
			if err != nil {
				return false
			}
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		})
	require.NoError(t, err, "server failed to become ready")

	// Test /health endpoint
	t.Run("GET /health returns 200 OK", func(t *testing.T) {
		resp, err := httpGet(ctx, baseURL+"/health")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "OK", string(body))
	})

	// Test /ready endpoint
	t.Run("GET /ready returns 200 READY", func(t *testing.T) {
		resp, err := httpGet(ctx, baseURL+"/ready")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "READY", string(body))
	})

	// Test /healthz endpoint (legacy)
	t.Run("GET /healthz returns 200 OK (legacy)", func(t *testing.T) {
		resp, err := httpGet(ctx, baseURL+"/healthz")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "OK", string(body))
	})

	// Test /readyz endpoint (legacy)
	t.Run("GET /readyz returns 200 READY (legacy)", func(t *testing.T) {
		resp, err := httpGet(ctx, baseURL+"/readyz")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "READY", string(body))
	})

	// Test health endpoints work without Host header (K8s probe simulation)
	t.Run("health endpoints work without Host header", func(t *testing.T) {
		client := &http.Client{}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
		require.NoError(t, err)
		req.Host = "" // Clear Host header

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"health endpoint must work without Host header for K8s probes")
	})

	// Cleanup
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutdownCancel()
	err = server.Shutdown(shutdownCtx)
	assert.NoError(t, err)
}

// TestIntegration_APIRoutes_RequiresTenantContext verifies that API routes
// are separate from health routes and would require tenant context in production.
func TestIntegration_APIRoutes_RequiresTenantContext(t *testing.T) {
	ctx := context.Background()

	// Find an available port
	port := getAvailablePort(ctx, t)

	// Create server without tenant resolver
	config := &Config{
		Port:        port,
		BaseDomain:  "api.test.io",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	// Start server
	serverCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		_ = server.Start(serverCtx)
	}()

	// Wait for server to be ready
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	err := await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			resp, err := httpGet(ctx, baseURL+"/health")
			if err != nil {
				return false
			}
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		})
	require.NoError(t, err)

	// Test API route returns placeholder response
	t.Run("GET /v1/test returns 501 Not Implemented", func(t *testing.T) {
		resp, err := httpGet(ctx, baseURL+"/v1/test")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "gateway routing not yet implemented")
	})

	// Cleanup
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
}

// TestIntegration_ServerGracefulShutdown verifies the server shuts down gracefully.
func TestIntegration_ServerGracefulShutdown(t *testing.T) {
	ctx := context.Background()

	// Find an available port
	port := getAvailablePort(ctx, t)

	// Create server
	config := &Config{
		Port:        port,
		BaseDomain:  "api.test.io",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	// Start server
	serverCtx, cancel := context.WithCancel(ctx)

	serverDone := make(chan struct{})
	go func() {
		_ = server.Start(serverCtx)
		close(serverDone)
	}()

	// Wait for server to be ready
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	err := await.New().
		AtMost(5 * time.Second).
		PollInterval(50 * time.Millisecond).
		Until(func() bool {
			resp, err := httpGet(ctx, baseURL+"/health")
			if err != nil {
				return false
			}
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		})
	require.NoError(t, err)

	// Cancel context to signal shutdown
	cancel()

	// Initiate graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutdownCancel()

	err = server.Shutdown(shutdownCtx)
	assert.NoError(t, err)

	// Wait for server goroutine to complete
	select {
	case <-serverDone:
		// Server shut down cleanly
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down in time")
	}

	// Verify server is no longer accepting connections
	_, err = httpGet(ctx, baseURL+"/health")
	assert.Error(t, err, "server should not accept connections after shutdown")
}
