package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/meridianhub/meridian/services/gateway/internal/mapping" //nolint:depguard
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Response recorder tests ---

func TestResponseRecorder_CapturesStatusAndBody(t *testing.T) {
	rec := newResponseRecorder()
	defer rec.release()

	rec.WriteHeader(http.StatusCreated)
	_, err := rec.Write([]byte(`{"id":"123"}`))
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, rec.code)
	assert.Equal(t, `{"id":"123"}`, rec.buf.String())
}

func TestResponseRecorder_DefaultStatusOK(t *testing.T) {
	rec := newResponseRecorder()
	defer rec.release()

	_, err := rec.Write([]byte("body"))
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, rec.code)
}

func TestResponseRecorder_HeadersCapture(t *testing.T) {
	rec := newResponseRecorder()
	defer rec.release()

	rec.Header().Set("Content-Type", "application/json")
	assert.Equal(t, "application/json", rec.headers.Get("Content-Type"))
}

// --- Outbound transformation tests ---

// vanguardHandler simulates a Vanguard backend returning a proto-JSON response.
func vanguardHandler(statusCode int, body string, headers map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = io.WriteString(w, body)
	})
}

func TestMappingMiddleware_OutboundTransformApplied(t *testing.T) {
	md := simpleMappingDef()
	// Fields: external "amount" <-> internal "amount"
	//         external "currency" <-> internal "currency_code"

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	// Vanguard returns proto-JSON with internal field names
	backend := vanguardHandler(http.StatusOK, `{"amount":100,"currency_code":"GBP"}`, nil)
	handler := mw.Handler(backend)

	body := `{"amount": 100, "currency": "GBP"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	err := json.Unmarshal(rec.Body.Bytes(), &result)
	require.NoError(t, err)

	// External field names should be present
	assert.Equal(t, float64(100), result["amount"])
	assert.Equal(t, "GBP", result["currency"])
	// Internal field name should be absent
	_, hasInternal := result["currency_code"]
	assert.False(t, hasInternal, "internal field name should not be exposed to client")
}

func TestMappingMiddleware_OutboundPreservesStatusCode(t *testing.T) {
	md := simpleMappingDef()
	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	backend := vanguardHandler(http.StatusCreated, `{"amount":50,"currency_code":"USD"}`, nil)
	handler := mw.Handler(backend)

	body := `{"amount": 50, "currency": "USD"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestMappingMiddleware_OutboundPreservesHeaders(t *testing.T) {
	md := simpleMappingDef()
	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	backend := vanguardHandler(http.StatusOK, `{"amount":10,"currency_code":"EUR"}`,
		map[string]string{"X-Request-Id": "req-xyz"})
	handler := mw.Handler(backend)

	body := `{"amount": 10, "currency": "EUR"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "req-xyz", rec.Header().Get("X-Request-Id"))
}

func TestMappingMiddleware_ErrorResponsePassThrough(t *testing.T) {
	md := simpleMappingDef()
	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	// Vanguard returns a 400 error — body should NOT be outbound-transformed.
	errorBody := `{"error":"invalid request","code":"INVALID_ARGUMENT"}`
	backend := vanguardHandler(http.StatusBadRequest, errorBody, nil)
	handler := mw.Handler(backend)

	body := `{"amount": 100, "currency": "GBP"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	// Body should be passed through with equivalent JSON (sanitization may reorder keys)
	assert.JSONEq(t, errorBody, rec.Body.String())
}

func TestMappingMiddleware_5xxResponsePassThrough(t *testing.T) {
	md := simpleMappingDef()
	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	errorBody := `{"error":"internal server error"}`
	backend := vanguardHandler(http.StatusInternalServerError, errorBody, nil)
	handler := mw.Handler(backend)

	body := `{"amount": 100, "currency": "GBP"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.JSONEq(t, errorBody, rec.Body.String())
}

func TestMappingMiddleware_OutboundUpdatesContentLength(t *testing.T) {
	md := &mappingv1.MappingDefinition{
		Id:            "00000000-0000-0000-0000-000000000001",
		TenantId:      "00000000-0000-0000-0000-000000000099",
		Name:          "test-mapping",
		TargetService: "meridian.payment_order.v1.PaymentOrderService",
		TargetRpc:     "InitiatePaymentOrder",
		Version:       1,
		Status:        mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
		Fields: []*mappingv1.FieldCorrespondence{
			// internal "id" -> external "transaction_id" (longer name)
			{ExternalPath: "transaction_id", InternalPath: "id"},
		},
	}

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	protoBody := `{"id":"abc-123"}`
	backend := vanguardHandler(http.StatusOK, protoBody, map[string]string{
		"Content-Length": fmt.Sprintf("%d", len(protoBody)),
	})
	handler := mw.Handler(backend)

	body := `{"transaction_id":"abc-123"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/test-mapping", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	// Verify Content-Length was updated to reflect the transformed body size.
	assert.Equal(t, fmt.Sprintf("%d", rec.Body.Len()), rec.Header().Get("Content-Length"))

	var result map[string]any
	err := json.Unmarshal(rec.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, "abc-123", result["transaction_id"])
}

func TestMappingMiddleware_OutboundValidationFailure(t *testing.T) {
	md := simpleMappingDef()
	// Add outbound validation that requires amount > 0 in internal response
	md.OutboundValidationCel = "payload.amount > 0"

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	// Vanguard returns amount = -1 which fails outbound validation
	backend := vanguardHandler(http.StatusOK, `{"amount":-1,"currency_code":"GBP"}`, nil)
	handler := mw.Handler(backend)

	body := `{"amount": -1, "currency": "GBP"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Outbound validation failure should return 500
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assertJSONErrorCode(t, rec.Body.Bytes(), "INTERNAL")
}

func TestMappingMiddleware_OutboundTransformFailure_Returns500(t *testing.T) {
	md := &mappingv1.MappingDefinition{
		Id:            "00000000-0000-0000-0000-000000000001",
		TenantId:      "00000000-0000-0000-0000-000000000099",
		Name:          "stripe-webhook",
		TargetService: "meridian.payment_order.v1.PaymentOrderService",
		TargetRpc:     "InitiatePaymentOrder",
		Version:       1,
		Status:        mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{
								"PENDING": "PENDING_STATUS",
							},
							// No fallback — unmapped values cause error
						},
					},
				},
			},
		},
	}

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	// Vanguard returns status = "UNKNOWN" which has no enum mapping
	backend := vanguardHandler(http.StatusOK, `{"status":"UNKNOWN_STATUS_NOT_IN_MAP"}`, nil)
	handler := mw.Handler(backend)

	body := `{"status": "PENDING"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assertJSONErrorCode(t, rec.Body.Bytes(), "INTERNAL")
}

func TestMappingMiddleware_EnumMappingInverted(t *testing.T) {
	md := &mappingv1.MappingDefinition{
		Id:            "00000000-0000-0000-0000-000000000001",
		TenantId:      "00000000-0000-0000-0000-000000000099",
		Name:          "stripe-webhook",
		TargetService: "meridian.payment_order.v1.PaymentOrderService",
		TargetRpc:     "InitiatePaymentOrder",
		Version:       1,
		Status:        mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "internal_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{
								"pending": "PENDING_STATUS",
								"active":  "ACTIVE_STATUS",
							},
						},
					},
				},
			},
		},
	}

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	// Vanguard returns internal enum value
	backend := vanguardHandler(http.StatusOK, `{"internal_status":"ACTIVE_STATUS"}`, nil)
	handler := mw.Handler(backend)

	body := `{"status": "active"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	err := json.Unmarshal(rec.Body.Bytes(), &result)
	require.NoError(t, err)
	assert.Equal(t, "active", result["status"])
}

func TestMappingMiddleware_DateFormatConverted(t *testing.T) {
	md := &mappingv1.MappingDefinition{
		Id:            "00000000-0000-0000-0000-000000000001",
		TenantId:      "00000000-0000-0000-0000-000000000099",
		Name:          "stripe-webhook",
		TargetService: "meridian.payment_order.v1.PaymentOrderService",
		TargetRpc:     "InitiatePaymentOrder",
		Version:       1,
		Status:        mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "created_date",
				InternalPath: "created_at",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_DateFormat{
						DateFormat: "2006-01-02",
					},
				},
			},
		},
	}

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	// Vanguard returns RFC3339 internal format
	backend := vanguardHandler(http.StatusOK, `{"created_at":"2024-03-15T10:30:00Z"}`, nil)
	handler := mw.Handler(backend)

	body := `{"created_date": "2024-03-15"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	err := json.Unmarshal(rec.Body.Bytes(), &result)
	require.NoError(t, err)
	// Should be converted from RFC3339 back to external date format
	assert.Equal(t, "2024-03-15", result["created_date"])
}

