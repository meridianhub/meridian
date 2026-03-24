package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"gorm.io/gorm"
)

// ========== Constructor nil-logger fallback tests ==========

func TestNewOperationalGatewayService_NilLogger_UsesDefault(t *testing.T) {
	svc, err := NewOperationalGatewayService(newMockInstructionRepo(), newMockConnectionRepo(), nil)
	require.NoError(t, err)
	assert.NotNil(t, svc)
	assert.NotNil(t, svc.logger)
}

func TestNewProviderConnectionService_NilLogger_UsesDefault(t *testing.T) {
	svc, err := NewProviderConnectionService(newMockConnectionRepo(), newMockInstructionRepo(), nil)
	require.NoError(t, err)
	assert.NotNil(t, svc)
	assert.NotNil(t, svc.logger)
}

// TestNewProviderConnectionService_NilInstructionRepo verifies ErrInstructionRepoNil when instruction
// repo is nil (distinct from the existing nil-connection-repo test in grpc_service_test.go).
func TestNewProviderConnectionService_NilInstructionRepo(t *testing.T) {
	_, err := NewProviderConnectionService(newMockConnectionRepo(), nil, nil)
	assert.ErrorIs(t, err, ErrInstructionRepoNil)
}

// ========== saveInstructionWithEvent: partial config via event publisher only ==========

// mockEventPublisher satisfies ports.InstructionEventPublisher for partial config tests.
type mockEventPublisher struct{}

func (m *mockEventPublisher) PublishCreated(_ context.Context, _ *gorm.DB, _ *domain.Instruction) error {
	return nil
}

func (m *mockEventPublisher) PublishDispatched(_ context.Context, _ *gorm.DB, _ *domain.Instruction) error {
	return nil
}

func (m *mockEventPublisher) PublishDelivered(_ context.Context, _ *gorm.DB, _ *domain.Instruction) error {
	return nil
}

func (m *mockEventPublisher) PublishAcknowledged(_ context.Context, _ *gorm.DB, _ *domain.Instruction) error {
	return nil
}

func (m *mockEventPublisher) PublishFailed(_ context.Context, _ *gorm.DB, _ *domain.Instruction) error {
	return nil
}

func (m *mockEventPublisher) PublishExpired(_ context.Context, _ *gorm.DB, _ *domain.Instruction) error {
	return nil
}

func (m *mockEventPublisher) PublishCancelled(_ context.Context, _ *gorm.DB, _ *domain.Instruction) error {
	return nil
}

// TestSaveInstructionWithEvent_PartialConfig_PublisherOnly verifies partial config (eventPublisher
// set, db/impl absent) returns ErrEventPublishingPartialConfig. This complements
// TestSaveInstructionWithEvent_PartialConfig (which sets db only).
func TestSaveInstructionWithEvent_PartialConfig_PublisherOnly(t *testing.T) {
	svc, _, _ := newTestOGService(t)

	// Set only eventPublisher — partial config (db and instructionRepoImpl are nil).
	svc.eventPublisher = &mockEventPublisher{}

	tid := uuid.MustParse(testTenantID())
	inst, err := domain.NewInstruction(tid, "device.command", uuid.Nil.String(), map[string]any{"x": 1})
	require.NoError(t, err)

	err = svc.saveInstructionWithEvent(context.Background(), inst, "key-partial", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEventPublishingPartialConfig)
}

// ========== DispatchInstruction financial type coverage ==========

// TestDispatchInstruction_AdditionalFinancialTypes verifies payment.refund, payment.initiate,
// and payment.transfer are all rejected (supplements the payment.collect case in grpc_service_test.go).
func TestDispatchInstruction_AdditionalFinancialTypes(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")
	payload, _ := structpb.NewStruct(map[string]any{"amount": 100.0})

	for _, itype := range []string{"payment.refund", "payment.initiate", "payment.transfer"} {
		t.Run(itype, func(t *testing.T) {
			_, err := svc.DispatchInstruction(ctx, &opgatewayv1.DispatchInstructionRequest{
				InstructionType: itype,
				Payload:         payload,
				IdempotencyKey:  &commonpb.IdempotencyKey{Key: "key-" + itype},
			})
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
			assert.Contains(t, st.Message(), "financial-gateway")
		})
	}
}

// ========== DispatchInstruction optional fields ==========

// TestDispatchInstruction_WithMetadata verifies metadata map is accepted on dispatch.
func TestDispatchInstruction_WithMetadata(t *testing.T) {
	svc, _, _ := newTestOGService(t)
	ctx := tenantContext("test-tenant")
	payload, _ := structpb.NewStruct(map[string]any{"device_id": "dev-001"})

	resp, err := svc.DispatchInstruction(ctx, &opgatewayv1.DispatchInstructionRequest{
		InstructionType: "device.command",
		Payload:         payload,
		IdempotencyKey:  &commonpb.IdempotencyKey{Key: "meta-key"},
		Metadata:        map[string]string{"env": "staging", "region": "eu-west"},
	})

	require.NoError(t, err)
	assert.NotEmpty(t, resp.Instruction.Id)
}
