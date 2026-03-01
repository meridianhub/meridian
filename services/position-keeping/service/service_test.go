package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	messagingpkg "github.com/meridianhub/meridian/services/position-keeping/adapters/messaging"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

func TestNewPositionKeepingService(t *testing.T) {
	t.Run("creates service with valid dependencies", func(t *testing.T) {
		repo := new(MockRepository)
		measurementRepo := new(MockMeasurementRepository)
		publisher := domain.NewInMemoryEventPublisher()
		idempotencySvc := new(MockIdempotencyService)
		outboxPub := newTestOutboxPublisher(t)

		svc, err := service.NewPositionKeepingService(repo, measurementRepo, publisher, idempotencySvc, outboxPub)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if svc == nil {
			t.Fatal("Expected non-nil service")
		}
	})
}

// TestNewPositionKeepingService_DefensiveTests verifies nil dependency validation per ADR-0008.
// Rationale: Financial services must validate all dependencies to prevent runtime panics
// that could cause service outages or data corruption.
func TestNewPositionKeepingService_DefensiveTests(t *testing.T) {
	validOutboxPub := newTestOutboxPublisher(t)

	tests := []struct {
		name            string
		repository      domain.FinancialPositionLogRepository
		measurementRepo domain.MeasurementRepository
		eventPub        domain.EventPublisher
		idempotencySvc  idempotency.Service
		outboxPub       *messagingpkg.OutboxEventPublisher
		wantErr         bool
		wantSentinel    error // Expected sentinel error for errors.Is() verification
		rationale       string
	}{
		{
			name:            "valid dependencies",
			repository:      new(MockRepository),
			measurementRepo: new(MockMeasurementRepository),
			eventPub:        domain.NewInMemoryEventPublisher(),
			idempotencySvc:  new(MockIdempotencyService),
			outboxPub:       validOutboxPub,
			wantErr:         false,
			wantSentinel:    nil,
			rationale:       "Standard valid initialization with all dependencies",
		},
		{
			name:            "nil repository",
			repository:      nil,
			measurementRepo: new(MockMeasurementRepository),
			eventPub:        domain.NewInMemoryEventPublisher(),
			idempotencySvc:  new(MockIdempotencyService),
			outboxPub:       validOutboxPub,
			wantErr:         true,
			wantSentinel:    service.ErrRepositoryNil,
			rationale:       "Repository is essential - nil would cause panic on first use",
		},
		{
			name:            "nil measurement repository",
			repository:      new(MockRepository),
			measurementRepo: nil,
			eventPub:        domain.NewInMemoryEventPublisher(),
			idempotencySvc:  new(MockIdempotencyService),
			outboxPub:       validOutboxPub,
			wantErr:         true,
			wantSentinel:    service.ErrMeasurementRepoNil,
			rationale:       "Measurement repository is essential - nil would cause panic on measurement operations",
		},
		{
			name:            "nil event publisher",
			repository:      new(MockRepository),
			measurementRepo: new(MockMeasurementRepository),
			eventPub:        nil,
			idempotencySvc:  new(MockIdempotencyService),
			outboxPub:       validOutboxPub,
			wantErr:         true,
			wantSentinel:    service.ErrEventPublisherNil,
			rationale:       "Event publisher is essential - nil would cause panic when publishing events",
		},
		{
			name:            "nil idempotency service",
			repository:      new(MockRepository),
			measurementRepo: new(MockMeasurementRepository),
			eventPub:        domain.NewInMemoryEventPublisher(),
			idempotencySvc:  nil,
			outboxPub:       validOutboxPub,
			wantErr:         true,
			wantSentinel:    service.ErrIdempotencyServiceNil,
			rationale:       "Idempotency service is essential - nil would cause panic on idempotent operations",
		},
		{
			name:            "nil outbox publisher",
			repository:      new(MockRepository),
			measurementRepo: new(MockMeasurementRepository),
			eventPub:        domain.NewInMemoryEventPublisher(),
			idempotencySvc:  new(MockIdempotencyService),
			outboxPub:       nil,
			wantErr:         true,
			wantSentinel:    service.ErrOutboxPublisherNil,
			rationale:       "Outbox publisher is essential - nil would cause panic when publishing events transactionally",
		},
		{
			name:            "all dependencies nil",
			repository:      nil,
			measurementRepo: nil,
			eventPub:        nil,
			idempotencySvc:  nil,
			outboxPub:       nil,
			wantErr:         true,
			wantSentinel:    service.ErrRepositoryNil,
			rationale:       "Should error on first nil check (repository)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := service.NewPositionKeepingService(tt.repository, tt.measurementRepo, tt.eventPub, tt.idempotencySvc, tt.outboxPub)
			if tt.wantErr {
				assert.Error(t, err, tt.rationale)
				assert.Nil(t, svc, "Service should be nil when error occurs")
				// Verify the specific sentinel error using errors.Is()
				assert.ErrorIs(t, err, tt.wantSentinel, "Should return the expected sentinel error")
			} else {
				assert.NoError(t, err, tt.rationale)
				assert.NotNil(t, svc, tt.rationale)
			}
		})
	}
}

func TestServiceImplementsGRPCInterface(t *testing.T) {
	t.Run("service implements PositionKeepingServiceServer", func(t *testing.T) {
		repo := new(MockRepository)
		measurementRepo := new(MockMeasurementRepository)
		publisher := domain.NewInMemoryEventPublisher()
		idempotencySvc := new(MockIdempotencyService)
		outboxPub := newTestOutboxPublisher(t)

		svc, err := service.NewPositionKeepingService(repo, measurementRepo, publisher, idempotencySvc, outboxPub)
		if err != nil {
			t.Fatalf("unexpected error creating service: %v", err)
		}

		// This will fail to compile if service doesn't implement the interface
		var _ positionkeepingv1.PositionKeepingServiceServer = svc
	})
}
