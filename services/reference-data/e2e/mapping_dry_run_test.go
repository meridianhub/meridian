//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	sharedmapping "github.com/meridianhub/meridian/shared/pkg/mapping"
)

// ============================================================================
// JSON example file helpers
// These structs mirror the snake_case JSON format used in the examples/ directory.
// ============================================================================

type exampleFieldTransform struct {
	EnumMapping      *exampleEnumMapping      `json:"enum_mapping,omitempty"`
	DateFormat       string                   `json:"date_format,omitempty"`
	DefaultValue     string                   `json:"default_value,omitempty"`
	AttributeFlatten *exampleAttributeFlatten `json:"attribute_flatten,omitempty"`
	CEL              *exampleCelTransform     `json:"cel,omitempty"`
}

type exampleEnumMapping struct {
	Values           map[string]string `json:"values"`
	Fallback         string            `json:"fallback,omitempty"`
	OutboundFallback string            `json:"outbound_fallback,omitempty"`
}

type exampleAttributeFlatten struct {
	SourceKeys  []string `json:"source_keys"`
	TargetField string   `json:"target_field"`
}

type exampleCelTransform struct {
	InboundCEL  string `json:"inbound_cel,omitempty"`
	OutboundCEL string `json:"outbound_cel,omitempty"`
}

type exampleFieldCorrespondence struct {
	ExternalPath string                 `json:"external_path"`
	InternalPath string                 `json:"internal_path"`
	Transform    *exampleFieldTransform `json:"transform,omitempty"`
}

type exampleComputedField struct {
	TargetPath    string `json:"target_path"`
	CELExpression string `json:"cel_expression"`
}

type exampleIdempotencyConfig struct {
	SourceSelector    string   `json:"source_selector,omitempty"`
	UseContentHash    bool     `json:"use_content_hash,omitempty"`
	ContentHashFields []string `json:"content_hash_fields,omitempty"`
}

type exampleMappingDefinition struct {
	Name                  string                       `json:"name"`
	TargetService         string                       `json:"target_service"`
	TargetRPC             string                       `json:"target_rpc"`
	Version               int                          `json:"version"`
	ExternalSchema        string                       `json:"external_schema,omitempty"`
	InboundValidationCEL  string                       `json:"inbound_validation_cel,omitempty"`
	OutboundValidationCEL string                       `json:"outbound_validation_cel,omitempty"`
	IsBatch               bool                         `json:"is_batch,omitempty"`
	BatchTargetPath       string                       `json:"batch_target_path,omitempty"`
	Fields                []exampleFieldCorrespondence `json:"fields,omitempty"`
	InboundComputed       []exampleComputedField       `json:"inbound_computed_fields,omitempty"`
	OutboundComputed      []exampleComputedField       `json:"outbound_computed_fields,omitempty"`
	Idempotency           *exampleIdempotencyConfig    `json:"idempotency,omitempty"`
}

// loadExampleProtoMapping reads a mapping JSON file from the examples directory
// and converts it to a proto MappingDefinition ready for the mapping engine.
func loadExampleProtoMapping(t *testing.T, filename string) *mappingv1.MappingDefinition {
	t.Helper()

	_, callerFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")

	examplesDir := filepath.Join(filepath.Dir(callerFile), "..", "examples")
	path := filepath.Join(examplesDir, filename)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "reading example file %s", filename)

	var ex exampleMappingDefinition
	require.NoError(t, json.Unmarshal(data, &ex), "unmarshaling %s", filename)

	return exampleToProtoMapping(&ex)
}

func exampleToProtoMapping(ex *exampleMappingDefinition) *mappingv1.MappingDefinition {
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
			pc.Transform = exampleTransformToProto(f.Transform)
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

func exampleTransformToProto(t *exampleFieldTransform) *mappingv1.FieldTransform {
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

// newTestMappingEngine creates a mapping engine for dry run tests.
func newTestMappingEngine(t *testing.T) *sharedmapping.Engine {
	t.Helper()
	eng, err := sharedmapping.NewEngine()
	require.NoError(t, err)
	return eng
}

// ============================================================================
// Dry Run Tests: Bank Party Onboarding
// ============================================================================

func TestDryRun_BankPartyOnboarding_Inbound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "bank-x-party-onboarding.json")

	sampleJSON := []byte(`{
		"party_type": "individual",
		"full_name": "Jane Smith",
		"first_name": "Jane",
		"date_of_birth": "1985-03-15",
		"govt_id": "GB-NI-123456",
		"email": "jane.smith@example.com",
		"phone": "+44 7700 900123"
	}`)

	result := eng.DryRunInbound(def, sampleJSON)

	assert.True(t, result.ValidationPassed, "validation should pass: %v", result.ValidationErrors)
	assert.Empty(t, result.TransformError)
	assert.NotEmpty(t, result.TransformedJSON)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.TransformedJSON), &out))
	assert.Equal(t, "PARTY_TYPE_PERSON", out["party_type"])
	assert.Equal(t, "Jane Smith", out["legal_name"])
	assert.Equal(t, "Jane", out["display_name"])
	assert.Equal(t, "GB-NI-123456", result.IdempotencyKey)

	// Verify traces contain all mapped fields
	assert.NotEmpty(t, result.FieldTraces)
	traceTypes := make(map[string]string)
	for _, tr := range result.FieldTraces {
		traceTypes[tr.SourcePath] = tr.TransformType
	}
	assert.Equal(t, "enum_mapping", traceTypes["party_type"])
	assert.Equal(t, "date_format", traceTypes["date_of_birth"])
}

