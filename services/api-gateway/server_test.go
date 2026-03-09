package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/api-gateway/auth"
	gwhealth "github.com/meridianhub/meridian/services/api-gateway/health"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test error sentinels
var (
	errConnectionTimeout = errors.New("connection timeout")
	errConnectionRefused = errors.New("connection refused")
	errServiceDown       = errors.New("down")
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
	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	// Should return 503 Service Unavailable (no backend configured)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "no API backend configured")
}

// TestWithTranscoder_UsedOverProxy verifies that when WithTranscoder is set,
// API requests are dispatched through the transcoder rather than the legacy proxy.
func TestWithTranscoder_UsedOverProxy(t *testing.T) {
	transcoderCalled := false
	fakeTranscoder := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		transcoderCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("transcoder"))
	})

	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
		// Also set Backends to confirm transcoder takes precedence.
		Backends: []BackendRoute{
			{Prefix: "/v1/party", Target: "party:50051"},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil, WithTranscoder(fakeTranscoder))

	req := httptest.NewRequest(http.MethodPost, "/meridian.party.v1.PartyService/CreateParty", nil)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	assert.True(t, transcoderCalled, "transcoder should be called when configured")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "transcoder", rec.Body.String())
}

// TestWithTranscoder_FallsBackToProxy verifies that when no transcoder is set,
// API requests fall through to the legacy proxy.
func TestWithTranscoder_FallsBackToProxy(t *testing.T) {
	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
		// No Backends configured → placeholder handler
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// No WithTranscoder option
	server := NewServer(config, logger, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/party", nil)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	// Should return 503 Service Unavailable from the fallback handler
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestWithTranscoder_StripsAndInjectsIdentityHeaders verifies that the
// identityHeaderMiddleware applied to the transcoder path strips client-supplied
// spoofed headers and injects authenticated identity headers from context.
func TestWithTranscoder_StripsAndInjectsIdentityHeaders(t *testing.T) {
	var capturedHeaders http.Header

	// Fake transcoder that captures the headers it receives.
	capture := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	})

	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil, WithTranscoder(capture))

	req := httptest.NewRequest(http.MethodPost, "/meridian.party.v1.PartyService/CreateParty", nil)
	// Simulate spoofed headers from a malicious client.
	req.Header.Set(HeaderUserID, "spoofed-user")
	req.Header.Set(HeaderTenantID, "spoofed-tenant")
	req.Header.Set(HeaderAuthMethod, "jwt")
	// Inject real authenticated identity via context (set by auth middleware).
	ctx := context.WithValue(req.Context(), auth.UserIDContextKey, "real-user-789")
	ctx = context.WithValue(ctx, auth.TenantIDContextKey, "real-tenant-abc")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	// Spoofed values must be replaced with the real authenticated identity.
	assert.Equal(t, "real-user-789", capturedHeaders.Get(HeaderUserID))
	assert.Equal(t, "real-tenant-abc", capturedHeaders.Get(HeaderTenantID))
	assert.Equal(t, AuthMethodJWT, capturedHeaders.Get(HeaderAuthMethod))
}

// TestWithTranscoder_VanguardFromDescriptor verifies that a real Vanguard
// transcoder built from the embedded descriptor set can be injected and
// routes known gRPC paths without panicking.
func TestWithTranscoder_VanguardFromDescriptor(t *testing.T) {
	transcoder, err := NewTranscoder(testDescriptorBytes, partyBackend)
	require.NoError(t, err)

	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil, WithTranscoder(transcoder))

	// Send an unknown path — Vanguard returns 404, not a panic.
	req := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
	rec := httptest.NewRecorder()

	assert.NotPanics(t, func() {
		server.mux.ServeHTTP(rec, req)
	})
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

// mockChecker implements health.Checker for testing.
type mockChecker struct {
	name   string
	status health.Status
	err    error
}

func (m *mockChecker) Name() string {
	return m.name
}

func (m *mockChecker) Check(_ context.Context) health.ComponentResult {
	message := m.name + " check successful"
	if m.err != nil {
		message = m.name + " check failed: " + m.err.Error()
	}
	return health.ComponentResult{
		Name:         m.name,
		Status:       m.status,
		Message:      message,
		ResponseTime: 10 * time.Millisecond,
		CheckedAt:    time.Now(),
		Error:        m.err,
	}
}

// TestHandleReady_NoHealthChecker verifies backwards compatibility when health checker is nil.
func TestHandleReady_NoHealthChecker(t *testing.T) {
	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "READY", rec.Body.String())
}

// TestHandleReady_AllHealthy verifies 200 OK when all checks pass.
func TestHandleReady_AllHealthy(t *testing.T) {
	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	checkers := []health.Checker{
		&mockChecker{name: "database", status: health.StatusHealthy},
		&mockChecker{name: "redis", status: health.StatusHealthy},
	}
	healthChecker := gwhealth.NewGatewayHealthChecker(gwhealth.Config{
		Checkers:     checkers,
		CheckTimeout: 5 * time.Second,
	})

	server := NewServer(config, logger, nil, WithHealthChecker(healthChecker))

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "READY", rec.Body.String())
}

