package service

import (
	"context"
	"errors"
	"time"
)

// Lock represents a distributed lock that must be released after use.
type Lock interface {
	// Release releases the lock.
	Release(ctx context.Context) error
}

// LockClient provides distributed locking to prevent concurrent operations.
// Used to prevent race conditions in lien execution status updates across multiple service instances.
type LockClient interface {
	// Obtain attempts to acquire a distributed lock with the given key and TTL.
	// Returns ErrLockNotObtained if the lock is already held by another process.
	// The lock must be released by calling Release() on the returned Lock.
	Obtain(ctx context.Context, key string, ttl time.Duration) (Lock, error)
}

// LockNotObtainedError is returned when a lock cannot be acquired because it's held by another process.
type LockNotObtainedError struct{}

func (e LockNotObtainedError) Error() string {
	return "lock not obtained: already held by another process"
}

// IsLockNotObtained checks if an error indicates lock contention.
func IsLockNotObtained(err error) bool {
	var lockErr LockNotObtainedError
	return errors.As(err, &lockErr)
}
