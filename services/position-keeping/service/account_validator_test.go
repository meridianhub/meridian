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
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/shared/platform/await"
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

// --- Internal Account Validator Tests ---

// mockInternalAccountClient is a test double for the Internal Account client
type mockInternalAccountClient struct {
	retrieveFunc  func(ctx context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error)
	callCount     atomic.Int32
	callCountLock sync.Mutex
	calls         []string
}

func (m *mockInternalAccountClient) RetrieveInternalAccount(ctx context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
	m.callCount.Add(1)
	m.callCountLock.Lock()
	m.calls = append(m.calls, req.GetAccountId())
	m.callCountLock.Unlock()
	if m.retrieveFunc != nil {
		return m.retrieveFunc(ctx, req)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockInternalAccountClient) GetCallCount() int {
	return int(m.callCount.Load())
}

func TestNewInternalAccountValidator(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockInternalAccountClient{}

	t.Run("creates validator with valid config", func(t *testing.T) {
		validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
			Client: client,
			Logger: logger,
		})

		require.NoError(t, err)
		assert.NotNil(t, validator)
	})

	t.Run("returns error when client is nil", func(t *testing.T) {
		validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
			Client: nil,
			Logger: logger,
		})

		require.Error(t, err)
		assert.Nil(t, validator)
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.Contains(t, err.Error(), "internal account client cannot be nil")
	})

	t.Run("returns error when logger is nil", func(t *testing.T) {
		validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
			Client: client,
			Logger: nil,
		})

		require.Error(t, err)
		assert.Nil(t, validator)
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.Contains(t, err.Error(), "logger cannot be nil")
	})

	t.Run("uses default TTL when not specified", func(t *testing.T) {
		validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
			Client: client,
			Logger: logger,
		})

		require.NoError(t, err)
		assert.Equal(t, DefaultValidationCacheTTL, validator.cacheTTL)
	})

	t.Run("uses custom TTL when specified", func(t *testing.T) {
		customTTL := 10 * time.Minute
		validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
			Client:   client,
			Logger:   logger,
			CacheTTL: customTTL,
		})

		require.NoError(t, err)
		assert.Equal(t, customTTL, validator.cacheTTL)
	})

	t.Run("uses default lookup timeout when not specified", func(t *testing.T) {
		validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
			Client: client,
			Logger: logger,
		})

		require.NoError(t, err)
		assert.Equal(t, DefaultValidationLookupTimeout, validator.lookupTimeout)
	})

	t.Run("uses custom lookup timeout when specified", func(t *testing.T) {
		customTimeout := 5 * time.Second
		validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
			Client:        client,
			Logger:        logger,
			LookupTimeout: customTimeout,
		})

		require.NoError(t, err)
		assert.Equal(t, customTimeout, validator.lookupTimeout)
	})
}

func TestInternalAccountValidator_ValidateExists_ValidAccount(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return &internalaccountv1.RetrieveInternalAccountResponse{
				Facility: &internalaccountv1.InternalAccountFacility{
					AccountId: req.GetAccountId(),
				},
			}, nil
		},
	}

	validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
		Client: client,
		Logger: logger,
	})
	require.NoError(t, err)

	err = validator.ValidateExists(context.Background(), "internal-account-123")

	assert.NoError(t, err)
	assert.Equal(t, 1, client.GetCallCount())
}

func TestInternalAccountValidator_ValidateExists_NotFound(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, _ *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return nil, status.Error(codes.NotFound, "account not found")
		},
	}

	validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
		Client: client,
		Logger: logger,
	})
	require.NoError(t, err)

	err = validator.ValidateExists(context.Background(), "missing-internal-account")

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "internal account not found")
}

func TestInternalAccountValidator_ValidateExists_ServiceUnavailable_GracefulDegradation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, _ *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return nil, status.Error(codes.Unavailable, "service unavailable")
		},
	}

	validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
		Client: client,
		Logger: logger,
	})
	require.NoError(t, err)

	err = validator.ValidateExists(context.Background(), "any-internal-account")
	assert.NoError(t, err) // Graceful degradation
}

