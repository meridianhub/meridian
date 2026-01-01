package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/shared/platform/await"
)

func TestNewServer(t *testing.T) {
	webhookHandler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("secret"),
	})
	require.NoError(t, err)

	tests := []struct {
		name    string
		cfg     ServerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: ServerConfig{
				Port:           8080,
				WebhookHandler: webhookHandler,
			},
			wantErr: false,
		},
		{
			name: "nil webhook handler",
			cfg: ServerConfig{
				Port:           8080,
				WebhookHandler: nil,
			},
			wantErr: true,
		},
		{
			name: "invalid port zero",
			cfg: ServerConfig{
				Port:           0,
				WebhookHandler: webhookHandler,
			},
			wantErr: true,
		},
		{
			name: "invalid port negative",
			cfg: ServerConfig{
				Port:           -1,
				WebhookHandler: webhookHandler,
			},
			wantErr: true,
		},
		{
			name: "invalid port too high",
			cfg: ServerConfig{
				Port:           65536,
				WebhookHandler: webhookHandler,
			},
			wantErr: true,
		},
		{
			name: "valid config with defaults",
			cfg: ServerConfig{
				Port:           8080,
				WebhookHandler: webhookHandler,
				// All other fields will use defaults
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := NewServer(tt.cfg)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, server)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, server)
			}
		})
	}
}

func TestDefaultServerConfig(t *testing.T) {
	cfg := DefaultServerConfig()

	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, float64(100), cfg.RateLimitPerSecond)
	assert.Equal(t, 200, cfg.RateLimitBurst)
	assert.Equal(t, 10000, cfg.RateLimitMaxEntries)
	assert.False(t, cfg.TrustProxyHeaders)
	assert.Equal(t, 10*time.Second, cfg.ReadTimeout)
	assert.Equal(t, 30*time.Second, cfg.WriteTimeout)
	assert.Equal(t, 60*time.Second, cfg.IdleTimeout)
}

func TestServer_HealthEndpoint(t *testing.T) {
	webhookHandler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{},
		HMACSecret:          []byte("secret"),
	})
	require.NoError(t, err)

	server, err := NewServer(ServerConfig{
		Port:           8080,
		WebhookHandler: webhookHandler,
	})
	require.NoError(t, err)

	// Create a test listener
	ctx := context.Background()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	// Start server in background
	serverDone := make(chan struct{})
	go func() {
		_ = server.StartWithListener(listener)
		close(serverDone)
	}()

	// Wait for server to be ready
	client := &http.Client{Timeout: 5 * time.Second}
	err = await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).UntilNoError(func() error {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/health", nil)
		if reqErr != nil {
			return reqErr
		}
		resp, respErr := client.Do(req)
		if respErr != nil {
			return respErr
		}
		_ = resp.Body.Close()
		return nil
	})
	require.NoError(t, err, "server should become ready")

	// Make request to health endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/health", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var healthResp map[string]string
	err = json.NewDecoder(resp.Body).Decode(&healthResp)
	require.NoError(t, err)
	assert.Equal(t, "healthy", healthResp["status"])

	// Shutdown server
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = server.Shutdown(shutdownCtx)
	require.NoError(t, err)

	<-serverDone
}

func TestServer_WebhookEndpoint(t *testing.T) {
	secret := []byte("test-secret")

	webhookHandler, err := NewWebhookHandler(WebhookHandlerConfig{
		PaymentOrderService: &mockPaymentOrderService{
			updateFunc: func(_ context.Context, _ *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
				return &pb.UpdatePaymentOrderResponse{
					PaymentOrder: &pb.PaymentOrder{
						PaymentOrderId: "test-order",
						Status:         pb.PaymentOrderStatus_PAYMENT_ORDER_STATUS_COMPLETED,
					},
				}, nil
			},
		},
		HMACSecret: secret,
	})
	require.NoError(t, err)

	server, err := NewServer(ServerConfig{
		Port:           8080,
		WebhookHandler: webhookHandler,
	})
	require.NoError(t, err)

	// Create a test listener
	ctx := context.Background()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	// Start server in background
	serverDone := make(chan struct{})
	go func() {
		_ = server.StartWithListener(listener)
		close(serverDone)
	}()

	// Wait for server to be ready
	client := &http.Client{Timeout: 5 * time.Second}
	err = await.New().AtMost(2 * time.Second).PollInterval(10 * time.Millisecond).UntilNoError(func() error {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/health", nil)
		if reqErr != nil {
			return reqErr
		}
		resp, respErr := client.Do(req)
		if respErr != nil {
			return respErr
		}
		_ = resp.Body.Close()
		return nil
	})
	require.NoError(t, err, "server should become ready")

	// Create webhook request
	webhookReq := WebhookRequest{
		GatewayReferenceID: "gw-123",
		Status:             "Settled",
		Timestamp:          time.Now().UTC(),
	}
	body, err := json.Marshal(webhookReq)
	require.NoError(t, err)

	signature := GenerateWebhookSignature(body, secret)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+listener.Addr().String()+"/webhook/payment-gateway",
		&fixedReader{data: body})
	require.NoError(t, err)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(WebhookSignatureHeader, signature)

	resp, err := client.Do(httpReq)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Shutdown server
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = server.Shutdown(shutdownCtx)
	require.NoError(t, err)

	<-serverDone
}

// fixedReader provides a simple io.Reader for testing
type fixedReader struct {
	data []byte
	pos  int
}

