package messaging

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/google/uuid"
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	auditdomain "github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/meridianhub/meridian/services/event-router/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var errPKUnavailable = errors.New("position keeping service unavailable")

// newTestTransformer creates a transformer with a test tenant account map
func newTestTransformer() *auditdomain.AuditEventTransformer {
	tenantZeroID := uuid.MustParse("00000000-0000-0000-0000-000000000000")
	tenantTestID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tenantAccountMap := map[uuid.UUID]uuid.UUID{
		tenantZeroID: tenantZeroID,
		tenantTestID: tenantTestID,
	}
	return auditdomain.NewAuditEventTransformer(tenantAccountMap)
}

// mockPositionKeepingClient implements domain.PositionKeepingClient for testing
type mockPositionKeepingClient struct {
	recordMeasurementFunc func(ctx context.Context, measurement *auditdomain.Measurement) error
	closeFunc             func() error
	measurements          []*auditdomain.Measurement // Store all recorded measurements
	mu                    sync.Mutex                 // Protects measurements slice
}

func newMockPositionKeepingClient() *mockPositionKeepingClient {
	return &mockPositionKeepingClient{
		measurements: make([]*auditdomain.Measurement, 0),
		recordMeasurementFunc: func(_ context.Context, _ *auditdomain.Measurement) error {
			return nil
		},
		closeFunc: func() error {
			return nil
		},
	}
}

func (m *mockPositionKeepingClient) RecordMeasurement(ctx context.Context, measurement *auditdomain.Measurement) error {
	m.mu.Lock()
	m.measurements = append(m.measurements, measurement)
	m.mu.Unlock()
	if m.recordMeasurementFunc != nil {
		return m.recordMeasurementFunc(ctx, measurement)
	}
	return nil
}

func (m *mockPositionKeepingClient) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func (m *mockPositionKeepingClient) getMeasurements() []*auditdomain.Measurement {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*auditdomain.Measurement, len(m.measurements))
	copy(result, m.measurements)
	return result
}

// mockUtilizationPublisher implements domain.UtilizationPublisher for testing
type mockUtilizationPublisher struct {
	mu           sync.Mutex
	measurements []*domain.UtilizationMeasurement
	publishFunc  func(m *domain.UtilizationMeasurement)
}

func newMockUtilizationPublisher() *mockUtilizationPublisher {
	return &mockUtilizationPublisher{
		measurements: make([]*domain.UtilizationMeasurement, 0),
	}
}

func (m *mockUtilizationPublisher) Publish(measurement *domain.UtilizationMeasurement) {
	m.mu.Lock()
	m.measurements = append(m.measurements, measurement)
	m.mu.Unlock()
	if m.publishFunc != nil {
		m.publishFunc(measurement)
	}
}

func (m *mockUtilizationPublisher) Stop() {
	// No-op for testing
}

func (m *mockUtilizationPublisher) getMeasurements() []*domain.UtilizationMeasurement {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*domain.UtilizationMeasurement, len(m.measurements))
	copy(result, m.measurements)
	return result
}

func TestNewAuditConsumer(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()

	tests := []struct {
		name        string
		config      kafka.ConsumerConfig
		transformer *auditdomain.AuditEventTransformer
		pkClient    domain.PositionKeepingClient
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config",
			config: kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
				ClientID:         "test-consumer",
			},
			transformer: transformer,
			pkClient:    mockPK,
			wantErr:     false,
		},
		{
			name: "nil transformer",
			config: kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
			},
			transformer: nil,
			pkClient:    mockPK,
			wantErr:     true,
			errContains: "transformer cannot be nil",
		},
		{
			name: "nil position keeping client",
			config: kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
			},
			transformer: transformer,
			pkClient:    nil,
			wantErr:     true,
			errContains: "position keeping client cannot be nil",
		},
		{
			name: "missing bootstrap servers",
			config: kafka.ConsumerConfig{
				GroupID: "test-group",
			},
			transformer: transformer,
			pkClient:    mockPK,
			wantErr:     true,
		},
		{
			name: "missing group ID",
			config: kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
			},
			transformer: transformer,
			pkClient:    mockPK,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			consumer, err := NewAuditConsumer(tt.config, tt.transformer, tt.pkClient)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			if consumer != nil {
				defer func() {
					_ = consumer.Close()
				}()
			}
		})
	}
}

