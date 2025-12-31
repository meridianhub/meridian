package clients

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errFACloseFailed = errors.New("close failed")

// mockFinancialAccountingGRPCClient implements financialAccountingGRPCClient for testing
type mockFinancialAccountingGRPCClient struct {
	initiateFinancialBookingLogFn func(ctx context.Context, req *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error)
	captureLedgerPostingFn        func(ctx context.Context, req *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error)
	updateFinancialBookingLogFn   func(ctx context.Context, req *financialaccountingv1.UpdateFinancialBookingLogRequest) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error)
	closeFn                       func() error
}

func (m *mockFinancialAccountingGRPCClient) InitiateFinancialBookingLog(ctx context.Context, in *financialaccountingv1.InitiateFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	if m.initiateFinancialBookingLogFn != nil {
		return m.initiateFinancialBookingLogFn(ctx, in)
	}
	return &financialaccountingv1.InitiateFinancialBookingLogResponse{}, nil
}

func (m *mockFinancialAccountingGRPCClient) CaptureLedgerPosting(ctx context.Context, in *financialaccountingv1.CaptureLedgerPostingRequest, _ ...grpc.CallOption) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	if m.captureLedgerPostingFn != nil {
		return m.captureLedgerPostingFn(ctx, in)
	}
	return &financialaccountingv1.CaptureLedgerPostingResponse{}, nil
}

func (m *mockFinancialAccountingGRPCClient) UpdateFinancialBookingLog(ctx context.Context, in *financialaccountingv1.UpdateFinancialBookingLogRequest, _ ...grpc.CallOption) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	if m.updateFinancialBookingLogFn != nil {
		return m.updateFinancialBookingLogFn(ctx, in)
	}
	return &financialaccountingv1.UpdateFinancialBookingLogResponse{}, nil
}

func (m *mockFinancialAccountingGRPCClient) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

// TestNewResilientFinancialAccountingClient_Success verifies client creation
func TestNewResilientFinancialAccountingClient_Success(t *testing.T) {
	t.Parallel()

	mockClient := &mockFinancialAccountingGRPCClient{}
	config := sharedclients.ResilientClientConfig{
		CircuitBreakerName: "test-fa-service",
		MaxRequests:        1,
		FailureThreshold:   5,
		MaxRetries:         3,
		InitialInterval:    100 * time.Millisecond,
		Logger:             slog.Default(),
	}

	resilientClient := newResilientFinancialAccountingClientForTesting(mockClient, config)

	require.NotNil(t, resilientClient)
	assert.NoError(t, resilientClient.Close())
}

// TestNewResilientFinancialAccountingClient_DefaultConfig verifies defaults are applied
func TestNewResilientFinancialAccountingClient_DefaultConfig(t *testing.T) {
	t.Parallel()

	mockClient := &mockFinancialAccountingGRPCClient{}
	config := sharedclients.ResilientClientConfig{}

	resilientClient := newResilientFinancialAccountingClientForTesting(mockClient, config)

	require.NotNil(t, resilientClient)
	assert.NoError(t, resilientClient.Close())
}

// TestNewResilientFinancialAccountingClient_NilLogger verifies default logger is used
func TestNewResilientFinancialAccountingClient_NilLogger(t *testing.T) {
	t.Parallel()

	mockClient := &mockFinancialAccountingGRPCClient{}
	config := sharedclients.ResilientClientConfig{
		Logger: nil,
	}

	resilientClient := newResilientFinancialAccountingClientForTesting(mockClient, config)

	require.NotNil(t, resilientClient)
	assert.NoError(t, resilientClient.Close())
}

// TestResilientFinancialAccountingClient_Close verifies Close is forwarded
func TestResilientFinancialAccountingClient_Close(t *testing.T) {
	t.Parallel()

	closed := false
	mockClient := &mockFinancialAccountingGRPCClient{
		closeFn: func() error {
			closed = true
			return nil
		},
	}

	config := sharedclients.ResilientClientConfig{}
	resilientClient := newResilientFinancialAccountingClientForTesting(mockClient, config)

	err := resilientClient.Close()

	assert.NoError(t, err)
	assert.True(t, closed)
}

// TestResilientFinancialAccountingClient_Close_Error verifies error propagation
func TestResilientFinancialAccountingClient_Close_Error(t *testing.T) {
	t.Parallel()

	mockClient := &mockFinancialAccountingGRPCClient{
		closeFn: func() error {
			return errFACloseFailed
		},
	}

	config := sharedclients.ResilientClientConfig{}
	resilientClient := newResilientFinancialAccountingClientForTesting(mockClient, config)

	err := resilientClient.Close()

	assert.ErrorIs(t, err, errFACloseFailed)
}

