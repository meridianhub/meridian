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