func TestNewAuditConsumer_WithMDSPublisher(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()
	mockMDS := newMockUtilizationPublisher()

	consumer, err := NewAuditConsumer(
		kafka.ConsumerConfig{
			BootstrapServers: "localhost:9092",
			GroupID:          "test-group",
		},
		transformer,
		mockPK,
		WithMDSPublisher(mockMDS),
	)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	assert.True(t, consumer.handler.HasMDSPublisher(), "MDS publisher should be set via option")
}

func TestAuditConsumer_handleAuditEvent_ValidEvent(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()

	consumer, err := NewAuditConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, transformer, mockPK)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &auditv1.AuditEvent{
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

	ctx := context.Background()
	err = consumer.handler.Handle(ctx, "audit.events", event, nil)

	require.NoError(t, err)

	// Use await to check measurements were recorded
	err = await.New().AtMost(1 * time.Second).Until(func() bool {
		return len(mockPK.getMeasurements()) > 0
	})
	require.NoError(t, err, "Measurement should be recorded")

	measurements := mockPK.getMeasurements()
	require.Len(t, measurements, 1)
	// Check that measurement has correct fields from the new domain model
	assert.Equal(t, "MERIDIAN-CURRENT-ACCOUNT-OPS", measurements[0].AssetCode)
	assert.Equal(t, "AUDIT_STREAM", measurements[0].Source)
	assert.Equal(t, "current_account", measurements[0].Attributes["service"])
	assert.Equal(t, "INSERT", measurements[0].Attributes["operation"])
}

func TestAuditConsumer_handleAuditEvent_InvalidProto(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()

	consumer, err := NewAuditConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, transformer, mockPK)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	// Create an event that will fail validation (missing required fields)
	event := &auditv1.AuditEvent{
		// Missing required fields like event_id, schema_name, etc.
	}

	ctx := context.Background()
	err = consumer.handler.Handle(ctx, "audit.events", event, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid audit event")
	assert.Empty(t, mockPK.getMeasurements(), "No measurements should be recorded for invalid events")
}

func TestAuditConsumer_handleAuditEvent_PositionKeepingError(t *testing.T) {
	transformer := newTestTransformer()

	mockPK := newMockPositionKeepingClient()
	mockPK.recordMeasurementFunc = func(_ context.Context, _ *auditdomain.Measurement) error {
		return errPKUnavailable
	}

	consumer, err := NewAuditConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, transformer, mockPK)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &auditv1.AuditEvent{
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

	ctx := context.Background()
	err = consumer.handler.Handle(ctx, "audit.events", event, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to record measurement")
}

func TestAuditConsumer_handleAuditEvent_AllServiceTypes(t *testing.T) {
	tests := []struct {
		service string
		eventID string
	}{
		{"current_account", "550e8400-e29b-41d4-a716-446655440010"},
		{"financial_accounting", "550e8400-e29b-41d4-a716-446655440011"},
		{"position_keeping", "550e8400-e29b-41d4-a716-446655440012"},
		{"party", "550e8400-e29b-41d4-a716-446655440013"},
		{"payment_order", "550e8400-e29b-41d4-a716-446655440014"},
		{"tenant", "550e8400-e29b-41d4-a716-446655440015"},
	}

	for _, tt := range tests {
		t.Run(tt.service, func(t *testing.T) {
			transformer := newTestTransformer()
			mockPK := newMockPositionKeepingClient()

			consumer, err := NewAuditConsumer(kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
			}, transformer, mockPK)
			if err != nil {
				t.Skip("Kafka not available, skipping integration test")
			}
			defer func() {
				_ = consumer.Close()
			}()

			event := &auditv1.AuditEvent{
				EventId:       tt.eventID,
				SchemaName:    tt.service,
				TableName:     tt.service,
				Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				RecordId:      "rec-123",
				ChangedBy:     "test-user",
				CorrelationId: "corr-456",
				Timestamp:     timestamppb.Now(),
				Metadata: map[string]string{
					"tenant_id": "11111111-1111-1111-1111-111111111111",
				},
			}

			ctx := context.Background()
			err = consumer.handler.Handle(ctx, "audit.events", event, nil)

			require.NoError(t, err)
			require.Len(t, mockPK.getMeasurements(), 1)
			assert.Equal(t, tt.service, mockPK.getMeasurements()[0].Attributes["service"])
		})
	}
}