// TestResilientFinancialAccountingClient_CircuitBreakerIntegration verifies circuit breaker opens after failures
func TestResilientFinancialAccountingClient_CircuitBreakerIntegration(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	mockClient := &mockFinancialAccountingGRPCClient{
		initiateFinancialBookingLogFn: func(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
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

	resilientClient := newResilientFinancialAccountingClientForTesting(mockClient, config)
	defer func() {
		assert.NoError(t, resilientClient.Close())
	}()

	// Make calls until circuit breaker opens
	ctx := context.Background()

	// First 3 calls should reach the service
	for i := 0; i < 3; i++ {
		_, _ = resilientClient.InitiateFinancialBookingLog(ctx, &financialaccountingv1.InitiateFinancialBookingLogRequest{})
	}

	beforeOpenCount := callCount.Load()
	assert.Equal(t, int32(3), beforeOpenCount)

	// Intentional sleep: Allow any async state updates in the circuit breaker to complete.
	// The resilient client doesn't expose circuit breaker state, so we can't poll for it.
	time.Sleep(100 * time.Millisecond)

	_, err := resilientClient.InitiateFinancialBookingLog(ctx, &financialaccountingv1.InitiateFinancialBookingLogRequest{})
	assert.Error(t, err)

	// Call count should not increase (circuit breaker blocked the call)
	afterOpenCount := callCount.Load()
	assert.Equal(t, beforeOpenCount, afterOpenCount, "circuit breaker should prevent calls from reaching service")
}

// TestResilientFinancialAccountingClient_RetryIntegration verifies retry logic works
func TestResilientFinancialAccountingClient_RetryIntegration(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	mockClient := &mockFinancialAccountingGRPCClient{
		initiateFinancialBookingLogFn: func(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
			count := callCount.Add(1)
			// Fail first 2 calls, succeed on 3rd
			if count < 3 {
				return nil, status.Error(codes.Unavailable, "service unavailable")
			}
			return &financialaccountingv1.InitiateFinancialBookingLogResponse{}, nil
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

	resilientClient := newResilientFinancialAccountingClientForTesting(mockClient, config)
	defer func() {
		assert.NoError(t, resilientClient.Close())
	}()

	ctx := context.Background()
	resp, err := resilientClient.InitiateFinancialBookingLog(ctx, &financialaccountingv1.InitiateFinancialBookingLogRequest{})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int32(3), callCount.Load(), "should retry until success")
}

// TestResilientFinancialAccountingClient_NonRetryableError verifies non-retryable errors fail immediately
func TestResilientFinancialAccountingClient_NonRetryableError(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	mockClient := &mockFinancialAccountingGRPCClient{
		initiateFinancialBookingLogFn: func(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
			callCount.Add(1)
			return nil, status.Error(codes.InvalidArgument, "invalid request")
		},
	}

	config := sharedclients.ResilientClientConfig{
		MaxRetries:      3,
		InitialInterval: 10 * time.Millisecond,
		Logger:          slog.Default(),
	}

	resilientClient := newResilientFinancialAccountingClientForTesting(mockClient, config)
	defer func() {
		assert.NoError(t, resilientClient.Close())
	}()

	ctx := context.Background()
	_, err := resilientClient.InitiateFinancialBookingLog(ctx, &financialaccountingv1.InitiateFinancialBookingLogRequest{})

	assert.Error(t, err)
	assert.Equal(t, int32(1), callCount.Load(), "should not retry non-retryable errors")
}

// TestResilientFinancialAccountingClient_ContextCancellation verifies context cancellation stops operations
func TestResilientFinancialAccountingClient_ContextCancellation(t *testing.T) {
	t.Parallel()

	mockClient := &mockFinancialAccountingGRPCClient{
		initiateFinancialBookingLogFn: func(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
			// Intentional sleep: Simulate slow operation to test context deadline handling
			time.Sleep(100 * time.Millisecond)
			return &financialaccountingv1.InitiateFinancialBookingLogResponse{}, nil
		},
	}

	config := sharedclients.ResilientClientConfig{
		MaxRetries:      3,
		InitialInterval: 10 * time.Millisecond,
		Logger:          slog.Default(),
	}

	resilientClient := newResilientFinancialAccountingClientForTesting(mockClient, config)
	defer func() {
		assert.NoError(t, resilientClient.Close())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := resilientClient.InitiateFinancialBookingLog(ctx, &financialaccountingv1.InitiateFinancialBookingLogRequest{})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

// TestResilientFinancialAccountingClient_AllOperations verifies all operations work with resilience
func TestResilientFinancialAccountingClient_AllOperations(t *testing.T) {
	t.Parallel()

	t.Run("InitiateFinancialBookingLog_Success", func(t *testing.T) {
		mockClient := &mockFinancialAccountingGRPCClient{
			initiateFinancialBookingLogFn: func(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
				return &financialaccountingv1.InitiateFinancialBookingLogResponse{}, nil
			},
		}

		config := sharedclients.ResilientClientConfig{Logger: slog.Default()}
		resilientClient := newResilientFinancialAccountingClientForTesting(mockClient, config)
		defer func() {
			assert.NoError(t, resilientClient.Close())
		}()

		resp, err := resilientClient.InitiateFinancialBookingLog(context.Background(), &financialaccountingv1.InitiateFinancialBookingLogRequest{})

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("CaptureLedgerPosting_Success", func(t *testing.T) {
		mockClient := &mockFinancialAccountingGRPCClient{
			captureLedgerPostingFn: func(_ context.Context, _ *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
				return &financialaccountingv1.CaptureLedgerPostingResponse{}, nil
			},
		}

		config := sharedclients.ResilientClientConfig{Logger: slog.Default()}
		resilientClient := newResilientFinancialAccountingClientForTesting(mockClient, config)
		defer func() {
			assert.NoError(t, resilientClient.Close())
		}()

		resp, err := resilientClient.CaptureLedgerPosting(context.Background(), &financialaccountingv1.CaptureLedgerPostingRequest{})

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("UpdateFinancialBookingLog_Success", func(t *testing.T) {
		mockClient := &mockFinancialAccountingGRPCClient{
			updateFinancialBookingLogFn: func(_ context.Context, _ *financialaccountingv1.UpdateFinancialBookingLogRequest) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
				return &financialaccountingv1.UpdateFinancialBookingLogResponse{}, nil
			},
		}

		config := sharedclients.ResilientClientConfig{Logger: slog.Default()}
		resilientClient := newResilientFinancialAccountingClientForTesting(mockClient, config)
		defer func() {
			assert.NoError(t, resilientClient.Close())
		}()

		resp, err := resilientClient.UpdateFinancialBookingLog(context.Background(), &financialaccountingv1.UpdateFinancialBookingLogRequest{})

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})
}
