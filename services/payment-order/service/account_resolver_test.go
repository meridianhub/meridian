package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockInternalAccountClient is a mock implementation of InternalAccountClient for testing.
type mockInternalAccountClient struct {
	listResponse *internalaccountv1.ListInternalAccountsResponse
	listErr      error
	callCount    int
	mu           sync.Mutex
}

func (m *mockInternalAccountClient) ListInternalAccounts(_ context.Context, _ *internalaccountv1.ListInternalAccountsRequest) (*internalaccountv1.ListInternalAccountsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	return m.listResponse, m.listErr
}

func (m *mockInternalAccountClient) GetBalance(_ context.Context, _ *internalaccountv1.GetBalanceRequest) (*internalaccountv1.GetBalanceResponse, error) {
	return nil, nil
}

func (m *mockInternalAccountClient) RetrieveInternalAccount(_ context.Context, _ *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
	return nil, nil
}

func (m *mockInternalAccountClient) Close() error {
	return nil
}

func (m *mockInternalAccountClient) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

func accountResolverTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Test errors for mock responses
var errAccountResolverTestConnectionRefused = errors.New("connection refused")

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNewAccountResolver_NilClient(t *testing.T) {
	_, err := NewAccountResolver(AccountResolverConfig{
		Client: nil,
		Logger: accountResolverTestLogger(),
	})

	assert.ErrorIs(t, err, ErrAccountResolverClientNil)
}

func TestNewAccountResolver_NilLogger(t *testing.T) {
	mockClient := &mockInternalAccountClient{}

	_, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: nil,
	})

	assert.ErrorIs(t, err, ErrAccountResolverLoggerNil)
}

func TestNewAccountResolver_DefaultCacheTTL(t *testing.T) {
	mockClient := &mockInternalAccountClient{}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
		// CacheTTL not specified - should default to DefaultCacheTTL
	})

	require.NoError(t, err)
	assert.Equal(t, DefaultCacheTTL, resolver.cacheTTL)
}

func TestNewAccountResolver_CustomCacheTTL(t *testing.T) {
	mockClient := &mockInternalAccountClient{}
	customTTL := 10 * time.Minute

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: customTTL,
	})

	require.NoError(t, err)
	assert.Equal(t, customTTL, resolver.cacheTTL)
}

func TestNewAccountResolver_DefaultLookupTimeout(t *testing.T) {
	mockClient := &mockInternalAccountClient{}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
		// LookupTimeout not specified - should default to DefaultLookupTimeout
	})

	require.NoError(t, err)
	assert.Equal(t, DefaultLookupTimeout, resolver.lookupTimeout)
}

func TestNewAccountResolver_CustomLookupTimeout(t *testing.T) {
	mockClient := &mockInternalAccountClient{}
	customTimeout := 5 * time.Second

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:        mockClient,
		Logger:        accountResolverTestLogger(),
		LookupTimeout: customTimeout,
	})

	require.NoError(t, err)
	assert.Equal(t, customTimeout, resolver.lookupTimeout)
}

// =============================================================================
// GetSettlementClearingAccount - Success Cases
// =============================================================================

func TestAccountResolver_GetSettlementClearingAccount_Success(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{
					AccountId:       "settlement-clearing-123",
					AccountCode:     "CLR-GBP-SETTLEMENT",
					Name:            "GBP Settlement Clearing Account",
					ClearingPurpose: internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_SETTLEMENT,
				},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	accountID, err := resolver.GetSettlementClearingAccount(context.Background(), "GBP")

	assert.NoError(t, err)
	assert.Equal(t, "settlement-clearing-123", accountID)
	assert.Equal(t, 1, mockClient.getCallCount())
}

// =============================================================================
// Cache Behavior Tests
// =============================================================================

func TestAccountResolver_GetSettlementClearingAccount_CacheHit(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "settlement-clearing-123"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// First call - cache miss
	accountID1, err := resolver.GetSettlementClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, "settlement-clearing-123", accountID1)

	// Second call - should be cache hit
	accountID2, err := resolver.GetSettlementClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, "settlement-clearing-123", accountID2)

	// Client should only be called once
	assert.Equal(t, 1, mockClient.getCallCount())
}

func TestAccountResolver_GetSettlementClearingAccount_CacheExpiry(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "settlement-clearing-123"},
			},
		},
	}

	// Use very short TTL for testing
	cacheTTL := 10 * time.Millisecond
	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: cacheTTL,
	})
	require.NoError(t, err)

	// First call - cache miss
	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 1, mockClient.getCallCount())

	// Wait for cache to expire (3x safety margin for CI scheduling variance)
	time.Sleep(3 * cacheTTL)

	// Second call - cache expired, should call client again
	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 2, mockClient.getCallCount())
}

// =============================================================================
// Error Cases
// =============================================================================

func TestAccountResolver_GetSettlementClearingAccount_NoClearingAccountFound(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{}, // Empty
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")

	assert.ErrorIs(t, err, ErrNoClearingAccountFound)
	assert.Contains(t, err.Error(), "SETTLEMENT")
	assert.Contains(t, err.Error(), "GBP")
}

func TestAccountResolver_GetSettlementClearingAccount_MultipleClearingAccountsFound(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "settlement-clearing-123", AccountCode: "CLR-GBP-SETTLEMENT-1"},
				{AccountId: "settlement-clearing-456", AccountCode: "CLR-GBP-SETTLEMENT-2"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")

	assert.ErrorIs(t, err, ErrMultipleClearingAccounts)
	assert.Contains(t, err.Error(), "SETTLEMENT")
	assert.Contains(t, err.Error(), "GBP")
	assert.Contains(t, err.Error(), "count: 2")
}

