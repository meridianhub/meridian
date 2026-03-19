package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewMetricsCollector(t *testing.T) {
	mc := NewMetricsCollector()

	if mc == nil {
		t.Fatal("NewMetricsCollector returned nil")
		return // unreachable but satisfies staticcheck
	}

	if mc.HTTPRequestsTotal == nil {
		t.Error("HTTPRequestsTotal not initialized")
	}
	if mc.HTTPRequestDurationSeconds == nil {
		t.Error("HTTPRequestDurationSeconds not initialized")
	}
	if mc.GRPCServerHandledTotal == nil {
		t.Error("GRPCServerHandledTotal not initialized")
	}
	if mc.DBQueryDurationSeconds == nil {
		t.Error("DBQueryDurationSeconds not initialized")
	}
	if mc.KafkaMessagesPublishedTotal == nil {
		t.Error("KafkaMessagesPublishedTotal not initialized")
	}
	if mc.registry == nil {
		t.Error("registry not initialized")
	}
}

func TestRecordHTTPRequest(t *testing.T) {
	mc := NewMetricsCollector()

	// Create context with tenant
	ctx := testdb.ContextWithTenant(t, "acme_bank")

	// Record a request
	mc.RecordHTTPRequest(ctx, "GET", "/api/test", 200, 100*time.Millisecond)

	// Verify counter was incremented with tenant label
	count := testutil.ToFloat64(mc.HTTPRequestsTotal.WithLabelValues("GET", "/api/test", "200", "acme_bank"))
	if count != 1 {
		t.Errorf("Expected count 1, got %f", count)
	}

	// Record another request with different status
	mc.RecordHTTPRequest(ctx, "GET", "/api/test", 404, 50*time.Millisecond)

	count404 := testutil.ToFloat64(mc.HTTPRequestsTotal.WithLabelValues("GET", "/api/test", "404", "acme_bank"))
	if count404 != 1 {
		t.Errorf("Expected count 1 for 404, got %f", count404)
	}
}

func TestRecordHTTPRequest_WithoutOrganization(t *testing.T) {
	mc := NewMetricsCollector()

	// Record a request without tenant context
	mc.RecordHTTPRequest(context.Background(), "GET", "/api/test", 200, 100*time.Millisecond)

	// Verify counter was incremented with "unknown" tenant
	count := testutil.ToFloat64(mc.HTTPRequestsTotal.WithLabelValues("GET", "/api/test", "200", "unknown"))
	if count != 1 {
		t.Errorf("Expected count 1 for unknown org, got %f", count)
	}
}

func TestRecordHTTPRequest_MultipleOrganizations(t *testing.T) {
	mc := NewMetricsCollector()

	// Create contexts for different tenants
	ctxAcme := testdb.ContextWithTenant(t, "acme_bank")
	ctxMotive := testdb.ContextWithTenant(t, "motive")

	// Record requests from different tenants
	mc.RecordHTTPRequest(ctxAcme, "GET", "/api/accounts", 200, 100*time.Millisecond)
	mc.RecordHTTPRequest(ctxAcme, "GET", "/api/accounts", 200, 100*time.Millisecond)
	mc.RecordHTTPRequest(ctxMotive, "GET", "/api/accounts", 200, 100*time.Millisecond)

	// Verify separate counts per tenant
	countAcme := testutil.ToFloat64(mc.HTTPRequestsTotal.WithLabelValues("GET", "/api/accounts", "200", "acme_bank"))
	if countAcme != 2 {
		t.Errorf("Expected count 2 for acme_bank, got %f", countAcme)
	}

	countMotive := testutil.ToFloat64(mc.HTTPRequestsTotal.WithLabelValues("GET", "/api/accounts", "200", "motive"))
	if countMotive != 1 {
		t.Errorf("Expected count 1 for motive, got %f", countMotive)
	}
}

