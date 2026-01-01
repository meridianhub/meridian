// Package idempotency provides distributed idempotency checking and locking capabilities
package idempotency

import (
	"context"
	"errors"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
)

var (
	// ErrOperationAlreadyProcessed indicates the operation was already completed
	ErrOperationAlreadyProcessed = errors.New("operation already processed")

	// ErrLockAcquisitionFailed indicates failed to acquire distributed lock
	ErrLockAcquisitionFailed = errors.New("failed to acquire lock")

	// ErrLockNotHeld indicates attempted to release a lock that is not held
	ErrLockNotHeld = errors.New("lock not held")

	// ErrInvalidKey indicates an invalid idempotency key was provided
	ErrInvalidKey = errors.New("invalid idempotency key")

	// ErrResultNotFound indicates no cached result found for the key
	ErrResultNotFound = errors.New("result not found")

	// ErrEmptyToken indicates lock token is empty
	ErrEmptyToken = errors.New("lock token cannot be empty")

	// ErrInvalidTTL indicates TTL is zero or negative
	ErrInvalidTTL = errors.New("TTL must be greater than zero")

	// ErrInvalidStatus indicates an invalid or unknown operation status
	ErrInvalidStatus = errors.New("invalid operation status")

	// ErrUnexpectedRedisResponse indicates Redis returned an unexpected response type
	ErrUnexpectedRedisResponse = errors.New("unexpected redis response type")

	// ErrNilResult indicates a nil result was provided where one was required
	ErrNilResult = errors.New("result cannot be nil")
)

// Key represents an idempotency key with namespace and operation context
type Key struct {
	// TenantID is the tenant identifier for multi-tenant isolation.
	// When set, keys are prefixed with the tenant ID to ensure isolation.
	// When empty, keys are generated without a tenant prefix (single-tenant mode).
	// Must not contain colons (':') to avoid key parsing ambiguity.
	TenantID string

	// Namespace groups related operations (e.g., "current-account")
	Namespace string

	// Operation identifies the type of operation (e.g., "deposit", "withdrawal")
	Operation string

	// EntityID is the unique identifier for the entity being operated on (e.g., account ID)
	EntityID string

	// RequestID is an optional client-provided idempotency token
	RequestID string
}

// String returns the Redis key format.
// With TenantID: {tenant_id}:idempotency:{namespace}:{operation}:{entity}:{request}
// Without TenantID: idempotency:{namespace}:{operation}:{entity}:{request}
// Note: Field values must not contain colons to avoid ambiguous key representations
func (k Key) String() string {
	var prefix string
	if k.TenantID != "" {
		prefix = k.TenantID + ":"
	}

	if k.RequestID != "" {
		return prefix + "idempotency:" + k.Namespace + ":" + k.Operation + ":" + k.EntityID + ":" + k.RequestID
	}
	return prefix + "idempotency:" + k.Namespace + ":" + k.Operation + ":" + k.EntityID
}

// Validate checks if the key has all required fields and that none contain colons
func (k Key) Validate() error {
	if k.Namespace == "" || k.Operation == "" || k.EntityID == "" {
		return ErrInvalidKey
	}

	// Prevent colon characters in fields to avoid key collisions
	if containsColon(k.TenantID) || containsColon(k.Namespace) ||
		containsColon(k.Operation) || containsColon(k.EntityID) ||
		containsColon(k.RequestID) {
		return ErrInvalidKey
	}

	return nil
}

// containsColon checks if a string contains the colon character
func containsColon(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return true
		}
	}
	return false
}

// Result represents a cached operation result
type Result struct {
	// Key is the idempotency key
	Key Key

	// Status indicates the operation outcome
	Status OperationStatus

	// Data contains the serialized operation result (JSON, protobuf, etc.)
	Data []byte

	// Error contains error message if Status is StatusFailed
	Error string

	// CreatedAt is when the operation started (set when status becomes PENDING)
	// Used by cleanup workers to detect stale PENDING keys
	CreatedAt time.Time

	// CompletedAt is when the operation finished
	CompletedAt time.Time

	// TTL is how long this result should be cached
	TTL time.Duration
}

// OperationStatus represents the state of an operation
type OperationStatus string

