package mapping_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/adapters/mapping"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
	sharedmapping "github.com/meridianhub/meridian/shared/pkg/mapping"
)

// stubResolver is an in-test DefinitionResolver that returns pre-configured definitions.
type stubResolver struct {
	defs map[string]*mappingv1.MappingDefinition
	err  error
}

func (r *stubResolver) Resolve(_ context.Context, name string) (*mappingv1.MappingDefinition, error) {
	if r.err != nil {
		return nil, r.err
	}
	def, ok := r.defs[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrMappingNotFound, name)
	}
	return def, nil
}

// newTestInstruction creates an Instruction with a simple payload for testing.
func newTestInstruction(t *testing.T, payload map[string]any) *domain.Instruction {
	t.Helper()
	inst, err := domain.NewInstruction(
		uuid.New(),
		"payment.create",
		"conn-001",
		payload,
	)
	require.NoError(t, err)
	return inst
}

// newEngine creates a shared mapping Engine for testing.
func newEngine(t *testing.T) *sharedmapping.Engine {
	t.Helper()
	engine, err := sharedmapping.NewEngine()
	require.NoError(t, err)
	return engine
}

// --- TransformOutbound tests ---

func TestTransformer_TransformOutbound_Passthrough_WhenNoMapping(t *testing.T) {
	engine := newEngine(t)
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	payload := map[string]any{"amount": "100.00", "currency": "GBP"}
	inst := newTestInstruction(t, payload)
	route := &ports.InstructionRoute{
		HTTPMethod:      "POST",
		PathTemplate:    "/payments",
		OutboundMapping: "", // no mapping
	}

	body, headers, err := tr.TransformOutbound(context.Background(), inst, route)
	require.NoError(t, err)

	// Body should be the raw JSON of the payload
	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "100.00", got["amount"])
	assert.Equal(t, "GBP", got["currency"])
	assert.Nil(t, headers)
}

func TestTransformer_TransformOutbound_AppliesMapping(t *testing.T) {
	engine := newEngine(t)
	def := &mappingv1.MappingDefinition{
		Name: "outbound-test",
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "payment_amount", InternalPath: "amount"},
			{ExternalPath: "currency_code", InternalPath: "currency"},
		},
	}
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{
		"outbound-test": def,
	}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	payload := map[string]any{"amount": "200.00", "currency": "USD"}
	inst := newTestInstruction(t, payload)
	route := &ports.InstructionRoute{
		OutboundMapping: "outbound-test",
	}

	body, headers, err := tr.TransformOutbound(context.Background(), inst, route)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "200.00", got["payment_amount"])
	assert.Equal(t, "USD", got["currency_code"])
	// Internal fields should be removed after outbound mapping
	assert.Nil(t, got["amount"])
	assert.Nil(t, got["currency"])
	assert.Nil(t, headers)
}

func TestTransformer_TransformOutbound_IncludesStaticHeaders(t *testing.T) {
	engine := newEngine(t)
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	inst := newTestInstruction(t, map[string]any{"x": 1})
	route := &ports.InstructionRoute{
		OutboundMapping: "",
		Headers: map[string]string{
			"X-Provider-Version": "2024-01",
			"Accept":             "application/json",
		},
	}

	_, headers, err := tr.TransformOutbound(context.Background(), inst, route)
	require.NoError(t, err)
	assert.Equal(t, "2024-01", headers["X-Provider-Version"])
	assert.Equal(t, "application/json", headers["Accept"])
}

func TestTransformer_TransformOutbound_ReturnsError_WhenResolverFails(t *testing.T) {
	engine := newEngine(t)
	resolverErr := errors.New("resolver unavailable")
	resolver := &stubResolver{err: resolverErr}
	tr := mapping.NewTransformer(resolver, engine, nil)

	inst := newTestInstruction(t, map[string]any{"x": 1})
	route := &ports.InstructionRoute{OutboundMapping: "some-mapping"}

	_, _, err := tr.TransformOutbound(context.Background(), inst, route)
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrTransformFailed)
	assert.ErrorIs(t, err, resolverErr)
}

func TestTransformer_TransformOutbound_ReturnsError_WhenMappingNotFound(t *testing.T) {
	engine := newEngine(t)
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	inst := newTestInstruction(t, map[string]any{"x": 1})
	route := &ports.InstructionRoute{OutboundMapping: "missing-mapping"}

	_, _, err := tr.TransformOutbound(context.Background(), inst, route)
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrTransformFailed)
	assert.ErrorIs(t, err, ports.ErrMappingNotFound)
}