func TestMappingMiddleware_AttributeUnflattening(t *testing.T) {
	md := &mappingv1.MappingDefinition{
		Id:            "00000000-0000-0000-0000-000000000001",
		TenantId:      "00000000-0000-0000-0000-000000000099",
		Name:          "stripe-webhook",
		TargetService: "meridian.payment_order.v1.PaymentOrderService",
		TargetRpc:     "InitiatePaymentOrder",
		Version:       1,
		Status:        mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "metadata",
				InternalPath: "attributes",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_AttributeFlatten{
						AttributeFlatten: &mappingv1.AttributeFlatten{
							SourceKeys: []string{"color", "size"},
						},
					},
				},
			},
		},
	}

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	// Vanguard returns the internal attributes array format
	backend := vanguardHandler(http.StatusOK, `{"attributes":[{"key":"color","value":"blue"},{"key":"size","value":"XL"}]}`, nil)
	handler := mw.Handler(backend)

	body := `{"metadata": {"color": "blue", "size": "XL"}}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	err := json.Unmarshal(rec.Body.Bytes(), &result)
	require.NoError(t, err)
	// Outbound: attributes array -> external map
	metadata, ok := result["metadata"].(map[string]any)
	require.True(t, ok, "metadata should be a map")
	assert.Equal(t, "blue", metadata["color"])
	assert.Equal(t, "XL", metadata["size"])
}

func TestMappingMiddleware_UnmappedFieldsPassThrough(t *testing.T) {
	md := simpleMappingDef()
	// Fields: external "amount" <-> internal "amount"
	//         external "currency" <-> internal "currency_code"

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	// Vanguard response has extra unmapped fields
	backend := vanguardHandler(http.StatusOK, `{"amount":100,"currency_code":"GBP","transaction_id":"tx-999","status":"COMPLETED"}`, nil)
	handler := mw.Handler(backend)

	body := `{"amount": 100, "currency": "GBP"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	err := json.Unmarshal(rec.Body.Bytes(), &result)
	require.NoError(t, err)

	// Mapped fields should be renamed
	assert.Equal(t, float64(100), result["amount"])
	assert.Equal(t, "GBP", result["currency"])
	// Unmapped fields pass through unchanged
	assert.Equal(t, "tx-999", result["transaction_id"])
	assert.Equal(t, "COMPLETED", result["status"])
}