func TestInternalAccountValidator_ValidateExists_CacheHit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return &internalaccountv1.RetrieveInternalAccountResponse{
				Facility: &internalaccountv1.InternalAccountFacility{
					AccountId: req.GetAccountId(),
				},
			}, nil
		},
	}

	validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
		Client:   client,
		Logger:   logger,
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// First call - hits service
	err = validator.ValidateExists(context.Background(), "cached-internal-acc")
	require.NoError(t, err)
	assert.Equal(t, 1, client.GetCallCount())

	// Second call - hits cache
	err = validator.ValidateExists(context.Background(), "cached-internal-acc")
	require.NoError(t, err)
	assert.Equal(t, 1, client.GetCallCount()) // No new call
}

func TestInternalAccountValidator_ValidateExists_CachesNotFoundResult(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, _ *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return nil, status.Error(codes.NotFound, "account not found")
		},
	}

	validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
		Client:   client,
		Logger:   logger,
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	// First call - cache miss, hits service
	err1 := validator.ValidateExists(context.Background(), "missing-cached-internal")
	require.Error(t, err1)
	assert.Equal(t, 1, client.GetCallCount())

	// Second call - cache hit (returns cached "not found")
	err2 := validator.ValidateExists(context.Background(), "missing-cached-internal")
	require.Error(t, err2)
	assert.Equal(t, 1, client.GetCallCount()) // No new call
	// Verify cached miss preserves the error contract
	assert.Contains(t, err2.Error(), "not found")
}

func TestInternalAccountValidator_IsCached(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return &internalaccountv1.RetrieveInternalAccountResponse{
				Facility: &internalaccountv1.InternalAccountFacility{
					AccountId: req.GetAccountId(),
				},
			}, nil
		},
	}

	validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
		Client:   client,
		Logger:   logger,
		CacheTTL: 5 * time.Minute,
	})
	require.NoError(t, err)

	t.Run("not cached before validation", func(t *testing.T) {
		assert.False(t, validator.IsCached("uncached-account"))
	})

	t.Run("cached after validation", func(t *testing.T) {
		err := validator.ValidateExists(context.Background(), "to-be-cached")
		require.NoError(t, err)
		assert.True(t, validator.IsCached("to-be-cached"))
	})

	t.Run("not cached for different account", func(t *testing.T) {
		assert.False(t, validator.IsCached("never-validated"))
	})
}

func TestInternalAccountValidator_IsCached_ExpiredEntry(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return &internalaccountv1.RetrieveInternalAccountResponse{
				Facility: &internalaccountv1.InternalAccountFacility{
					AccountId: req.GetAccountId(),
				},
			}, nil
		},
	}

	validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
		Client:   client,
		Logger:   logger,
		CacheTTL: 1 * time.Millisecond, // Very short TTL
	})
	require.NoError(t, err)

	err = validator.ValidateExists(context.Background(), "expiring-account")
	require.NoError(t, err)

	// Poll until cache entry expires
	err = await.New().
		AtMost(1 * time.Second).
		PollInterval(1 * time.Millisecond).
		Until(func() bool {
			return !validator.IsCached("expiring-account")
		})
	require.NoError(t, err)
}

func TestInternalAccountValidator_queryInternalAccount_NilResponse(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	client := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, _ *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			// Return non-nil response but nil Facility
			return &internalaccountv1.RetrieveInternalAccountResponse{
				Facility: nil,
			}, nil
		},
	}

	validator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{
		Client: client,
		Logger: logger,
	})
	require.NoError(t, err)

	// queryInternalAccount should return false when Facility is nil
	exists, queryErr := validator.queryInternalAccount(context.Background(), "nil-facility-account")
	assert.NoError(t, queryErr)
	assert.False(t, exists)
}

