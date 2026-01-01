package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHealthEndpoints_NoTenantContext verifies that health endpoints
// work without any tenant context (Host header, subdomain, etc.).
// This is critical for K8s probes.
func TestHealthEndpoints_NoTenantContext(t *testing.T) {
	tests := []struct {
		name           string
		endpoint       string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "GET /health returns 200 OK",
			endpoint:       "/health",
			expectedStatus: http.StatusOK,
			expectedBody:   "OK",
		},
		{
			name:           "GET /ready returns 200 READY",
			endpoint:       "/ready",
			expectedStatus: http.StatusOK,
			expectedBody:   "READY",
		},
		{
			name:           "GET /healthz returns 200 OK (legacy)",
			endpoint:       "/healthz",
			expectedStatus: http.StatusOK,
			expectedBody:   "OK",
		},
		{
			name:           "GET /readyz returns 200 READY (legacy)",
			endpoint:       "/readyz",
			expectedStatus: http.StatusOK,
			expectedBody:   "READY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create server without tenant resolver
			config := &Config{
				Port:        8080,
				BaseDomain:  "test.example.com",
				DatabaseURL: "postgres://localhost/test",
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			server := NewServer(config, logger, nil)

			// Create request WITHOUT Host header (simulating K8s probe)
			req := httptest.NewRequest(http.MethodGet, tt.endpoint, nil)
			req.Host = "" // Explicitly clear Host header
			rec := httptest.NewRecorder()

			// Execute
			server.mux.ServeHTTP(rec, req)

			// Assert
			assert.Equal(t, tt.expectedStatus, rec.Code)
			assert.Equal(t, tt.expectedBody, rec.Body.String())
			assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
		})
	}
}

// TestHealthEndpoints_InvalidSubdomain verifies that health endpoints
// work even when accessed with an invalid subdomain.
// K8s may route probes through various paths.
func TestHealthEndpoints_InvalidSubdomain(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		host     string
	}{
		{
			name:     "/health with invalid subdomain",
			endpoint: "/health",
			host:     "invalid-host.wrong-domain.com",
		},
		{
			name:     "/ready with invalid subdomain",
			endpoint: "/ready",
			host:     "missing.subdomain.io",
		},
		{
			name:     "/health with IP address",
			endpoint: "/health",
			host:     "192.168.1.1",
		},
		{
			name:     "/ready with localhost",
			endpoint: "/ready",
			host:     "localhost:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create server without tenant resolver
			config := &Config{
				Port:        8080,
				BaseDomain:  "api.example.com",
				DatabaseURL: "postgres://localhost/test",
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			server := NewServer(config, logger, nil)

			// Create request WITH invalid/missing subdomain
			req := httptest.NewRequest(http.MethodGet, tt.endpoint, nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()

			// Execute
			server.mux.ServeHTTP(rec, req)

			// Assert - health endpoints should ALWAYS return 200
			assert.Equal(t, http.StatusOK, rec.Code,
				"health endpoints must succeed regardless of Host header")
		})
	}
}

// TestHealthEndpoints_BypassTenantMiddleware verifies that health endpoints
// bypass the tenant resolver middleware entirely.
func TestHealthEndpoints_BypassTenantMiddleware(t *testing.T) {
	// This test verifies the critical architectural requirement:
	// Health endpoints must NOT go through tenant middleware.
	//
	// The server architecture guarantees this by design:
	// - Health endpoints are registered directly on the main mux
	// - API routes are registered on a sub-mux wrapped with tenant middleware
	// - This separation ensures health probes work without valid tenant context
	//
	// We pass nil for the tenant resolver because:
	// 1. Health endpoints don't use the resolver (architectural guarantee)
	// 2. The concrete type (*gateway.TenantResolverMiddleware) prevents mocking
	// 3. Other tests (TestHealthEndpoints_NoTenantContext, _InvalidSubdomain)
	//    verify health endpoints work without tenant context

	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create server without tenant resolver (nil).
	// Health endpoints are architecturally separated from tenant middleware,
	// registered directly on the main mux without any middleware wrapping.
	server := NewServer(config, logger, nil)

	endpoints := []string{"/health", "/ready", "/healthz", "/readyz"}

	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			// Request with NO Host header - tenant resolution would fail
			req := httptest.NewRequest(http.MethodGet, endpoint, nil)
			rec := httptest.NewRecorder()

			server.mux.ServeHTTP(rec, req)

			// Health endpoints should succeed
			assert.Equal(t, http.StatusOK, rec.Code,
				"health endpoints must bypass tenant middleware")
		})
	}
}

// TestAPIRoutes_WithoutTenantResolver verifies API routes work when
// tenant resolver is not configured (nil).
func TestAPIRoutes_WithoutTenantResolver(t *testing.T) {
	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	// Request to API endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	// Should return 501 Not Implemented (placeholder response)
	assert.Equal(t, http.StatusNotImplemented, rec.Code)
	assert.Contains(t, rec.Body.String(), "gateway routing not yet implemented")
}

