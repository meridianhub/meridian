package idempotency

import (
	"context"
	"time"

	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/redis/go-redis/v9"
)

// LazyService wraps a LazyClient[*redis.Client] and implements Service.
// Before Redis is resolved, all operations degrade gracefully:
//   - Check returns ErrResultNotFound (allows the request to proceed)
//   - Locking operations return nil (no-op)
//
// Once Redis is available, operations are forwarded to a real RedisService.
type LazyService struct {
	lazy *bootstrap.LazyClient[*redis.Client]
}

// NewLazyService creates an idempotency Service backed by a lazily-resolved Redis client.
// The returned service is immediately usable; operations degrade gracefully until Redis connects.
func NewLazyService(lazy *bootstrap.LazyClient[*redis.Client]) *LazyService {
	return &LazyService{lazy: lazy}
}

// IsReady reports whether the underlying Redis client has been resolved.
func (s *LazyService) IsReady() bool {
	return s.lazy.IsReady()
}

func (s *LazyService) getService() (*RedisService, bool) {
	client, err := s.lazy.Get()
	if err != nil {
		return nil, false
	}
	return NewRedisService(client), true
}

// Check verifies if an operation has already been processed.
// Returns ErrResultNotFound when Redis is not yet available (allows request to proceed).
func (s *LazyService) Check(ctx context.Context, key Key) (*Result, error) {
	svc, ok := s.getService()
	if !ok {
		return nil, ErrResultNotFound
	}
	return svc.Check(ctx, key)
}

// MarkPending marks a key as pending in Redis.
// No-op when Redis is not yet available.
func (s *LazyService) MarkPending(ctx context.Context, key Key, ttl time.Duration) error {
	svc, ok := s.getService()
	if !ok {
		return nil
	}
	return svc.MarkPending(ctx, key, ttl)
}

// StoreResult stores an idempotency result in Redis.
// No-op when Redis is not yet available.
func (s *LazyService) StoreResult(ctx context.Context, result Result) error {
	svc, ok := s.getService()
	if !ok {
		return nil
	}
	return svc.StoreResult(ctx, result)
}

// Delete removes an idempotency key from Redis.
// No-op when Redis is not yet available.
func (s *LazyService) Delete(ctx context.Context, key Key) error {
	svc, ok := s.getService()
	if !ok {
		return nil
	}
	return svc.Delete(ctx, key)
}

// Acquire attempts to acquire a distributed lock.
// No-op when Redis is not yet available.
func (s *LazyService) Acquire(ctx context.Context, key Key, opts LockOptions) error {
	svc, ok := s.getService()
	if !ok {
		return nil
	}
	return svc.Acquire(ctx, key, opts)
}

// Release releases a distributed lock.
// No-op when Redis is not yet available.
func (s *LazyService) Release(ctx context.Context, key Key, token string) error {
	svc, ok := s.getService()
	if !ok {
		return nil
	}
	return svc.Release(ctx, key, token)
}

// Refresh extends the TTL of a held lock.
// No-op when Redis is not yet available.
func (s *LazyService) Refresh(ctx context.Context, key Key, token string, ttl time.Duration) error {
	svc, ok := s.getService()
	if !ok {
		return nil
	}
	return svc.Refresh(ctx, key, token, ttl)
}

// IsHeld checks if a lock is currently held.
// Returns false when Redis is not yet available.
func (s *LazyService) IsHeld(ctx context.Context, key Key) (bool, error) {
	svc, ok := s.getService()
	if !ok {
		return false, nil
	}
	return svc.IsHeld(ctx, key)
}
