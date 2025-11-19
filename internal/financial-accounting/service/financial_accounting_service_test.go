package service

import (
	"context"
	"testing"
	"time"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/internal/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/pkg/platform/idempotency"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// mockEventPublisher is a test double for EventPublisher
type mockEventPublisher struct{}

func (m *mockEventPublisher) Publish(_ context.Context, _ DomainEvent) error {
	return nil
}

func (m *mockEventPublisher) PublishBatch(_ context.Context, _ []DomainEvent) error {
	return nil
}

// TestNewFinancialAccountingService verifies the constructor creates a valid service instance.
func TestNewFinancialAccountingService(t *testing.T) {
	// Arrange
	repo := persistence.NewLedgerRepository(&gorm.DB{})
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}

	// Act
	service := NewFinancialAccountingService(repo, publisher, idempotencySvc)

	// Assert
	assert.NotNil(t, service, "Service should not be nil")
	assert.NotNil(t, service.repository, "Repository should be injected")
	assert.NotNil(t, service.eventPublisher, "Event publisher should be injected")
	assert.NotNil(t, service.idempotency, "Idempotency service should be injected")
}

// TestFinancialAccountingService_ImplementsInterface verifies the service implements the gRPC interface.
func TestFinancialAccountingService_ImplementsInterface(_ *testing.T) {
	// Arrange
	repo := persistence.NewLedgerRepository(&gorm.DB{})
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}

	// Act
	service := NewFinancialAccountingService(repo, publisher, idempotencySvc)

	// Assert - compile-time check that service implements the interface
	var _ financialaccountingv1.FinancialAccountingServiceServer = service
}

// TestNewFinancialAccountingService_DefensiveTests verifies nil dependency validation per ADR-0008.
// Rationale: Financial services must validate all dependencies to prevent runtime panics
// that could cause service outages or data corruption.
func TestNewFinancialAccountingService_DefensiveTests(t *testing.T) {
	tests := []struct {
		name           string
		repository     *persistence.LedgerRepository
		eventPub       EventPublisher
		idempotencySvc idempotency.Service
		shouldPanic    bool
		rationale      string
	}{
		// Happy path - covered by TestNewFinancialAccountingService
		{
			name:           "valid dependencies",
			repository:     persistence.NewLedgerRepository(&gorm.DB{}),
			eventPub:       &mockEventPublisher{},
			idempotencySvc: &mockIdempotencyService{},
			shouldPanic:    false,
			rationale:      "Standard valid initialization with all dependencies",
		},

		// Unhappy paths - nil dependencies (ADR-0008 mandatory tests)
		{
			name:           "nil repository",
			repository:     nil,
			eventPub:       &mockEventPublisher{},
			idempotencySvc: &mockIdempotencyService{},
			shouldPanic:    true,
			rationale:      "Repository is essential - nil would cause panic on first use",
		},
		{
			name:           "nil event publisher",
			repository:     persistence.NewLedgerRepository(&gorm.DB{}),
			eventPub:       nil,
			idempotencySvc: &mockIdempotencyService{},
			shouldPanic:    true,
			rationale:      "Event publisher is essential - nil would cause panic when publishing events",
		},
		{
			name:           "nil idempotency service",
			repository:     persistence.NewLedgerRepository(&gorm.DB{}),
			eventPub:       &mockEventPublisher{},
			idempotencySvc: nil,
			shouldPanic:    true,
			rationale:      "Idempotency service is essential - nil would cause panic on idempotent operations",
		},

		// Edge case - multiple nil dependencies
		{
			name:           "all dependencies nil",
			repository:     nil,
			eventPub:       nil,
			idempotencySvc: nil,
			shouldPanic:    true,
			rationale:      "Should panic on first nil check (repository)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.shouldPanic {
				assert.Panics(t, func() {
					NewFinancialAccountingService(tt.repository, tt.eventPub, tt.idempotencySvc)
				}, tt.rationale)
			} else {
				assert.NotPanics(t, func() {
					service := NewFinancialAccountingService(tt.repository, tt.eventPub, tt.idempotencySvc)
					assert.NotNil(t, service, tt.rationale)
					assert.NotNil(t, service.repository, "Repository should be injected")
					assert.NotNil(t, service.eventPublisher, "Event publisher should be injected")
					assert.NotNil(t, service.idempotency, "Idempotency service should be injected")
				}, tt.rationale)
			}
		})
	}
}

// mockIdempotencyService is a test double for idempotency.Service
type mockIdempotencyService struct{}

// Checker interface methods
func (m *mockIdempotencyService) Check(_ context.Context, _ idempotency.Key) (*idempotency.Result, error) {
	return nil, idempotency.ErrResultNotFound
}

func (m *mockIdempotencyService) MarkPending(_ context.Context, _ idempotency.Key, _ time.Duration) error {
	return nil
}

func (m *mockIdempotencyService) StoreResult(_ context.Context, _ idempotency.Result) error {
	return nil
}

func (m *mockIdempotencyService) Delete(_ context.Context, _ idempotency.Key) error {
	return nil
}

// Locker interface methods
func (m *mockIdempotencyService) Acquire(_ context.Context, _ idempotency.Key, _ idempotency.LockOptions) error {
	return nil
}

func (m *mockIdempotencyService) Release(_ context.Context, _ idempotency.Key, _ string) error {
	return nil
}

func (m *mockIdempotencyService) Refresh(_ context.Context, _ idempotency.Key, _ string, _ time.Duration) error {
	return nil
}

func (m *mockIdempotencyService) IsHeld(_ context.Context, _ idempotency.Key) (bool, error) {
	return false, nil
}
