//go:build integration

// Package mapping_e2e_test contains full HTTP round-trip tests for the gateway
// mapping layer, covering transformation, routing, error handling, idempotency,
// and multi-tenant isolation.
//
// These tests use the shared/pkg/mapping engine directly (the same engine used
// by the gateway middleware) with in-process HTTP test infrastructure to verify
// the complete inbound→transform→forward→outbound pipeline, complementing the
// unit and middleware tests in services/api-gateway/integration_test/.
package mapping_e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	sharedmapping "github.com/meridianhub/meridian/shared/pkg/mapping"
)

// =============================================================================
// Sentinel errors
// =============================================================================

// ErrMappingNotFound is returned by the test resolver when a mapping has no entry.
var ErrMappingNotFound = errors.New("mapping not found")

// ErrBackendUnavailable is returned by unavailableResolver.
var ErrBackendUnavailable = errors.New("backend unavailable")

// =============================================================================
// Local pipeline infrastructure
//
// These types replicate the gateway mapping middleware pipeline using only the
// shared/pkg/mapping engine, allowing tests to live outside the internal package
// boundary while exercising the same transformation logic.
// =============================================================================

// mappingResolver resolves a MappingDefinition for a given tenant and name.
type mappingResolver interface {
	Resolve(ctx context.Context, tenantID, name string) (*mappingv1.MappingDefinition, error)
}

// staticResolver always returns the same mapping definition.
type staticResolver struct {
	def *mappingv1.MappingDefinition
}

func (s *staticResolver) Resolve(_ context.Context, _, _ string) (*mappingv1.MappingDefinition, error) {
	return s.def, nil
}

// notFoundResolver always returns ErrMappingNotFound.
type notFoundResolver struct{}

func (n *notFoundResolver) Resolve(_ context.Context, _, _ string) (*mappingv1.MappingDefinition, error) {
	return nil, ErrMappingNotFound
}

// unavailableResolver simulates a resolver failure (e.g. reference-data down).
type unavailableResolver struct{}

func (u *unavailableResolver) Resolve(_ context.Context, _, _ string) (*mappingv1.MappingDefinition, error) {
	return nil, ErrBackendUnavailable
}

// tenantResolver returns different mappings per tenant. Returns ErrMappingNotFound
// if the tenantID has no registered mapping for the requested name.
type tenantResolver struct {
	// key: "tenantID:mappingName"
	mappings map[string]*mappingv1.MappingDefinition
}

func (t *tenantResolver) Resolve(_ context.Context, tenantID, name string) (*mappingv1.MappingDefinition, error) {
	if def, ok := t.mappings[tenantID+":"+name]; ok {
		return def, nil
	}
	return nil, ErrMappingNotFound
}

// headerTenantIDKey is the context key used to carry the tenant ID.
type contextKey string

const tenantIDContextKey contextKey = "x-tenant-id"

// withTenant injects a tenant ID into the request context.
func withTenant(r *http.Request, tenantID string) *http.Request {
	ctx := context.WithValue(r.Context(), tenantIDContextKey, tenantID)
	return r.WithContext(ctx)
}

// tenantFromCtx extracts the tenant ID from context; returns ("", false) if absent.
func tenantFromCtx(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(tenantIDContextKey).(string)
	return v, ok && v != ""
}

// mappingPipeline is a minimal HTTP handler that replicates the gateway mapping
// middleware pipeline using the shared/pkg/mapping engine. It:
//  1. Extracts the mapping name from /mapping/{name}.
//  2. Resolves the mapping definition for the tenant.
//  3. Reads and inbound-transforms the request body.
//  4. Injects the idempotency key header.
//  5. Rewrites the request path to /{service}/{rpc}.
//  6. Forwards to the next handler (Vanguard transcoder stub).
//  7. Applies outbound transformation on the response body.
type mappingPipeline struct {
	resolver mappingResolver
	engine   *sharedmapping.Engine
	logger   *slog.Logger
	next     http.Handler
}

