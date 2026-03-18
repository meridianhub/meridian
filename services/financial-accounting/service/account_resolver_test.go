package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/await"
)

// mockInternalAccountClient is a mock implementation for testing.
type mockInternalAccountClient struct {
	listResponse *internalaccountv1.ListInternalAccountsResponse
	listErr      error
	callCount    atomic.Int32
	mu           sync.Mutex

	// For capturing request parameters in tests
	lastRequest *internalaccountv1.ListInternalAccountsRequest

	// For simulating latency
	latency time.Duration
}

func (m *mockInternalAccountClient) ListInternalAccounts(_ context.Context, req *internalaccountv1.ListInternalAccountsRequest) (*internalaccountv1.ListInternalAccountsResponse, error) {
	if m.latency > 0 {
		time.Sleep(m.latency) //nolint:forbidigo // mock: simulates configurable backend latency for singleflight coalescing tests
	}
	m.callCount.Add(1)
	m.mu.Lock()
	m.lastRequest = req
	m.mu.Unlock()
	return m.listResponse, m.listErr
}

func (m *mockInternalAccountClient) RetrieveInternalAccount(_ context.Context, _ *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
	return nil, nil
}

func (m *mockInternalAccountClient) Close() error {
	return nil
}

func (m *mockInternalAccountClient) getCallCount() int {
	return int(m.callCount.Load())
}

func (m *mockInternalAccountClient) getLastRequest() *internalaccountv1.ListInternalAccountsRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastRequest
}

func accountResolverTestLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

// =============================================================================
// Unit tests for basic functionality
// =============================================================================

func TestNewAccountResolver_NilClient_ReturnsError(t *testing.T) {
	_, err := NewAccountResolver(AccountResolverConfig{
		Client: nil,
		Logger: accountResolverTestLogger(),
	})

	assert.ErrorIs(t, err, ErrAccountResolverClientNil)
}

func TestNewAccountResolver_NilLogger_ReturnsError(t *testing.T) {
	mockClient := &mockInternalAccountClient{}

	_, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: nil,
	})

	assert.ErrorIs(t, err, ErrAccountResolverLoggerNil)
}

func TestNewAccountResolver_DefaultConfig(t *testing.T) {
	mockClient := &mockInternalAccountClient{}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
		// CacheTTL not specified - should default to DefaultCacheTTL
		// LookupTimeout not specified - should default to DefaultLookupTimeout
	})

	require.NoError(t, err)
	assert.Equal(t, DefaultCacheTTL, resolver.cacheTTL)
	assert.Equal(t, DefaultLookupTimeout, resolver.lookupTimeout)
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

func TestCacheKeyFormat(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "test-account"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	// Test cache key format for different clearing types
	testCases := []struct {
		clearingType ClearingAccountType
		instrument   string
		expectedKey  string
	}{
		{ClearingAccountTypeDeposit, "GBP", "DEPOSIT:GBP"},
		{ClearingAccountTypeWithdrawal, "USD", "WITHDRAWAL:USD"},
		{ClearingAccountTypeSettlement, "EUR", "SETTLEMENT:EUR"},
		{ClearingAccountTypeDeposit, "KWH", "DEPOSIT:KWH"},
	}

	for _, tc := range testCases {
		t.Run(tc.expectedKey, func(t *testing.T) {
			key := resolver.cacheKey(tc.clearingType, tc.instrument)
			assert.Equal(t, tc.expectedKey, key)
		})
	}
}

// =============================================================================
// Cache behavior tests
// =============================================================================

func TestCacheHit(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-123"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// First call - cache miss, should call backend
	accountID1, err := resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, "clearing-account-123", accountID1)
	assert.Equal(t, 1, mockClient.getCallCount())

	// Second call - should be cache hit, NO backend call
	accountID2, err := resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, "clearing-account-123", accountID2)
	assert.Equal(t, 1, mockClient.getCallCount(), "Cache hit should NOT call backend")
}

