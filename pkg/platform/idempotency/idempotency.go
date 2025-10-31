// Package idempotency provides distributed idempotency checking and locking capabilities
package idempotency

import (
	"context"
	"errors"
	"time"
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
)

// Key represents an idempotency key with namespace and operation context
type Key struct {
	// Namespace groups related operations (e.g., "current-account")
	Namespace string

	// Operation identifies the type of operation (e.g., "deposit", "withdrawal")
	Operation string

	// EntityID is the unique identifier for the entity being operated on (e.g., account ID)
	EntityID string

	// RequestID is an optional client-provided idempotency token
	RequestID string
}

// String returns the Redis key format: {namespace}:{operation}:{entity}:{request}
func (k Key) String() string {
	if k.RequestID != "" {
		return k.Namespace + ":" + k.Operation + ":" + k.EntityID + ":" + k.RequestID
	}
	return k.Namespace + ":" + k.Operation + ":" + k.EntityID
}

// Validate checks if the key has all required fields
func (k Key) Validate() error {
	if k.Namespace == "" || k.Operation == "" || k.EntityID == "" {
		return ErrInvalidKey
	}
	return nil
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
		TTL:        30 * time.Second,
		RetryDelay: 100 * time.Millisecond,
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
