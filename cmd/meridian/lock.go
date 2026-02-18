// Package main provides the unified Meridian binary entry point.
package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/meridianhub/meridian/shared/platform/scheduler"
)

// Compile-time assertion: localLockManager satisfies the scheduler.DistributedLock interface.
var _ scheduler.DistributedLock = (*localLockManager)(nil)

// localLockManager is an in-process mutex-based lock manager satisfying
// the scheduler.DistributedLock interface. It is suitable for single-process
// deployments where Redis is not available.
type localLockManager struct {
	mu    sync.Mutex
	locks map[string]bool
}

func newLocalLockManager() *localLockManager {
	return &localLockManager{locks: make(map[string]bool)}
}

func (m *localLockManager) key(tenantID, resourceID string) string {
	return fmt.Sprintf("%s:%s", tenantID, resourceID)
}

// Acquire attempts to acquire the lock for the given tenant and resource.
// Returns (true, release, nil) if acquired, (false, nil, nil) if already held.
func (m *localLockManager) Acquire(_ context.Context, tenantID, resourceID string) (bool, func(), error) {
	k := m.key(tenantID, resourceID)

	m.mu.Lock()
	if m.locks[k] {
		m.mu.Unlock()
		return false, nil, nil
	}
	m.locks[k] = true
	m.mu.Unlock()

	release := func() {
		m.mu.Lock()
		delete(m.locks, k)
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