func TestCurrentAccountValidator_IsCached(t *testing.T) {
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

	t.Run("not cached before validation", func(t *testing.T) {
		assert.False(t, validator.IsCached("uncached-current-account"))
	})

	t.Run("cached after validation", func(t *testing.T) {
		err := validator.ValidateExists(context.Background(), "current-acc-cached")
		require.NoError(t, err)
		assert.True(t, validator.IsCached("current-acc-cached"))
	})

	t.Run("expired entry returns false", func(t *testing.T) {
		shortTTLValidator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{
			Client:   client,
			Logger:   logger,
			CacheTTL: 1 * time.Millisecond,
		})
		require.NoError(t, err)

		err = shortTTLValidator.ValidateExists(context.Background(), "expiring-current-acc")
		require.NoError(t, err)

		err = await.New().
			AtMost(1 * time.Second).
			PollInterval(1 * time.Millisecond).
			Until(func() bool {
				return !shortTTLValidator.IsCached("expiring-current-acc")
			})
		require.NoError(t, err)
	})
}

// --- Composite Account Validator Tests ---

func TestNewCompositeAccountValidator(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	currentClient := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{AccountId: req.GetAccountId()},
			}, nil
		},
	}
	internalClient := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return &internalaccountv1.RetrieveInternalAccountResponse{
				Facility: &internalaccountv1.InternalAccountFacility{AccountId: req.GetAccountId()},
			}, nil
		},
	}

	currentValidator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{Client: currentClient, Logger: logger})
	require.NoError(t, err)
	internalValidator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{Client: internalClient, Logger: logger})
	require.NoError(t, err)

	t.Run("creates with both validators", func(t *testing.T) {
		composite, err := NewCompositeAccountValidator(CompositeAccountValidatorConfig{
			CurrentAccountValidator:  currentValidator,
			InternalAccountValidator: internalValidator,
			Logger:                   logger,
		})
		require.NoError(t, err)
		assert.NotNil(t, composite)
	})

	t.Run("creates with only current account validator", func(t *testing.T) {
		composite, err := NewCompositeAccountValidator(CompositeAccountValidatorConfig{
			CurrentAccountValidator: currentValidator,
			Logger:                  logger,
		})
		require.NoError(t, err)
		assert.NotNil(t, composite)
	})

	t.Run("creates with only internal account validator", func(t *testing.T) {
		composite, err := NewCompositeAccountValidator(CompositeAccountValidatorConfig{
			InternalAccountValidator: internalValidator,
			Logger:                   logger,
		})
		require.NoError(t, err)
		assert.NotNil(t, composite)
	})

	t.Run("returns error when both validators are nil", func(t *testing.T) {
		composite, err := NewCompositeAccountValidator(CompositeAccountValidatorConfig{
			Logger: logger,
		})
		require.Error(t, err)
		assert.Nil(t, composite)
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.Contains(t, err.Error(), "at least one account validator must be provided")
	})

	t.Run("returns error when logger is nil", func(t *testing.T) {
		composite, err := NewCompositeAccountValidator(CompositeAccountValidatorConfig{
			CurrentAccountValidator: currentValidator,
			Logger:                  nil,
		})
		require.Error(t, err)
		assert.Nil(t, composite)
		assert.Contains(t, err.Error(), "logger cannot be nil")
	})
}

