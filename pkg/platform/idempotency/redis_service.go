package idempotency

import (
	"context"
	"errors"
	"fmt"
	"time"

	platformv1 "github.com/meridianhub/meridian/api/proto/meridian/platform/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RedisService implements Service using Redis for distributed idempotency and locking
type RedisService struct {
	client *redis.Client
}

// NewRedisService creates a new Redis-based idempotency service
func NewRedisService(client *redis.Client) *RedisService {
	return &RedisService{
		client: client,
	}
}

// Check verifies if an operation has already been processed
func (r *RedisService) Check(ctx context.Context, key Key) (*Result, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}

	redisKey := r.resultKey(key)
	data, err := r.client.Get(ctx, redisKey).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			// Key doesn't exist - operation hasn't been processed
			return nil, ErrResultNotFound
		}
		return nil, fmt.Errorf("failed to check idempotency: %w", err)
	}

	// Deserialize protobuf result
	var pbResult platformv1.IdempotencyResult
	if err := proto.Unmarshal(data, &pbResult); err != nil {
		return nil, fmt.Errorf("failed to deserialize result: %w", err)
	}

	// Convert protobuf to domain model
	result := fromProto(&pbResult)

	// If operation was completed, return the cached result
	if result.Status == StatusCompleted {
		return result, ErrOperationAlreadyProcessed
	}

	return result, nil
}

// MarkPending marks an operation as in-progress
func (r *RedisService) MarkPending(ctx context.Context, key Key, ttl time.Duration) error {
	if err := key.Validate(); err != nil {
		return err
	}

	result := Result{
		Key:         key,
		Status:      StatusPending,
		Data:        nil,
		Error:       "",
		CompletedAt: time.Time{}, // Zero time for pending operations
		TTL:         ttl,
	}

	return r.StoreResult(ctx, result)
}

// StoreResult saves the operation result for future idempotency checks
func (r *RedisService) StoreResult(ctx context.Context, result Result) error {
	if err := result.Key.Validate(); err != nil {
		return err
	}

	// Convert to protobuf
	pbResult := toProto(result)

	// Serialize protobuf
	data, err := proto.Marshal(pbResult)
	if err != nil {
		return fmt.Errorf("failed to serialize result: %w", err)
	}

	// Store with TTL
	redisKey := r.resultKey(result.Key)
	if err := r.client.Set(ctx, redisKey, data, result.TTL).Err(); err != nil {
		return fmt.Errorf("failed to store result: %w", err)
	}

	return nil
}

// Delete removes an idempotency record
func (r *RedisService) Delete(ctx context.Context, key Key) error {
	if err := key.Validate(); err != nil {
		return err
	}

	redisKey := r.resultKey(key)
	if err := r.client.Del(ctx, redisKey).Err(); err != nil {
		return fmt.Errorf("failed to delete key: %w", err)
	}

	return nil
}

// Acquire attempts to acquire a distributed lock
func (r *RedisService) Acquire(ctx context.Context, key Key, opts LockOptions) error {
	if err := key.Validate(); err != nil {
		return err
	}

	if opts.TTL <= 0 {
		return ErrInvalidTTL
	}

	if opts.Token == "" {
		return ErrEmptyToken
	}

	redisKey := r.lockKey(key)

	// Try to acquire lock with retries
	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		// Use SET NX (set if not exists) with expiration
		success, err := r.client.SetNX(ctx, redisKey, opts.Token, opts.TTL).Result()
		if err != nil {
			return fmt.Errorf("failed to acquire lock: %w", err)
		}

		if success {
			return nil
		}

		// Lock acquisition failed, check if we should retry
		if attempt < opts.MaxRetries {
			select {
			case <-ctx.Done():
				return fmt.Errorf("lock acquisition cancelled: %w", ctx.Err())
			case <-time.After(opts.RetryDelay):
				continue
			}
		}
	}

	return ErrLockAcquisitionFailed
}

// Release releases a previously acquired lock
func (r *RedisService) Release(ctx context.Context, key Key, token string) error {
	if err := key.Validate(); err != nil {
		return err
	}

	if token == "" {
		return ErrEmptyToken
	}

	redisKey := r.lockKey(key)

	// Use Lua script to ensure atomic check-and-delete
	// Only delete if the token matches (prevents releasing someone else's lock)
	script := redis.NewScript(`
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`)

	result, err := script.Run(ctx, r.client, []string{redisKey}, token).Result()
	if err != nil {
		return fmt.Errorf("failed to release lock: %w", err)
	}

	// Check if lock was actually released
	deleted, ok := result.(int64)
	if !ok || deleted == 0 {
		return ErrLockNotHeld
	}

	return nil
}

