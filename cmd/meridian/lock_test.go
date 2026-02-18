package main

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalLockManager_AcquireAndRelease(t *testing.T) {
	m := newLocalLockManager()
	ctx := context.Background()

	acquired, release, err := m.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired)
	assert.NotNil(t, release)

	release()

	// After release, can acquire again
	acquired, release, err = m.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired)
	assert.NotNil(t, release)
	release()
}

func TestLocalLockManager_DoubleAcquireReturnsFalse(t *testing.T) {
	m := newLocalLockManager()
	ctx := context.Background()

	acquired1, release1, err := m.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired1)
	defer release1()

	// Second acquire on same key should fail
	acquired2, release2, err := m.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.False(t, acquired2)
	assert.Nil(t, release2)
}

func TestLocalLockManager_ReleaseOfUnheldLockIsIdempotent(t *testing.T) {
	m := newLocalLockManager()
	ctx := context.Background()

	acquired, release, err := m.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired)

	// Call release twice - should not panic
	release()
	release()

	// Lock should be acquirable again
	acquired2, release2, err := m.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired2)
	release2()
}

func TestLocalLockManager_DifferentKeysAreIndependent(t *testing.T) {
	m := newLocalLockManager()
	ctx := context.Background()

	acquired1, release1, err := m.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired1)
	defer release1()

	// Different resource on same tenant can be acquired
	acquired2, release2, err := m.Acquire(ctx, "tenant-1", "resource-b")
	require.NoError(t, err)
	assert.True(t, acquired2)
	defer release2()

	// Different tenant, same resource can be acquired
	acquired3, release3, err := m.Acquire(ctx, "tenant-2", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired3)
	defer release3()
}

func TestLocalLockManager_StaleReleaseDoesNotUnlockNewHolder(t *testing.T) {
	m := newLocalLockManager()
	ctx := context.Background()

	// First acquisition
	acquired1, release1, err := m.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired1)

	// Release - lock is now free
	release1()

	// Second acquisition of same key
	acquired2, release2, err := m.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.True(t, acquired2)
	defer release2()

	// Stale release1 called again - must NOT release the new holder's lock
	release1()

	// Lock should still be held: a third acquire must fail
	acquired3, release3, err := m.Acquire(ctx, "tenant-1", "resource-a")
	require.NoError(t, err)
	assert.False(t, acquired3, "stale release should not have freed the lock held by the second acquirer")
	assert.Nil(t, release3)
}

func TestLocalLockManager_ColonInResourceIDNoCollision(t *testing.T) {
	m := newLocalLockManager()
	ctx := context.Background()

	// These two pairs would produce the same string with a ":" delimiter:
	// "tenant" + ":" + "a:b" == "tenant:a" + ":" + "b"
	// The struct key prevents this collision.
	acquired1, release1, err := m.Acquire(ctx, "tenant", "a:b")
	require.NoError(t, err)
	assert.True(t, acquired1)
	defer release1()

	acquired2, release2, err := m.Acquire(ctx, "tenant:a", "b")
	require.NoError(t, err)
	assert.True(t, acquired2, "keys with ':' in resourceID must not collide with different tenantID")
	defer release2()
}

func TestAlwaysLeader_IsLeaderReturnsTrue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l := &alwaysLeader{}
	l.Start(ctx)
	assert.True(t, l.IsLeader())
	l.Stop()
	assert.True(t, l.IsLeader())
}

func TestLocalLockManager_Concurrency(t *testing.T) {
	m := newLocalLockManager()
	ctx := context.Background()

	const goroutines = 50
	acquisitions := make([]bool, goroutines)
	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			acquired, release, err := m.Acquire(ctx, "tenant-1", "resource-a")
			if err == nil && acquired {
				acquisitions[idx] = true
				release()
			}
		}(i)
	}

	wg.Wait()

	// At least one goroutine should have acquired the lock
	var count int
	for _, a := range acquisitions {
		if a {
			count++
		}
	}
	assert.Greater(t, count, 0)
}
