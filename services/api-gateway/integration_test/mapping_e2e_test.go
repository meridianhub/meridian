//go:build integration

// Package integration_test provides end-to-end tests for the gateway mapping layer.
// These tests verify that the reference mapping examples transform correctly through
// the full inbound and outbound pipeline.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/meridianhub/meridian/services/api-gateway/auth"
	gatewaymapping "github.com/meridianhub/meridian/services/api-gateway/internal/mapping"
	"github.com/meridianhub/meridian/services/api-gateway/internal/middleware"
)

// ============================================================================
// JSON example structs (mirrors the snake_case format in examples/ directory)
// ============================================================================

type exFieldTransform struct {
	EnumMapping      *exEnumMapping      `json:"enum_mapping,omitempty"`
	DateFormat       string              `json:"date_format,omitempty"`
	DefaultValue     string              `json:"default_value,omitempty"`
	AttributeFlatten *exAttributeFlatten `json:"attribute_flatten,omitempty"`
	CEL              *exCelTransform     `json:"cel,omitempty"`
}

type exEnumMapping struct {
	Values           map[string]string `json:"values"`
	Fallback         string            `json:"fallback,omitempty"`
	OutboundFallback string            `json:"outbound_fallback,omitempty"`
}

type exAttributeFlatten struct {
	SourceKeys  []string `json:"source_keys"`
	TargetField string   `json:"target_field"`
}

type exCelTransform struct {
	InboundCEL  string `json:"inbound_cel,omitempty"`
	OutboundCEL string `json:"outbound_cel,omitempty"`
}

type exFieldCorrespondence struct {
	ExternalPath string            `json:"external_path"`
	InternalPath string            `json:"internal_path"`
	Transform    *exFieldTransform `json:"transform,omitempty"`
}

type exComputedField struct {
	TargetPath    string `json:"target_path"`
	CELExpression string `json:"cel_expression"`
}

type exIdempotencyConfig struct {
	SourceSelector    string   `json:"source_selector,omitempty"`
	UseContentHash    bool     `json:"use_content_hash,omitempty"`
	ContentHashFields []string `json:"content_hash_fields,omitempty"`
}

type exMappingDefinition struct {
	Name                  string                  `json:"name"`
	TargetService         string                  `json:"target_service"`
	TargetRPC             string                  `json:"target_rpc"`
	Version               int                     `json:"version"`
	InboundValidationCEL  string                  `json:"inbound_validation_cel,omitempty"`
	OutboundValidationCEL string                  `json:"outbound_validation_cel,omitempty"`
	IsBatch               bool                    `json:"is_batch,omitempty"`
	BatchTargetPath       string                  `json:"batch_target_path,omitempty"`
	Fields                []exFieldCorrespondence `json:"fields,omitempty"`
	InboundComputed       []exComputedField       `json:"inbound_computed_fields,omitempty"`
	OutboundComputed      []exComputedField       `json:"outbound_computed_fields,omitempty"`
	Idempotency           *exIdempotencyConfig    `json:"idempotency,omitempty"`
}

// ============================================================================
// Test helpers
// ============================================================================

// examplesDir returns the absolute path to services/reference-data/examples/.
func examplesDir(t *testing.T) string {
	t.Helper()
	_, callerFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Join(filepath.Dir(callerFile), "..", "..", "reference-data", "examples")
}

// loadProtoMapping reads an example JSON file and converts it to a proto MappingDefinition.
func loadProtoMapping(t *testing.T, filename string) *mappingv1.MappingDefinition {
	t.Helper()
	path := filepath.Join(examplesDir(t), filename)
	data, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s", filename)

	var ex exMappingDefinition
	require.NoError(t, json.Unmarshal(data, &ex), "unmarshaling %s", filename)
	return exToProto(&ex)
}

