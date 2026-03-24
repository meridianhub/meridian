package domain_test

import (
	"context"
	"errors"
	"testing"

	"github.com/meridianhub/meridian/services/event-router/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// fakeEventHandler is a test double implementing domain.EventHandler.
type fakeEventHandler struct {
	calls []handleCall
	err   error
}

type handleCall struct {
	Channel  string
	Metadata map[string]string
}

func (f *fakeEventHandler) Handle(_ context.Context, channel string, _ proto.Message, metadata map[string]string) error {
	f.calls = append(f.calls, handleCall{Channel: channel, Metadata: metadata})
	return f.err
}

// Verify fakeEventHandler satisfies the domain.EventHandler interface at compile time.
var _ domain.EventHandler = (*fakeEventHandler)(nil)

// TestEventHandler_InterfaceCompliance verifies that any struct implementing Handle satisfies EventHandler.
func TestEventHandler_InterfaceCompliance(t *testing.T) {
	var h domain.EventHandler = &fakeEventHandler{}
	assert.NotNil(t, h)
}

// TestEventHandler_Handle_RoutesChannelAndMetadata verifies channel and metadata are passed through.
func TestEventHandler_Handle_RoutesChannelAndMetadata(t *testing.T) {
	handler := &fakeEventHandler{}

	event, err := structpb.NewStruct(map[string]any{"key": "value"})
	require.NoError(t, err)

	metadata := map[string]string{"x-correlation-id": "corr-123", "tenant_id": "t-1"}
	callErr := handler.Handle(context.Background(), "accounts.created", event, metadata)

	require.NoError(t, callErr)
	require.Len(t, handler.calls, 1)
	assert.Equal(t, "accounts.created", handler.calls[0].Channel)
	assert.Equal(t, "corr-123", handler.calls[0].Metadata["x-correlation-id"])
}

// TestEventHandler_Handle_ErrorPropagation verifies errors returned by Handle are propagated.
func TestEventHandler_Handle_ErrorPropagation(t *testing.T) {
	expectedErr := errors.New("handler failed")
	handler := &fakeEventHandler{err: expectedErr}

	event, err := structpb.NewStruct(map[string]any{})
	require.NoError(t, err)

	callErr := handler.Handle(context.Background(), "test.channel", event, nil)

	require.Error(t, callErr)
	assert.ErrorIs(t, callErr, expectedErr)
}

// TestEventHandler_Handle_MultipleChannels verifies handler can be called for different channels.
func TestEventHandler_Handle_MultipleChannels(t *testing.T) {
	handler := &fakeEventHandler{}
	channels := []string{"accounts.created", "payments.processed", "positions.updated"}

	event, err := structpb.NewStruct(map[string]any{"id": "1"})
	require.NoError(t, err)

	for _, ch := range channels {
		callErr := handler.Handle(context.Background(), ch, event, nil)
		require.NoError(t, callErr)
	}

	require.Len(t, handler.calls, 3)
	for i, ch := range channels {
		assert.Equal(t, ch, handler.calls[i].Channel)
	}
}

// TestEventHandler_Handle_NilMetadata verifies handler accepts nil metadata gracefully.
func TestEventHandler_Handle_NilMetadata(t *testing.T) {
	handler := &fakeEventHandler{}

	event, err := structpb.NewStruct(map[string]any{})
	require.NoError(t, err)

	callErr := handler.Handle(context.Background(), "test.channel", event, nil)

	require.NoError(t, callErr)
	require.Len(t, handler.calls, 1)
	assert.Nil(t, handler.calls[0].Metadata)
}