func TestCacheMiss_QueriesBackend(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-123"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	// Query different instruments - each should trigger a backend call
	currencies := []string{"GBP", "USD", "EUR"}
	for i, currency := range currencies {
		accountID, err := resolver.GetDepositClearingAccount(context.Background(), currency)
		require.NoError(t, err)
		assert.Equal(t, "clearing-account-123", accountID)
		assert.Equal(t, i+1, mockClient.getCallCount(), "Cache miss for %s should trigger backend call", currency)
	}
}

func TestCacheTTL_Expiration(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-123"},
			},
		},
	}

	// Use very short TTL for testing (50ms)
	shortTTL := 50 * time.Millisecond
	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: shortTTL,
	})
	require.NoError(t, err)

	// First call - cache miss
	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 1, mockClient.getCallCount())

	// Immediate second call - cache hit
	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 1, mockClient.getCallCount(), "Should still be cached")

	// Wait for cache to expire, then verify the next call reaches the backend
	var thirdErr error
	require.NoError(t, await.New().AtMost(500*time.Millisecond).PollInterval(5*time.Millisecond).Until(func() bool {
		_, thirdErr = resolver.GetDepositClearingAccount(context.Background(), "GBP")
		return mockClient.getCallCount() >= 2
	}), "Expired cache should trigger new backend call")

	require.NoError(t, thirdErr)
	assert.GreaterOrEqual(t, mockClient.getCallCount(), 2)
}

func TestCacheStampede_SingleflightCoalescing(t *testing.T) {
	var backendCallCount atomic.Int32

	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-123"},
			},
		},
		// Add latency to simulate slow backend, increasing chance of concurrent requests
		latency: 50 * time.Millisecond,
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// Launch many concurrent requests for the same cache key
	const numGoroutines = 50
	var wg sync.WaitGroup
	errChan := make(chan error, numGoroutines)
	results := make(chan string, numGoroutines)

	// Use a barrier to ensure all goroutines start at roughly the same time
	startBarrier := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startBarrier // Wait for the start signal
			accountID, err := resolver.GetDepositClearingAccount(context.Background(), "GBP")
			if err != nil {
				errChan <- err
				return
			}
			results <- accountID
			backendCallCount.Add(1)
		}()
	}

	// Start all goroutines at the same time
	close(startBarrier)
	wg.Wait()
	close(errChan)
	close(results)

	// No errors should occur
	for err := range errChan {
		t.Errorf("unexpected error: %v", err)
	}

	// All results should be the same
	for accountID := range results {
		assert.Equal(t, "clearing-account-123", accountID)
	}

	// Due to singleflight coalescing, backend should be called only ONCE
	// (or very few times if timing is unlucky)
	actualCalls := mockClient.getCallCount()
	assert.LessOrEqual(t, actualCalls, 3,
		"Singleflight should coalesce concurrent requests: got %d backend calls for %d requests",
		actualCalls, numGoroutines)

	t.Logf("Singleflight coalesced %d concurrent requests into %d backend calls", numGoroutines, actualCalls)
}

// =============================================================================
// Query filtering tests with mock
// =============================================================================

func TestGetDepositClearingAccount_UsesClearingPurposeFilter(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "deposit-clearing-123"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)

	// Verify the request sent to backend has correct filtering
	req := mockClient.getLastRequest()
	require.NotNil(t, req)
	assert.Equal(t, internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_DEPOSIT, req.ClearingPurposeFilter,
		"GetDepositClearingAccount should filter by CLEARING_PURPOSE_DEPOSIT")
	assert.Equal(t, "CLEARING", req.BehaviorClassFilter)
	assert.Equal(t, internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, req.StatusFilter)
	assert.Equal(t, "GBP", req.InstrumentCodeFilter)
}