// ServeHTTP processes the request through the mapping pipeline.
func (p *mappingPipeline) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const mappingPrefix = "/mapping/"

	// Pass through non-mapping paths unchanged.
	if !strings.HasPrefix(r.URL.Path, mappingPrefix) {
		p.next.ServeHTTP(w, r)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, mappingPrefix)
	if name == "" || strings.Contains(name, "/") {
		writeJSONError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "mapping name must be a single path segment")
		return
	}

	tenantID, ok := tenantFromCtx(r.Context())
	if !ok {
		writeJSONError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "tenant ID not found in request context")
		return
	}

	def, err := p.resolver.Resolve(r.Context(), tenantID, name)
	if err != nil {
		if errors.Is(err, ErrMappingNotFound) {
			writeJSONError(w, http.StatusNotFound, "NOT_FOUND",
				fmt.Sprintf("no active mapping found for name %q", name))
			return
		}
		p.logger.Error("failed to resolve mapping", "name", name, "tenant_id", tenantID, "error", err)
		writeJSONError(w, http.StatusBadGateway, "UNAVAILABLE", "failed to resolve mapping definition")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read request body")
		return
	}
	_ = r.Body.Close()

	if len(body) == 0 {
		writeJSONError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is empty")
		return
	}

	result, err := p.engine.TransformInbound(def, body)
	if err != nil {
		p.logger.Warn("inbound transformation failed", "name", name, "error", err)
		writeJSONError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
			fmt.Sprintf("inbound transformation failed: %v", err))
		return
	}

	if result.IdempotencyKey != "" {
		r.Header.Set("Idempotency-Key", result.IdempotencyKey)
	}

	r.Body = io.NopCloser(bytes.NewReader(result.ProtoJSON))
	r.ContentLength = int64(len(result.ProtoJSON))

	targetService := def.GetTargetService()
	targetRPC := def.GetTargetRpc()
	if targetService == "" || targetRPC == "" {
		writeJSONError(w, http.StatusBadGateway, "INTERNAL",
			"mapping definition has empty target_service or target_rpc")
		return
	}

	r.URL.Path = "/" + targetService + "/" + targetRPC

	// Capture the downstream response.
	rec := &responseRecorder{code: http.StatusOK, headers: make(http.Header)}
	p.next.ServeHTTP(rec, r)

	// Non-2xx responses pass through without outbound transformation.
	if rec.code < 200 || rec.code >= 300 {
		copyHeaders(w.Header(), rec.headers)
		w.WriteHeader(rec.code)
		_, _ = w.Write(rec.body)
		return
	}

	if len(rec.body) == 0 {
		copyHeaders(w.Header(), rec.headers)
		w.WriteHeader(rec.code)
		return
	}

	transformed, err := p.engine.TransformOutbound(def, rec.body)
	if err != nil {
		p.logger.Error("outbound transformation failed", "name", name, "error", err)
		writeJSONError(w, http.StatusInternalServerError, "INTERNAL", "outbound transformation failed")
		return
	}

	copyHeaders(w.Header(), rec.headers)
	w.Header().Del("Transfer-Encoding")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(transformed)))
	w.WriteHeader(rec.code)
	_, _ = w.Write(transformed)
}

// responseRecorder captures a downstream response.
type responseRecorder struct {
	code    int
	headers http.Header
	body    []byte
}

func (r *responseRecorder) Header() http.Header  { return r.headers }
func (r *responseRecorder) WriteHeader(code int) { r.code = code }
func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return len(b), nil
}

func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func writeJSONError(w http.ResponseWriter, code int, errCode, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": message,
		"code":  errCode,
	})
}

