package idempotency

import (
	"context"
	"log/slog"
	"time"
)

// NoopService is an idempotency Service that accepts all operations without persisting state.
// It is intended for non-production environments where Redis is unavailable, allowing the
// service to start and operate in a degraded mode. Idempotency guarantees are NOT provided.
//
// WARNING: Using NoopService in production is unsafe — duplicate requests will not be detected.
// Services should record degradation metrics and alert when this service is active.
type NoopService struct {
	logger *slog.Logger
}

// NewNoopService creates a NoopService that logs a warning on creation.
// Callers should record degradation metrics before or after calling this constructor.
func NewNoopService(logger *slog.Logger) *NoopService {
	logger.Warn("idempotency service running in noop mode — duplicate requests will NOT be detected (non-production only)")
	return &NoopService{logger: logger}
}

// Check always returns ErrResultNotFound, allowing the operation to proceed.
func (s *NoopService) Check(_ context.Context, _ Key) (*Result, error) {
	return nil, ErrResultNotFound
}

// MarkPending is a no-op.
func (s *NoopService) MarkPending(_ context.Context, _ Key, _ time.Duration) error {
	return nil
}

// StoreResult is a no-op.
func (s *NoopService) StoreResult(_ context.Context, _ Result) error {
	return nil
}

// Delete is a no-op.
func (s *NoopService) Delete(_ context.Context, _ Key) error {
	return nil
}

// Acquire always succeeds without acquiring a real lock.
func (s *NoopService) Acquire(_ context.Context, _ Key, _ LockOptions) error {
	return nil
}

// Release is a no-op.
func (s *NoopService) Release(_ context.Context, _ Key, _ string) error {
	return nil
}

// Refresh is a no-op.
func (s *NoopService) Refresh(_ context.Context, _ Key, _ string, _ time.Duration) error {
	return nil
}

// IsHeld always returns false.
func (s *NoopService) IsHeld(_ context.Context, _ Key) (bool, error) {
	return false, nil
}
