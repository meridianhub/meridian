package gateway_test

import (
	"sync"
	"testing"
	"time"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateStore_SetAndGet(t *testing.T) {
	store := gateway.NewStateStore(5 * time.Minute)
	tid, _ := tenant.NewTenantID("volterra")

	data := gateway.StateData{
		CodeVerifier: "test-verifier",
		TenantID:     tid,
		ReturnURL:    "https://volterra.demo.meridianhub.cloud/dashboard",
	}

	key, err := store.Set(data)
	require.NoError(t, err)
	assert.NotEmpty(t, key)

	got, ok := store.Get(key)
	assert.True(t, ok)
	assert.Equal(t, data.CodeVerifier, got.CodeVerifier)
	assert.Equal(t, data.TenantID, got.TenantID)
	assert.Equal(t, data.ReturnURL, got.ReturnURL)
}

func TestStateStore_GetDeletesEntry(t *testing.T) {
	store := gateway.NewStateStore(5 * time.Minute)
	tid, _ := tenant.NewTenantID("volterra")

	key, err := store.Set(gateway.StateData{
		CodeVerifier: "v",
		TenantID:     tid,
	})
	require.NoError(t, err)

	_, ok := store.Get(key)
	assert.True(t, ok)

	// Second get returns false (one-time use)
	_, ok = store.Get(key)
	assert.False(t, ok)
}

func TestStateStore_GetUnknownKey(t *testing.T) {
	store := gateway.NewStateStore(5 * time.Minute)

	_, ok := store.Get("nonexistent")
	assert.False(t, ok)
}

func TestStateStore_ExpiredEntry(t *testing.T) {
	store := gateway.NewStateStore(1 * time.Millisecond)
	tid, _ := tenant.NewTenantID("volterra")

	key, err := store.Set(gateway.StateData{
		CodeVerifier: "v",
		TenantID:     tid,
	})
	require.NoError(t, err)

	time.Sleep(5 * time.Millisecond)

	_, ok := store.Get(key)
	assert.False(t, ok, "expired entry should not be returned")
}

func TestStateStore_ConcurrentAccess(t *testing.T) {
	store := gateway.NewStateStore(5 * time.Minute)
	tid, _ := tenant.NewTenantID("volterra")

	var wg sync.WaitGroup
	keys := make(chan string, 50)

	// Concurrent writes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := store.Set(gateway.StateData{
				CodeVerifier: "v",
				TenantID:     tid,
			})
			require.NoError(t, err)
			keys <- key
		}()
	}

	wg.Wait()
	close(keys)

	// All keys should be retrievable exactly once
	for key := range keys {
		_, ok := store.Get(key)
		assert.True(t, ok)
	}
}

func TestStateStore_DefaultTTL(t *testing.T) {
	store := gateway.NewStateStore(0) // should default to 5 min
	tid, _ := tenant.NewTenantID("volterra")

	key, err := store.Set(gateway.StateData{
		CodeVerifier: "v",
		TenantID:     tid,
	})
	require.NoError(t, err)

	_, ok := store.Get(key)
	assert.True(t, ok, "entry should still be valid with default TTL")
}