func (r *fixedReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name              string
		xForwardedFor     string
		xRealIP           string
		remoteAddr        string
		trustProxyHeaders bool
		expectedIP        string
	}{
		{
			name:              "X-Forwarded-For single IP with trust",
			xForwardedFor:     "192.168.1.1",
			remoteAddr:        "10.0.0.1:12345",
			trustProxyHeaders: true,
			expectedIP:        "192.168.1.1",
		},
		{
			name:              "X-Forwarded-For multiple IPs with trust",
			xForwardedFor:     "192.168.1.1, 10.0.0.2, 172.16.0.1",
			remoteAddr:        "10.0.0.1:12345",
			trustProxyHeaders: true,
			expectedIP:        "192.168.1.1",
		},
		{
			name:              "X-Real-IP with trust",
			xRealIP:           "192.168.2.2",
			remoteAddr:        "10.0.0.1:12345",
			trustProxyHeaders: true,
			expectedIP:        "192.168.2.2",
		},
		{
			name:              "Remote address with port",
			remoteAddr:        "192.168.3.3:54321",
			trustProxyHeaders: false,
			expectedIP:        "192.168.3.3",
		},
		{
			name:              "Remote address without port",
			remoteAddr:        "192.168.4.4",
			trustProxyHeaders: false,
			expectedIP:        "192.168.4.4",
		},
		{
			name:              "X-Forwarded-For ignored without trust",
			xForwardedFor:     "192.168.1.1",
			remoteAddr:        "10.0.0.1:12345",
			trustProxyHeaders: false,
			expectedIP:        "10.0.0.1",
		},
		{
			name:              "X-Real-IP ignored without trust",
			xRealIP:           "192.168.2.2",
			remoteAddr:        "10.0.0.1:12345",
			trustProxyHeaders: false,
			expectedIP:        "10.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tt.xForwardedFor)
			}
			if tt.xRealIP != "" {
				req.Header.Set("X-Real-IP", tt.xRealIP)
			}
			req.RemoteAddr = tt.remoteAddr

			ip := getClientIP(req, tt.trustProxyHeaders)
			assert.Equal(t, tt.expectedIP, ip)
		})
	}
}

func TestGetRequestID(t *testing.T) {
	// Test with request ID in context
	ctx := context.WithValue(context.Background(), requestIDKey, "test-request-id")
	id := GetRequestID(ctx)
	assert.Equal(t, "test-request-id", id)

	// Test without request ID in context
	id = GetRequestID(context.Background())
	assert.Empty(t, id)
}

func TestIPRateLimiter(t *testing.T) {
	// Create a rate limiter that allows 2 requests per second with burst of 2 and max 100 entries
	limiter := newIPRateLimiter(2, 2, 100)

	ip := "192.168.1.1"

	// First two requests should be allowed (burst)
	assert.True(t, limiter.allow(ip))
	assert.True(t, limiter.allow(ip))

	// Third request should be denied (exceeded burst)
	assert.False(t, limiter.allow(ip))

	// Different IP should have its own limiter
	ip2 := "192.168.1.2"
	assert.True(t, limiter.allow(ip2))
	assert.True(t, limiter.allow(ip2))
}

func TestIPRateLimiterEviction(t *testing.T) {
	// Create a rate limiter with max 2 entries
	limiter := newIPRateLimiter(100, 100, 2)

	// Add two IPs
	limiter.allow("192.168.1.1")
	limiter.allow("192.168.1.2")

	// Verify both exist
	assert.Equal(t, 2, len(limiter.limiters))

	// Add a third IP - should evict the oldest
	limiter.allow("192.168.1.3")

	// Should still have only 2 entries
	assert.Equal(t, 2, len(limiter.limiters))

	// The newest two should exist
	_, exists2 := limiter.limiters["192.168.1.2"]
	_, exists3 := limiter.limiters["192.168.1.3"]
	assert.True(t, exists2)
	assert.True(t, exists3)
}

func TestMiddlewareChain(t *testing.T) {
	var order []string

	middleware1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m1-before")
			next.ServeHTTP(w, r)
			order = append(order, "m1-after")
		})
	}

	middleware2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m2-before")
			next.ServeHTTP(w, r)
			order = append(order, "m2-after")
		})
	}

	handler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		order = append(order, "handler")
	})

	chained := chainMiddleware(handler, middleware1, middleware2)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	chained.ServeHTTP(rr, req)

	// Middleware should execute in order: m1 wraps m2 wraps handler
	expected := []string{"m1-before", "m2-before", "handler", "m2-after", "m1-after"}
	assert.Equal(t, expected, order)
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

// TestHealthHandler_NoPanicOnWriteError verifies the handler doesn't panic when Write fails.
func TestHealthHandler_NoPanicOnWriteError(t *testing.T) {
	// Create failing response writer
	w := newFailingResponseWriter(io.ErrUnexpectedEOF)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "192.168.1.100:54321"

	// Execute - should NOT panic
	assert.NotPanics(t, func() {
		healthHandler(w, req)
	})

	// Verify WriteHeader was still called (status set before Write attempt)
	assert.Equal(t, http.StatusOK, w.statusCode)
	assert.Equal(t, "application/json", w.header.Get("Content-Type"))
}

func TestRecoveryMiddleware(t *testing.T) {
	panicHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	recovered := recoveryMiddleware(logger)(panicHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	// Should not panic
	recovered.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}