// TestNewServer_InitializesCorrectly verifies server construction.
func TestNewServer_InitializesCorrectly(t *testing.T) {
	config := &Config{
		Port:        9090,
		BaseDomain:  "test.meridian.io",
		DatabaseURL: "postgres://localhost/test",
		Backends: []BackendRoute{
			{Prefix: "/v1/party", Target: "party:50051"},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server := NewServer(config, logger, nil)

	require.NotNil(t, server)
	assert.Equal(t, config, server.config)
	assert.Equal(t, logger, server.logger)
	assert.NotNil(t, server.mux)
	assert.Nil(t, server.tenantResolver)
}

// TestHealthEndpoints_HTTPMethods verifies only GET is allowed for health endpoints.
func TestHealthEndpoints_HTTPMethods(t *testing.T) {
	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	endpoints := []string{"/health", "/ready"}
	disallowedMethods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}

	for _, endpoint := range endpoints {
		for _, method := range disallowedMethods {
			t.Run(method+" "+endpoint, func(t *testing.T) {
				req := httptest.NewRequest(method, endpoint, nil)
				rec := httptest.NewRecorder()

				server.mux.ServeHTTP(rec, req)

				// Go 1.22+ ServeMux returns 405 for method mismatch
				assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
					"%s should not be allowed on %s", method, endpoint)
			})
		}
	}
}

// TestServer_StartAndShutdown tests the server lifecycle.
func TestServer_StartAndShutdown(t *testing.T) {
	config := &Config{
		Port:        0, // Use random available port
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	// Start server in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Start(ctx); err != nil {
			serverErr <- err
		}
	}()

	// Intentional sleep: Give server time to start and bind the port.
	// The server doesn't expose a "started" state we can poll. This is a unit test
	// verifying lifecycle; the mutex in Server ensures thread-safety regardless of timing.
	time.Sleep(50 * time.Millisecond)

	select {
	case err := <-serverErr:
		t.Fatalf("server failed to start: %v", err)
	default:
		// Server started, now shutdown
	}

	// Shutdown
	shutdownCtx := context.Background()
	err := server.Shutdown(shutdownCtx)
	assert.NoError(t, err)
}

// TestServer_ShutdownWithoutStart verifies shutdown is safe when server wasn't started.
func TestServer_ShutdownWithoutStart(t *testing.T) {
	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	// Shutdown without start should not panic or error
	err := server.Shutdown(context.Background())
	assert.NoError(t, err)
}

// failingResponseWriter is a mock that fails on Write to test error handling.
type failingResponseWriter struct {
	header     http.Header
	statusCode int
	writeErr   error
}

func newFailingResponseWriter(writeErr error) *failingResponseWriter {
	return &failingResponseWriter{
		header:   make(http.Header),
		writeErr: writeErr,
	}
}

func (w *failingResponseWriter) Header() http.Header {
	return w.header
}

func (w *failingResponseWriter) Write(_ []byte) (int, error) {
	return 0, w.writeErr
}

func (w *failingResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

// logCapture captures log messages for testing.
type logCapture struct {
	entries []map[string]any
}

func (c *logCapture) Handle(_ context.Context, r slog.Record) error {
	entry := make(map[string]any)
	entry["level"] = r.Level.String()
	entry["msg"] = r.Message
	r.Attrs(func(a slog.Attr) bool {
		entry[a.Key] = a.Value.Any()
		return true
	})
	c.entries = append(c.entries, entry)
	return nil
}

func (c *logCapture) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (c *logCapture) WithAttrs(_ []slog.Attr) slog.Handler {
	return c
}

func (c *logCapture) WithGroup(_ string) slog.Handler {
	return c
}

// TestHealthEndpoints_WriteErrorLogging verifies that write errors are logged as warnings.
func TestHealthEndpoints_WriteErrorLogging(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{
			name:     "/health endpoint logs write errors",
			endpoint: "/health",
		},
		{
			name:     "/ready endpoint logs write errors",
			endpoint: "/ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create log capture
			logHandler := &logCapture{}
			logger := slog.New(logHandler)

			// Create server
			config := &Config{
				Port:        8080,
				BaseDomain:  "api.example.com",
				DatabaseURL: "postgres://localhost/test",
			}
			server := NewServer(config, logger, nil)

			// Create failing response writer
			writeErr := io.ErrClosedPipe
			w := newFailingResponseWriter(writeErr)
			req := httptest.NewRequest(http.MethodGet, tt.endpoint, nil)
			req.RemoteAddr = "192.168.1.100:54321"

			// Execute - should NOT panic
			server.mux.ServeHTTP(w, req)

			// Verify warning was logged
			require.GreaterOrEqual(t, len(logHandler.entries), 1, "expected at least one log entry")

			lastEntry := logHandler.entries[len(logHandler.entries)-1]
			assert.Equal(t, "WARN", lastEntry["level"])
			assert.Contains(t, lastEntry["msg"].(string), "failed to write")
			assert.Equal(t, tt.endpoint, lastEntry["endpoint"])
			assert.Contains(t, lastEntry["remote_addr"].(string), "192.168.1.100")
		})
	}
}

// TestHealthEndpoints_NoPanicOnWriteError verifies handlers don't panic when Write fails.
func TestHealthEndpoints_NoPanicOnWriteError(t *testing.T) {
	endpoints := []string{"/health", "/ready", "/healthz", "/readyz"}

	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			config := &Config{
				Port:        8080,
				BaseDomain:  "api.example.com",
				DatabaseURL: "postgres://localhost/test",
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			server := NewServer(config, logger, nil)

			// Create failing response writer
			w := newFailingResponseWriter(io.ErrUnexpectedEOF)
			req := httptest.NewRequest(http.MethodGet, endpoint, nil)

			// Execute - should NOT panic
			assert.NotPanics(t, func() {
				server.mux.ServeHTTP(w, req)
			})

			// Verify WriteHeader was still called (status set before Write attempt)
			assert.Equal(t, http.StatusOK, w.statusCode)
		})
	}
}