func TestGetWithdrawalClearingAccount_UsesClearingPurposeFilter(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "withdrawal-clearing-456"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetWithdrawalClearingAccount(context.Background(), "USD")
	require.NoError(t, err)

	// Verify the request sent to backend has correct filtering
	req := mockClient.getLastRequest()
	require.NotNil(t, req)
	assert.Equal(t, internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_WITHDRAWAL, req.ClearingPurposeFilter,
		"GetWithdrawalClearingAccount should filter by CLEARING_PURPOSE_WITHDRAWAL")
	assert.Equal(t, "CLEARING", req.BehaviorClassFilter)
	assert.Equal(t, internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, req.StatusFilter)
	assert.Equal(t, "USD", req.InstrumentCodeFilter)
}

func TestGetSettlementClearingAccount_UsesClearingPurposeFilter(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "settlement-clearing-789"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetSettlementClearingAccount(context.Background(), "EUR")
	require.NoError(t, err)

	// Verify the request sent to backend has correct filtering
	req := mockClient.getLastRequest()
	require.NotNil(t, req)
	assert.Equal(t, internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_SETTLEMENT, req.ClearingPurposeFilter,
		"GetSettlementClearingAccount should filter by CLEARING_PURPOSE_SETTLEMENT")
	assert.Equal(t, "CLEARING", req.BehaviorClassFilter)
	assert.Equal(t, internalaccountv1.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, req.StatusFilter)
	assert.Equal(t, "EUR", req.InstrumentCodeFilter)
}

// =============================================================================
// Error handling tests
// =============================================================================

func TestNoResults_ReturnsError(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{}, // Empty result
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetDepositClearingAccount(context.Background(), "XYZ")

	assert.ErrorIs(t, err, ErrNoClearingAccountFound)
}

var errAccountResolverTestConnectionRefused = errors.New("connection refused")

func TestBackendError_PropagatesError(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listErr: errAccountResolverTestConnectionRefused,
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")

	assert.Error(t, err)
	assert.ErrorIs(t, err, errAccountResolverTestConnectionRefused)
}

// =============================================================================
// Multiple accounts error tests (fail-fast behavior)
// =============================================================================

func TestMultipleAccounts_GetDepositClearingAccount_ReturnsError(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-1"},
				{AccountId: "clearing-account-2"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")

	assert.ErrorIs(t, err, ErrMultipleClearingAccounts)
	assert.Contains(t, err.Error(), "DEPOSIT")
	assert.Contains(t, err.Error(), "GBP")
	assert.Contains(t, err.Error(), "count: 2")
}

func TestMultipleAccounts_GetWithdrawalClearingAccount_ReturnsError(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-1"},
				{AccountId: "clearing-account-2"},
				{AccountId: "clearing-account-3"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetWithdrawalClearingAccount(context.Background(), "USD")

	assert.ErrorIs(t, err, ErrMultipleClearingAccounts)
	assert.Contains(t, err.Error(), "WITHDRAWAL")
	assert.Contains(t, err.Error(), "USD")
	assert.Contains(t, err.Error(), "count: 3")
}

func TestMultipleAccounts_GetSettlementClearingAccount_ReturnsError(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-1"},
				{AccountId: "clearing-account-2"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	_, err = resolver.GetSettlementClearingAccount(context.Background(), "EUR")

	assert.ErrorIs(t, err, ErrMultipleClearingAccounts)
	assert.Contains(t, err.Error(), "SETTLEMENT")
	assert.Contains(t, err.Error(), "EUR")
	assert.Contains(t, err.Error(), "count: 2")
}

func TestMultipleAccounts_DoesNotCache(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-1"},
				{AccountId: "clearing-account-2"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// First call - should fail
	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	assert.ErrorIs(t, err, ErrMultipleClearingAccounts)
	assert.Equal(t, 1, mockClient.getCallCount())

	// Second call - should also fail, and should NOT be cached (client called again)
	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	assert.ErrorIs(t, err, ErrMultipleClearingAccounts)
	assert.Equal(t, 2, mockClient.getCallCount(), "Error responses should not be cached")
}

func TestSingleAccount_StillWorks(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "single-clearing-account"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	// Test deposit
	accountID, err := resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, "single-clearing-account", accountID)

	// Test withdrawal
	accountID, err = resolver.GetWithdrawalClearingAccount(context.Background(), "USD")
	require.NoError(t, err)
	assert.Equal(t, "single-clearing-account", accountID)

	// Test settlement
	accountID, err = resolver.GetSettlementClearingAccount(context.Background(), "EUR")
	require.NoError(t, err)
	assert.Equal(t, "single-clearing-account", accountID)
}

// =============================================================================
// Additional tests (from current-account patterns)
// =============================================================================

func TestAccountResolver_InvalidateCache(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-123"},
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
	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 1, mockClient.getCallCount())

	// Second call - cache hit
	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 1, mockClient.getCallCount())

	// Invalidate cache
	resolver.InvalidateCache()

	// Third call - cache invalidated, should call client again
	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 2, mockClient.getCallCount())
}