func exToProto(ex *exMappingDefinition) *mappingv1.MappingDefinition {
	proto := &mappingv1.MappingDefinition{
		Name:                  ex.Name,
		TargetService:         ex.TargetService,
		TargetRpc:             ex.TargetRPC,
		Version:               int32(ex.Version),
		InboundValidationCel:  ex.InboundValidationCEL,
		OutboundValidationCel: ex.OutboundValidationCEL,
		IsBatch:               ex.IsBatch,
		BatchTargetPath:       ex.BatchTargetPath,
	}
	for _, f := range ex.Fields {
		pc := &mappingv1.FieldCorrespondence{
			ExternalPath: f.ExternalPath,
			InternalPath: f.InternalPath,
		}
		if f.Transform != nil {
			pc.Transform = exTransformToProto(f.Transform)
		}
		proto.Fields = append(proto.Fields, pc)
	}
	for _, cf := range ex.InboundComputed {
		proto.InboundComputedFields = append(proto.InboundComputedFields, &mappingv1.ComputedField{
			TargetPath:    cf.TargetPath,
			CelExpression: cf.CELExpression,
		})
	}
	for _, cf := range ex.OutboundComputed {
		proto.OutboundComputedFields = append(proto.OutboundComputedFields, &mappingv1.ComputedField{
			TargetPath:    cf.TargetPath,
			CelExpression: cf.CELExpression,
		})
	}
	if ex.Idempotency != nil {
		proto.Idempotency = &mappingv1.IdempotencyConfig{
			SourceSelector:    ex.Idempotency.SourceSelector,
			UseContentHash:    ex.Idempotency.UseContentHash,
			ContentHashFields: ex.Idempotency.ContentHashFields,
		}
	}
	return proto
}

func exTransformToProto(t *exFieldTransform) *mappingv1.FieldTransform {
	if t == nil {
		return nil
	}
	ft := &mappingv1.FieldTransform{}
	switch {
	case t.CEL != nil:
		ft.Transform = &mappingv1.FieldTransform_Cel{Cel: &mappingv1.CelTransform{
			InboundCel: t.CEL.InboundCEL, OutboundCel: t.CEL.OutboundCEL,
		}}
	case t.EnumMapping != nil:
		ft.Transform = &mappingv1.FieldTransform_EnumMapping{EnumMapping: &mappingv1.EnumMapping{
			Values: t.EnumMapping.Values, Fallback: t.EnumMapping.Fallback, OutboundFallback: t.EnumMapping.OutboundFallback,
		}}
	case t.DateFormat != "":
		ft.Transform = &mappingv1.FieldTransform_DateFormat{DateFormat: t.DateFormat}
	case t.DefaultValue != "":
		ft.Transform = &mappingv1.FieldTransform_DefaultValue{DefaultValue: t.DefaultValue}
	case t.AttributeFlatten != nil:
		ft.Transform = &mappingv1.FieldTransform_AttributeFlatten{AttributeFlatten: &mappingv1.AttributeFlatten{
			SourceKeys: t.AttributeFlatten.SourceKeys, TargetField: t.AttributeFlatten.TargetField,
		}}
	}
	return ft
}

// staticResolver returns a fixed mapping definition for all Resolve calls.
type staticResolver struct {
	def *mappingv1.MappingDefinition
}

func (s *staticResolver) Resolve(_ context.Context, _, _ string) (*mappingv1.MappingDefinition, error) {
	return s.def, nil
}

// echoJSONHandler captures the transformed body and echoes it back as the "response".
// It serves as the downstream Vanguard transcoder stub.
type echoJSONHandler struct {
	body []byte
}

func (h *echoJSONHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.body, _ = io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// Echo back the received body so outbound transform has something to work with
	_, _ = w.Write(h.body)
}

func newMappingMiddleware(t *testing.T, def *mappingv1.MappingDefinition) *middleware.MappingMiddleware {
	t.Helper()
	eng, err := gatewaymapping.NewEngine()
	require.NoError(t, err)
	resolver := &staticResolver{def: def}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return middleware.NewMappingMiddleware(resolver, eng, logger)
}

// requestWithTenant creates an HTTP request with tenant ID in context.
func requestWithTenant(method, path string, body []byte, tenantID string) *http.Request {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(r.Context(), auth.TenantIDContextKey, tenantID)
	return r.WithContext(ctx)
}

// ============================================================================
// E2E Test: Bank Party Onboarding
// ============================================================================

func TestE2E_BankPartyOnboarding_InboundTransform(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "bank-x-party-onboarding.json")
	mw := newMappingMiddleware(t, def)
	echo := &echoJSONHandler{}

	externalPayload := []byte(`{
		"party_type": "individual",
		"full_name": "Jane Smith",
		"first_name": "Jane",
		"date_of_birth": "1985-03-15",
		"govt_id": "GB-NI-123456",
		"email": "jane.smith@example.com"
	}`)

	req := requestWithTenant(http.MethodPost, "/mapping/bank-x-party-onboarding", externalPayload, "tenant-001")
	rr := httptest.NewRecorder()
	mw.Handler(echo).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	// The echo body contains the inbound-transformed payload (forwarded to Vanguard)
	var forwarded map[string]any
	require.NoError(t, json.Unmarshal(echo.body, &forwarded))

	assert.Equal(t, "PARTY_TYPE_PERSON", forwarded["party_type"])
	assert.Equal(t, "Jane Smith", forwarded["legal_name"])
	assert.Equal(t, "Jane", forwarded["display_name"])

	// Idempotency key header must be set
	assert.Equal(t, "GB-NI-123456", req.Header.Get(middleware.HeaderIdempotencyKey))
}

