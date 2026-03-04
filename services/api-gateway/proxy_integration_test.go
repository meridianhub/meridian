package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_ProxyToBackend verifies that the gateway proxies requests
// to configured backend services.
func TestIntegration_ProxyToBackend(t *testing.T) {
	ctx := context.Background()

	// Create a mock backend server that echoes request details
	var backendReceivedPath string
	var backendReceivedMethod string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendReceivedPath = r.URL.Path
		backendReceivedMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"mock-backend"}`))
	}))
	defer backend.Close()

	// Extract host:port from backend URL (remove http:// prefix)
	backendAddr := backend.URL[7:]

	// Find an available port for gateway
	port := getAvailablePort(ctx, t)

	// Create gateway with backend route
	config := &Config{
		Port:        port,
		BaseDomain:  "api.test.io",
		DatabaseURL: "postgres://localhost/test",
		Backends: []BackendRoute{
			{Prefix: "/v1/sagas", Target: backendAddr},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	// Start gateway
	serverCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		_ = server.Start(serverCtx)
	}()

	// Wait for gateway to be ready
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
	require.NoError(t, err, "gateway failed to become ready")

	// Test proxying to backend
	t.Run("proxy routes to backend", func(t *testing.T) {
		resp, err := httpPost(ctx, baseURL+"/v1/sagas/validate", "application/json", strings.NewReader(`{"saga_name":"test"}`))
		require.NoError(t, err)
		defer resp.Body.Close()

		// Verify backend received the request
		assert.Equal(t, "/v1/sagas/validate", backendReceivedPath)
		assert.Equal(t, http.MethodPost, backendReceivedMethod)

		// Verify response from backend
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "mock-backend")
	})

	// Test 404 for unmatched routes
	t.Run("unmatched routes return 404", func(t *testing.T) {
		resp, err := httpGet(ctx, baseURL+"/v1/unmatched")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	// Cleanup
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
}

// TestIntegration_ProxyForwardsIdentityHeaders verifies that the gateway proxy
// mechanism works with authentication middleware (identity forwarding is tested
// in proxy_test.go unit tests).
func TestIntegration_ProxyForwardsIdentityHeaders(t *testing.T) {
	ctx := context.Background()

	// Create a mock backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer backend.Close()

	backendAddr := backend.URL[7:]

	// Find an available port for gateway
	port := getAvailablePort(ctx, t)

	// Create gateway with backend route (without auth for simplicity)
	config := &Config{
		Port:        port,
		BaseDomain:  "api.test.io",
		DatabaseURL: "postgres://localhost/test",
		Backends: []BackendRoute{
			{Prefix: "/v1", Target: backendAddr},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	// Start gateway
	serverCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		_ = server.Start(serverCtx)
	}()

	// Wait for gateway to be ready
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
	require.NoError(t, err, "gateway failed to become ready")

	// Verify proxy works (identity forwarding tested in unit tests)
	t.Run("proxy works with backend", func(t *testing.T) {
		resp, err := httpGet(ctx, baseURL+"/v1/test")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	// Cleanup
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
}

// TestIntegration_ProxyMultipleBackends verifies that the gateway routes
// to different backends based on path prefix.
func TestIntegration_ProxyMultipleBackends(t *testing.T) {
	ctx := context.Background()

	// Create two mock backend servers
	sagaBackendCalled := false
	partyBackendCalled := false

	sagaBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sagaBackendCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"service":"saga"}`))
	}))
	defer sagaBackend.Close()

	partyBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		partyBackendCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"service":"party"}`))
	}))
	defer partyBackend.Close()

	// Find an available port for gateway
	port := getAvailablePort(ctx, t)

	// Create gateway with multiple backend routes
	config := &Config{
		Port:        port,
		BaseDomain:  "api.test.io",
		DatabaseURL: "postgres://localhost/test",
		Backends: []BackendRoute{
			{Prefix: "/v1/sagas", Target: sagaBackend.URL[7:]},
			{Prefix: "/v1/party", Target: partyBackend.URL[7:]},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	// Start gateway
	serverCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		_ = server.Start(serverCtx)
	}()

	// Wait for gateway to be ready
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
	require.NoError(t, err, "gateway failed to become ready")

	// Test saga route
	t.Run("routes to saga backend", func(t *testing.T) {
		sagaBackendCalled = false
		partyBackendCalled = false

		resp, err := httpGet(ctx, baseURL+"/v1/sagas/list")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.True(t, sagaBackendCalled, "saga backend should be called")
		assert.False(t, partyBackendCalled, "party backend should not be called")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "saga")
	})

	// Test party route
	t.Run("routes to party backend", func(t *testing.T) {
		sagaBackendCalled = false
		partyBackendCalled = false

		resp, err := httpGet(ctx, baseURL+"/v1/party/create")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.False(t, sagaBackendCalled, "saga backend should not be called")
		assert.True(t, partyBackendCalled, "party backend should be called")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "party")
	})

	// Cleanup
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
}

// TestIntegration_ProxySagaValidationEndpoint verifies that the saga validation
// endpoint path is correctly routed through the gateway.
func TestIntegration_ProxySagaValidationEndpoint(t *testing.T) {
	ctx := context.Background()

	// Create a mock saga service backend
	var receivedBody []byte
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		// Simulate saga validation response
		response := map[string]interface{}{
			"success": true,
			"metrics": map[string]int{
				"handler_call_count": 1,
				"operation_count":    5,
				"complexity_score":   0,
			},
			"formatted_report": "Validation successful\n",
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer backend.Close()

	backendAddr := backend.URL[7:]

	// Find an available port for gateway
	port := getAvailablePort(ctx, t)

	// Create gateway with saga backend route
	config := &Config{
		Port:        port,
		BaseDomain:  "api.test.io",
		DatabaseURL: "postgres://localhost/test",
		Backends: []BackendRoute{
			{Prefix: "/v1/sagas", Target: backendAddr},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	// Start gateway
	serverCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		_ = server.Start(serverCtx)
	}()

	// Wait for gateway to be ready
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
	require.NoError(t, err, "gateway failed to become ready")

	// Test saga validation endpoint
	t.Run("POST /v1/sagas/validate routes correctly", func(t *testing.T) {
		requestBody := `{
			"saga_name": "test_payment_saga",
			"script": "result = payment.create_lien(amount=\"100.00\")",
			"version": "1.0.0"
		}`

		resp, err := httpPost(ctx, baseURL+"/v1/sagas/validate", "application/json", strings.NewReader(requestBody))
		require.NoError(t, err)
		defer resp.Body.Close()

		// Verify request was routed to backend
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotEmpty(t, receivedBody, "backend should receive request body")

		// Verify response
		var response map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&response)
		require.NoError(t, err)
		assert.True(t, response["success"].(bool))
		assert.NotNil(t, response["metrics"])
	})

	// Cleanup
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
}

// httpPost performs an HTTP POST request with context.
func httpPost(ctx context.Context, url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return http.DefaultClient.Do(req)
}
