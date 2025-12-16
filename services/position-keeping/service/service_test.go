package service_test

import (
	"testing"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
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

	t.Run("creates service with nil checks", func(t *testing.T) {
		// TODO(tech-debt-cleanup#4): Add nil parameter validation in constructor
		// Currently the constructor accepts nil dependencies
		t.Skip("Nil validation not yet implemented")
	})
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