func TestE2E_BankPartyOnboarding_InvalidJSON_Returns400(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "bank-x-party-onboarding.json")
	mw := newMappingMiddleware(t, def)
	echo := &echoJSONHandler{}

	req := requestWithTenant(http.MethodPost, "/mapping/bank-x-party-onboarding", []byte(`{bad json`), "tenant-001")
	rr := httptest.NewRecorder()
	mw.Handler(echo).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestE2E_BankPartyOnboarding_EmptyBody_Returns400(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "bank-x-party-onboarding.json")
	mw := newMappingMiddleware(t, def)
	echo := &echoJSONHandler{}

	req := requestWithTenant(http.MethodPost, "/mapping/bank-x-party-onboarding", []byte{}, "tenant-001")
	rr := httptest.NewRecorder()
	mw.Handler(echo).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestE2E_BankPartyOnboarding_ValidationFails_Returns400(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "bank-x-party-onboarding.json")
	mw := newMappingMiddleware(t, def)
	echo := &echoJSONHandler{}

	// Missing full_name (required by inbound CEL validation)
	payload := []byte(`{"party_type":"individual"}`)
	req := requestWithTenant(http.MethodPost, "/mapping/bank-x-party-onboarding", payload, "tenant-001")
	rr := httptest.NewRecorder()
	mw.Handler(echo).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestE2E_BankPartyOnboarding_UnmappedEnum_Returns400(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "bank-x-party-onboarding.json")
	mw := newMappingMiddleware(t, def)
	echo := &echoJSONHandler{}

	// party_type value not in enum mapping and no fallback defined
	payload := []byte(`{"party_type":"partnership","full_name":"Some Firm"}`)
	req := requestWithTenant(http.MethodPost, "/mapping/bank-x-party-onboarding", payload, "tenant-001")
	rr := httptest.NewRecorder()
	mw.Handler(echo).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestE2E_BankPartyOnboarding_MissingTenantID_Returns401(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "bank-x-party-onboarding.json")
	mw := newMappingMiddleware(t, def)
	echo := &echoJSONHandler{}

	payload := []byte(`{"party_type":"individual","full_name":"Jane"}`)
	// No tenant context
	req := httptest.NewRequest(http.MethodPost, "/mapping/bank-x-party-onboarding", bytes.NewReader(payload))
	rr := httptest.NewRecorder()
	mw.Handler(echo).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ============================================================================
// E2E Test: Energy Metering Feed (Batch)
// ============================================================================

func TestE2E_EnergyMeteringFeed_InboundBatchTransform(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "energy-metering-feed.json")
	eng, err := gatewaymapping.NewEngine()
	require.NoError(t, err)

	batchInput := []byte(`[
		{
			"meter_id": "METER-001",
			"timestamp": "2024-01-15T10:00:00Z",
			"value_kwh": 100.5,
			"quality": "actual",
			"tenor": "1H",
			"settlement_date": "2024-01-16"
		},
		{
			"meter_id": "METER-002",
			"timestamp": "2024-01-15T10:00:00Z",
			"value_kwh": 75.0,
			"quality": "estimated"
		}
	]`)

	results, err := eng.TransformInboundBatch(def, batchInput)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// First element
	var obs0 map[string]any
	require.NoError(t, json.Unmarshal(results[0].ProtoJSON, &obs0))
	obs0List, ok := obs0["observations"].([]any)
	require.True(t, ok, "observations must be an array")
	require.Len(t, obs0List, 1)
	item0 := obs0List[0].(map[string]any)
	assert.Equal(t, "METER-001", item0["source_id"])
	assert.Equal(t, "DATA_QUALITY_ACTUAL", item0["data_quality"])
	assert.NotEmpty(t, results[0].IdempotencyKey)

	// Second element
	var obs1 map[string]any
	require.NoError(t, json.Unmarshal(results[1].ProtoJSON, &obs1))
	obs1List := obs1["observations"].([]any)
	item1 := obs1List[0].(map[string]any)
	assert.Equal(t, "METER-002", item1["source_id"])
	assert.Equal(t, "DATA_QUALITY_ESTIMATE", item1["data_quality"])

	// Idempotency keys should be different per element
	assert.NotEqual(t, results[0].IdempotencyKey, results[1].IdempotencyKey)
}

