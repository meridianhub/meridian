package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/meridianhub/meridian/services/api-gateway/auth"
	"github.com/meridianhub/meridian/services/api-gateway/internal/mapping"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubResolver implements MappingResolver for testing.
type stubResolver struct {
	mapping *mappingv1.MappingDefinition
	err     error
	calls   int
}

func (s *stubResolver) Resolve(_ context.Context, _, _ string) (*mappingv1.MappingDefinition, error) {
	s.calls++
	return s.mapping, s.err
}

// captureHandler captures the forwarded request for assertion.
type captureHandler struct {
	captured *http.Request
	body     []byte
}

func (h *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.captured = r
	h.body, _ = io.ReadAll(r.Body)
	w.WriteHeader(http.StatusOK)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testEngine(t *testing.T) *mapping.Engine {
	t.Helper()
	eng, err := mapping.NewEngine()
	require.NoError(t, err)
	return eng
}

// withTenantContext adds a tenant ID to the request context via the auth package's context key.
func withTenantContext(r *http.Request, tenantID string) *http.Request {
	ctx := context.WithValue(r.Context(), auth.TenantIDContextKey, tenantID)
	return r.WithContext(ctx)
}

func simpleMappingDef() *mappingv1.MappingDefinition {
	return &mappingv1.MappingDefinition{
		Id:            "00000000-0000-0000-0000-000000000001",
		TenantId:      "00000000-0000-0000-0000-000000000099",
		Name:          "stripe-webhook",
		TargetService: "meridian.payment_order.v1.PaymentOrderService",
		TargetRpc:     "InitiatePaymentOrder",
		Version:       1,
		Status:        mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "amount",
				InternalPath: "amount",
			},
			{
				ExternalPath: "currency",
				InternalPath: "currency_code",
			},
		},
	}
}