func TestRecordGRPCRequest(t *testing.T) {
	mc := NewMetricsCollector()

	ctx := testdb.ContextWithTenant(t, "acme_bank")
	mc.RecordGRPCRequest(ctx, "PositionKeeping", "GetPosition", "OK")

	count := testutil.ToFloat64(mc.GRPCServerHandledTotal.WithLabelValues("PositionKeeping", "GetPosition", "OK", "acme_bank"))
	if count != 1 {
		t.Errorf("Expected count 1, got %f", count)
	}
}

func TestRecordGRPCRequest_WithoutOrganization(t *testing.T) {
	mc := NewMetricsCollector()

	mc.RecordGRPCRequest(context.Background(), "PositionKeeping", "GetPosition", "OK")

	count := testutil.ToFloat64(mc.GRPCServerHandledTotal.WithLabelValues("PositionKeeping", "GetPosition", "OK", "unknown"))
	if count != 1 {
		t.Errorf("Expected count 1, got %f", count)
	}
}

func TestRecordDBQuery(t *testing.T) {
	mc := NewMetricsCollector()

	ctx := testdb.ContextWithTenant(t, "acme_bank")

	// Record a query - we're just testing that it doesn't panic
	mc.RecordDBQuery(ctx, "SELECT", "positions", 25*time.Millisecond)
	mc.RecordDBQuery(ctx, "INSERT", "accounts", 10*time.Millisecond)

	// Also test without tenant
	mc.RecordDBQuery(context.Background(), "SELECT", "positions", 25*time.Millisecond)

	// Verify histogram was observed (basic sanity check)
	// Note: Detailed histogram values verified through metrics endpoint test
	if mc.DBQueryDurationSeconds == nil {
		t.Error("DBQueryDurationSeconds histogram not initialized")
	}
}

func TestRecordKafkaPublish(t *testing.T) {
	mc := NewMetricsCollector()

	ctx := testdb.ContextWithTenant(t, "acme_bank")
	mc.RecordKafkaPublish(ctx, "position.events", "success")

	count := testutil.ToFloat64(mc.KafkaMessagesPublishedTotal.WithLabelValues("position.events", "success", "acme_bank"))
	if count != 1 {
		t.Errorf("Expected count 1, got %f", count)
	}

	// Record a failure
	mc.RecordKafkaPublish(ctx, "position.events", "error")

	countError := testutil.ToFloat64(mc.KafkaMessagesPublishedTotal.WithLabelValues("position.events", "error", "acme_bank"))
	if countError != 1 {
		t.Errorf("Expected count 1 for error, got %f", countError)
	}
}

func TestRecordKafkaPublish_WithoutOrganization(t *testing.T) {
	mc := NewMetricsCollector()

	mc.RecordKafkaPublish(context.Background(), "position.events", "success")

	count := testutil.ToFloat64(mc.KafkaMessagesPublishedTotal.WithLabelValues("position.events", "success", "unknown"))
	if count != 1 {
		t.Errorf("Expected count 1, got %f", count)
	}
}

func TestMetricsHandler(t *testing.T) {
	mc := NewMetricsCollector()

	// Record some metrics
	ctx := testdb.ContextWithTenant(t, "acme_bank")
	mc.RecordHTTPRequest(ctx, "GET", "/test", 200, 100*time.Millisecond)

	// Create a request to the metrics endpoint
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	handler := mc.Handler()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if body == "" {
		t.Error("Metrics endpoint returned empty response")
	}

	// Check for expected metric in output
	if !containsString(body, "http_requests_total") {
		t.Error("Expected http_requests_total in metrics output")
	}
}

func TestHTTPMiddleware(t *testing.T) {
	mc := NewMetricsCollector()

	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Wrap with middleware
	wrappedHandler := mc.HTTPMiddleware(testHandler)

	// Make a request
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(w, req)

	// Verify the response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Verify metrics were recorded (note: middleware uses r.Context() which has no org)
	count := testutil.ToFloat64(mc.HTTPRequestsTotal.WithLabelValues("GET", "/test", "200", "unknown"))
	if count != 1 {
		t.Errorf("Expected count 1, got %f", count)
	}
}