// TestHandleReady_Degraded verifies 200 OK when system is degraded (optional deps down).
func TestHandleReady_Degraded(t *testing.T) {
	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}

	logHandler := &logCapture{}
	logger := slog.New(logHandler)

	checkers := []health.Checker{
		&mockChecker{name: "database", status: health.StatusHealthy},
		&mockChecker{name: "redis", status: health.StatusDegraded, err: errConnectionTimeout},
	}
	healthChecker := gwhealth.NewGatewayHealthChecker(gwhealth.Config{
		Checkers:     checkers,
		CheckTimeout: 5 * time.Second,
	})

	server := NewServer(config, logger, nil, WithHealthChecker(healthChecker))

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "degraded system should still return 200 OK")
	assert.Equal(t, "READY", rec.Body.String())

	// Verify warning was logged
	require.GreaterOrEqual(t, len(logHandler.entries), 1)
	found := false
	for _, entry := range logHandler.entries {
		if entry["level"] == "WARN" && entry["msg"] == "gateway readiness check" {
			found = true
			assert.Equal(t, "degraded", entry["overall_status"])
			break
		}
	}
	assert.True(t, found, "expected warning log for degraded status")
}

// TestHandleReady_Unhealthy verifies 503 when critical deps are down.
func TestHandleReady_Unhealthy(t *testing.T) {
	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}

	logHandler := &logCapture{}
	logger := slog.New(logHandler)

	checkers := []health.Checker{
		&mockChecker{name: "database", status: health.StatusUnhealthy, err: errConnectionRefused},
		&mockChecker{name: "redis", status: health.StatusHealthy},
	}
	healthChecker := gwhealth.NewGatewayHealthChecker(gwhealth.Config{
		Checkers:     checkers,
		CheckTimeout: 5 * time.Second,
	})

	server := NewServer(config, logger, nil, WithHealthChecker(healthChecker))

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "unhealthy system should return 503")
	assert.Equal(t, "NOT READY", rec.Body.String())

	// Verify error was logged
	require.GreaterOrEqual(t, len(logHandler.entries), 1)
	foundReadinessLog := false
	foundComponentLog := false
	for _, entry := range logHandler.entries {
		if entry["level"] == "ERROR" && entry["msg"] == "gateway readiness check" {
			foundReadinessLog = true
			assert.Equal(t, "unhealthy", entry["overall_status"])
		}
		if entry["level"] == "ERROR" && entry["msg"] == "component unhealthy" {
			foundComponentLog = true
			assert.Equal(t, "database", entry["component"])
		}
	}
	assert.True(t, foundReadinessLog, "expected error log for unhealthy status")
	assert.True(t, foundComponentLog, "expected error log for unhealthy component")
}

// TestHandleReady_WithHealthChecker_LegacyEndpoints verifies legacy endpoints also use health checker.
func TestHandleReady_WithHealthChecker_LegacyEndpoints(t *testing.T) {
	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	checkers := []health.Checker{
		&mockChecker{name: "database", status: health.StatusUnhealthy, err: errServiceDown},
	}
	healthChecker := gwhealth.NewGatewayHealthChecker(gwhealth.Config{
		Checkers:     checkers,
		CheckTimeout: 5 * time.Second,
	})

	server := NewServer(config, logger, nil, WithHealthChecker(healthChecker))

	// Test /readyz legacy endpoint
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "/readyz should also use health checker")
	assert.Equal(t, "NOT READY", rec.Body.String())
}

// TestWithEventStreamHandler_RouteRegistered verifies that /ws/events is registered
// when an event stream handler is provided.
func TestWithEventStreamHandler_RouteRegistered(t *testing.T) {
	handlerCalled := false
	fakeHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusSwitchingProtocols)
	})

	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Build a real eventstream handler wrapping fakeHandler via an adapter.
	// We bypass the full eventstream.Handler and instead verify routing works
	// by directly constructing the server option.
	server := NewServer(config, logger, nil, WithEventStreamHandlerHTTP(fakeHandler))

	req := httptest.NewRequest(http.MethodGet, "/ws/events", nil)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	assert.True(t, handlerCalled, "event stream handler should be called for GET /ws/events")
}

// TestWithEventStreamHandler_NotRegisteredWhenNil verifies that /ws/events is NOT
// registered when no event stream handler is provided.
func TestWithEventStreamHandler_NotRegisteredWhenNil(t *testing.T) {
	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil)

	req := httptest.NewRequest(http.MethodGet, "/ws/events", nil)
	rec := httptest.NewRecorder()

	server.mux.ServeHTTP(rec, req)

	// Without an event stream handler, the route is not registered.
	// The request falls through to the "/" catch-all API handler which
	// returns 503 when no transcoder or proxy is configured.
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestWithEventStreamHandler_HealthEndpointsUnaffected verifies that adding the
// event stream handler does not break health check endpoints.
func TestWithEventStreamHandler_HealthEndpointsUnaffected(t *testing.T) {
	fakeHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	})

	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(config, logger, nil, WithEventStreamHandlerHTTP(fakeHandler))

	for _, endpoint := range []string{"/health", "/ready"} {
		req := httptest.NewRequest(http.MethodGet, endpoint, nil)
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "health endpoint %s must still work", endpoint)
	}
}

// TestWithHealthChecker verifies the functional option works correctly.
func TestWithHealthChecker(t *testing.T) {
	config := &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	healthChecker := gwhealth.NewGatewayHealthChecker(gwhealth.Config{
		Checkers:     []health.Checker{},
		CheckTimeout: 5 * time.Second,
	})

	server := NewServer(config, logger, nil, WithHealthChecker(healthChecker))

	assert.Equal(t, healthChecker, server.healthChecker)
}
