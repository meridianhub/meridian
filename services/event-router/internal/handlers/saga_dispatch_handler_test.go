package handlers_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/event-router/internal/handlers"
	"github.com/meridianhub/meridian/services/event-router/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// fakeSagaTrigger is a test double for domain.SagaTrigger.
type fakeSagaTrigger struct {
	calls []triggerCall
	err   error
}

type triggerCall struct {
	SagaName       string
	InputData      map[string]any
	IdempotencyKey string
}

func (f *fakeSagaTrigger) TriggerSaga(_ context.Context, sagaName string, inputData map[string]any, idempotencyKey string) (string, error) {
	f.calls = append(f.calls, triggerCall{
		SagaName:       sagaName,
		InputData:      inputData,
		IdempotencyKey: idempotencyKey,
	})
	if f.err != nil {
		return "", f.err
	}
	return "saga-instance-" + sagaName, nil
}

func (f *fakeSagaTrigger) Close() error { return nil }

// newTestEvent creates a simple structpb.Struct as a proto.Message for testing.
func newTestEvent(t *testing.T, fields map[string]any) proto.Message {
	t.Helper()
	s, err := structpb.NewStruct(fields)
	require.NoError(t, err)
	return s
}

func TestSagaDispatchHandler_MatchingFilter(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	filter := `event.account_id == "acct-123"`
	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "test_saga", Trigger: "event:accounts", Filter: &filter, Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger, handlers.WithLogger(slog.Default()))

	event := newTestEvent(t, map[string]any{"account_id": "acct-123"})
	err = h.Handle(context.Background(), "accounts", event, map[string]string{"x-correlation-id": "corr-1"})

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
	assert.Equal(t, "test_saga", trigger.calls[0].SagaName)
	assert.Equal(t, "corr-1", trigger.calls[0].IdempotencyKey)
}

func TestSagaDispatchHandler_NonMatchingFilter(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	filter := `event.account_id == "acct-999"`
	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "test_saga", Trigger: "event:accounts", Filter: &filter, Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger)

	event := newTestEvent(t, map[string]any{"account_id": "acct-123"})
	err = h.Handle(context.Background(), "accounts", event, map[string]string{"x-correlation-id": "corr-1"})

	require.NoError(t, err)
	assert.Empty(t, trigger.calls)
}

func TestSagaDispatchHandler_NilFilter_AlwaysMatches(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "always_saga", Trigger: "event:orders", Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger)

	event := newTestEvent(t, map[string]any{"order_id": "ord-1"})
	err = h.Handle(context.Background(), "orders", event, map[string]string{"x-correlation-id": "corr-2"})

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
	assert.Equal(t, "always_saga", trigger.calls[0].SagaName)
}

func TestSagaDispatchHandler_MultipleSagas_AllMatchingExecute(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	filter1 := `event.amount > 0`
	filter2 := `event.amount > 100`
	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "low_threshold", Trigger: "event:payments", Filter: &filter1, Script: "def run(ctx): pass"},
		{Name: "high_threshold", Trigger: "event:payments", Filter: &filter2, Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger)

	event := newTestEvent(t, map[string]any{"amount": 200.0})
	err = h.Handle(context.Background(), "payments", event, map[string]string{"x-correlation-id": "corr-3"})

	require.NoError(t, err)
	require.Len(t, trigger.calls, 2)
	assert.Equal(t, "low_threshold", trigger.calls[0].SagaName)
	assert.Equal(t, "high_threshold", trigger.calls[1].SagaName)
}

func TestSagaDispatchHandler_MultipleSagas_PartialMatch(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	filter1 := `event.amount > 0`
	filter2 := `event.amount > 500`
	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "low_threshold", Trigger: "event:payments", Filter: &filter1, Script: "def run(ctx): pass"},
		{Name: "high_threshold", Trigger: "event:payments", Filter: &filter2, Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger)

	event := newTestEvent(t, map[string]any{"amount": 200.0})
	err = h.Handle(context.Background(), "payments", event, map[string]string{"x-correlation-id": "corr-4"})

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
	assert.Equal(t, "low_threshold", trigger.calls[0].SagaName)
}

func TestSagaDispatchHandler_NoSagasForChannel(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	// Registry is empty — no sagas registered
	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger)

	event := newTestEvent(t, map[string]any{"id": "1"})
	err = h.Handle(context.Background(), "unknown-channel", event, nil)

	require.NoError(t, err)
	assert.Empty(t, trigger.calls)
}

func TestSagaDispatchHandler_ChainDepthExceeded(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "chain_saga", Trigger: "event:orders", Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger, handlers.WithMaxChainDepth(3))

	event := newTestEvent(t, map[string]any{"order_id": "ord-1"})
	metadata := map[string]string{
		"x-correlation-id": "corr-5",
		"x-chain-depth":    "5",
	}
	err = h.Handle(context.Background(), "orders", event, metadata)

	require.NoError(t, err)
	assert.Empty(t, trigger.calls, "should not trigger saga when chain depth exceeded")
}

func TestSagaDispatchHandler_ChainDepthAtLimit(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "chain_saga", Trigger: "event:orders", Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger, handlers.WithMaxChainDepth(3))

	event := newTestEvent(t, map[string]any{"order_id": "ord-1"})
	metadata := map[string]string{
		"x-correlation-id": "corr-6",
		"x-chain-depth":    "3",
	}
	err = h.Handle(context.Background(), "orders", event, metadata)

	require.NoError(t, err)
	assert.Empty(t, trigger.calls, "should not trigger saga when chain depth equals max")
}

