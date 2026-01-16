package service

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockCurrentAccountClient is a test double for the Current Account client
type mockCurrentAccountClient struct {
	retrieveFunc  func(ctx context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error)
	callCount     atomic.Int32
	callCountLock sync.Mutex
	calls         []string // Track account IDs that were queried
}

func (m *mockCurrentAccountClient) RetrieveCurrentAccount(ctx context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
	m.callCount.Add(1)
	m.callCountLock.Lock()
	m.calls = append(m.calls, req.GetAccountId())
	m.callCountLock.Unlock()
	if m.retrieveFunc != nil {
		return m.retrieveFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockCurrentAccountClient) GetCallCount() int {
	return int(m.callCount.Load())
}

func (m *mockCurrentAccountClient) GetCalls() []string {
	m.callCountLock.Lock()
	defer m.callCountLock.Unlock()
	result := make([]string, len(m.calls))
	copy(result, m.calls)
	return result
}

func TestNewCurrentAccountValidator(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockCurrentAccountClient{}

	t.Run("creates validator with valid config", func(t *testing.T) {
		validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
			Client: client,
			Logger: logger,
		})

		require.NoError(t, err)
		assert.NotNil(t, validator)
	})

	t.Run("returns error when client is nil", func(t *testing.T) {
		validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
			Client: nil,
			Logger: logger,
		})

		require.Error(t, err)
		assert.Nil(t, validator)
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.Contains(t, err.Error(), "client cannot be nil")
	})

	t.Run("returns error when logger is nil", func(t *testing.T) {
		validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
			Client: client,
			Logger: nil,
		})

		require.Error(t, err)
		assert.Nil(t, validator)
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.Contains(t, err.Error(), "logger cannot be nil")
	})

	t.Run("uses default TTL when not specified", func(t *testing.T) {
		validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
			Client: client,
			Logger: logger,
		})

		require.NoError(t, err)
		assert.Equal(t, DefaultValidationCacheTTL, validator.cacheTTL)
	})

	t.Run("uses custom TTL when specified", func(t *testing.T) {
		customTTL := 5 * time.Minute
		validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
			Client:   client,
			Logger:   logger,
			CacheTTL: customTTL,
		})

		require.NoError(t, err)
		assert.Equal(t, customTTL, validator.cacheTTL)
	})

	t.Run("uses default lookup timeout when not specified", func(t *testing.T) {
		validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
			Client: client,
			Logger: logger,
		})

		require.NoError(t, err)
		assert.Equal(t, DefaultValidationLookupTimeout, validator.lookupTimeout)
	})
}

func TestValidateExists_ValidAccount(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{
					AccountId: req.GetAccountId(),
				},
			}, nil
		},
	}

	validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
		Client: client,
		Logger: logger,
	})
	require.NoError(t, err)

	err = validator.ValidateExists(context.Background(), "valid-account-123")

	assert.NoError(t, err)
	assert.Equal(t, 1, client.GetCallCount())
}

func TestValidateExists_InvalidAccount(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return nil, status.Error(codes.NotFound, "account not found")
		},
	}

	validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
		Client: client,
		Logger: logger,
	})
	require.NoError(t, err)

	err = validator.ValidateExists(context.Background(), "invalid-account-123")

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "account not found")
	assert.Contains(t, err.Error(), "invalid-account-123")
}

func TestValidateExists_CacheHit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{
					AccountId: req.GetAccountId(),
				},
			}, nil
		},
	}

	validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
		Client:   client,
		Logger:   logger,
		CacheTTL: 5 * time.Minute, // Long TTL to ensure cache hit
	})
	require.NoError(t, err)

	// First call - should hit the service
	err = validator.ValidateExists(context.Background(), "cached-account-123")
	require.NoError(t, err)
	assert.Equal(t, 1, client.GetCallCount())

	// Second call - should hit cache
	err = validator.ValidateExists(context.Background(), "cached-account-123")
	require.NoError(t, err)
	assert.Equal(t, 1, client.GetCallCount()) // Count should NOT increase
}

func TestValidateExists_CacheMiss(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{
					AccountId: req.GetAccountId(),
				},
			}, nil
		},
	}

	validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
		Client:   client,
		Logger:   logger,
		CacheTTL: 1 * time.Millisecond, // Very short TTL to ensure cache miss
	})
	require.NoError(t, err)

	// First call
	err = validator.ValidateExists(context.Background(), "cache-miss-account")
	require.NoError(t, err)
	assert.Equal(t, 1, client.GetCallCount())

	// Wait for cache to expire
	time.Sleep(5 * time.Millisecond)

	// Second call - cache should have expired
	err = validator.ValidateExists(context.Background(), "cache-miss-account")
	require.NoError(t, err)
	assert.Equal(t, 2, client.GetCallCount()) // Count should increase
}

func TestValidateExists_ServiceUnavailable_GracefulDegradation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return nil, status.Error(codes.Unavailable, "service unavailable")
		},
	}

	validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
		Client: client,
		Logger: logger,
	})
	require.NoError(t, err)

	// Service is unavailable - validation should pass (graceful degradation)
	err = validator.ValidateExists(context.Background(), "any-account-123")

	assert.NoError(t, err) // Should NOT return error - graceful degradation
}

