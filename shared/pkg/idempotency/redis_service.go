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
	result, err := fromProto(&pbResult)
	if err != nil {
		return nil, fmt.Errorf("failed to convert result from proto: %w", err)
	}

	// If operation was completed, return the cached result
	// Note: Between checking status and returning, the key could theoretically expire
	// (unlikely with typical TTLs of hours/days, but documented for completeness)
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
		CreatedAt:   time.Now(),  // Track when PENDING state started for cleanup
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

	if result.TTL <= 0 {
		return ErrInvalidTTL
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
		_, err := r.client.SetArgs(ctx, redisKey, opts.Token, redis.SetArgs{
			Mode: "NX",
			TTL:  opts.TTL,
		}).Result()
		if err == nil {
			// Lock acquired successfully
			return nil
		}
		if !errors.Is(err, redis.Nil) {
			return fmt.Errorf("failed to acquire lock: %w", err)
		}
		// redis.Nil means key already exists (lock held by someone else), fall through to retry

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

	// Check if lock was actually released (defensive type assertion)
	deleted, ok := result.(int64)
	if !ok {
		return fmt.Errorf("%w: got %T, expected int64", ErrUnexpectedRedisResponse, result)
	}
	if deleted == 0 {
		return ErrLockNotHeld
	}

	return nil
}

