// Package redislock provides distributed locking backed by Redis.
//
// Two patterns are supported:
//
//   - Per-resource locking via [Lock]: multiple locks keyed by tenant and resource,
//     suitable for preventing concurrent execution of the same task.
//   - Leader election via [Leader]: a single lock for leader/follower coordination
//     across replicas.
//
// Both patterns use background renewal goroutines to keep locks alive during
// long-running operations, with automatic cleanup on context cancellation or
// explicit release.
package redislock
