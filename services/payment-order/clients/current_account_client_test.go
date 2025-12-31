package clients

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errCloseFailed = errors.New("close failed")

// mockCurrentAccountGRPCClient implements currentAccountGRPCClient for testing
type mockCurrentAccountGRPCClient struct {
	initiateLienFn  func(ctx context.Context, req *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error)
	terminateLienFn func(ctx context.Context, req *currentaccountv1.TerminateLienRequest) (*currentaccountv1.TerminateLienResponse, error)
	executeLienFn   func(ctx context.Context, req *currentaccountv1.ExecuteLienRequest) (*currentaccountv1.ExecuteLienResponse, error)
	closeFn         func() error
}

func (m *mockCurrentAccountGRPCClient) InitiateLien(ctx context.Context, in *currentaccountv1.InitiateLienRequest, _ ...grpc.CallOption) (*currentaccountv1.InitiateLienResponse, error) {
	if m.initiateLienFn != nil {
		return m.initiateLienFn(ctx, in)
	}
	return &currentaccountv1.InitiateLienResponse{}, nil
}

func (m *mockCurrentAccountGRPCClient) TerminateLien(ctx context.Context, in *currentaccountv1.TerminateLienRequest, _ ...grpc.CallOption) (*currentaccountv1.TerminateLienResponse, error) {
	if m.terminateLienFn != nil {
		return m.terminateLienFn(ctx, in)
	}
	return &currentaccountv1.TerminateLienResponse{}, nil
}

func (m *mockCurrentAccountGRPCClient) ExecuteLien(ctx context.Context, in *currentaccountv1.ExecuteLienRequest, _ ...grpc.CallOption) (*currentaccountv1.ExecuteLienResponse, error) {
	if m.executeLienFn != nil {
		return m.executeLienFn(ctx, in)
	}
	return &currentaccountv1.ExecuteLienResponse{}, nil
}

func (m *mockCurrentAccountGRPCClient) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

// TestNewResilientCurrentAccountClient_Success verifies client creation
func TestNewResilientCurrentAccountClient_Success(t *testing.T) {
	t.Parallel()

	mockClient := &mockCurrentAccountGRPCClient{}
	config := sharedclients.ResilientClientConfig{
		CircuitBreakerName: "test-service",
		MaxRequests:        1,
		FailureThreshold:   5,
		MaxRetries:         3,
		InitialInterval:    100 * time.Millisecond,
		Logger:             slog.Default(),
	}

	resilientClient := newResilientCurrentAccountClientForTesting(mockClient, config)

	require.NotNil(t, resilientClient)
	assert.NoError(t, resilientClient.Close())
}

// TestNewResilientCurrentAccountClient_DefaultConfig verifies defaults are applied
func TestNewResilientCurrentAccountClient_DefaultConfig(t *testing.T) {
	t.Parallel()

	mockClient := &mockCurrentAccountGRPCClient{}
	config := sharedclients.ResilientClientConfig{}

	resilientClient := newResilientCurrentAccountClientForTesting(mockClient, config)

	require.NotNil(t, resilientClient)
	assert.NoError(t, resilientClient.Close())
}

// TestNewResilientCurrentAccountClient_NilLogger verifies default logger is used
func TestNewResilientCurrentAccountClient_NilLogger(t *testing.T) {
	t.Parallel()

	mockClient := &mockCurrentAccountGRPCClient{}
	config := sharedclients.ResilientClientConfig{
		Logger: nil,
	}

	resilientClient := newResilientCurrentAccountClientForTesting(mockClient, config)

	require.NotNil(t, resilientClient)
	assert.NoError(t, resilientClient.Close())
}

// TestResilientCurrentAccountClient_Close verifies Close is forwarded
func TestResilientCurrentAccountClient_Close(t *testing.T) {
	t.Parallel()

	closed := false
	mockClient := &mockCurrentAccountGRPCClient{
		closeFn: func() error {
			closed = true
			return nil
		},
	}

	config := sharedclients.ResilientClientConfig{}
	resilientClient := newResilientCurrentAccountClientForTesting(mockClient, config)

	err := resilientClient.Close()

	assert.NoError(t, err)
	assert.True(t, closed)
}