// newPipeline creates a mappingPipeline with the given resolver and downstream.
func newPipeline(t *testing.T, resolver mappingResolver, next http.Handler) http.Handler {
	t.Helper()
	eng, err := sharedmapping.NewEngine()
	require.NoError(t, err)
	return &mappingPipeline{
		resolver: resolver,
		engine:   eng,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		next:     next,
	}
}

// echoHandler captures the inbound-transformed body and echoes it back.
type echoHandler struct {
	received []byte
}

func (h *echoHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.received, _ = io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(h.received)
}

// errorHandler returns a 503 to simulate a backend service being down.
type errorHandler struct{}

func (h *errorHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusServiceUnavailable)
}

// backendValidationErrorHandler returns a 400 with a JSON body to simulate
// backend service rejecting the request after proto validation.
type backendValidationErrorHandler struct{}

func (h *backendValidationErrorHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"error":"validation failed","code":"INVALID_ARGUMENT"}`))
}

// newRequest creates an HTTP POST request with the given tenant ID in context.
func newRequest(t *testing.T, path string, body []byte, tenantID string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return withTenant(r, tenantID)
}

// =============================================================================
// Mapping definitions used across tests
// =============================================================================

// bankPartyOnboardingMapping returns a MappingDefinition that mirrors the
// bank-x-party-onboarding scenario: enum-mapped party_type, renamed fields,
// CEL inbound validation requiring full_name, and a govt_id idempotency selector.
func bankPartyOnboardingMapping() *mappingv1.MappingDefinition {
	return &mappingv1.MappingDefinition{
		Name:                 "bank-x-party-onboarding",
		TargetService:        "meridian.party.v1.PartyService",
		TargetRpc:            "RegisterParty",
		Version:              1,
		InboundValidationCel: `has(payload.full_name) && payload.full_name != ""`,
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "party_type",
				InternalPath: "party_type",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{
								"individual": "PARTY_TYPE_PERSON",
								"corporate":  "PARTY_TYPE_ORGANIZATION",
							},
						},
					},
				},
			},
			{
				ExternalPath: "full_name",
				InternalPath: "legal_name",
			},
			{
				ExternalPath: "first_name",
				InternalPath: "display_name",
			},
		},
		Idempotency: &mappingv1.IdempotencyConfig{
			SourceSelector: "govt_id",
		},
	}
}

// =============================================================================
// Happy Path: Single request inbound transformation
// =============================================================================

func TestMappingE2E_SingleRequestInboundTransform(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	echo := &echoHandler{}
	handler := newPipeline(t, &staticResolver{def: bankPartyOnboardingMapping()}, echo)

	payload := []byte(`{
		"party_type": "individual",
		"full_name": "Jane Smith",
		"first_name": "Jane",
		"govt_id": "GB-NI-123456"
	}`)

	req := newRequest(t, "/mapping/bank-x-party-onboarding", payload, "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var forwarded map[string]any
	require.NoError(t, json.Unmarshal(echo.received, &forwarded))

	assert.Equal(t, "PARTY_TYPE_PERSON", forwarded["party_type"],
		"party_type should be enum-mapped from individual to PARTY_TYPE_PERSON")
	assert.Equal(t, "Jane Smith", forwarded["legal_name"],
		"full_name should map to legal_name")
	assert.Equal(t, "Jane", forwarded["display_name"],
		"first_name should map to display_name")

	assert.Equal(t, "GB-NI-123456", req.Header.Get("Idempotency-Key"),
		"idempotency key should be set from govt_id selector")
}

// =============================================================================
// Happy Path: Outbound response transformation
// =============================================================================

func TestMappingE2E_OutboundResponseTransform(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := &mappingv1.MappingDefinition{
		Name:          "reverse-enum-mapping",
		TargetService: "meridian.party.v1.PartyService",
		TargetRpc:     "RegisterParty",
		Version:       1,
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "party_type",
				InternalPath: "party_type",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{
								"individual": "PARTY_TYPE_PERSON",
								"corporate":  "PARTY_TYPE_ORGANIZATION",
							},
							OutboundFallback: "unknown",
						},
					},
				},
			},
			{
				ExternalPath: "name",
				InternalPath: "legal_name",
			},
		},
	}

	// Downstream returns the internal (proto-JSON) representation.
	internalResponse := []byte(`{"party_type":"PARTY_TYPE_PERSON","legal_name":"Jane Smith"}`)
	downstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(internalResponse)
	})

	handler := newPipeline(t, &staticResolver{def: def}, downstream)

	payload := []byte(`{"party_type":"individual","name":"Jane Smith"}`)
	req := newRequest(t, "/mapping/reverse-enum-mapping", payload, "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var response map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))

	assert.Equal(t, "individual", response["party_type"],
		"outbound transform should reverse-map PARTY_TYPE_PERSON to individual")
	assert.Equal(t, "Jane Smith", response["name"],
		"name should be mapped back from legal_name")
}

// =============================================================================
// Happy Path: Batch mapping via engine
// =============================================================================

func TestMappingE2E_BatchMapping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng, err := sharedmapping.NewEngine()
	require.NoError(t, err)

	def := &mappingv1.MappingDefinition{
		Name:            "energy-batch",
		TargetService:   "meridian.observation.v1.ObservationService",
		TargetRpc:       "RecordObservation",
		Version:         1,
		IsBatch:         true,
		BatchTargetPath: "observations",
		Fields: []*mappingv1.FieldCorrespondence{
			{
				ExternalPath: "meter_id",
				InternalPath: "source_id",
			},
			{
				ExternalPath: "quality",
				InternalPath: "data_quality",
				Transform: &mappingv1.FieldTransform{
					Transform: &mappingv1.FieldTransform_EnumMapping{
						EnumMapping: &mappingv1.EnumMapping{
							Values: map[string]string{
								"actual":   "DATA_QUALITY_ACTUAL",
								"estimate": "DATA_QUALITY_ESTIMATE",
							},
						},
					},
				},
			},
		},
		Idempotency: &mappingv1.IdempotencyConfig{
			UseContentHash:    true,
			ContentHashFields: []string{"meter_id", "quality"},
		},
	}

	batchInput := []byte(`[
		{"meter_id":"METER-001","quality":"actual"},
		{"meter_id":"METER-002","quality":"estimate"}
	]`)

	results, err := eng.TransformInboundBatch(def, batchInput)
	require.NoError(t, err)
	require.Len(t, results, 2, "batch should produce one result per input element")

	// Each element is wrapped at BatchTargetPath ("observations") as a one-element array.
	// Unwrap to access the transformed fields.
	var env0, env1 map[string]any
	require.NoError(t, json.Unmarshal(results[0].ProtoJSON, &env0))
	require.NoError(t, json.Unmarshal(results[1].ProtoJSON, &env1))

	obs0Arr, ok0 := env0["observations"].([]any)
	require.True(t, ok0 && len(obs0Arr) > 0, "result[0] must have observations array")
	obs0, ok0m := obs0Arr[0].(map[string]any)
	require.True(t, ok0m, "result[0] observations[0] must be a JSON object")

	obs1Arr, ok1 := env1["observations"].([]any)
	require.True(t, ok1 && len(obs1Arr) > 0, "result[1] must have observations array")
	obs1, ok1m := obs1Arr[0].(map[string]any)
	require.True(t, ok1m, "result[1] observations[0] must be a JSON object")

	assert.Equal(t, "METER-001", obs0["source_id"])
	assert.Equal(t, "DATA_QUALITY_ACTUAL", obs0["data_quality"])
	assert.Equal(t, "METER-002", obs1["source_id"])
	assert.Equal(t, "DATA_QUALITY_ESTIMATE", obs1["data_quality"])

	// Idempotency keys must be non-empty and distinct per element.
	assert.NotEmpty(t, results[0].IdempotencyKey)
	assert.NotEmpty(t, results[1].IdempotencyKey)
	assert.NotEqual(t, results[0].IdempotencyKey, results[1].IdempotencyKey,
		"different batch elements should produce different idempotency keys")
}

// =============================================================================
// Happy Path: Round-trip data preservation
// =============================================================================

func TestMappingE2E_RoundTripPreservesData(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Identity mapping: external path equals internal path so all fields pass
	// through unchanged. The round-trip should preserve all field values.
	def := &mappingv1.MappingDefinition{
		Name:          "passthrough-mapping",
		TargetService: "meridian.party.v1.PartyService",
		TargetRpc:     "RegisterParty",
		Version:       1,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "name"},
			{ExternalPath: "code", InternalPath: "code"},
		},
	}

	inputPayload := []byte(`{"name":"Test Entity","code":"TEST-001"}`)

	downstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})

	handler := newPipeline(t, &staticResolver{def: def}, downstream)

	req := newRequest(t, "/mapping/passthrough-mapping", inputPayload, "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var inputMap, outputMap map[string]any
	require.NoError(t, json.Unmarshal(inputPayload, &inputMap))
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &outputMap))

	assert.Equal(t, inputMap["name"], outputMap["name"],
		"name should be preserved through round-trip")
	assert.Equal(t, inputMap["code"], outputMap["code"],
		"code should be preserved through round-trip")
}

// =============================================================================
// Error Path: Unknown mapping → HTTP 404
// =============================================================================

func TestMappingE2E_UnknownMapping_Returns404(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	handler := newPipeline(t, &notFoundResolver{}, &echoHandler{})

	req := newRequest(t, "/mapping/nonexistent-mapping", []byte(`{"field":"value"}`), "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)

	var errBody map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &errBody))
	assert.Equal(t, "NOT_FOUND", errBody["code"])
}

// =============================================================================
// Error Path: Missing required field (CEL validation) → HTTP 400
// =============================================================================

func TestMappingE2E_TransformFailure_MissingField_Returns400(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	handler := newPipeline(t,
		&staticResolver{def: bankPartyOnboardingMapping()},
		&echoHandler{},
	)

	// full_name is required by the CEL inbound validation expression.
	payload := []byte(`{"party_type":"individual"}`)
	req := newRequest(t, "/mapping/bank-x-party-onboarding", payload, "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code,
		"missing required field should return 400")
}

// =============================================================================
// Error Path: Unmapped enum value → HTTP 400
// =============================================================================

func TestMappingE2E_EnumMappingMiss_Returns400(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	handler := newPipeline(t,
		&staticResolver{def: bankPartyOnboardingMapping()},
		&echoHandler{},
	)

	// "partnership" is not in the enum mapping and there is no fallback defined.
	payload := []byte(`{"party_type":"partnership","full_name":"Some Firm"}`)
	req := newRequest(t, "/mapping/bank-x-party-onboarding", payload, "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code,
		"unmapped enum value without fallback should return 400")
}

// =============================================================================
// Error Path: Invalid JSON → HTTP 400
// =============================================================================

func TestMappingE2E_InvalidJSON_Returns400(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	handler := newPipeline(t,
		&staticResolver{def: bankPartyOnboardingMapping()},
		&echoHandler{},
	)

	req := newRequest(t, "/mapping/bank-x-party-onboarding", []byte(`{not valid json`), "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code,
		"malformed JSON body should return 400")
}

// =============================================================================
// Error Path: Target service down → HTTP 503 pass-through
// =============================================================================

func TestMappingE2E_TargetServiceDown_Returns503(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := &mappingv1.MappingDefinition{
		Name:          "simple-mapping",
		TargetService: "meridian.party.v1.PartyService",
		TargetRpc:     "RegisterParty",
		Version:       1,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "legal_name"},
		},
	}

	handler := newPipeline(t, &staticResolver{def: def}, &errorHandler{})

	req := newRequest(t, "/mapping/simple-mapping", []byte(`{"name":"Test"}`), "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code,
		"503 from downstream should be passed through unchanged")
}

// =============================================================================
// Error Path: Backend validation failure → HTTP 400 pass-through
// =============================================================================

func TestMappingE2E_BackendValidationFailure_Returns400(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := &mappingv1.MappingDefinition{
		Name:          "simple-mapping",
		TargetService: "meridian.party.v1.PartyService",
		TargetRpc:     "RegisterParty",
		Version:       1,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "legal_name"},
		},
	}

	handler := newPipeline(t, &staticResolver{def: def}, &backendValidationErrorHandler{})

	req := newRequest(t, "/mapping/simple-mapping", []byte(`{"name":"Test"}`), "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code,
		"400 from backend should be passed through")

	var errBody map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &errBody))
	assert.Equal(t, "INVALID_ARGUMENT", errBody["code"],
		"backend error body should be preserved")
}

// =============================================================================
// Idempotency: Same payload → same idempotency key
// =============================================================================

func TestMappingE2E_IdempotencyKey_SamePayloadSameKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	handler := newPipeline(t,
		&staticResolver{def: bankPartyOnboardingMapping()},
		&echoHandler{},
	)

	payload := []byte(`{
		"party_type": "individual",
		"full_name": "Alice Testor",
		"govt_id": "GB-NI-999888"
	}`)

	req1 := newRequest(t, "/mapping/bank-x-party-onboarding", payload, "tenant_001")
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	require.Equal(t, http.StatusOK, rr1.Code)

	firstKey := req1.Header.Get("Idempotency-Key")
	require.NotEmpty(t, firstKey, "idempotency key must be set on first request")
	assert.Equal(t, "GB-NI-999888", firstKey,
		"idempotency key should be derived from govt_id selector")

	// Second request with the same payload must produce the same key.
	req2 := newRequest(t, "/mapping/bank-x-party-onboarding", payload, "tenant_001")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	require.Equal(t, http.StatusOK, rr2.Code)

	secondKey := req2.Header.Get("Idempotency-Key")
	assert.Equal(t, firstKey, secondKey,
		"identical payloads must produce the same idempotency key")
}

// TestMappingE2E_IdempotencyKey_DifferentPayloadsDifferentKeys verifies that
// distinct payloads produce distinct idempotency keys.
func TestMappingE2E_IdempotencyKey_DifferentPayloadsDifferentKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	handler := newPipeline(t,
		&staticResolver{def: bankPartyOnboardingMapping()},
		&echoHandler{},
	)

	payload1 := []byte(`{"party_type":"individual","full_name":"Alice","govt_id":"GB-ID-001"}`)
	payload2 := []byte(`{"party_type":"individual","full_name":"Bob","govt_id":"GB-ID-002"}`)

	req1 := newRequest(t, "/mapping/bank-x-party-onboarding", payload1, "tenant_001")
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)
	require.Equal(t, http.StatusOK, rr1.Code)

	req2 := newRequest(t, "/mapping/bank-x-party-onboarding", payload2, "tenant_001")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	require.Equal(t, http.StatusOK, rr2.Code)

	key1 := req1.Header.Get("Idempotency-Key")
	key2 := req2.Header.Get("Idempotency-Key")

	assert.NotEmpty(t, key1)
	assert.NotEmpty(t, key2)
	assert.NotEqual(t, key1, key2,
		"different payloads must produce different idempotency keys")
}

// =============================================================================
// Multi-Tenant Isolation: Mappings not visible across tenants
// =============================================================================

func TestMappingE2E_TenantIsolation_Mappings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := &mappingv1.MappingDefinition{
		Name:          "tenant-specific-mapping",
		TargetService: "meridian.party.v1.PartyService",
		TargetRpc:     "RegisterParty",
		Version:       1,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "legal_name"},
		},
	}

	// Only tenant_a has the mapping registered.
	resolver := &tenantResolver{
		mappings: map[string]*mappingv1.MappingDefinition{
			"tenant_a:tenant-specific-mapping": def,
		},
	}

	handler := newPipeline(t, resolver, &echoHandler{})
	payload := []byte(`{"name":"Acme Corp"}`)

	// Tenant A → 200.
	reqA := newRequest(t, "/mapping/tenant-specific-mapping", payload, "tenant_a")
	rrA := httptest.NewRecorder()
	handler.ServeHTTP(rrA, reqA)
	assert.Equal(t, http.StatusOK, rrA.Code,
		"tenant A should be able to use its registered mapping")

	// Tenant B → 404 (mapping not registered for this tenant).
	reqB := newRequest(t, "/mapping/tenant-specific-mapping", payload, "tenant_b")
	rrB := httptest.NewRecorder()
	handler.ServeHTTP(rrB, reqB)
	assert.Equal(t, http.StatusNotFound, rrB.Code,
		"tenant B should receive 404 for a mapping it does not own")
}

// TestMappingE2E_TenantIsolation_TransformedFieldsNotContaminated verifies
// that sequential requests from different tenants each carry their own transformed
// data without cross-contamination (state isolation, not concurrency safety).
func TestMappingE2E_TenantIsolation_TransformedFieldsNotContaminated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := &mappingv1.MappingDefinition{
		Name:          "shared-mapping",
		TargetService: "meridian.party.v1.PartyService",
		TargetRpc:     "RegisterParty",
		Version:       1,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "org_name", InternalPath: "legal_name"},
			{ExternalPath: "tenant_ref", InternalPath: "external_reference"},
		},
	}

	resolver := &tenantResolver{
		mappings: map[string]*mappingv1.MappingDefinition{
			"tenant_a:shared-mapping": def,
			"tenant_b:shared-mapping": def,
		},
	}

	// Use a single long-lived pipeline to verify that shared handler state does
	// not contaminate sequential requests from different tenants.
	sharedEcho := &echoHandler{}
	pipeline := newPipeline(t, resolver, sharedEcho)

	payloadA := []byte(`{"org_name":"ACME Corp","tenant_ref":"TENANT-A-REF"}`)
	payloadB := []byte(`{"org_name":"Beta Ltd","tenant_ref":"TENANT-B-REF"}`)

	reqA := newRequest(t, "/mapping/shared-mapping", payloadA, "tenant_a")
	rrA := httptest.NewRecorder()
	pipeline.ServeHTTP(rrA, reqA)
	require.Equal(t, http.StatusOK, rrA.Code)
	// Capture inbound-transformed body echoed back in the response before the
	// second request overwrites sharedEcho.received.
	receivedA := make([]byte, len(sharedEcho.received))
	copy(receivedA, sharedEcho.received)

	reqB := newRequest(t, "/mapping/shared-mapping", payloadB, "tenant_b")
	rrB := httptest.NewRecorder()
	pipeline.ServeHTTP(rrB, reqB)
	require.Equal(t, http.StatusOK, rrB.Code)
	receivedB := sharedEcho.received

	var forwardedA, forwardedB map[string]any
	require.NoError(t, json.Unmarshal(receivedA, &forwardedA))
	require.NoError(t, json.Unmarshal(receivedB, &forwardedB))

	assert.Equal(t, "ACME Corp", forwardedA["legal_name"],
		"tenant A payload must not contain tenant B data")
	assert.Equal(t, "Beta Ltd", forwardedB["legal_name"],
		"tenant B payload must not contain tenant A data")
	assert.Equal(t, "TENANT-A-REF", forwardedA["external_reference"])
	assert.Equal(t, "TENANT-B-REF", forwardedB["external_reference"])
}

// =============================================================================
// Performance Baseline: Full round-trip under 100ms
// =============================================================================

func TestMappingE2E_Performance_Sub100ms(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	handler := newPipeline(t,
		&staticResolver{def: bankPartyOnboardingMapping()},
		&echoHandler{},
	)

	payload := []byte(`{
		"party_type": "individual",
		"full_name": "Performance Test User",
		"first_name": "Performance",
		"govt_id": "PERF-ID-001"
	}`)

	// Allow overriding the threshold via TEST_PERF_THRESHOLD_MS to avoid flakiness
	// on slow CI environments. Default is 100ms.
	thresholdMs := 100
	if v := os.Getenv("TEST_PERF_THRESHOLD_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			thresholdMs = n
		}
	}
	threshold := time.Duration(thresholdMs) * time.Millisecond

	start := time.Now()
	req := newRequest(t, "/mapping/bank-x-party-onboarding", payload, "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	elapsed := time.Since(start)

	t.Logf("mapping round-trip elapsed: %s (threshold: %s)", elapsed, threshold)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Less(t, elapsed, threshold,
		"full round-trip for a simple mapping should complete within threshold, got %s", elapsed)
}

// =============================================================================
// Additional: Non-mapping path passes through unchanged
// =============================================================================

func TestMappingE2E_NonMappingPath_Passthrough(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	called := false
	passthrough := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := newPipeline(t, &staticResolver{def: bankPartyOnboardingMapping()}, passthrough)

	req := newRequest(t, "/v1/parties", []byte(`{}`), "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called, "request to non-mapping path should be forwarded to next handler")
	assert.Equal(t, http.StatusOK, rr.Code)
}

// =============================================================================
// Additional: Missing tenant ID → HTTP 401
// =============================================================================

func TestMappingE2E_MissingTenantID_Returns401(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	handler := newPipeline(t,
		&staticResolver{def: bankPartyOnboardingMapping()},
		&echoHandler{},
	)

	// Request without tenant ID in context.
	r := httptest.NewRequest(http.MethodPost, "/mapping/bank-x-party-onboarding",
		bytes.NewReader([]byte(`{"party_type":"individual","full_name":"Jane"}`)))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, r)

	assert.Equal(t, http.StatusUnauthorized, rr.Code,
		"request without tenant ID should return 401")
}

// =============================================================================
// Additional: Empty body → HTTP 400
// =============================================================================

func TestMappingE2E_EmptyBody_Returns400(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	handler := newPipeline(t,
		&staticResolver{def: bankPartyOnboardingMapping()},
		&echoHandler{},
	)

	req := newRequest(t, "/mapping/bank-x-party-onboarding", []byte{}, "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code,
		"empty request body should return 400")
}

// =============================================================================
// Additional: Resolver unavailable → HTTP 502
// =============================================================================

func TestMappingE2E_ResolverUnavailable_Returns502(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	handler := newPipeline(t, &unavailableResolver{}, &echoHandler{})

	req := newRequest(t, "/mapping/some-mapping", []byte(`{"name":"test"}`), "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadGateway, rr.Code,
		"resolver failure should return 502")
}

// =============================================================================
// Additional: Verify URL is rewritten to target service path
// =============================================================================

func TestMappingE2E_URLRewrittenToTargetPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := &mappingv1.MappingDefinition{
		Name:          "url-rewrite-test",
		TargetService: "meridian.party.v1.PartyService",
		TargetRpc:     "RegisterParty",
		Version:       1,
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "name", InternalPath: "legal_name"},
		},
	}

	var capturedPath string
	capture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"legal_name":"Test"}`))
	})

	handler := newPipeline(t, &staticResolver{def: def}, capture)

	req := newRequest(t, "/mapping/url-rewrite-test", []byte(`{"name":"Test"}`), "tenant_001")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "/meridian.party.v1.PartyService/RegisterParty", capturedPath,
		"middleware should rewrite the URL to /{service}/{rpc}")
}
