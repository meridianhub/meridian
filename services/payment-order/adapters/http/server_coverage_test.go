package http

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestIDMiddleware(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("generates new request ID when not provided", func(t *testing.T) {
		var capturedID string
		handler := requestIDMiddleware(logger)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedID = GetRequestID(r.Context())
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.NotEmpty(t, capturedID)
		assert.Equal(t, capturedID, rr.Header().Get("X-Request-ID"))
	})

	t.Run("preserves existing request ID", func(t *testing.T) {
		var capturedID string
		handler := requestIDMiddleware(logger)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			capturedID = GetRequestID(r.Context())
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Request-ID", "existing-id-123")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.Equal(t, "existing-id-123", capturedID)
		assert.Equal(t, "existing-id-123", rr.Header().Get("X-Request-ID"))
	})
}

func TestLoggingMiddleware(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler := loggingMiddleware(logger, false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusCreated, rr.Code)
}

func TestResponseWriter_WriteHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rr, statusCode: http.StatusOK}

	rw.WriteHeader(http.StatusNotFound)
	assert.Equal(t, http.StatusNotFound, rw.statusCode)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestRateLimitMiddleware(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	limiter := newIPRateLimiter(1, 1, 100) // 1 req/sec, burst 1

	handler := rateLimitMiddleware(limiter, logger, false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request should pass
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	// Second request should be rate limited
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req)
	assert.Equal(t, http.StatusTooManyRequests, rr2.Code)
}

func TestNewServer_NilLogger(t *testing.T) {
	webhookHandler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("secret"),
	})
	require.NoError(t, err)

	server, err := NewServer(ServerConfig{
		Port:           8080,
		WebhookHandler: webhookHandler,
		Logger:         nil, // Should use default
	})
	require.NoError(t, err)
	assert.NotNil(t, server)
}

func TestServer_Addr(t *testing.T) {
	webhookHandler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("secret"),
	})
	require.NoError(t, err)

	server, err := NewServer(ServerConfig{
		Port:           9090,
		WebhookHandler: webhookHandler,
	})
	require.NoError(t, err)

	assert.Equal(t, ":9090", server.Addr())
}

func TestNewServer_DefaultValues(t *testing.T) {
	webhookHandler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("secret"),
	})
	require.NoError(t, err)

	// Zero values for rate limit should get defaults
	server, err := NewServer(ServerConfig{
		Port:                8080,
		WebhookHandler:      webhookHandler,
		RateLimitPerSecond:  0,
		RateLimitBurst:      0,
		RateLimitMaxEntries: 0,
	})
	require.NoError(t, err)
	assert.NotNil(t, server)
}

func TestIPRateLimiter_GetLimiter_ReturnsExisting(t *testing.T) {
	limiter := newIPRateLimiter(100, 100, 10)

	// Get limiter for an IP
	l1 := limiter.getLimiter("192.168.1.1")
	// Get again should return the same limiter
	l2 := limiter.getLimiter("192.168.1.1")

	assert.Equal(t, l1, l2)
}

func TestGetClientIP_XForwardedFor_TrustEnabled(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.100, 10.0.0.1")
	req.RemoteAddr = "172.16.0.1:12345"

	ip := getClientIP(req, true)
	assert.Equal(t, "192.168.1.100", ip)
}

func TestGetClientIP_XRealIP_FallbackWhenNoXFF(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-IP", "10.10.10.10")
	req.RemoteAddr = "172.16.0.1:12345"

	ip := getClientIP(req, true)
	assert.Equal(t, "10.10.10.10", ip)
}

func TestServer_Start_And_Shutdown(t *testing.T) {
	webhookHandler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("secret"),
	})
	require.NoError(t, err)

	server, err := NewServer(ServerConfig{
		Port:           18392, // Use an unlikely-to-conflict port
		WebhookHandler: webhookHandler,
	})
	require.NoError(t, err)

	// Start server in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start()
	}()

	// Wait for server to start accepting connections
	awaitErr := await.New().
		AtMost(2 * time.Second).
		PollInterval(10 * time.Millisecond).
		UntilNoError(func() error {
			dialer := &net.Dialer{Timeout: 50 * time.Millisecond}
			conn, dialErr := dialer.DialContext(context.Background(), "tcp", server.Addr())
			if dialErr != nil {
				return dialErr
			}
			conn.Close()
			return nil
		})
	require.NoError(t, awaitErr, "server did not start in time")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = server.Shutdown(ctx)
	assert.NoError(t, err)

	// Start should return nil (ErrServerClosed is swallowed)
	err = <-errCh
	assert.NoError(t, err)
}

func TestRecoveryMiddleware_NoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler := recoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}