// TestResilientCurrentAccountClient_Close_Error verifies error propagation
func TestResilientCurrentAccountClient_Close_Error(t *testing.T) {
	t.Parallel()

	mockClient := &mockCurrentAccountGRPCClient{
		closeFn: func() error {
			return errCloseFailed
		},
	}

	config := sharedclients.ResilientClientConfig{}
	resilientClient := newResilientCurrentAccountClientForTesting(mockClient, config)

	err := resilientClient.Close()

	assert.ErrorIs(t, err, errCloseFailed)
}

// TestResilientCurrentAccountClient_CircuitBreakerIntegration verifies circuit breaker opens after failures
func TestResilientCurrentAccountClient_CircuitBreakerIntegration(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	mockClient := &mockCurrentAccountGRPCClient{
		initiateLienFn: func(_ context.Context, _ *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error) {
			callCount.Add(1)
			return nil, status.Error(codes.Unavailable, "service unavailable")
		},
	}

	config := sharedclients.ResilientClientConfig{
		MaxRequests:      1,
		FailureThreshold: 3, // Open after 3 failures
		MaxRetries:       0, // No retries to test circuit breaker directly
		Logger:           slog.Default(),
	}

	resilientClient := newResilientCurrentAccountClientForTesting(mockClient, config)
	defer func() {
		assert.NoError(t, resilientClient.Close())
	}()

	// Make calls until circuit breaker opens
	ctx := context.Background()

	// First 3 calls should reach the service
	for i := 0; i < 3; i++ {
		_, _ = resilientClient.InitiateLien(ctx, &currentaccountv1.InitiateLienRequest{})
	}

	beforeOpenCount := callCount.Load()
	assert.Equal(t, int32(3), beforeOpenCount)

	// Intentional sleep: Allow any async state updates in the circuit breaker to complete.
	// The resilient client doesn't expose circuit breaker state, so we can't poll for it.
	time.Sleep(100 * time.Millisecond)

	_, err := resilientClient.InitiateLien(ctx, &currentaccountv1.InitiateLienRequest{})
	assert.Error(t, err)

	// Call count should not increase (circuit breaker blocked the call)
	afterOpenCount := callCount.Load()
	assert.Equal(t, beforeOpenCount, afterOpenCount, "circuit breaker should prevent calls from reaching service")
}

// TestResilientCurrentAccountClient_RetryIntegration verifies retry logic works
func TestResilientCurrentAccountClient_RetryIntegration(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	mockClient := &mockCurrentAccountGRPCClient{
		initiateLienFn: func(_ context.Context, _ *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error) {
			count := callCount.Add(1)
			// Fail first 2 calls, succeed on 3rd
			if count < 3 {
				return nil, status.Error(codes.Unavailable, "service unavailable")
			}
			return &currentaccountv1.InitiateLienResponse{}, nil
		},
	}

	config := sharedclients.ResilientClientConfig{
		MaxRequests:         1,
		FailureThreshold:    10, // High threshold so circuit breaker doesn't open
		MaxRetries:          3,
		InitialInterval:     10 * time.Millisecond,
		MaxInterval:         100 * time.Millisecond,
		Multiplier:          2.0,
		RandomizationFactor: 0.1,
		Logger:              slog.Default(),
	}

	resilientClient := newResilientCurrentAccountClientForTesting(mockClient, config)
	defer func() {
		assert.NoError(t, resilientClient.Close())
	}()

	ctx := context.Background()
	resp, err := resilientClient.InitiateLien(ctx, &currentaccountv1.InitiateLienRequest{})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int32(3), callCount.Load(), "should retry until success")
}