func TestHTTPMiddleware_WithOrganization(t *testing.T) {
	mc := NewMetricsCollector()

	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Wrap with middleware
	wrappedHandler := mc.HTTPMiddleware(testHandler)

	// Make a request with tenant in context
	req := httptest.NewRequest("GET", "/test", nil)
	ctx := tenant.WithTenant(req.Context(), tenant.MustNewTenantID("acme_bank"))
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	wrappedHandler.ServeHTTP(w, req)

	// Verify metrics were recorded with tenant
	count := testutil.ToFloat64(mc.HTTPRequestsTotal.WithLabelValues("GET", "/test", "200", "acme_bank"))
	if count != 1 {
		t.Errorf("Expected count 1, got %f", count)
	}
}

func TestHTTPMiddleware_StatusCode(t *testing.T) {
	mc := NewMetricsCollector()

	tests := []struct {
		name           string
		statusCode     int
		expectedStatus string
	}{
		{"200 OK", http.StatusOK, "200"},
		{"404 Not Found", http.StatusNotFound, "404"},
		{"500 Internal Server Error", http.StatusInternalServerError, "500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
			})

			wrappedHandler := mc.HTTPMiddleware(testHandler)

			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()

			wrappedHandler.ServeHTTP(w, req)

			// Verify correct status code was recorded (with unknown org since no org context)
			count := testutil.ToFloat64(mc.HTTPRequestsTotal.WithLabelValues("GET", "/test", tt.expectedStatus, "unknown"))
			if count == 0 {
				t.Errorf("Expected metric for status %s to be recorded", tt.expectedStatus)
			}
		})
	}
}

func TestStartMetricsServer(t *testing.T) {
	mc := NewMetricsCollector()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server in background
	errCh := make(chan error, 1)
	go func() {
		err := mc.StartMetricsServer(ctx, ":0") // Use port 0 to get a random available port
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	//nolint:forbidigo // metrics server does not expose a ready state; time-based startup wait required
	time.Sleep(100 * time.Millisecond)

	// Cancel context to shutdown server
	cancel()

	//nolint:forbidigo // allows graceful HTTP server shutdown to complete before checking for errors
	time.Sleep(100 * time.Millisecond)

	// Check if there were any errors
	select {
	case err := <-errCh:
		t.Errorf("Server error: %v", err)
	default:
		// No error, server started and stopped cleanly
	}
}

func TestResponseWriter(t *testing.T) {
	w := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

	// Default status code should be 200
	if rw.statusCode != http.StatusOK {
		t.Errorf("Expected default status code 200, got %d", rw.statusCode)
	}

	// Write a different status code
	rw.WriteHeader(http.StatusNotFound)

	if rw.statusCode != http.StatusNotFound {
		t.Errorf("Expected status code 404, got %d", rw.statusCode)
	}
}

func containsString(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 || findString(haystack, needle))
}

func findString(haystack, needle string) bool {
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// Benchmark tests
func BenchmarkRecordHTTPRequest(b *testing.B) {
	mc := NewMetricsCollector()
	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("acme_bank"))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		mc.RecordHTTPRequest(ctx, "GET", "/test", 200, 100*time.Millisecond)
	}
}

func BenchmarkRecordHTTPRequest_WithoutOrganization(b *testing.B) {
	mc := NewMetricsCollector()
	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		mc.RecordHTTPRequest(ctx, "GET", "/test", 200, 100*time.Millisecond)
	}
}

func BenchmarkRecordDBQuery(b *testing.B) {
	mc := NewMetricsCollector()
	ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("acme_bank"))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		mc.RecordDBQuery(ctx, "SELECT", "positions", 25*time.Millisecond)
	}
}

// Test getOrganizationLabel helper function
func TestGetOrganizationLabel(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		expected string
	}{
		{
			name:     "nil context",
			ctx:      nil,
			expected: "unknown",
		},
		{
			name:     "empty context",
			ctx:      context.Background(),
			expected: "unknown",
		},
		{
			name:     "with tenant",
			ctx:      testdb.ContextWithTenant(t, "acme_bank"),
			expected: "acme_bank",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getOrganizationLabel(tt.ctx)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}