func TestDryRun_BankPartyOnboarding_Inbound_Corporate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "bank-x-party-onboarding.json")

	sampleJSON := []byte(`{
		"party_type": "corporate",
		"full_name": "Acme Corp Ltd",
		"govt_id": "UK-CRN-12345678"
	}`)

	result := eng.DryRunInbound(def, sampleJSON)

	assert.True(t, result.ValidationPassed)
	assert.Empty(t, result.TransformError)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.TransformedJSON), &out))
	assert.Equal(t, "PARTY_TYPE_ORGANIZATION", out["party_type"])
	assert.Equal(t, "Acme Corp Ltd", out["legal_name"])
	// display_name falls back to full_name when first_name absent
	assert.Equal(t, "Acme Corp Ltd", out["display_name"])
}

func TestDryRun_BankPartyOnboarding_Inbound_ValidationFails_MissingFullName(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "bank-x-party-onboarding.json")

	// Missing required full_name field
	sampleJSON := []byte(`{"party_type": "individual"}`)

	result := eng.DryRunInbound(def, sampleJSON)

	assert.False(t, result.ValidationPassed)
	assert.NotEmpty(t, result.ValidationErrors)
	assert.Empty(t, result.TransformedJSON)
}

func TestDryRun_BankPartyOnboarding_Outbound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "bank-x-party-onboarding.json")

	// Internal proto JSON (what the service would return)
	internalJSON := []byte(`{
		"party_type": "PARTY_TYPE_PERSON",
		"legal_name": "Jane Smith",
		"display_name": "Jane",
		"reference": {"government_id": "GB-NI-123456"},
		"contact": {"email": "jane.smith@example.com"}
	}`)

	result := eng.DryRunOutbound(def, internalJSON)

	assert.True(t, result.ValidationPassed)
	assert.Empty(t, result.TransformError)
	assert.NotEmpty(t, result.TransformedJSON)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.TransformedJSON), &out))
	// Internal field party_type maps to external party_type (via reverse enum)
	assert.Equal(t, "individual", out["party_type"])
}

// ============================================================================
// Dry Run Tests: Energy Metering Feed (Batch)
// ============================================================================

func TestDryRun_EnergyMeteringFeed_Inbound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "energy-metering-feed.json")

	sampleJSON := []byte(`{
		"meter_id": "METER-UK-001",
		"timestamp": "2024-01-15T10:30:00Z",
		"value_kwh": 125.5,
		"quality": "actual",
		"tenor": "1H",
		"settlement_date": "2024-01-16"
	}`)

	result := eng.DryRunInbound(def, sampleJSON)

	assert.True(t, result.ValidationPassed, "validation should pass: %v", result.ValidationErrors)
	assert.Empty(t, result.TransformError)
	assert.NotEmpty(t, result.TransformedJSON)

	// Verify trace includes attribute_flatten transform
	traceTypes := make(map[string]string)
	for _, tr := range result.FieldTraces {
		traceTypes[tr.SourcePath] = tr.TransformType
	}
	assert.Equal(t, "enum_mapping", traceTypes["quality"])
	assert.Equal(t, "date_format", traceTypes["timestamp"])
	assert.Equal(t, "attribute_flatten", traceTypes["attributes"])

	// Content hash should be derived
	assert.NotEmpty(t, result.IdempotencyKey)
}

func TestDryRun_EnergyMeteringFeed_Inbound_ValidationFails_NegativeValue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "energy-metering-feed.json")

	sampleJSON := []byte(`{
		"meter_id": "METER-UK-001",
		"timestamp": "2024-01-15T10:30:00Z",
		"value_kwh": -5.0,
		"quality": "actual"
	}`)

	result := eng.DryRunInbound(def, sampleJSON)

	assert.False(t, result.ValidationPassed)
	assert.NotEmpty(t, result.ValidationErrors)
}

func TestDryRun_EnergyMeteringFeed_Outbound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "energy-metering-feed.json")

	// Internal proto JSON
	internalJSON := []byte(`{
		"source_id": "METER-UK-001",
		"observed_at": "2024-01-15T10:30:00Z",
		"quantity": 125.5,
		"data_quality": "DATA_QUALITY_ACTUAL",
		"resolution_key_value": "METER-UK-001:2024-01-15T10:30:00Z"
	}`)

	result := eng.DryRunOutbound(def, internalJSON)

	assert.True(t, result.ValidationPassed)
	assert.Empty(t, result.TransformError)
	assert.NotEmpty(t, result.TransformedJSON)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.TransformedJSON), &out))
	assert.Equal(t, "METER-UK-001", out["meter_id"])
	// Enum reverse-maps DATA_QUALITY_ACTUAL -> actual
	assert.Equal(t, "actual", out["quality"])
}

