// Package idempotency provides distributed idempotency checking and locking capabilities.
//
// The package prevents duplicate processing of operations that carry an idempotency key
// by persisting results and acquiring distributed locks before execution. Two backends
// are provided: a PostgreSQL-backed service for durable storage and a no-op service for
// testing.
//
// # Usage
//
//	executor := idempotency.NewExecutor(service, lock)
//	result, err := executor.Execute(ctx, key, func(ctx context.Context) (any, error) {
//	    return processPayment(ctx, req)
//	})
//
// Sentinel errors:
//   - [ErrOperationAlreadyProcessed]: key was seen before; cached result is returned
//   - [ErrLockAcquisitionFailed]: concurrent caller holds the lock
package idempotency