// TestResilientCurrentAccountClient_NonRetryableError verifies non-retryable errors fail immediately
func TestResilientCurrentAccountClient_NonRetryableError(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	mockClient := &mockCurrentAccountGRPCClient{
		initiateLienFn: func(_ context.Context, _ *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error) {
			callCount.Add(1)
			return nil, status.Error(codes.InvalidArgument, "invalid request")
		},
	}

	config := sharedclients.ResilientClientConfig{
		MaxRetries:      3,
		InitialInterval: 10 * time.Millisecond,
		Logger:          slog.Default(),
	}

	resilientClient := newResilientCurrentAccountClientForTesting(mockClient, config)
	defer func() {
		assert.NoError(t, resilientClient.Close())
	}()

	ctx := context.Background()
	_, err := resilientClient.InitiateLien(ctx, &currentaccountv1.InitiateLienRequest{})

	assert.Error(t, err)
	assert.Equal(t, int32(1), callCount.Load(), "should not retry non-retryable errors")
}

// TestResilientCurrentAccountClient_ContextCancellation verifies context cancellation stops operations
func TestResilientCurrentAccountClient_ContextCancellation(t *testing.T) {
	t.Parallel()

	mockClient := &mockCurrentAccountGRPCClient{
		initiateLienFn: func(_ context.Context, _ *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error) {
			// Intentional sleep: Simulate slow operation to test context deadline handling
			time.Sleep(100 * time.Millisecond)
			return &currentaccountv1.InitiateLienResponse{}, nil
		},
	}

	config := sharedclients.ResilientClientConfig{
		MaxRetries:      3,
		InitialInterval: 10 * time.Millisecond,
		Logger:          slog.Default(),
	}

	resilientClient := newResilientCurrentAccountClientForTesting(mockClient, config)
	defer func() {
		assert.NoError(t, resilientClient.Close())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := resilientClient.InitiateLien(ctx, &currentaccountv1.InitiateLienRequest{})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

// TestResilientCurrentAccountClient_AllOperations verifies all operations work with resilience
func TestResilientCurrentAccountClient_AllOperations(t *testing.T) {
	t.Parallel()

	t.Run("InitiateLien_Success", func(t *testing.T) {
		mockClient := &mockCurrentAccountGRPCClient{
			initiateLienFn: func(_ context.Context, _ *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error) {
				return &currentaccountv1.InitiateLienResponse{}, nil
			},
		}

		config := sharedclients.ResilientClientConfig{Logger: slog.Default()}
		resilientClient := newResilientCurrentAccountClientForTesting(mockClient, config)
		defer func() {
			assert.NoError(t, resilientClient.Close())
		}()

		resp, err := resilientClient.InitiateLien(context.Background(), &currentaccountv1.InitiateLienRequest{})

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("TerminateLien_Success", func(t *testing.T) {
		mockClient := &mockCurrentAccountGRPCClient{
			terminateLienFn: func(_ context.Context, _ *currentaccountv1.TerminateLienRequest) (*currentaccountv1.TerminateLienResponse, error) {
				return &currentaccountv1.TerminateLienResponse{}, nil
			},
		}

		config := sharedclients.ResilientClientConfig{Logger: slog.Default()}
		resilientClient := newResilientCurrentAccountClientForTesting(mockClient, config)
		defer func() {
			assert.NoError(t, resilientClient.Close())
		}()

		resp, err := resilientClient.TerminateLien(context.Background(), &currentaccountv1.TerminateLienRequest{})

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("ExecuteLien_Success", func(t *testing.T) {
		mockClient := &mockCurrentAccountGRPCClient{
			executeLienFn: func(_ context.Context, _ *currentaccountv1.ExecuteLienRequest) (*currentaccountv1.ExecuteLienResponse, error) {
				return &currentaccountv1.ExecuteLienResponse{}, nil
			},
		}

		config := sharedclients.ResilientClientConfig{Logger: slog.Default()}
		resilientClient := newResilientCurrentAccountClientForTesting(mockClient, config)
		defer func() {
			assert.NoError(t, resilientClient.Close())
		}()

		resp, err := resilientClient.ExecuteLien(context.Background(), &currentaccountv1.ExecuteLienRequest{})

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})
}