// --- TransformInbound tests ---

func TestTransformer_TransformInbound_Passthrough_WhenNoMapping_Success(t *testing.T) {
	engine := newEngine(t)
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	route := &ports.InstructionRoute{InboundMapping: ""}
	outcome, err := tr.TransformInbound(context.Background(), 200, []byte(`{"id":"pmt-001"}`), route)
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.Equal(t, "ACCEPTED", outcome.ProviderStatus)
	assert.Equal(t, "", outcome.ExternalID)
	assert.False(t, outcome.ShouldRetry)
	assert.Equal(t, "", outcome.FailureReason)
}

func TestTransformer_TransformInbound_Passthrough_WhenNoMapping_Failure(t *testing.T) {
	engine := newEngine(t)
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	route := &ports.InstructionRoute{InboundMapping: ""}
	outcome, err := tr.TransformInbound(context.Background(), 422, []byte(`{"error":"invalid account"}`), route)
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.Equal(t, "REJECTED", outcome.ProviderStatus)
	assert.Contains(t, outcome.FailureReason, "422")
}

func TestTransformer_TransformInbound_AppliesMapping(t *testing.T) {
	engine := newEngine(t)
	// Map provider fields to outcome fields.
	// provider response: {"payment_id": "pmt-123", "status": "PROCESSED"}
	// expected outcome: {external_id: "pmt-123", provider_status: "PROCESSED"}
	def := &mappingv1.MappingDefinition{
		Name: "inbound-test",
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "payment_id", InternalPath: "external_id"},
			{ExternalPath: "status", InternalPath: "provider_status"},
		},
	}
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{
		"inbound-test": def,
	}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	route := &ports.InstructionRoute{InboundMapping: "inbound-test"}
	body := []byte(`{"payment_id":"pmt-123","status":"PROCESSED"}`)
	outcome, err := tr.TransformInbound(context.Background(), 200, body, route)
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.Equal(t, "pmt-123", outcome.ExternalID)
	assert.Equal(t, "PROCESSED", outcome.ProviderStatus)
	assert.False(t, outcome.ShouldRetry)
	assert.Equal(t, "", outcome.FailureReason)
}

func TestTransformer_TransformInbound_ShouldRetry_FromMapping(t *testing.T) {
	engine := newEngine(t)
	// Map the provider's retry flag to should_retry using a CEL expression.
	def := &mappingv1.MappingDefinition{
		Name: "retry-mapping",
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "ext_id", InternalPath: "external_id"},
		},
		InboundComputedFields: []*mappingv1.ComputedField{
			{
				TargetPath:    "should_retry",
				CelExpression: "input.retryable == true",
			},
		},
	}
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{
		"retry-mapping": def,
	}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	route := &ports.InstructionRoute{InboundMapping: "retry-mapping"}
	body := []byte(`{"ext_id":"pmt-999","retryable":true}`)
	outcome, err := tr.TransformInbound(context.Background(), 429, body, route)
	require.NoError(t, err)
	require.NotNil(t, outcome)
	assert.Equal(t, "pmt-999", outcome.ExternalID)
	assert.True(t, outcome.ShouldRetry)
}

func TestTransformer_TransformInbound_FallsBackToHTTPStatus_WhenNoProviderStatus(t *testing.T) {
	engine := newEngine(t)
	// Mapping that only extracts external_id, no provider_status
	def := &mappingv1.MappingDefinition{
		Name: "id-only-mapping",
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "id", InternalPath: "external_id"},
		},
	}
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{
		"id-only-mapping": def,
	}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	route := &ports.InstructionRoute{InboundMapping: "id-only-mapping"}

	// 201 Created - should be treated as success
	body := []byte(`{"id":"pmt-777"}`)
	outcome, err := tr.TransformInbound(context.Background(), 201, body, route)
	require.NoError(t, err)
	assert.Equal(t, "pmt-777", outcome.ExternalID)
	assert.Equal(t, "ACCEPTED", outcome.ProviderStatus)

	// 400 Bad Request - should be treated as failure
	body400 := []byte(`{"id":"pmt-888"}`)
	outcome400, err := tr.TransformInbound(context.Background(), 400, body400, route)
	require.NoError(t, err)
	assert.Equal(t, "pmt-888", outcome400.ExternalID)
	assert.Equal(t, "REJECTED", outcome400.ProviderStatus)
	assert.Contains(t, outcome400.FailureReason, "400")
}