func TestCompositeAccountValidator_ValidateExists_FoundInCurrentAccount(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	currentClient := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return &currentaccountv1.RetrieveCurrentAccountResponse{
				Facility: &currentaccountv1.CurrentAccountFacility{AccountId: req.GetAccountId()},
			}, nil
		},
	}
	internalClient := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, _ *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			t.Fatal("internal account should not be queried when found in current account")
			return nil, nil
		},
	}

	currentValidator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{Client: currentClient, Logger: logger})
	require.NoError(t, err)
	internalValidator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{Client: internalClient, Logger: logger})
	require.NoError(t, err)

	composite, err := NewCompositeAccountValidator(CompositeAccountValidatorConfig{
		CurrentAccountValidator:  currentValidator,
		InternalAccountValidator: internalValidator,
		Logger:                   logger,
	})
	require.NoError(t, err)

	err = composite.ValidateExists(context.Background(), "current-account-id")
	assert.NoError(t, err)
	assert.Equal(t, 1, currentClient.GetCallCount())
	assert.Equal(t, 0, internalClient.GetCallCount())
}

func TestCompositeAccountValidator_ValidateExists_FallbackToInternalAccount(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	currentClient := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	internalClient := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return &internalaccountv1.RetrieveInternalAccountResponse{
				Facility: &internalaccountv1.InternalAccountFacility{AccountId: req.GetAccountId()},
			}, nil
		},
	}

	currentValidator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{Client: currentClient, Logger: logger})
	require.NoError(t, err)
	internalValidator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{Client: internalClient, Logger: logger})
	require.NoError(t, err)

	composite, err := NewCompositeAccountValidator(CompositeAccountValidatorConfig{
		CurrentAccountValidator:  currentValidator,
		InternalAccountValidator: internalValidator,
		Logger:                   logger,
	})
	require.NoError(t, err)

	err = composite.ValidateExists(context.Background(), "internal-account-id")
	assert.NoError(t, err)
	assert.Equal(t, 1, currentClient.GetCallCount())
	assert.Equal(t, 1, internalClient.GetCallCount())
}

func TestCompositeAccountValidator_ValidateExists_NotFoundInAnyService(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	currentClient := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	internalClient := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, _ *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}

	currentValidator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{Client: currentClient, Logger: logger})
	require.NoError(t, err)
	internalValidator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{Client: internalClient, Logger: logger})
	require.NoError(t, err)

	composite, err := NewCompositeAccountValidator(CompositeAccountValidatorConfig{
		CurrentAccountValidator:  currentValidator,
		InternalAccountValidator: internalValidator,
		Logger:                   logger,
	})
	require.NoError(t, err)

	err = composite.ValidateExists(context.Background(), "nonexistent-account")
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "account not found")
}

func TestCompositeAccountValidator_ValidateExists_OnlyInternalValidator(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	internalClient := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return &internalaccountv1.RetrieveInternalAccountResponse{
				Facility: &internalaccountv1.InternalAccountFacility{AccountId: req.GetAccountId()},
			}, nil
		},
	}

	internalValidator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{Client: internalClient, Logger: logger})
	require.NoError(t, err)

	composite, err := NewCompositeAccountValidator(CompositeAccountValidatorConfig{
		InternalAccountValidator: internalValidator,
		Logger:                   logger,
	})
	require.NoError(t, err)

	err = composite.ValidateExists(context.Background(), "internal-only-account")
	assert.NoError(t, err)
	// Verify internal validator was actually called (delegation check)
	assert.Equal(t, 1, internalClient.GetCallCount())
}

