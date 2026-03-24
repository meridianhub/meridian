package messaging

import (
	"context"
	"errors"
	"testing"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	auditdomain "github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/meridianhub/meridian/services/event-router/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// newValidAuditEvent returns an AuditEvent that passes protovalidate checks.
func newValidAuditEvent() *auditv1.AuditEvent {
	return &auditv1.AuditEvent{
		EventId:       "550e8400-e29b-41d4-a716-446655440001",
		SchemaName:    "current_account",
		TableName:     "current_account",
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:      "rec-123",
		ChangedBy:     "test-user",
		CorrelationId: "corr-456",
		Timestamp:     timestamppb.Now(),
		Metadata: map[string]string{
			"tenant_id": "00000000-0000-0000-0000-000000000000",
		},
	}
}

// TestNewPlatformMeteringHandler_Valid verifies successful handler creation without options.
func TestNewPlatformMeteringHandler_Valid(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()

	h, err := NewPlatformMeteringHandler(transformer, mockPK)
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.False(t, h.HasMDSPublisher())
}

// TestNewPlatformMeteringHandler_WithMDSPublisher_SetsFlag verifies the option sets the publisher flag.
func TestNewPlatformMeteringHandler_WithMDSPublisher_SetsFlag(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()
	mockMDS := newMockUtilizationPublisher()

	h, err := NewPlatformMeteringHandler(transformer, mockPK, WithMeteringMDSPublisher(mockMDS))
	require.NoError(t, err)
	assert.True(t, h.HasMDSPublisher())
}

// TestPlatformMeteringHandler_Handle_InvalidEvent verifies proto validation rejects events
// with missing required fields (e.g., no event_id, no schema_name).
func TestPlatformMeteringHandler_Handle_InvalidEvent(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()

	h, err := NewPlatformMeteringHandler(transformer, mockPK)
	require.NoError(t, err)

	// AuditEvent with no required fields set — protovalidate should reject it
	emptyEvent := &auditv1.AuditEvent{}

	handleErr := h.Handle(context.Background(), "audit.events", emptyEvent, nil)

	require.Error(t, handleErr)
	assert.ErrorIs(t, handleErr, ErrInvalidAuditEvent)
	assert.Empty(t, mockPK.getMeasurements())
}

// TestPlatformMeteringHandler_Handle_Success verifies a valid event produces the correct measurement.
func TestPlatformMeteringHandler_Handle_Success(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()

	h, err := NewPlatformMeteringHandler(transformer, mockPK)
	require.NoError(t, err)

	event := newValidAuditEvent()
	handleErr := h.Handle(context.Background(), "audit.events", event, nil)

	require.NoError(t, handleErr)
	measurements := mockPK.getMeasurements()
	require.Len(t, measurements, 1)
	assert.Equal(t, "MERIDIAN-CURRENT-ACCOUNT-OPS", measurements[0].AssetCode)
	assert.Equal(t, "AUDIT_STREAM", measurements[0].Source)
	assert.Equal(t, "current_account", measurements[0].Attributes["service"])
	assert.Equal(t, "INSERT", measurements[0].Attributes["operation"])
}

// TestPlatformMeteringHandler_Handle_PKError verifies error propagation when the PK client fails.
func TestPlatformMeteringHandler_Handle_PKError(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()
	mockPK.recordMeasurementFunc = func(_ context.Context, _ *auditdomain.Measurement) error {
		return errors.New("position keeping service unavailable")
	}

	h, err := NewPlatformMeteringHandler(transformer, mockPK)
	require.NoError(t, err)

	event := newValidAuditEvent()
	handleErr := h.Handle(context.Background(), "audit.events", event, nil)

	require.Error(t, handleErr)
	assert.Contains(t, handleErr.Error(), "failed to record measurement")
}

// TestPlatformMeteringHandler_Handle_WithMDS_ServiceAndOperation verifies dual-output fan-out
// preserves ServiceName and OperationType in the MDS measurement.
func TestPlatformMeteringHandler_Handle_WithMDS_ServiceAndOperation(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()
	mockMDS := newMockUtilizationPublisher()

	h, err := NewPlatformMeteringHandler(transformer, mockPK, WithMeteringMDSPublisher(mockMDS))
	require.NoError(t, err)

	event := newValidAuditEvent()
	handleErr := h.Handle(context.Background(), "audit.events", event, nil)

	require.NoError(t, handleErr)
	assert.Len(t, mockPK.getMeasurements(), 1)
	// MDS measurement carries ServiceName and OperationType from the AuditEvent
	require.Len(t, mockMDS.getMeasurements(), 1)
	mds := mockMDS.getMeasurements()[0]
	assert.Equal(t, "current_account", mds.ServiceName)
	assert.Equal(t, "INSERT", mds.OperationType)
}

// TestPlatformMeteringHandler_Handle_PKError_DoesNotPublishToMDS verifies that when PK fails,
// MDS does not receive the measurement (PK path is the primary gate).
func TestPlatformMeteringHandler_Handle_PKError_DoesNotPublishToMDS(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()
	mockPK.recordMeasurementFunc = func(_ context.Context, _ *auditdomain.Measurement) error {
		return errors.New("pk failure")
	}
	mockMDS := newMockUtilizationPublisher()

	h, err := NewPlatformMeteringHandler(transformer, mockPK, WithMeteringMDSPublisher(mockMDS))
	require.NoError(t, err)

	event := newValidAuditEvent()
	handleErr := h.Handle(context.Background(), "audit.events", event, nil)

	require.Error(t, handleErr)
	// MDS must not receive a measurement when PK failed
	assert.Empty(t, mockMDS.getMeasurements())
}

// TestPlatformMeteringHandler_Handle_MDSPublish_PanicRecovery verifies that a panic inside
// the MDS publish path is recovered and does not propagate to the caller.
func TestPlatformMeteringHandler_Handle_MDSPublish_PanicRecovery(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()
	panicPublisher := &mockUtilizationPublisher{
		publishFunc: func(_ *domain.UtilizationMeasurement) {
			panic("mds publish panic")
		},
	}

	h, err := NewPlatformMeteringHandler(transformer, mockPK, WithMeteringMDSPublisher(panicPublisher))
	require.NoError(t, err)

	event := newValidAuditEvent()
	// A panic inside publishToMDS must not escape the handler
	handleErr := h.Handle(context.Background(), "audit.events", event, nil)

	require.NoError(t, handleErr)
	// PK measurement was recorded despite MDS panic
	assert.Len(t, mockPK.getMeasurements(), 1)
}

// TestPlatformMeteringHandler_Handle_MultipleEvents verifies sequential processing of events.
func TestPlatformMeteringHandler_Handle_MultipleEvents(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()

	h, err := NewPlatformMeteringHandler(transformer, mockPK)
	require.NoError(t, err)

	events := []*auditv1.AuditEvent{
		newValidAuditEvent(),
		{
			EventId:       "550e8400-e29b-41d4-a716-446655440002",
			SchemaName:    "payment_order",
			TableName:     "payment_order",
			Operation:     auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
			RecordId:      "rec-456",
			ChangedBy:     "test-user",
			CorrelationId: "corr-789",
			Timestamp:     timestamppb.Now(),
			Metadata: map[string]string{
				"tenant_id": "11111111-1111-1111-1111-111111111111",
			},
		},
	}

	for _, event := range events {
		require.NoError(t, h.Handle(context.Background(), "audit.events", event, nil))
	}

	assert.Len(t, mockPK.getMeasurements(), 2)
}