func TestTransformer_TransformInbound_ReturnsError_WhenResolverFails(t *testing.T) {
	engine := newEngine(t)
	resolverErr := errors.New("grpc unavailable")
	resolver := &stubResolver{err: resolverErr}
	tr := mapping.NewTransformer(resolver, engine, nil)

	route := &ports.InstructionRoute{InboundMapping: "some-mapping"}
	_, err := tr.TransformInbound(context.Background(), 200, []byte(`{}`), route)
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrTransformFailed)
	assert.ErrorIs(t, err, resolverErr)
}

func TestTransformer_TransformInbound_ReturnsError_WhenMappingNotFound(t *testing.T) {
	engine := newEngine(t)
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	route := &ports.InstructionRoute{InboundMapping: "missing"}
	_, err := tr.TransformInbound(context.Background(), 200, []byte(`{}`), route)
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrTransformFailed)
	assert.ErrorIs(t, err, ports.ErrMappingNotFound)
}

// --- Nil guard tests ---

func TestNewTransformer_PanicsOnNilResolver(t *testing.T) {
	engine := newEngine(t)
	assert.Panics(t, func() {
		mapping.NewTransformer(nil, engine, nil)
	})
}

func TestNewTransformer_PanicsOnNilEngine(t *testing.T) {
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{}}
	assert.Panics(t, func() {
		mapping.NewTransformer(resolver, nil, nil)
	})
}

func TestTransformer_TransformOutbound_ReturnsError_WhenInstructionNil(t *testing.T) {
	engine := newEngine(t)
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	route := &ports.InstructionRoute{}
	_, _, err := tr.TransformOutbound(context.Background(), nil, route)
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrTransformFailed)
}

func TestTransformer_TransformOutbound_ReturnsError_WhenRouteNil(t *testing.T) {
	engine := newEngine(t)
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	inst := newTestInstruction(t, map[string]any{"x": 1})
	_, _, err := tr.TransformOutbound(context.Background(), inst, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrTransformFailed)
}

func TestTransformer_TransformInbound_ReturnsError_WhenRouteNil(t *testing.T) {
	engine := newEngine(t)
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	_, err := tr.TransformInbound(context.Background(), 200, []byte(`{}`), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrTransformFailed)
}

// --- Interface compliance ---

func TestTransformer_ImplementsPayloadTransformer(t *testing.T) {
	engine := newEngine(t)
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{}}
	var _ ports.PayloadTransformer = mapping.NewTransformer(resolver, engine, nil)
}

// --- Benchmark ---

func BenchmarkTransformer_TransformOutbound(b *testing.B) {
	engine, err := sharedmapping.NewEngine()
	if err != nil {
		b.Fatal(err)
	}
	def := &mappingv1.MappingDefinition{
		Name: "bench-outbound",
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "payment_amount", InternalPath: "amount"},
			{ExternalPath: "payment_currency", InternalPath: "currency"},
			{ExternalPath: "beneficiary_account", InternalPath: "destination_account"},
		},
	}
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{"bench-outbound": def}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	inst2, err := domain.NewInstruction(uuid.New(), "payment.create", "conn-001", map[string]any{
		"amount":              "500.00",
		"currency":            "GBP",
		"destination_account": "GB29NWBK60161331926819",
	})
	if err != nil {
		b.Fatal(err)
	}

	route := &ports.InstructionRoute{OutboundMapping: "bench-outbound"}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := tr.TransformOutbound(ctx, inst2, route)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTransformer_TransformInbound(b *testing.B) {
	engine, err := sharedmapping.NewEngine()
	if err != nil {
		b.Fatal(err)
	}
	def := &mappingv1.MappingDefinition{
		Name: "bench-inbound",
		Fields: []*mappingv1.FieldCorrespondence{
			{ExternalPath: "payment_id", InternalPath: "external_id"},
			{ExternalPath: "status", InternalPath: "provider_status"},
		},
	}
	resolver := &stubResolver{defs: map[string]*mappingv1.MappingDefinition{"bench-inbound": def}}
	tr := mapping.NewTransformer(resolver, engine, nil)

	body := []byte(`{"payment_id":"pmt-` + fmt.Sprintf("%d", time.Now().UnixNano()) + `","status":"ACCEPTED"}`)
	route := &ports.InstructionRoute{InboundMapping: "bench-inbound"}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := tr.TransformInbound(ctx, 200, body, route)
		if err != nil {
			b.Fatal(err)
		}
	}
}
