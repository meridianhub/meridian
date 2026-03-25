package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
	"github.com/meridianhub/meridian/shared/platform/events"
)

// TestNewFinancialAccountingService_WithRegistryOption verifies that the WithRegistry option
// is applied correctly during service construction.
func TestNewFinancialAccountingService_WithRegistryOption(t *testing.T) {
	db := &gorm.DB{}
	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	mockRegistry := &stubInstrumentRegistry{}

	svc, err := NewFinancialAccountingService(
		repo, publisher, idempotencySvc, outboxPublisher, outboxRepo,
		WithRegistry(mockRegistry),
	)

	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, mockRegistry, svc.registry, "WithRegistry option should inject the registry")
}

// TestNewFinancialAccountingService_WithInstrumentResolver verifies that the instrument
// resolver option is applied correctly.
func TestNewFinancialAccountingService_WithInstrumentResolver(t *testing.T) {
	db := &gorm.DB{}
	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	resolver := &stubInstrumentResolver{}

	svc, err := NewFinancialAccountingService(
		repo, publisher, idempotencySvc, outboxPublisher, outboxRepo,
		WithInstrumentResolver(resolver),
	)

	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, resolver, svc.instrumentResolver, "WithInstrumentResolver option should inject the resolver")
}

// TestNewFinancialAccountingService_MultipleOptions verifies that multiple options can be
// composed together without conflict.
func TestNewFinancialAccountingService_MultipleOptions(t *testing.T) {
	db := &gorm.DB{}
	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	registry := &stubInstrumentRegistry{}
	resolver := &stubInstrumentResolver{}

	svc, err := NewFinancialAccountingService(
		repo, publisher, idempotencySvc, outboxPublisher, outboxRepo,
		WithRegistry(registry),
		WithInstrumentResolver(resolver),
	)

	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, registry, svc.registry)
	assert.Equal(t, resolver, svc.instrumentResolver)
}

// TestNewFinancialAccountingService_IdempotencyExecutorInitialized verifies that the
// idempotency executor is created as part of service initialization.
func TestNewFinancialAccountingService_IdempotencyExecutorInitialized(t *testing.T) {
	db := &gorm.DB{}
	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	svc, err := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.NotNil(t, svc.idempotencyExecutor, "idempotency executor should be initialized")
}

// stubInstrumentRegistry is a minimal InstrumentRegistry for option injection tests.
type stubInstrumentRegistry struct{}

func (s *stubInstrumentRegistry) GetInstrument(_ context.Context, _ string, _ int) (InstrumentDefinition, error) {
	return nil, nil
}

// stubInstrumentResolver is a minimal refdata.InstrumentResolver for option injection tests.
type stubInstrumentResolver struct{}

func (s *stubInstrumentResolver) Resolve(_ context.Context, code string) (refdata.InstrumentProperties, error) {
	return refdata.InstrumentProperties{Code: code}, nil
}

// Verify stubInstrumentResolver implements idempotency.Service indirectly - just test interface.
var _ idempotency.Service = &mockIdempotencyService{}