func TestAccountResolver_InvalidateCacheEntry(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-123"},
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
	_, _ = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	_, _ = resolver.GetDepositClearingAccount(context.Background(), "USD")
	assert.Equal(t, 2, mockClient.getCallCount())

	// Invalidate only GBP entry
	resolver.InvalidateCacheEntry(ClearingAccountTypeDeposit, "GBP")

	// GBP should trigger new lookup
	_, _ = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	assert.Equal(t, 3, mockClient.getCallCount())

	// USD should still be cached
	_, _ = resolver.GetDepositClearingAccount(context.Background(), "USD")
	assert.Equal(t, 3, mockClient.getCallCount()) // No additional call
}

func TestAccountResolver_DepositVsWithdrawalVsSettlement_SeparateCacheKeys(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-123"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// Query all three clearing types for GBP
	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 1, mockClient.getCallCount())

	_, err = resolver.GetWithdrawalClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 2, mockClient.getCallCount(), "Withdrawal should be separate cache entry")

	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 3, mockClient.getCallCount(), "Settlement should be separate cache entry")

	// Query each again - all should hit cache
	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	_, err = resolver.GetWithdrawalClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	_, err = resolver.GetSettlementClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 3, mockClient.getCallCount(), "All should hit cache")
}

func TestAccountResolver_ConcurrentAccess_NoRace(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-123"},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// Run concurrent requests with different methods
	var wg sync.WaitGroup
	errChan := make(chan error, 150)

	// 50 deposit requests
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := resolver.GetDepositClearingAccount(context.Background(), "GBP")
			if err != nil {
				errChan <- err
			}
		}()
	}

	// 50 withdrawal requests
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := resolver.GetWithdrawalClearingAccount(context.Background(), "GBP")
			if err != nil {
				errChan <- err
			}
		}()
	}

	// 50 settlement requests
	for i := 0; i < 50; i++ {
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

	// Client should be called a reasonable number of times (not 150)
	assert.Less(t, mockClient.getCallCount(), 15, "Cache should reduce client calls significantly")
}

func TestMapClearingTypeToPurpose(t *testing.T) {
	testCases := []struct {
		name         string
		clearingType ClearingAccountType
		expected     internalaccountv1.ClearingPurpose
	}{
		{
			name:         "deposit maps to CLEARING_PURPOSE_DEPOSIT",
			clearingType: ClearingAccountTypeDeposit,
			expected:     internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_DEPOSIT,
		},
		{
			name:         "withdrawal maps to CLEARING_PURPOSE_WITHDRAWAL",
			clearingType: ClearingAccountTypeWithdrawal,
			expected:     internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_WITHDRAWAL,
		},
		{
			name:         "settlement maps to CLEARING_PURPOSE_SETTLEMENT",
			clearingType: ClearingAccountTypeSettlement,
			expected:     internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_SETTLEMENT,
		},
		{
			name:         "unknown type maps to CLEARING_PURPOSE_UNSPECIFIED",
			clearingType: ClearingAccountType("UNKNOWN"),
			expected:     internalaccountv1.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := mapClearingTypeToPurpose(tc.clearingType)
			assert.Equal(t, tc.expected, result)
		})
	}
}