func TestCompositeAccountValidator_ResolveServiceDomain(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	currentClient := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			if req.GetAccountId() == "current-acc" {
				return &currentaccountv1.RetrieveCurrentAccountResponse{
					Facility: &currentaccountv1.CurrentAccountFacility{AccountId: req.GetAccountId()},
				}, nil
			}
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	internalClient := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			if req.GetAccountId() == "internal-acc" {
				return &internalaccountv1.RetrieveInternalAccountResponse{
					Facility: &internalaccountv1.InternalAccountFacility{AccountId: req.GetAccountId()},
				}, nil
			}
			return nil, status.Error(codes.NotFound, "not found")
		},
	}

	currentValidator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{Client: currentClient, Logger: logger, CacheTTL: 5 * time.Minute})
	require.NoError(t, err)
	internalValidator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{Client: internalClient, Logger: logger, CacheTTL: 5 * time.Minute})
	require.NoError(t, err)

	composite, err := NewCompositeAccountValidator(CompositeAccountValidatorConfig{
		CurrentAccountValidator:  currentValidator,
		InternalAccountValidator: internalValidator,
		Logger:                   logger,
	})
	require.NoError(t, err)

	t.Run("resolves current account domain after validation", func(t *testing.T) {
		err := composite.ValidateExists(context.Background(), "current-acc")
		require.NoError(t, err)
		domain := composite.ResolveServiceDomain(context.Background(), "current-acc")
		assert.Equal(t, AccountServiceDomainCurrentAccount, domain)
	})

	t.Run("resolves internal account domain after validation", func(t *testing.T) {
		err := composite.ValidateExists(context.Background(), "internal-acc")
		require.NoError(t, err)
		domain := composite.ResolveServiceDomain(context.Background(), "internal-acc")
		assert.Equal(t, AccountServiceDomainInternalAccount, domain)
	})

	t.Run("returns unspecified for unknown account", func(t *testing.T) {
		domain := composite.ResolveServiceDomain(context.Background(), "unknown-acc")
		assert.Equal(t, AccountServiceDomainUnspecified, domain)
	})
}

func TestCompositeAccountValidator_ValidateExists_CurrentAccountServiceUnavailable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	currentClient := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return nil, status.Error(codes.Unavailable, "service unavailable")
		},
	}
	internalClient := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return &internalaccountv1.RetrieveInternalAccountResponse{
				Facility: &internalaccountv1.InternalAccountFacility{AccountId: req.GetAccountId()},
			}, nil
		},
	}

	currentValidator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{Client: currentClient, Logger: logger})
	require.NoError(t, err)
	internalValidator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{Client: internalClient, Logger: logger})
	require.NoError(t, err)

	composite, err := NewCompositeAccountValidator(CompositeAccountValidatorConfig{
		CurrentAccountValidator:  currentValidator,
		InternalAccountValidator: internalValidator,
		Logger:                   logger,
	})
	require.NoError(t, err)

	// When current account service is unavailable, the CurrentAccountValidator returns nil
	// (graceful degradation), so the composite validator also returns nil without trying internal
	err = composite.ValidateExists(context.Background(), "any-account")
	assert.NoError(t, err)
	// Current was attempted, internal was NOT attempted (early return on graceful degradation)
	assert.Equal(t, 1, currentClient.GetCallCount())
	assert.Equal(t, 0, internalClient.GetCallCount())
}

func TestCompositeAccountValidator_ValidateExists_InternalAccountServiceUnavailable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	currentClient := &mockCurrentAccountClient{
		retrieveFunc: func(_ context.Context, _ *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	internalClient := &mockInternalAccountClient{
		retrieveFunc: func(_ context.Context, _ *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
			return nil, status.Error(codes.Unavailable, "service unavailable")
		},
	}

	currentValidator, err := NewCurrentAccountValidator(CurrentAccountValidatorConfig{Client: currentClient, Logger: logger})
	require.NoError(t, err)
	internalValidator, err := NewInternalAccountValidator(InternalAccountValidatorConfig{Client: internalClient, Logger: logger})
	require.NoError(t, err)

	composite, err := NewCompositeAccountValidator(CompositeAccountValidatorConfig{
		CurrentAccountValidator:  currentValidator,
		InternalAccountValidator: internalValidator,
		Logger:                   logger,
	})
	require.NoError(t, err)

	// Not found in current account, internal account service unavailable
	// InternalAccountValidator returns nil (graceful degradation)
	// So composite should also return nil (graceful degradation)
	err = composite.ValidateExists(context.Background(), "any-account")
	assert.NoError(t, err)
	// Both validators were attempted in sequence
	assert.Equal(t, 1, currentClient.GetCallCount())
	assert.Equal(t, 1, internalClient.GetCallCount())
}