// Refresh extends the TTL of a held lock
func (r *RedisService) Refresh(ctx context.Context, key Key, token string, ttl time.Duration) error {
	if err := key.Validate(); err != nil {
		return err
	}

	if ttl <= 0 {
		return ErrInvalidTTL
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

	// Check if lock was actually refreshed (defensive type assertion)
	refreshed, ok := result.(int64)
	if !ok {
		return fmt.Errorf("%w: got %T, expected int64", ErrUnexpectedRedisResponse, result)
	}
	if refreshed == 0 {
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
	var createdAt *timestamppb.Timestamp
	if !result.CreatedAt.IsZero() {
		createdAt = timestamppb.New(result.CreatedAt)
	}

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
		CreatedAt:   createdAt,
		CompletedAt: completedAt,
		TtlSeconds:  int64(result.TTL.Seconds()),
	}
}

// fromProto converts protobuf to Result
func fromProto(pb *platformv1.IdempotencyResult) (*Result, error) {
	var createdAt time.Time
	if pb.CreatedAt != nil {
		createdAt = pb.CreatedAt.AsTime()
	}

	var completedAt time.Time
	if pb.CompletedAt != nil {
		completedAt = pb.CompletedAt.AsTime()
	}

	status := statusFromProto(pb.Status)

	// Validate that status is one of the defined constants
	if status != StatusPending && status != StatusCompleted && status != StatusFailed {
		return nil, fmt.Errorf("%w from proto: %v", ErrInvalidStatus, pb.Status)
	}

	return &Result{
		Key: Key{
			Namespace: pb.Namespace,
			Operation: pb.Operation,
			EntityID:  pb.EntityId,
			RequestID: pb.RequestId,
		},
		Status:      status,
		Data:        pb.Data,
		Error:       pb.Error,
		CreatedAt:   createdAt,
		CompletedAt: completedAt,
		TTL:         time.Duration(pb.TtlSeconds) * time.Second,
	}, nil
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

// ScanStalePendingKeys scans for PENDING keys older than the threshold.
// It uses Redis SCAN to iterate through keys matching the pattern and checks
// each one for PENDING status with age exceeding the threshold.
//
// Parameters:
//   - pattern: Redis key pattern to scan (e.g., "idempotency:result:*")
//   - threshold: How long a PENDING key must exist before considered stale
//   - limit: Maximum number of stale keys to return (batch size)
//
// Returns stale keys found, up to the limit. Returns empty slice if none found.
func (r *RedisService) ScanStalePendingKeys(ctx context.Context, pattern string, threshold time.Duration, limit int) ([]StalePendingKey, error) {
	if limit <= 0 {
		limit = 100 // Default batch size
	}

	now := time.Now()
	var staleKeys []StalePendingKey
	var cursor uint64

	// Scan through Redis keys matching the pattern
	for {
		if err := ctx.Err(); err != nil {
			return staleKeys, err
		}

		// SCAN returns a cursor and a batch of keys
		keys, nextCursor, err := r.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to scan keys: %w", err)
		}

		// Process each key in this batch
		staleKeys = r.processKeyBatch(ctx, keys, staleKeys, threshold, now, limit)
		if len(staleKeys) >= limit {
			return staleKeys, nil
		}

		// Move to next cursor position
		cursor = nextCursor
		if cursor == 0 {
			// Completed full scan
			break
		}
	}

	return staleKeys, nil
}

// processKeyBatch processes a batch of Redis keys and appends stale PENDING keys to the result.
func (r *RedisService) processKeyBatch(ctx context.Context, keys []string, staleKeys []StalePendingKey, threshold time.Duration, now time.Time, limit int) []StalePendingKey {
	for _, redisKey := range keys {
		if len(staleKeys) >= limit {
			return staleKeys
		}

		staleKey := r.checkKeyForStaleness(ctx, redisKey, threshold, now)
		if staleKey != nil {
			staleKeys = append(staleKeys, *staleKey)
		}
	}
	return staleKeys
}

// checkKeyForStaleness checks if a single key is a stale PENDING key.
// Returns nil if the key is not stale or cannot be checked.
func (r *RedisService) checkKeyForStaleness(ctx context.Context, redisKey string, threshold time.Duration, now time.Time) *StalePendingKey {
	data, err := r.client.Get(ctx, redisKey).Bytes()
	if err != nil {
		return nil // Key deleted or error - skip
	}

	var pbResult platformv1.IdempotencyResult
	if err := proto.Unmarshal(data, &pbResult); err != nil {
		return nil // Malformed entry - skip
	}

	result, err := fromProto(&pbResult)
	if err != nil {
		return nil // Invalid entry - skip
	}

	if result.Status != StatusPending || result.CreatedAt.IsZero() {
		return nil // Not pending or legacy key without CreatedAt
	}

	age := now.Sub(result.CreatedAt)
	if age <= threshold {
		return nil // Not stale yet
	}

	return &StalePendingKey{
		RedisKey: redisKey,
		Result:   result,
		Age:      age,
	}
}

// MarkStaleAsFailed updates a stale PENDING key to FAILED status.
// It preserves the original TTL and adds the failure reason.
//
// This operation is idempotent - if the key no longer exists or is no longer
// PENDING, no error is returned (the desired state is achieved).
//
// Note on race conditions: There is a small window between reading the key,
// verifying it's still PENDING, and updating it to FAILED. If another process
// completes the operation in this window, the COMPLETED status would be
// overwritten with FAILED. This is an accepted trade-off because:
// 1. The window is very small (microseconds)
// 2. The consequence is recoverable (client can retry with new idempotency key)
// 3. Atomic protobuf parsing in Lua scripts is not practical
// 4. WATCH/MULTI/EXEC adds complexity for a rare edge case
//
// If this becomes a problem in production, consider using Redis WATCH for
// optimistic locking or storing a simple status field alongside the protobuf.
func (r *RedisService) MarkStaleAsFailed(ctx context.Context, staleKey StalePendingKey, reason string) error {
	if staleKey.Result == nil {
		return ErrNilResult
	}

	// Re-read the current state to minimize race window
	data, err := r.client.Get(ctx, staleKey.RedisKey).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			// Key was deleted - desired state achieved
			return nil
		}
		return fmt.Errorf("failed to read key for update: %w", err)
	}

	var pbResult platformv1.IdempotencyResult
	if err := proto.Unmarshal(data, &pbResult); err != nil {
		return fmt.Errorf("failed to deserialize key: %w", err)
	}

	// Verify still PENDING (avoid overwriting completed operations)
	if pbResult.Status != platformv1.OperationStatus_OPERATION_STATUS_PENDING {
		// No longer pending - skip update (not an error)
		return nil
	}

	// Get remaining TTL to preserve it
	ttl, err := r.client.TTL(ctx, staleKey.RedisKey).Result()
	if err != nil {
		return fmt.Errorf("failed to get TTL: %w", err)
	}

	// If TTL is -2, key doesn't exist; if -1, no expiry set
	if ttl < 0 {
		ttl = time.Hour // Default fallback TTL
	}

	// Update to FAILED status
	pbResult.Status = platformv1.OperationStatus_OPERATION_STATUS_FAILED
	pbResult.Error = reason
	pbResult.CompletedAt = timestamppb.Now()

	// Serialize and store
	updatedData, err := proto.Marshal(&pbResult)
	if err != nil {
		return fmt.Errorf("failed to serialize updated result: %w", err)
	}

	if err := r.client.Set(ctx, staleKey.RedisKey, updatedData, ttl).Err(); err != nil {
		return fmt.Errorf("failed to update key: %w", err)
	}

	return nil
}