func TestValidateExists_InternalError_GracefulDegradation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return nil, status.Error(codes.Internal, "internal server error")
		},
	}

	validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
		Client: client,
		Logger: logger,
	})
	require.NoError(t, err)

	// Internal error - validation should pass (graceful degradation)
	err = validator.ValidateExists(context.Background(), "any-account-123")

	assert.NoError(t, err) // Should NOT return error - graceful degradation
}

func TestValidateExists_CachesInvalidAccountResult(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return nil, status.Error(codes.NotFound, "account not found")
		},
	}

	validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
		Client:   client,
		Logger:   logger,
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// First call - should hit the service and cache the "not found" result
	err1 := validator.ValidateExists(context.Background(), "invalid-cached-account")
	require.Error(t, err1)
	assert.Equal(t, 1, client.GetCallCount())

	// Second call - should hit cache (returning "not found" from cache)
	err2 := validator.ValidateExists(context.Background(), "invalid-cached-account")
	require.Error(t, err2)
	assert.Equal(t, 1, client.GetCallCount()) // Count should NOT increase
}

func TestValidateExists_ConcurrentRequests_NoStampede(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	callCount := atomic.Int32{}

	client := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			callCount.Add(1)
			// Simulate slow service response
			time.Sleep(50 * time.Millisecond)
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{
					AccountId: req.GetAccountId(),
				},
			}, nil
		},
	}

	validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
		Client:        client,
		Logger:        logger,
		LookupTimeout: 5 * time.Second,
	})
	require.NoError(t, err)

	// Start multiple concurrent validations for the same account
	var wg sync.WaitGroup
	const numGoroutines = 10
	errors := make([]error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errors[idx] = validator.ValidateExists(context.Background(), "stampede-test-account")
		}(i)
	}

	wg.Wait()

	// All validations should succeed
	for i, err := range errors {
		assert.NoError(t, err, "goroutine %d failed", i)
	}

	// Service should only be called once due to singleflight
	assert.Equal(t, int32(1), callCount.Load(), "singleflight should coalesce requests")
}

func TestValidateExists_DifferentAccounts_SeparateCalls(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{
					AccountId: req.GetAccountId(),
				},
			}, nil
		},
	}

	validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
		Client: client,
		Logger: logger,
	})
	require.NoError(t, err)

	// Validate different accounts
	err = validator.ValidateExists(context.Background(), "account-1")
	require.NoError(t, err)

	err = validator.ValidateExists(context.Background(), "account-2")
	require.NoError(t, err)

	err = validator.ValidateExists(context.Background(), "account-3")
	require.NoError(t, err)

	// Each account should trigger a separate service call
	assert.Equal(t, 3, client.GetCallCount())
	calls := client.GetCalls()
	assert.Contains(t, calls, "account-1")
	assert.Contains(t, calls, "account-2")
	assert.Contains(t, calls, "account-3")
}

func TestInvalidateCache(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{
					AccountId: req.GetAccountId(),
				},
			}, nil
		},
	}

	validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
		Client:   client,
		Logger:   logger,
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// Populate cache
	err = validator.ValidateExists(context.Background(), "cache-invalidate-test")
	require.NoError(t, err)
	assert.Equal(t, 1, client.GetCallCount())

	// Verify cache is hit
	err = validator.ValidateExists(context.Background(), "cache-invalidate-test")
	require.NoError(t, err)
	assert.Equal(t, 1, client.GetCallCount())

	// Invalidate entire cache
	validator.InvalidateCache()

	// Next call should hit service again
	err = validator.ValidateExists(context.Background(), "cache-invalidate-test")
	require.NoError(t, err)
	assert.Equal(t, 2, client.GetCallCount())
}

func TestInvalidateCacheEntry(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{
					AccountId: req.GetAccountId(),
				},
			}, nil
		},
	}

	validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
		Client:   client,
		Logger:   logger,
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// Populate cache with two accounts
	err = validator.ValidateExists(context.Background(), "account-keep")
	require.NoError(t, err)
	err = validator.ValidateExists(context.Background(), "account-remove")
	require.NoError(t, err)
	assert.Equal(t, 2, client.GetCallCount())

	// Invalidate only one entry
	validator.InvalidateCacheEntry("account-remove")

	// account-keep should still be cached
	err = validator.ValidateExists(context.Background(), "account-keep")
	require.NoError(t, err)
	assert.Equal(t, 2, client.GetCallCount()) // No new call

	// account-remove should trigger new call
	err = validator.ValidateExists(context.Background(), "account-remove")
	require.NoError(t, err)
	assert.Equal(t, 3, client.GetCallCount()) // New call made
}

func TestValidateExists_ContextTimeout(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockCurrentAccountClient{
		retrieveFunc: func(ctx context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			// Simulate slow response that exceeds timeout
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
				return &currentaccountv1.RetrieveCurrentAccountResponse{
					Facility: &currentaccountv1.CurrentAccountFacility{
						AccountId: req.GetAccountId(),
					},
				}, nil
			}
		},
	}

	validator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
		Client:        client,
		Logger:        logger,
		LookupTimeout: 50 * time.Millisecond, // Very short timeout
	})
	require.NoError(t, err)

	// Validation should gracefully degrade (return nil) when timeout occurs
	err = validator.ValidateExists(context.Background(), "timeout-test-account")

	assert.NoError(t, err) // Graceful degradation - allow operation
}
