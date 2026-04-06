package gateway

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestConsentCodeStore(t *testing.T) *ConsentCodeStore {
	t.Helper()
	s := NewConsentCodeStore()
	t.Cleanup(s.Close)
	return s
}

func TestConsentCodeStore_StoreAndConsume(t *testing.T) {
	s := newTestConsentCodeStore(t)

	entry := ConsentCodeEntry{
		Email:          "alice@example.com",
		TenantID:       "tid-123",
		TenantSlug:     "acme",
		MCPState:       "state-abc",
		ClientID:       "client-1",
		ApprovedScopes: []string{"mcp:default"},
		CreatedAt:      time.Now(),
	}

	code, err := s.Store(entry)
	require.NoError(t, err)
	assert.NotEmpty(t, code)

	// First consume succeeds.
	got, ok := s.Consume(code)
	assert.True(t, ok)
	assert.Equal(t, entry.Email, got.Email)
	assert.Equal(t, entry.TenantID, got.TenantID)
	assert.Equal(t, entry.TenantSlug, got.TenantSlug)
	assert.Equal(t, entry.MCPState, got.MCPState)
	assert.Equal(t, entry.ClientID, got.ClientID)
	assert.Equal(t, entry.ApprovedScopes, got.ApprovedScopes)

	// Second consume fails (one-time use).
	_, ok = s.Consume(code)
	assert.False(t, ok)
}

func TestConsentCodeStore_ConsumeExpired(t *testing.T) {
	s := newTestConsentCodeStore(t)

	entry := ConsentCodeEntry{
		Email:     "expired@example.com",
		CreatedAt: time.Now().Add(-consentCodeTTL - time.Second),
	}

	code, err := s.Store(entry)
	require.NoError(t, err)

	_, ok := s.Consume(code)
	assert.False(t, ok, "expired entry should not be consumable")

	// Entry should have been deleted despite being expired.
	_, ok = s.Consume(code)
	assert.False(t, ok, "expired entry should be cleaned up after first consume attempt")
}

func TestConsentCodeStore_ConcurrentConsume(t *testing.T) {
	s := newTestConsentCodeStore(t)

	entry := ConsentCodeEntry{
		Email:     "concurrent@example.com",
		CreatedAt: time.Now(),
	}

	code, err := s.Store(entry)
	require.NoError(t, err)

	const goroutines = 50
	var (
		wg        sync.WaitGroup
		successes int32
		mu        sync.Mutex
	)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, ok := s.Consume(code)
			if ok {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, int32(1), successes, "exactly one goroutine should consume the code")
}

func TestConsentCodeStore_CapacityLimit(t *testing.T) {
	s := newTestConsentCodeStore(t)

	// Fill to capacity.
	for i := 0; i < consentCodeMaxEntries; i++ {
		_, err := s.Store(ConsentCodeEntry{
			Email:     "fill@example.com",
			CreatedAt: time.Now(),
		})
		require.NoError(t, err)
	}

	// Next store should fail.
	_, err := s.Store(ConsentCodeEntry{
		Email:     "overflow@example.com",
		CreatedAt: time.Now(),
	})
	assert.ErrorIs(t, err, errConsentCodeStoreFull)
}

func TestConsentCodeStore_EvictionLoop(t *testing.T) {
	// Create store with manual control over eviction (don't use the background loop).
	s := &ConsentCodeStore{
		entries: make(map[string]ConsentCodeEntry),
		stop:    make(chan struct{}),
	}
	t.Cleanup(func() { s.closeOnce.Do(func() { close(s.stop) }) })

	// Store an expired entry.
	s.mu.Lock()
	s.entries["expired-code"] = ConsentCodeEntry{
		Email:     "old@example.com",
		CreatedAt: time.Now().Add(-consentCodeTTL - time.Second),
	}
	// Store a valid entry.
	s.entries["valid-code"] = ConsentCodeEntry{
		Email:     "new@example.com",
		CreatedAt: time.Now(),
	}
	s.mu.Unlock()

	// Run eviction.
	s.evictExpired()

	s.mu.Lock()
	defer s.mu.Unlock()
	assert.NotContains(t, s.entries, "expired-code", "expired entry should be evicted")
	assert.Contains(t, s.entries, "valid-code", "valid entry should remain")
}

func TestConsentCodeStore_ConsumeNotFound(t *testing.T) {
	s := newTestConsentCodeStore(t)

	_, ok := s.Consume("nonexistent-code")
	assert.False(t, ok)
}

func TestConsentCodeStore_UniqueCodeGeneration(t *testing.T) {
	s := newTestConsentCodeStore(t)

	codes := make(map[string]struct{})
	for i := 0; i < 100; i++ {
		code, err := s.Store(ConsentCodeEntry{
			Email:     "unique@example.com",
			CreatedAt: time.Now(),
		})
		require.NoError(t, err)
		assert.NotContains(t, codes, code, "generated codes should be unique")
		codes[code] = struct{}{}
	}
}
