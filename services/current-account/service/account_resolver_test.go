package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
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
	callCount    int
	mu           sync.Mutex
}

func (m *mockInternalAccountClient) ListInternalAccounts(_ context.Context, _ *internalaccountv1.ListInternalAccountsRequest) (*internalaccountv1.ListInternalAccountsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++
	return m.listResponse, m.listErr
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
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

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

func TestAccountResolver_GetDepositClearingAccount_Success(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{
					AccountId:   "clearing-account-123",
					AccountCode: "CLR-GBP-001",
					Name:        "GBP Clearing Account",
				},
			},
		},
	}

	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client: mockClient,
		Logger: accountResolverTestLogger(),
	})
	require.NoError(t, err)

	accountID, err := resolver.GetDepositClearingAccount(context.Background(), "GBP")

	assert.NoError(t, err)
	assert.Equal(t, "clearing-account-123", accountID)
	assert.Equal(t, 1, mockClient.getCallCount())
}

func TestAccountResolver_GetDepositClearingAccount_CacheHit(t *testing.T) {
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
	accountID1, err := resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, "clearing-account-123", accountID1)

	// Second call - should be cache hit
	accountID2, err := resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, "clearing-account-123", accountID2)

	// Client should only be called once
	assert.Equal(t, 1, mockClient.getCallCount())
}

func TestAccountResolver_GetDepositClearingAccount_CacheExpiry(t *testing.T) {
	mockClient := &mockInternalAccountClient{
		listResponse: &internalaccountv1.ListInternalAccountsResponse{
			Facilities: []*internalaccountv1.InternalAccountFacility{
				{AccountId: "clearing-account-123"},
			},
		},
	}

	// Use very short TTL for testing
	resolver, err := NewAccountResolver(AccountResolverConfig{
		Client:   mockClient,
		Logger:   accountResolverTestLogger(),
		CacheTTL: 10 * time.Millisecond,
	})
	require.NoError(t, err)

	// First call - cache miss
	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 1, mockClient.getCallCount())

	// Wait for cache to expire, then verify the next call reaches the client
	var secondErr error
	require.NoError(t, await.New().AtMost(500*time.Millisecond).PollInterval(5*time.Millisecond).Until(func() bool {
		_, secondErr = resolver.GetDepositClearingAccount(context.Background(), "GBP")
		return mockClient.getCallCount() >= 2
	}), "cache should have expired and triggered a new client call")

	require.NoError(t, secondErr)
	assert.GreaterOrEqual(t, mockClient.getCallCount(), 2)
}

func TestAccountResolver_GetDepositClearingAccount_NoClearingAccountFound(t *testing.T) {
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

	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")

	assert.ErrorIs(t, err, ErrNoClearingAccountFound)
}

var errAccountResolverTestConnectionRefused = errors.New("connection refused")

func TestAccountResolver_GetDepositClearingAccount_ClientError(t *testing.T) {
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

func TestAccountResolver_GetWithdrawalClearingAccount_Success(t *testing.T) {
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

	accountID, err := resolver.GetWithdrawalClearingAccount(context.Background(), "USD")

	assert.NoError(t, err)
	assert.Equal(t, "withdrawal-clearing-456", accountID)
}

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

func TestAccountResolver_DifferentInstrumentCodes(t *testing.T) {
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

	// Query different instruments
	currencies := []string{"GBP", "USD", "EUR", "KWH"}
	for _, currency := range currencies {
		_, err := resolver.GetDepositClearingAccount(context.Background(), currency)
		require.NoError(t, err)
	}

	// Each unique instrument should trigger one client call
	assert.Equal(t, 4, mockClient.getCallCount())
}

func TestAccountResolver_ConcurrentAccess(t *testing.T) {
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

	// Run concurrent requests
	var wg sync.WaitGroup
	errChan := make(chan error, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := resolver.GetDepositClearingAccount(context.Background(), "GBP")
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

	// Due to caching, client should be called a small number of times
	// (exact count depends on timing, but should be much less than 100)
	assert.Less(t, mockClient.getCallCount(), 10, "Cache should reduce client calls significantly")
}

func TestAccountResolver_DepositVsWithdrawal_SeparateCacheKeys(t *testing.T) {
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

	// Query deposit clearing for GBP
	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 1, mockClient.getCallCount())

	// Query withdrawal clearing for GBP - should be separate cache entry
	_, err = resolver.GetWithdrawalClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 2, mockClient.getCallCount())

	// Query deposit again - should hit cache
	_, err = resolver.GetDepositClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 2, mockClient.getCallCount())

	// Query withdrawal again - should hit cache
	_, err = resolver.GetWithdrawalClearingAccount(context.Background(), "GBP")
	require.NoError(t, err)
	assert.Equal(t, 2, mockClient.getCallCount())
}
