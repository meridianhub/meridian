// Package main provides the unified Meridian binary entry point.
package main

import (
	"context"
	"sync"

	"github.com/meridianhub/meridian/shared/platform/scheduler"
)

// Compile-time assertion: localLockManager satisfies the scheduler.DistributedLock interface.
var _ scheduler.DistributedLock = (*localLockManager)(nil)

// lockKey is a composite key for per-resource locks. Using a struct avoids
// delimiter-based collisions when tenantID or resourceID contain ":".
type lockKey struct {
	tenantID   string
	resourceID string
}

// localLockManager is an in-process mutex-based lock manager satisfying
// the scheduler.DistributedLock interface. It is suitable for single-process
// deployments where Redis is not available.
type localLockManager struct {
	mu        sync.Mutex
	locks     map[lockKey]uint64
	nextToken uint64
}

func newLocalLockManager() *localLockManager {
	return &localLockManager{locks: make(map[lockKey]uint64)}
}

// Acquire attempts to acquire the lock for the given tenant and resource.
// Returns (true, release, nil) if acquired, (false, nil, nil) if already held.
// The release function is safe to call multiple times; only the first call from
// the original acquirer has effect - stale releases are ignored via token check.
func (m *localLockManager) Acquire(_ context.Context, tenantID, resourceID string) (bool, func(), error) {
	k := lockKey{tenantID: tenantID, resourceID: resourceID}

	m.mu.Lock()
	if _, ok := m.locks[k]; ok {
		m.mu.Unlock()
		return false, nil, nil
	}
	m.nextToken++
	token := m.nextToken
	m.locks[k] = token
	m.mu.Unlock()

	release := func() {
		m.mu.Lock()
		if m.locks[k] == token {
			delete(m.locks, k)
		}
		m.mu.Unlock()
	}
	return true, release, nil
}

// alwaysLeader is a no-op leader election stub that always reports the current
// instance as leader. Valid for single-process deployments where there is only
// one instance to coordinate.
type alwaysLeader struct{}

func (a *alwaysLeader) IsLeader() bool          { return true }
func (a *alwaysLeader) Start(_ context.Context) {}
func (a *alwaysLeader) Stop()                   {}