func TestAccountResolver_GetSettlementClearingAccount_EmptyAccountID(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{
					AccountId:   "", // Empty account_id
					AccountCode: "CLR-GBP-SETTLEMENT",
				},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")

	assert.ErrorIs(t, err, ErrEmptyClearingAccountID)
	assert.Contains(t, err.Error(), "SETTLEMENT")
	assert.Contains(t, err.Error(), "GBP")
}

func TestAccountResolver_GetSettlementClearingAccount_NilResponse(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: nil, // Nil response
		listErr:      nil, // No error - but response is nil
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")

	assert.ErrorIs(t, err, ErrNilClearingAccountResponse)
	assert.Contains(t, err.Error(), "SETTLEMENT")
	assert.Contains(t, err.Error(), "GBP")
}

func TestAccountResolver_GetSettlementClearingAccount_ClientError(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listErr: errAccountResolverTestConnectionRefused,
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")

	assert.Error(t, err)
	assert.ErrorIs(t, err, errAccountResolverTestConnectionRefused)
}

// =============================================================================
// Cache Management Tests
// =============================================================================

func TestAccountResolver_InvalidateCache(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "settlement-clearing-123"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// First call - cache miss
	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 1, mockClient.getCallCount())

	// Second call - cache hit
	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 1, mockClient.getCallCount())

	// Invalidate cache
	resolver.InvalidateCache()

	// Third call - cache invalidated, should call client again
	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 2, mockClient.getCallCount())
}

func TestAccountResolver_InvalidateCacheEntry(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "settlement-clearing-123"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// Populate cache for GBP and USD
	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	_, err = resolver.GetSettlementClearingAccount(context.Background(), "USD")
	require.NoError(t, err)
	assert.Equal(t, 2, mockClient.getCallCount())

	// Invalidate only GBP entry
	resolver.InvalidateCacheEntry(ClearingAccountTypeSettlement, "GBP")

	// GBP should trigger new lookup
	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 3, mockClient.getCallCount())

	// USD should still be cached
	_, err = resolver.GetSettlementClearingAccount(context.Background(), "USD")
	require.NoError(t, err)
	assert.Equal(t, 3, mockClient.getCallCount()) // No additional call
}

// =============================================================================
// Multiple Instrument Tests
// =============================================================================

func TestAccountResolver_DifferentInstrumentCodes(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "settlement-clearing-123"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	// Query different instruments
	instruments := []string{"GBP", "USD", "EUR", "KWH"}
	for _, instrument := range instruments {
		_, err := resolver.GetSettlementClearingAccount(context.Background(), instrument)
		require.NoError(t, err)
	}

	// Each unique instrument should trigger one client call
	assert.Equal(t, 4, mockClient.getCallCount())
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestAccountResolver_ConcurrentAccess(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "settlement-clearing-123"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// Run concurrent requests for the same instrument
	var wg sync.WaitGroup
	errChan := make(chan error, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := resolver.GetSettlementClearingAccount(context.Background(), "GBP")
			if err != nil {
				errChan <- err
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// No errors should occur
	for err := range errChan {
		t.Errorf("unexpected error: %v", err)
	}

	// Due to singleflight + caching, client should be called a small number of times
	// (exact count depends on timing, but should be much less than 100)
	assert.Less(t, mockClient.getCallCount(), 10, "Singleflight + cache should reduce client calls significantly")
}

func TestAccountResolver_ConcurrentAccessDifferentInstruments(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "settlement-clearing-123"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	instruments := []string{"GBP", "USD", "EUR", "JPY"}
	var wg sync.WaitGroup
	errChan := make(chan error, 400)

	// 25 concurrent requests per instrument
	for _, instrument := range instruments {
		for i := 0; i < 25; i++ {
			wg.Add(1)
			go func(inst string) {
				defer wg.Done()
				_, err := resolver.GetSettlementClearingAccount(context.Background(), inst)
				if err != nil {
					errChan <- err
				}
			}(instrument)
		}
	}

	wg.Wait()
	close(errChan)

	// No errors should occur
	for err := range errChan {
		t.Errorf("unexpected error: %v", err)
	}

	// Should have approximately 4 calls (one per instrument) due to singleflight
	// Allow some slack for timing variations
	assert.LessOrEqual(t, mockClient.getCallCount(), 16, "Singleflight should coalesce concurrent requests per instrument")
}

// =============================================================================
// ClearingAccountType Mapping Tests
// =============================================================================

func TestMapClearingTypeToPurpose_Settlement(t *testing.T) {
	purpose := mapClearingTypeToPurpose(ClearingAccountTypeSettlement)
	assert.Equal(t, internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_SETTLEMENT, purpose)
}

func TestMapClearingTypeToPurpose_Unknown(t *testing.T) {
	// For unknown types, should return UNSPECIFIED
	purpose := mapClearingTypeToPurpose(ClearingAccountType("UNKNOWN"))
	assert.Equal(t, internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED, purpose)
}

// =============================================================================
// Cache Key Tests
// =============================================================================

func TestAccountResolver_CacheKey(t *testing.T) {
	mockClient := &mockInternalAccountClient{}
	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	key := resolver.cacheKey(ClearingAccountTypeSettlement, "GBP")
	assert.Equal(t, "SETTLEMENT:GBP", key)
}