func TestSagaDispatchHandler_ChainDepthBelowLimit(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "chain_saga", Trigger: "event:orders", Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger, handlers.WithMaxChainDepth(3))

	event := newTestEvent(t, map[string]any{"order_id": "ord-1"})
	metadata := map[string]string{
		"x-correlation-id": "corr-7",
		"x-chain-depth":    "2",
	}
	err = h.Handle(context.Background(), "orders", event, metadata)

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
}

func TestSagaDispatchHandler_ChainDepthMissing_DefaultsToZero(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "chain_saga", Trigger: "event:orders", Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger, handlers.WithMaxChainDepth(3))

	event := newTestEvent(t, map[string]any{"order_id": "ord-1"})
	err = h.Handle(context.Background(), "orders", event, map[string]string{"x-correlation-id": "corr-8"})

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
}

func TestSagaDispatchHandler_ChainDepthInvalid_DefaultsToZero(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "chain_saga", Trigger: "event:orders", Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger, handlers.WithMaxChainDepth(3))

	event := newTestEvent(t, map[string]any{"order_id": "ord-1"})
	metadata := map[string]string{
		"x-correlation-id": "corr-9",
		"x-chain-depth":    "not-a-number",
	}
	err = h.Handle(context.Background(), "orders", event, metadata)

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
}

func TestSagaDispatchHandler_CELFilterError_SkipsSaga_ContinuesOthers(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	// First saga has a filter that will error because event.missing_field doesn't exist
	// on a dyn type this won't error at compile time but will at eval time
	filter1 := `event.missing_field.nested == "x"`
	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "error_saga", Trigger: "event:orders", Filter: &filter1, Script: "def run(ctx): pass"},
		{Name: "good_saga", Trigger: "event:orders", Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger)

	event := newTestEvent(t, map[string]any{"order_id": "ord-1"})
	err = h.Handle(context.Background(), "orders", event, map[string]string{"x-correlation-id": "corr-10"})

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
	assert.Equal(t, "good_saga", trigger.calls[0].SagaName)
}

func TestSagaDispatchHandler_TriggerError_ReturnsError(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "fail_saga", Trigger: "event:orders", Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{err: errors.New("trigger failed")}
	h := handlers.NewSagaDispatchHandler(reg, trigger)

	event := newTestEvent(t, map[string]any{"order_id": "ord-1"})
	err = h.Handle(context.Background(), "orders", event, map[string]string{"x-correlation-id": "corr-11"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "trigger failed")
}

func TestSagaDispatchHandler_NilEvent_ReturnsError(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "test_saga", Trigger: "event:orders", Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger)

	err = h.Handle(context.Background(), "orders", nil, map[string]string{"x-correlation-id": "corr-12"})

	require.Error(t, err)
}

func TestSagaDispatchHandler_MetadataFilter(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	filter := `metadata.source == "billing"`
	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "billing_saga", Trigger: "event:payments", Filter: &filter, Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger)

	event := newTestEvent(t, map[string]any{"amount": 100.0})
	err = h.Handle(context.Background(), "payments", event, map[string]string{
		"x-correlation-id": "corr-13",
		"source":           "billing",
	})

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
	assert.Equal(t, "billing_saga", trigger.calls[0].SagaName)
}

func TestSagaDispatchHandler_CorrelationIDFromMetadata(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "test_saga", Trigger: "event:orders", Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger)

	event := newTestEvent(t, map[string]any{"id": "1"})
	err = h.Handle(context.Background(), "orders", event, map[string]string{
		"x-correlation-id": "my-unique-corr-id",
	})

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
	assert.Equal(t, "my-unique-corr-id", trigger.calls[0].IdempotencyKey)
}

func TestSagaDispatchHandler_NoCorrelationID_GeneratesKey(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "test_saga", Trigger: "event:orders", Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger)

	event := newTestEvent(t, map[string]any{"id": "1"})
	err = h.Handle(context.Background(), "orders", event, map[string]string{})

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)
	assert.NotEmpty(t, trigger.calls[0].IdempotencyKey, "should generate an idempotency key when correlation_id missing")
}

func TestSagaDispatchHandler_InputDataContainsEventAndMetadata(t *testing.T) {
	reg, err := registry.NewSagaRegistry()
	require.NoError(t, err)

	require.NoError(t, reg.Reload([]*controlplanev1.SagaDefinition{
		{Name: "test_saga", Trigger: "event:orders", Script: "def run(ctx): pass"},
	}))

	trigger := &fakeSagaTrigger{}
	h := handlers.NewSagaDispatchHandler(reg, trigger)

	event := newTestEvent(t, map[string]any{"order_id": "ord-42"})
	metadata := map[string]string{
		"x-correlation-id": "corr-14",
		"source":           "test",
	}
	err = h.Handle(context.Background(), "orders", event, metadata)

	require.NoError(t, err)
	require.Len(t, trigger.calls, 1)

	inputData := trigger.calls[0].InputData
	assert.Contains(t, inputData, "event")
	assert.Contains(t, inputData, "metadata")
}