func TestMappingMiddleware_PassthroughNonMappingRequests(t *testing.T) {
	resolver := &stubResolver{mapping: simpleMappingDef()}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	capture := &captureHandler{}
	handler := mw.Handler(capture)

	// Request to /api/v1/parties should pass through
	req := httptest.NewRequest(http.MethodPost, "/api/v1/parties", bytes.NewBufferString(`{"name":"test"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotNil(t, capture.captured)
	assert.Equal(t, "/api/v1/parties", capture.captured.URL.Path)
	assert.Equal(t, 0, resolver.calls, "resolver should not be called for non-mapping requests")
}

func TestMappingMiddleware_TransformsAndForwards(t *testing.T) {
	resolver := &stubResolver{mapping: simpleMappingDef()}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	capture := &captureHandler{}
	handler := mw.Handler(capture)

	body := `{"amount": 100, "currency": "GBP"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, capture.captured)

	// URL should be rewritten to target service/RPC path
	assert.Equal(t, "/meridian.payment_order.v1.PaymentOrderService/InitiatePaymentOrder", capture.captured.URL.Path)

	// Body should be transformed
	var result map[string]any
	err := json.Unmarshal(capture.body, &result)
	require.NoError(t, err)
	assert.Equal(t, float64(100), result["amount"])
	assert.Equal(t, "GBP", result["currency_code"])
}

func TestMappingMiddleware_SetsIdempotencyKey(t *testing.T) {
	md := simpleMappingDef()
	md.Idempotency = &mappingv1.IdempotencyConfig{
		SourceSelector: "ref_id",
	}

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	capture := &captureHandler{}
	handler := mw.Handler(capture)

	body := `{"amount": 50, "currency": "USD", "ref_id": "abc-123"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, capture.captured)
	assert.Equal(t, "abc-123", capture.captured.Header.Get(HeaderIdempotencyKey))
}

func TestMappingMiddleware_MissingTenant(t *testing.T) {
	resolver := &stubResolver{mapping: simpleMappingDef()}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	handler := mw.Handler(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	body := `{"amount": 100}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	// No tenant context
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assertJSONErrorCode(t, rec.Body.Bytes(), "UNAUTHENTICATED")
}

func TestMappingMiddleware_MappingNotFound(t *testing.T) {
	resolver := &stubResolver{err: ErrMappingNotFound}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	handler := mw.Handler(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	body := `{"amount": 100}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/nonexistent", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assertJSONErrorCode(t, rec.Body.Bytes(), "NOT_FOUND")
}

func TestMappingMiddleware_EmptyBody(t *testing.T) {
	resolver := &stubResolver{mapping: simpleMappingDef()}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	handler := mw.Handler(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", nil)
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assertJSONErrorCode(t, rec.Body.Bytes(), "INVALID_ARGUMENT")
}

func TestMappingMiddleware_InvalidMappingName(t *testing.T) {
	resolver := &stubResolver{mapping: simpleMappingDef()}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	handler := mw.Handler(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	tests := []struct {
		name string
		path string
	}{
		{"empty name", "/mapping/"},
		{"nested path", "/mapping/foo/bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(`{}`))
			req = withTenantContext(req, "tenant-abc")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assertJSONErrorCode(t, rec.Body.Bytes(), "INVALID_ARGUMENT")
		})
	}
}

func TestMappingMiddleware_TransformError(t *testing.T) {
	md := simpleMappingDef()
	md.InboundValidationCel = "payload.amount > 0"

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	handler := mw.Handler(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	// amount = -1 should fail validation
	body := `{"amount": -1, "currency": "GBP"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assertJSONErrorCode(t, rec.Body.Bytes(), "INVALID_ARGUMENT")
}

func TestMappingMiddleware_ResolverError(t *testing.T) {
	resolver := &stubResolver{err: assert.AnError}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	handler := mw.Handler(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	body := `{"amount": 100}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assertJSONErrorCode(t, rec.Body.Bytes(), "UNAVAILABLE")
}

func TestMappingMiddleware_EmptyTargetService(t *testing.T) {
	md := simpleMappingDef()
	md.TargetService = ""

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	capture := &captureHandler{}
	handler := mw.Handler(capture)

	body := `{"amount": 100, "currency": "GBP"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assertJSONErrorCode(t, rec.Body.Bytes(), "INTERNAL")
	assert.Nil(t, capture.captured, "next handler should not be called")
}

func TestMappingMiddleware_EmptyTargetRPC(t *testing.T) {
	md := simpleMappingDef()
	md.TargetRpc = ""

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	capture := &captureHandler{}
	handler := mw.Handler(capture)

	body := `{"amount": 100, "currency": "GBP"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assertJSONErrorCode(t, rec.Body.Bytes(), "INTERNAL")
	assert.Nil(t, capture.captured, "next handler should not be called")
}

// --- CachedMappingResolver tests ---

func TestCachedMappingResolver_CachesResult(t *testing.T) {
	delegate := &stubResolver{mapping: simpleMappingDef()}
	cached := NewCachedMappingResolver(delegate, 5*time.Minute)

	// First call should hit delegate
	md, err := cached.Resolve(context.Background(), "tenant-1", "stripe-webhook")
	require.NoError(t, err)
	assert.Equal(t, "stripe-webhook", md.GetName())
	assert.Equal(t, 1, delegate.calls)

	// Second call should use cache
	md2, err := cached.Resolve(context.Background(), "tenant-1", "stripe-webhook")
	require.NoError(t, err)
	assert.Equal(t, md, md2)
	assert.Equal(t, 1, delegate.calls, "delegate should not be called again for cached entry")
}

func TestCachedMappingResolver_ExpiredEntry(t *testing.T) {
	delegate := &stubResolver{mapping: simpleMappingDef()}
	cached := NewCachedMappingResolver(delegate, 1*time.Millisecond)

	// First call
	_, err := cached.Resolve(context.Background(), "tenant-1", "stripe-webhook")
	require.NoError(t, err)
	assert.Equal(t, 1, delegate.calls)

	// Wait for expiry
	time.Sleep(5 * time.Millisecond) //nolint:forbidigo // triggers cache TTL expiry to test re-resolution

	// Second call should re-resolve
	_, err = cached.Resolve(context.Background(), "tenant-1", "stripe-webhook")
	require.NoError(t, err)
	assert.Equal(t, 2, delegate.calls)
}

func TestCachedMappingResolver_DifferentTenants(t *testing.T) {
	delegate := &stubResolver{mapping: simpleMappingDef()}
	cached := NewCachedMappingResolver(delegate, 5*time.Minute)

	_, err := cached.Resolve(context.Background(), "tenant-1", "stripe-webhook")
	require.NoError(t, err)
	assert.Equal(t, 1, delegate.calls)

	// Different tenant should trigger a new resolve
	_, err = cached.Resolve(context.Background(), "tenant-2", "stripe-webhook")
	require.NoError(t, err)
	assert.Equal(t, 2, delegate.calls)
}

func TestCachedMappingResolver_Invalidate(t *testing.T) {
	delegate := &stubResolver{mapping: simpleMappingDef()}
	cached := NewCachedMappingResolver(delegate, 5*time.Minute)

	_, err := cached.Resolve(context.Background(), "tenant-1", "stripe-webhook")
	require.NoError(t, err)
	assert.Equal(t, 1, delegate.calls)

	// Invalidate and re-resolve
	cached.Invalidate("tenant-1", "stripe-webhook")

	_, err = cached.Resolve(context.Background(), "tenant-1", "stripe-webhook")
	require.NoError(t, err)
	assert.Equal(t, 2, delegate.calls)
}

func TestCachedMappingResolver_DelegateError(t *testing.T) {
	delegate := &stubResolver{err: ErrMappingNotFound}
	cached := NewCachedMappingResolver(delegate, 5*time.Minute)

	_, err := cached.Resolve(context.Background(), "tenant-1", "stripe-webhook")
	assert.ErrorIs(t, err, ErrMappingNotFound)
}

// --- Helper functions ---

func assertJSONErrorCode(t *testing.T, body []byte, expectedCode string) {
	t.Helper()
	var resp struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	err := json.Unmarshal(body, &resp)
	require.NoError(t, err, "response body should be valid JSON: %s", string(body))
	assert.Equal(t, expectedCode, resp.Code)
}
