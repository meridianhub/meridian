package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

func TestNewPositionKeepingService(t *testing.T) {
	t.Run("creates service with valid dependencies", func(t *testing.T) {
		repo := new(MockRepository)
		publisher := domain.NewInMemoryEventPublisher()
		idempotencySvc := new(MockIdempotencyService)

		svc := service.NewPositionKeepingService(repo, publisher, idempotencySvc)

		if svc == nil {
			t.Fatal("Expected non-nil service")
		}
	})
}

// TestNewPositionKeepingService_DefensiveTests verifies nil dependency validation per ADR-0008.
// Rationale: Financial services must validate all dependencies to prevent runtime panics
// that could cause service outages or data corruption.
func TestNewPositionKeepingService_DefensiveTests(t *testing.T) {
	tests := []struct {
		name           string
		repository     domain.FinancialPositionLogRepository
		eventPub       domain.EventPublisher
		idempotencySvc idempotency.Service
		shouldPanic    bool
		rationale      string
	}{
		{
			name:           "valid dependencies",
			repository:     new(MockRepository),
			eventPub:       domain.NewInMemoryEventPublisher(),
			idempotencySvc: new(MockIdempotencyService),
			shouldPanic:    false,
			rationale:      "Standard valid initialization with all dependencies",
		},
		{
			name:           "nil repository",
			repository:     nil,
			eventPub:       domain.NewInMemoryEventPublisher(),
			idempotencySvc: new(MockIdempotencyService),
			shouldPanic:    true,
			rationale:      "Repository is essential - nil would cause panic on first use",
		},
		{
			name:           "nil event publisher",
			repository:     new(MockRepository),
			eventPub:       nil,
			idempotencySvc: new(MockIdempotencyService),
			shouldPanic:    true,
			rationale:      "Event publisher is essential - nil would cause panic when publishing events",
		},
		{
			name:           "nil idempotency service",
			repository:     new(MockRepository),
			eventPub:       domain.NewInMemoryEventPublisher(),
			idempotencySvc: nil,
			shouldPanic:    true,
			rationale:      "Idempotency service is essential - nil would cause panic on idempotent operations",
		},
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
					service.NewPositionKeepingService(tt.repository, tt.eventPub, tt.idempotencySvc)
				}, tt.rationale)
			} else {
				assert.NotPanics(t, func() {
					svc := service.NewPositionKeepingService(tt.repository, tt.eventPub, tt.idempotencySvc)
					assert.NotNil(t, svc, tt.rationale)
				}, tt.rationale)
			}
		})
	}
}

func TestServiceImplementsGRPCInterface(t *testing.T) {
	t.Run("service implements PositionKeepingServiceServer", func(_ *testing.T) {
		repo := new(MockRepository)
		publisher := domain.NewInMemoryEventPublisher()
		idempotencySvc := new(MockIdempotencyService)

		svc := service.NewPositionKeepingService(repo, publisher, idempotencySvc)

		// This will fail to compile if service doesn't implement the interface
		var _ positionkeepingv1.PositionKeepingServiceServer = svc
	})
}