const (
	// StatusPending indicates operation is in progress
	StatusPending OperationStatus = "pending"

	// StatusCompleted indicates operation finished successfully
	StatusCompleted OperationStatus = "completed"

	// StatusFailed indicates operation failed
	StatusFailed OperationStatus = "failed"
)

// LockOptions configures distributed lock behavior
type LockOptions struct {
	// TTL is how long the lock is valid before automatic expiration
	TTL time.Duration

	// RetryDelay is how long to wait between retry attempts
	RetryDelay time.Duration

	// MaxRetries is maximum number of acquisition attempts (0 = no retries)
	MaxRetries int

	// Token is a unique identifier for this lock holder (e.g., UUID)
	Token string
}

// DefaultLockOptions returns sensible defaults for lock acquisition
func DefaultLockOptions() LockOptions {
	return LockOptions{
		TTL:        defaults.DefaultRPCTimeout,
		RetryDelay: defaults.DefaultRetryDelay,
		MaxRetries: 3,
	}
}

// Checker provides idempotency checking capabilities
type Checker interface {
	// Check verifies if an operation has already been processed
	// Returns ErrOperationAlreadyProcessed if the operation was already completed
	// Returns the cached Result if available
	Check(ctx context.Context, key Key) (*Result, error)

	// MarkPending marks an operation as in-progress
	// This prevents concurrent execution of the same operation
	MarkPending(ctx context.Context, key Key, ttl time.Duration) error

	// StoreResult saves the operation result for future idempotency checks
	StoreResult(ctx context.Context, result Result) error

	// Delete removes an idempotency record (useful for testing or cleanup)
	Delete(ctx context.Context, key Key) error
}

// Locker provides distributed locking capabilities
type Locker interface {
	// Acquire attempts to acquire a distributed lock
	// Returns ErrLockAcquisitionFailed if lock cannot be acquired
	Acquire(ctx context.Context, key Key, opts LockOptions) error

	// Release releases a previously acquired lock
	// Returns ErrLockNotHeld if the lock is not held by this token
	Release(ctx context.Context, key Key, token string) error

	// Refresh extends the TTL of a held lock
	Refresh(ctx context.Context, key Key, token string, ttl time.Duration) error

	// IsHeld checks if a lock is currently held (by any token)
	IsHeld(ctx context.Context, key Key) (bool, error)
}

// Service combines idempotency checking and distributed locking
type Service interface {
	Checker
	Locker
}

// StalePendingKey represents a PENDING idempotency key that has exceeded
// the stale timeout threshold and is eligible for cleanup.
type StalePendingKey struct {
	// RedisKey is the full Redis key (e.g., "idempotency:result:ns:op:entity")
	RedisKey string

	// Result is the parsed idempotency result
	Result *Result

	// Age is how long the key has been in PENDING state
	Age time.Duration
}

// CleanupResult contains the outcome of a cleanup batch operation.
// Reserved for future use: This type is defined for a planned batch cleanup API
// that would allow callers to process multiple stale keys and receive aggregate results.
// Currently, the IdempotencyCleanupWorker tracks these metrics internally.
type CleanupResult struct {
	// Processed is the number of stale keys that were processed
	Processed int

	// Failed is the number of keys that failed to be marked as FAILED
	Failed int

	// Errors contains any errors encountered during cleanup
	Errors []error
}

// Cleaner provides capabilities for detecting and cleaning up stale PENDING keys.
// This is used by background workers to prevent keys from being stuck in PENDING
// state indefinitely when the original request failed without completing.
type Cleaner interface {
	// ScanStalePendingKeys scans for PENDING keys older than the threshold.
	// It returns up to `limit` stale keys in each call.
	// The pattern parameter filters keys (e.g., "idempotency:result:*").
	// Returns empty slice if no stale keys found.
	ScanStalePendingKeys(ctx context.Context, pattern string, threshold time.Duration, limit int) ([]StalePendingKey, error)

	// MarkStaleAsFailed updates a stale PENDING key to FAILED status with a timeout reason.
	// This allows the operation to be retried with a new idempotency key.
	MarkStaleAsFailed(ctx context.Context, key StalePendingKey, reason string) error
}

// CleanableService combines Service with Cleaner for full cleanup support.
type CleanableService interface {
	Service
	Cleaner
}