// Refresh extends the TTL of a held lock
func (r *RedisService) Refresh(ctx context.Context, key Key, token string, ttl time.Duration) error {
	if err := key.Validate(); err != nil {
		return err
	}

	if token == "" {
		return ErrEmptyToken
	}

	redisKey := r.lockKey(key)

	// Use Lua script for atomic check-and-refresh
	script := redis.NewScript(`
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("pexpire", KEYS[1], ARGV[2])
		else
			return 0
		end
	`)

	result, err := script.Run(ctx, r.client, []string{redisKey}, token, ttl.Milliseconds()).Result()
	if err != nil {
		return fmt.Errorf("failed to refresh lock: %w", err)
	}

	// Check if lock was actually refreshed
	refreshed, ok := result.(int64)
	if !ok || refreshed == 0 {
		return ErrLockNotHeld
	}

	return nil
}

// IsHeld checks if a lock is currently held
func (r *RedisService) IsHeld(ctx context.Context, key Key) (bool, error) {
	if err := key.Validate(); err != nil {
		return false, err
	}

	redisKey := r.lockKey(key)
	exists, err := r.client.Exists(ctx, redisKey).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check lock: %w", err)
	}

	return exists > 0, nil
}

// resultKey generates Redis key for idempotency results
func (r *RedisService) resultKey(key Key) string {
	return "idempotency:result:" + key.String()
}

// lockKey generates Redis key for distributed locks
func (r *RedisService) lockKey(key Key) string {
	return "idempotency:lock:" + key.String()
}

// toProto converts Result to protobuf
func toProto(result Result) *platformv1.IdempotencyResult {
	var completedAt *timestamppb.Timestamp
	if !result.CompletedAt.IsZero() {
		completedAt = timestamppb.New(result.CompletedAt)
	}

	return &platformv1.IdempotencyResult{
		Namespace:   result.Key.Namespace,
		Operation:   result.Key.Operation,
		EntityId:    result.Key.EntityID,
		RequestId:   result.Key.RequestID,
		Status:      statusToProto(result.Status),
		Data:        result.Data,
		Error:       result.Error,
		CompletedAt: completedAt,
		TtlSeconds:  int64(result.TTL.Seconds()),
	}
}

// fromProto converts protobuf to Result
func fromProto(pb *platformv1.IdempotencyResult) *Result {
	var completedAt time.Time
	if pb.CompletedAt != nil {
		completedAt = pb.CompletedAt.AsTime()
	}

	return &Result{
		Key: Key{
			Namespace: pb.Namespace,
			Operation: pb.Operation,
			EntityID:  pb.EntityId,
			RequestID: pb.RequestId,
		},
		Status:      statusFromProto(pb.Status),
		Data:        pb.Data,
		Error:       pb.Error,
		CompletedAt: completedAt,
		TTL:         time.Duration(pb.TtlSeconds) * time.Second,
	}
}

// statusToProto converts OperationStatus to protobuf enum
func statusToProto(status OperationStatus) platformv1.OperationStatus {
	switch status {
	case StatusPending:
		return platformv1.OperationStatus_OPERATION_STATUS_PENDING
	case StatusCompleted:
		return platformv1.OperationStatus_OPERATION_STATUS_COMPLETED
	case StatusFailed:
		return platformv1.OperationStatus_OPERATION_STATUS_FAILED
	default:
		return platformv1.OperationStatus_OPERATION_STATUS_UNSPECIFIED
	}
}

// statusFromProto converts protobuf enum to OperationStatus
func statusFromProto(status platformv1.OperationStatus) OperationStatus {
	switch status {
	case platformv1.OperationStatus_OPERATION_STATUS_PENDING:
		return StatusPending
	case platformv1.OperationStatus_OPERATION_STATUS_COMPLETED:
		return StatusCompleted
	case platformv1.OperationStatus_OPERATION_STATUS_FAILED:
		return StatusFailed
	case platformv1.OperationStatus_OPERATION_STATUS_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}