// ============================================================================
// Dry Run Tests: Verra Carbon Credit
// ============================================================================

func TestDryRun_VerraCarbonCredit_Inbound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "verra-carbon-credit.json")

	sampleJSON := []byte(`{
		"vcs_id": "VCS-2845",
		"project_name": "Rimba Raya Biodiversity Reserve",
		"vintage_year": 2022,
		"methodology": "VM0009",
		"registry": "Verra",
		"min_amount": 1.0,
		"validation_date": "2023-06-01"
	}`)

	result := eng.DryRunInbound(def, sampleJSON)

	assert.True(t, result.ValidationPassed, "validation should pass: %v", result.ValidationErrors)
	assert.Empty(t, result.TransformError)
	assert.NotEmpty(t, result.TransformedJSON)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.TransformedJSON), &out))
	assert.Equal(t, "VCS-2845", out["code"])
	assert.Equal(t, "Rimba Raya Biodiversity Reserve", out["display_name"])
	assert.Equal(t, "Carbon", out["dimension"])
	assert.Equal(t, "VCS-2845", result.IdempotencyKey)

	// Verify attribute_flatten trace
	traceTypes := make(map[string]string)
	for _, tr := range result.FieldTraces {
		traceTypes[tr.SourcePath] = tr.TransformType
	}
	assert.Equal(t, "attribute_flatten", traceTypes["attributes"])
	assert.Equal(t, "date_format", traceTypes["validation_date"])
}

func TestDryRun_VerraCarbonCredit_Inbound_ValidationFails_ZeroMinAmount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "verra-carbon-credit.json")

	sampleJSON := []byte(`{
		"vcs_id": "VCS-0000",
		"project_name": "Test Project",
		"vintage_year": 2022,
		"methodology": "VM0001",
		"min_amount": 0
	}`)

	result := eng.DryRunInbound(def, sampleJSON)

	assert.False(t, result.ValidationPassed)
	assert.NotEmpty(t, result.ValidationErrors)
}

func TestDryRun_VerraCarbonCredit_Inbound_ValidationFails_MissingVcsID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "verra-carbon-credit.json")

	sampleJSON := []byte(`{
		"project_name": "No ID Project",
		"vintage_year": 2022,
		"methodology": "VM0001",
		"min_amount": 5.0
	}`)

	result := eng.DryRunInbound(def, sampleJSON)

	assert.False(t, result.ValidationPassed)
	assert.NotEmpty(t, result.ValidationErrors)
}

func TestDryRun_VerraCarbonCredit_Outbound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "verra-carbon-credit.json")

	internalJSON := []byte(`{
		"code": "VCS-2845",
		"display_name": "Rimba Raya Biodiversity Reserve",
		"dimension": "Carbon",
		"min_amount": 1.0,
		"validation_date": "2023-06-01T00:00:00Z"
	}`)

	result := eng.DryRunOutbound(def, internalJSON)

	assert.True(t, result.ValidationPassed)
	assert.Empty(t, result.TransformError)
	assert.NotEmpty(t, result.TransformedJSON)

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.TransformedJSON), &out))
	// code maps back to vcs_id
	assert.Equal(t, "VCS-2845", out["vcs_id"])
	assert.Equal(t, "Rimba Raya Biodiversity Reserve", out["project_name"])
}

// ============================================================================
// Dry Run Tests: Execution Time Reporting
// ============================================================================

func TestDryRun_ExecutionTimeIsReported(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "bank-x-party-onboarding.json")

	sampleJSON := []byte(`{
		"party_type": "individual",
		"full_name": "Test User",
		"govt_id": "TEST-001"
	}`)

	result := eng.DryRunInbound(def, sampleJSON)

	// ExecutionTimeMs must be non-negative (may be 0 for very fast runs)
	assert.GreaterOrEqual(t, result.ExecutionTimeMs, int64(0))
}

// ============================================================================
// Dry Run Tests: Invalid JSON
// ============================================================================

func TestDryRun_InvalidJSON_Inbound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	eng := newTestMappingEngine(t)
	def := loadExampleProtoMapping(t, "bank-x-party-onboarding.json")

	// Malformed JSON - DryRunInbound internally calls TransformInbound which
	// returns ErrInvalidJSON; the DryRunResult will have either a transform error
	// or a validation error depending on whether the CEL validation ran.
	result := eng.DryRunInbound(def, []byte(`{not valid json`))

	// The transform must have failed (either validation could not run, or transform errored)
	failed := !result.ValidationPassed || result.TransformError != ""
	assert.True(t, failed, "invalid JSON should produce a failure in validation or transform")
}