func TestMappingMiddleware_NonMappingRequestNotRecorded(t *testing.T) {
	// Non /mapping/* paths should not intercept the response at all.
	resolver := &stubResolver{mapping: simpleMappingDef()}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	backend := vanguardHandler(http.StatusOK, `{"name":"Alice"}`, nil)
	handler := mw.Handler(backend)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/parties", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, `{"name":"Alice"}`, rec.Body.String())
}

func BenchmarkMappingMiddleware_OutboundTransform(b *testing.B) {
	md := simpleMappingDef()
	resolver := &stubResolver{mapping: md}
	eng, err := mapping.NewEngine()
	if err != nil {
		b.Fatal(err)
	}
	mw := NewMappingMiddleware(resolver, eng, slog.New(slog.NewTextHandler(io.Discard, nil)))

	backend := vanguardHandler(http.StatusOK, `{"amount":100,"currency_code":"GBP"}`, nil)
	handler := mw.Handler(backend)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body := `{"amount": 100, "currency": "GBP"}`
		req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
		req = withTenantContext(req, "tenant-abc")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func TestMappingMiddleware_RoundTripInboundOutbound(t *testing.T) {
	// Integration test: inbound transforms external -> internal, outbound reverses internal -> external.
	md := &mappingv1.MappingDefinition{
		Id:            "00000000-0000-0000-0000-000000000001",
		TenantId:      "00000000-0000-0000-0000-000000000099",
		Name:          "round-trip",
		TargetService: "meridian.payment_order.v1.PaymentOrderService",
		TargetRpc:     "InitiatePaymentOrder",
		Version:       1,
		Status:        mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "cust_name", InternalPath: "customer_name"},
			{ExternalPath: "amt", InternalPath: "amount"},
		},
	}

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	// Backend echoes back the internal proto body it receives.
	var capturedInternal []byte
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedInternal, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(capturedInternal)
	})
	handler := mw.Handler(backend)

	// External inbound body
	externalBody := `{"cust_name": "Alice", "amt": 99}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/round-trip", bytes.NewBufferString(externalBody))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	// Internal proto should have renamed fields
	var internal map[string]any
	err := json.Unmarshal(capturedInternal, &internal)
	require.NoError(t, err)
	assert.Equal(t, "Alice", internal["customer_name"])
	assert.Equal(t, float64(99), internal["amount"])

	// External response should have original field names restored
	var external map[string]any
	err = json.Unmarshal(rec.Body.Bytes(), &external)
	require.NoError(t, err)
	assert.Equal(t, "Alice", external["cust_name"])
	assert.Equal(t, float64(99), external["amt"])
}

func TestMappingMiddleware_OutboundTransformFailure_DoesNotExposeProtoFields(t *testing.T) {
	md := &mappingv1.MappingDefinition{
		Id:            "00000000-0000-0000-0000-000000000001",
		TenantId:      "00000000-0000-0000-0000-000000000099",
		Name:          "stripe-webhook",
		TargetService: "meridian.payment_order.v1.PaymentOrderService",
		TargetRpc:     "InitiatePaymentOrder",
		Version:       1,
		Status:        mappingv1.MappingStatus_MAPPING_STATUS_ACTIVE,
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "internal_status",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{"pending": "PENDING_STATUS"},
							// No fallback
						},
					},
				},
			},
		},
	}

	resolver := &stubResolver{mapping: md}
	eng := testEngine(t)
	mw := NewMappingMiddleware(resolver, eng, testLogger())

	backend := vanguardHandler(http.StatusOK, `{"internal_status":"UNMAPPABLE_VALUE"}`, nil)
	handler := mw.Handler(backend)

	body := `{"status": "pending"}`
	req := httptest.NewRequest(http.MethodPost, "/mapping/stripe-webhook", bytes.NewBufferString(body))
	req = withTenantContext(req, "tenant-abc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	// Error response should NOT contain internal proto field names
	responseBody := rec.Body.String()
	assert.False(t, strings.Contains(responseBody, "internal_status"),
		"error response should not expose internal field names: %s", responseBody)
	assert.False(t, strings.Contains(responseBody, "UNMAPPABLE_VALUE"),
		"error response should not expose internal proto values: %s", responseBody)
}