func TestE2E_EnergyMeteringFeed_BatchValidationFails_NegativeValue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "energy-metering-feed.json")
	eng, err := gatewaymapping.NewEngine()
	require.NoError(t, err)

	batchInput := []byte(`[
		{"meter_id": "METER-001", "timestamp": "2024-01-15T10:00:00Z", "value_kwh": -10.0, "quality": "actual"}
	]`)

	_, err = eng.TransformInboundBatch(def, batchInput)
	assert.Error(t, err, "batch with negative value_kwh should fail validation")
}

func TestE2E_EnergyMeteringFeed_BatchNotArray_Fails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "energy-metering-feed.json")
	eng, err := gatewaymapping.NewEngine()
	require.NoError(t, err)

	// Not an array
	_, err = eng.TransformInboundBatch(def, []byte(`{"meter_id":"METER-001"}`))
	assert.Error(t, err)
}

// ============================================================================
// E2E Test: Verra Carbon Credit
// ============================================================================

func TestE2E_VerraCarbonCredit_InboundTransform(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "verra-carbon-credit.json")
	mw := newMappingMiddleware(t, def)
	echo := &echoJSONHandler{}

	payload := []byte(`{
		"vcs_id": "VCS-2845",
		"project_name": "Rimba Raya Biodiversity Reserve",
		"vintage_year": 2022,
		"methodology": "VM0009",
		"registry": "Verra",
		"min_amount": 1.0,
		"validation_date": "2023-06-01"
	}`)

	req := requestWithTenant(http.MethodPost, "/mapping/verra-carbon-credit", payload, "tenant-001")
	rr := httptest.NewRecorder()
	mw.Handler(echo).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var forwarded map[string]any
	require.NoError(t, json.Unmarshal(echo.body, &forwarded))

	assert.Equal(t, "VCS-2845", forwarded["code"])
	assert.Equal(t, "Rimba Raya Biodiversity Reserve", forwarded["display_name"])
	assert.Equal(t, "Carbon", forwarded["dimension"])

	// Idempotency key is the vcs_id
	assert.Equal(t, "VCS-2845", req.Header.Get(middleware.HeaderIdempotencyKey))
}

func TestE2E_VerraCarbonCredit_ValidationFails_ZeroMinAmount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "verra-carbon-credit.json")
	mw := newMappingMiddleware(t, def)
	echo := &echoJSONHandler{}

	payload := []byte(`{
		"vcs_id": "VCS-0000",
		"project_name": "Test",
		"vintage_year": 2022,
		"methodology": "VM0001",
		"min_amount": 0
	}`)

	req := requestWithTenant(http.MethodPost, "/mapping/verra-carbon-credit", payload, "tenant-001")
	rr := httptest.NewRecorder()
	mw.Handler(echo).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestE2E_VerraCarbonCredit_ValidationFails_MissingVcsID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "verra-carbon-credit.json")
	mw := newMappingMiddleware(t, def)
	echo := &echoJSONHandler{}

	payload := []byte(`{
		"project_name": "No ID Project",
		"vintage_year": 2022,
		"methodology": "VM0001",
		"min_amount": 5.0
	}`)

	req := requestWithTenant(http.MethodPost, "/mapping/verra-carbon-credit", payload, "tenant-001")
	rr := httptest.NewRecorder()
	mw.Handler(echo).ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ============================================================================
// E2E Test: Non-mapping paths pass through unchanged
// ============================================================================

func TestE2E_NonMappingPath_PassThrough(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	def := loadProtoMapping(t, "bank-x-party-onboarding.json")
	mw := newMappingMiddleware(t, def)

	called := false
	passthrough := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := requestWithTenant(http.MethodPost, "/v1/parties", []byte(`{}`), "tenant-001")
	rr := httptest.NewRecorder()
	mw.Handler(passthrough).ServeHTTP(rr, req)

	assert.True(t, called, "request to non-mapping path should pass through")
	assert.Equal(t, http.StatusOK, rr.Code)
}