func TestAuditConsumer_handleAuditEvent_AllOperationTypes(t *testing.T) {
	operations := []auditv1.AuditOperation{
		auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
		auditv1.AuditOperation_AUDIT_OPERATION_DELETE,
	}

	for _, op := range operations {
		t.Run(op.String(), func(t *testing.T) {
			transformer := newTestTransformer()
			mockPK := newMockPositionKeepingClient()

			consumer, err := NewAuditConsumer(kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
			}, transformer, mockPK)
			if err != nil {
				t.Skip("Kafka not available, skipping integration test")
			}
			defer func() {
				_ = consumer.Close()
			}()

			event := &auditv1.AuditEvent{
				EventId:       "550e8400-e29b-41d4-a716-446655440002",
				SchemaName:    "current_account",
				TableName:     "current_account",
				Operation:     op,
				RecordId:      "rec-123",
				ChangedBy:     "test-user",
				CorrelationId: "corr-456",
				Timestamp:     timestamppb.Now(),
				Metadata: map[string]string{
					"tenant_id": "11111111-1111-1111-1111-111111111111",
				},
			}

			ctx := context.Background()
			err = consumer.handler.Handle(ctx, "audit.events", event, nil)

			require.NoError(t, err)
			require.Len(t, mockPK.getMeasurements(), 1)
			// ProtoToOperation converts the proto enum to uppercase string (e.g., "INSERT", "UPDATE")
			expectedOp := auditdomain.ProtoToOperation(op)
			if expectedOp != "" {
				assert.Equal(t, expectedOp, mockPK.getMeasurements()[0].Attributes["operation"])
			}
		})
	}
}

func TestAuditConsumer_handleAuditEvent_ContextCancellation(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()
	mockPK.recordMeasurementFunc = func(ctx context.Context, _ *auditdomain.Measurement) error {
		// Simulate slow operation that respects context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return nil
		}
	}

	consumer, err := NewAuditConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, transformer, mockPK)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &auditv1.AuditEvent{
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

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = consumer.handler.Handle(ctx, "audit.events", event, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to record measurement")
}

func TestAuditConsumer_Start(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping Kafka integration test in short mode")
	}

	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()

	consumer, err := NewAuditConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, transformer, mockPK)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	topics := []string{
		"audit.events.current-account.v1",
		"audit.events.financial-accounting.v1",
		"audit.events.position-keeping.v1",
		"audit.events.party.v1",
		"audit.events.payment-order.v1",
		"audit.events.tenant.v1",
	}

	// Start in a goroutine with timeout since Start blocks
	done := make(chan error, 1)
	go func() {
		done <- consumer.Start(topics)
	}()

	// Wait briefly then close - this test just validates Start doesn't panic
	select {
	case err := <-done:
		if err != nil {
			t.Logf("Start returned error (expected if Kafka unavailable): %v", err)
		}
	case <-time.After(2 * time.Second):
		// Expected case - Start is blocking, which is correct behavior
		// Explicitly close to trigger shutdown (defer also closes, but be explicit)
		t.Log("Start is blocking as expected, closing consumer")
		_ = consumer.Close()
	}
}

func TestAuditConsumer_Close(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping Kafka integration test in short mode")
	}

	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()

	consumer, err := NewAuditConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, transformer, mockPK)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}

	err = consumer.Close()
	// Close should not return an error in normal circumstances
	if err != nil {
		t.Logf("Close returned error: %v", err)
	}
}

// TestProtoValidation tests that protovalidate is properly integrated
func TestProtoValidation(t *testing.T) {
	validator, err := protovalidate.New()
	require.NoError(t, err)

	tests := []struct {
		name    string
		event   *auditv1.AuditEvent
		wantErr bool
	}{
		{
			name: "valid event",
			event: &auditv1.AuditEvent{
				EventId:       "550e8400-e29b-41d4-a716-446655440003",
				SchemaName:    "current_account",
				TableName:     "current_account",
				Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				RecordId:      "rec-123",
				ChangedBy:     "test-user",
				CorrelationId: "corr-456",
				Timestamp:     timestamppb.Now(),
			},
			wantErr: false,
		},
		{
			name:  "missing required fields",
			event: &auditv1.AuditEvent{
				// Missing event_id, schema_name, etc.
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.Validate(tt.event)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// --- Dual-output integration tests ---

func TestAuditConsumer_DualOutput_BothReceiveCalls(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()
	mockMDS := newMockUtilizationPublisher()

	consumer, err := NewAuditConsumer(
		kafka.ConsumerConfig{
			BootstrapServers: "localhost:9092",
			GroupID:          "test-group",
		},
		transformer,
		mockPK,
		WithMDSPublisher(mockMDS),
	)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &auditv1.AuditEvent{
		EventId:       "550e8400-e29b-41d4-a716-446655440020",
		SchemaName:    "current_account",
		TableName:     "current_account",
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:      "rec-dual-1",
		ChangedBy:     "test-user",
		CorrelationId: "corr-dual-1",
		Timestamp:     timestamppb.Now(),
		Metadata: map[string]string{
			"tenant_id": "00000000-0000-0000-0000-000000000000",
		},
	}

	ctx := context.Background()
	err = consumer.handler.Handle(ctx, "audit.events", event, nil)
	require.NoError(t, err)

	// Verify PK received the measurement
	err = await.New().AtMost(1 * time.Second).Until(func() bool {
		return len(mockPK.getMeasurements()) > 0
	})
	require.NoError(t, err, "PK should receive measurement")

	pkMeasurements := mockPK.getMeasurements()
	require.Len(t, pkMeasurements, 1)
	assert.Equal(t, "MERIDIAN-CURRENT-ACCOUNT-OPS", pkMeasurements[0].AssetCode)

	// Verify MDS received the measurement
	err = await.New().AtMost(1 * time.Second).Until(func() bool {
		return len(mockMDS.getMeasurements()) > 0
	})
	require.NoError(t, err, "MDS should receive measurement")

	mdsMeasurements := mockMDS.getMeasurements()
	require.Len(t, mdsMeasurements, 1)
	assert.Equal(t, "current_account", mdsMeasurements[0].ServiceName)
	assert.Equal(t, "INSERT", mdsMeasurements[0].OperationType)
	assert.Equal(t, "00000000-0000-0000-0000-000000000000", mdsMeasurements[0].TenantID)
}

func TestAuditConsumer_DualOutput_PKFailurePreventsOnlyMDSCall(t *testing.T) {
	transformer := newTestTransformer()

	mockPK := newMockPositionKeepingClient()
	mockPK.recordMeasurementFunc = func(_ context.Context, _ *auditdomain.Measurement) error {
		return errPKUnavailable
	}

	mockMDS := newMockUtilizationPublisher()

	consumer, err := NewAuditConsumer(
		kafka.ConsumerConfig{
			BootstrapServers: "localhost:9092",
			GroupID:          "test-group",
		},
		transformer,
		mockPK,
		WithMDSPublisher(mockMDS),
	)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &auditv1.AuditEvent{
		EventId:       "550e8400-e29b-41d4-a716-446655440021",
		SchemaName:    "current_account",
		TableName:     "current_account",
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:      "rec-pk-fail",
		ChangedBy:     "test-user",
		CorrelationId: "corr-pk-fail",
		Timestamp:     timestamppb.Now(),
		Metadata: map[string]string{
			"tenant_id": "00000000-0000-0000-0000-000000000000",
		},
	}

	ctx := context.Background()
	err = consumer.handler.Handle(ctx, "audit.events", event, nil)

	require.Error(t, err, "PK failure should short-circuit")
	assert.Contains(t, err.Error(), "failed to record measurement")

	// MDS should NOT receive the measurement since PK failed first
	assert.Empty(t, mockMDS.getMeasurements(), "MDS should not receive measurement when PK fails")
}

func TestAuditConsumer_DualOutput_MDSFailureDoesNotBlockPK(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()

	mockMDS := newMockUtilizationPublisher()
	mockMDS.publishFunc = func(_ *domain.UtilizationMeasurement) {
		panic("MDS publisher panicked")
	}

	consumer, err := NewAuditConsumer(
		kafka.ConsumerConfig{
			BootstrapServers: "localhost:9092",
			GroupID:          "test-group",
		},
		transformer,
		mockPK,
		WithMDSPublisher(mockMDS),
	)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &auditv1.AuditEvent{
		EventId:       "550e8400-e29b-41d4-a716-446655440022",
		SchemaName:    "current_account",
		TableName:     "current_account",
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:      "rec-mds-fail",
		ChangedBy:     "test-user",
		CorrelationId: "corr-mds-fail",
		Timestamp:     timestamppb.Now(),
		Metadata: map[string]string{
			"tenant_id": "00000000-0000-0000-0000-000000000000",
		},
	}

	ctx := context.Background()
	err = consumer.handler.Handle(ctx, "audit.events", event, nil)

	// Should succeed - MDS panic should NOT block the handler
	require.NoError(t, err, "MDS failure should not block PK path")

	// PK should still have received the measurement
	err = await.New().AtMost(1 * time.Second).Until(func() bool {
		return len(mockPK.getMeasurements()) > 0
	})
	require.NoError(t, err, "PK should receive measurement despite MDS failure")
	assert.Len(t, mockPK.getMeasurements(), 1)
}

func TestAuditConsumer_DualOutput_WithoutMDSPublisher(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()

	// No MDS publisher option - backward compatible
	consumer, err := NewAuditConsumer(
		kafka.ConsumerConfig{
			BootstrapServers: "localhost:9092",
			GroupID:          "test-group",
		},
		transformer,
		mockPK,
	)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	event := &auditv1.AuditEvent{
		EventId:       "550e8400-e29b-41d4-a716-446655440023",
		SchemaName:    "current_account",
		TableName:     "current_account",
		Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:      "rec-no-mds",
		ChangedBy:     "test-user",
		CorrelationId: "corr-no-mds",
		Timestamp:     timestamppb.Now(),
		Metadata: map[string]string{
			"tenant_id": "00000000-0000-0000-0000-000000000000",
		},
	}

	ctx := context.Background()
	err = consumer.handler.Handle(ctx, "audit.events", event, nil)

	require.NoError(t, err, "Should work fine without MDS publisher")

	// PK should still receive measurement
	err = await.New().AtMost(1 * time.Second).Until(func() bool {
		return len(mockPK.getMeasurements()) > 0
	})
	require.NoError(t, err)
	assert.Len(t, mockPK.getMeasurements(), 1)
}

func TestAuditConsumer_DualOutput_MultipleEvents(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()
	mockMDS := newMockUtilizationPublisher()

	consumer, err := NewAuditConsumer(
		kafka.ConsumerConfig{
			BootstrapServers: "localhost:9092",
			GroupID:          "test-group",
		},
		transformer,
		mockPK,
		WithMDSPublisher(mockMDS),
	)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	eventCount := 10
	ctx := context.Background()

	for i := range eventCount {
		event := &auditv1.AuditEvent{
			EventId:       uuid.New().String(),
			SchemaName:    "current_account",
			TableName:     "current_account",
			Operation:     auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			RecordId:      fmt.Sprintf("rec-multi-%d", i),
			ChangedBy:     "test-user",
			CorrelationId: fmt.Sprintf("corr-multi-%d", i),
			Timestamp:     timestamppb.Now(),
			Metadata: map[string]string{
				"tenant_id": "00000000-0000-0000-0000-000000000000",
			},
		}
		err = consumer.handler.Handle(ctx, "audit.events", event, nil)
		require.NoError(t, err, "Event %d should succeed", i)
	}

	// Both outputs should receive all events
	err = await.New().AtMost(2 * time.Second).Until(func() bool {
		return len(mockPK.getMeasurements()) == eventCount &&
			len(mockMDS.getMeasurements()) == eventCount
	})
	require.NoError(t, err, "Both PK and MDS should receive all %d events", eventCount)
}

// TestPlatformMeteringHandler_ImplementsEventHandler verifies the interface contract.
func TestPlatformMeteringHandler_ImplementsEventHandler(t *testing.T) {
	transformer := newTestTransformer()
	mockPK := newMockPositionKeepingClient()

	handler, err := NewPlatformMeteringHandler(transformer, mockPK)
	require.NoError(t, err)

	// Verify it implements domain.EventHandler
	var _ domain.EventHandler = handler
}
