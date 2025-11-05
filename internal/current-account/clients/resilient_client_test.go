package clients_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/internal/current-account/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errCloseFailed = errors.New("close failed")

// mockPositionKeepingClient for testing
type mockPositionKeepingClient struct {
	initiateFn func(ctx context.Context, req *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error)
	closeFn    func() error
}

func (m *mockPositionKeepingClient) InitiateFinancialPositionLog(ctx context.Context, req *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	return m.initiateFn(ctx, req)
}

func (m *mockPositionKeepingClient) UpdateFinancialPositionLog(_ context.Context, _ *positionkeepingv1.UpdateFinancialPositionLogRequest) (*positionkeepingv1.UpdateFinancialPositionLogResponse, error) {
	return nil, nil
}

func (m *mockPositionKeepingClient) RetrieveFinancialPositionLog(_ context.Context, _ *positionkeepingv1.RetrieveFinancialPositionLogRequest) (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error) {
	return nil, nil
}

func (m *mockPositionKeepingClient) BulkImportTransactions(_ context.Context, _ *positionkeepingv1.BulkImportTransactionsRequest) (*positionkeepingv1.BulkImportTransactionsResponse, error) {
	return nil, nil
}

func (m *mockPositionKeepingClient) ListFinancialPositionLogs(_ context.Context, _ *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	return nil, nil
}

func (m *mockPositionKeepingClient) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

// TestNewResilientPositionKeepingClient_Success verifies client creation
func TestNewResilientPositionKeepingClient_Success(t *testing.T) {
	t.Parallel()

	mockClient := &mockPositionKeepingClient{}
	config := clients.ResilientClientConfig{
		CircuitBreakerName: "test-service",
		MaxRequests:        1,
		FailureThreshold:   5,
		MaxRetries:         3,
		InitialInterval:    100 * time.Millisecond,
		Logger:             slog.Default(),
	}

	resilientClient := clients.NewResilientPositionKeepingClient(mockClient, config)

	require.NotNil(t, resilientClient)
	assert.NoError(t, resilientClient.Close())
}

// TestNewResilientPositionKeepingClient_DefaultConfig verifies defaults are applied
func TestNewResilientPositionKeepingClient_DefaultConfig(t *testing.T) {
	t.Parallel()

	mockClient := &mockPositionKeepingClient{}
	config := clients.ResilientClientConfig{}

	resilientClient := clients.NewResilientPositionKeepingClient(mockClient, config)

	require.NotNil(t, resilientClient)
	assert.NoError(t, resilientClient.Close())
}

// TestNewResilientPositionKeepingClient_NilLogger verifies default logger is used
func TestNewResilientPositionKeepingClient_NilLogger(t *testing.T) {
	t.Parallel()

	mockClient := &mockPositionKeepingClient{}
	config := clients.ResilientClientConfig{
		Logger: nil,
	}

	resilientClient := clients.NewResilientPositionKeepingClient(mockClient, config)

	require.NotNil(t, resilientClient)
	assert.NoError(t, resilientClient.Close())
}

// TestResilientPositionKeepingClient_Close verifies Close is forwarded
func TestResilientPositionKeepingClient_Close(t *testing.T) {
	t.Parallel()

	closed := false
	mockClient := &mockPositionKeepingClient{
		closeFn: func() error {
			closed = true
			return nil
		},
	}

	config := clients.ResilientClientConfig{}
	resilientClient := clients.NewResilientPositionKeepingClient(mockClient, config)

	err := resilientClient.Close()

	assert.NoError(t, err)
	assert.True(t, closed)
}

// TestResilientPositionKeepingClient_Close_Error verifies error propagation
func TestResilientPositionKeepingClient_Close_Error(t *testing.T) {
	t.Parallel()

	mockClient := &mockPositionKeepingClient{
		closeFn: func() error {
			return errCloseFailed
		},
	}

	config := clients.ResilientClientConfig{}
	resilientClient := clients.NewResilientPositionKeepingClient(mockClient, config)

	err := resilientClient.Close()

	assert.ErrorIs(t, err, errCloseFailed)
}

// TestNewResilientFinancialAccountingClient_Success verifies client creation
func TestNewResilientFinancialAccountingClient_Success(t *testing.T) {
	t.Parallel()

	mockClient := &mockFinancialAccountingClient{}
	config := clients.ResilientClientConfig{
		CircuitBreakerName: "test-fa-service",
		MaxRequests:        1,
		FailureThreshold:   5,
		MaxRetries:         3,
		InitialInterval:    100 * time.Millisecond,
		Logger:             slog.Default(),
	}

	resilientClient := clients.NewResilientFinancialAccountingClient(mockClient, config)

	require.NotNil(t, resilientClient)
	assert.NoError(t, resilientClient.Close())
}

// TestNewResilientFinancialAccountingClient_DefaultConfig verifies defaults are applied
func TestNewResilientFinancialAccountingClient_DefaultConfig(t *testing.T) {
	t.Parallel()

	mockClient := &mockFinancialAccountingClient{}
	config := clients.ResilientClientConfig{}

	resilientClient := clients.NewResilientFinancialAccountingClient(mockClient, config)

	require.NotNil(t, resilientClient)
	assert.NoError(t, resilientClient.Close())
}

// TestResilientFinancialAccountingClient_Close verifies Close is forwarded
func TestResilientFinancialAccountingClient_Close(t *testing.T) {
	t.Parallel()

	closed := false
	mockClient := &mockFinancialAccountingClient{
		closeFn: func() error {
			closed = true
			return nil
		},
	}

	config := clients.ResilientClientConfig{}
	resilientClient := clients.NewResilientFinancialAccountingClient(mockClient, config)

	err := resilientClient.Close()

	assert.NoError(t, err)
	assert.True(t, closed)
}

// TestResilientClient_CircuitBreakerIntegration verifies circuit breaker opens after failures
func TestResilientClient_CircuitBreakerIntegration(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	mockClient := &mockPositionKeepingClient{
		initiateFn: func(_ context.Context, _ *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
			callCount.Add(1)
			return nil, status.Error(codes.Unavailable, "service unavailable")
		},
	}

	config := clients.ResilientClientConfig{
		MaxRequests:      1,
		FailureThreshold: 3, // Open after 3 failures
		MaxRetries:       0, // No retries to test circuit breaker directly
		Logger:           slog.Default(),
	}

	resilientClient := clients.NewResilientPositionKeepingClient(mockClient, config)
	defer func() {
		assert.NoError(t, resilientClient.Close())
	}()

	// Make calls until circuit breaker opens
	ctx := context.Background()

	// First 3 calls should reach the service
	for i := 0; i < 3; i++ {
		_, _ = resilientClient.InitiateFinancialPositionLog(ctx, &positionkeepingv1.InitiateFinancialPositionLogRequest{})
	}

	beforeOpenCount := callCount.Load()
	assert.Equal(t, int32(3), beforeOpenCount)

	// Circuit breaker should now be open, next calls should fail fast
	time.Sleep(100 * time.Millisecond) // Allow circuit breaker to transition

	_, err := resilientClient.InitiateFinancialPositionLog(ctx, &positionkeepingv1.InitiateFinancialPositionLogRequest{})
	assert.Error(t, err)

	// Call count should not increase (circuit breaker blocked the call)
	afterOpenCount := callCount.Load()
	assert.Equal(t, beforeOpenCount, afterOpenCount, "circuit breaker should prevent calls from reaching service")
}

// TestResilientClient_RetryIntegration verifies retry logic works
func TestResilientClient_RetryIntegration(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	mockClient := &mockPositionKeepingClient{
		initiateFn: func(_ context.Context, _ *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
			count := callCount.Add(1)
			// Fail first 2 calls, succeed on 3rd
			if count < 3 {
				return nil, status.Error(codes.Unavailable, "service unavailable")
			}
			return &positionkeepingv1.InitiateFinancialPositionLogResponse{}, nil
		},
	}

	config := clients.ResilientClientConfig{
		MaxRequests:         1,
		FailureThreshold:    10, // High threshold so circuit breaker doesn't open
		MaxRetries:          3,
		InitialInterval:     10 * time.Millisecond,
		MaxInterval:         100 * time.Millisecond,
		Multiplier:          2.0,
		RandomizationFactor: 0.1,
		Logger:              slog.Default(),
	}

	resilientClient := clients.NewResilientPositionKeepingClient(mockClient, config)
	defer func() {
		assert.NoError(t, resilientClient.Close())
	}()

	ctx := context.Background()
	resp, err := resilientClient.InitiateFinancialPositionLog(ctx, &positionkeepingv1.InitiateFinancialPositionLogRequest{})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int32(3), callCount.Load(), "should retry until success")
}

// TestResilientClient_NonRetryableError verifies non-retryable errors fail immediately
func TestResilientClient_NonRetryableError(t *testing.T) {
	t.Parallel()

	var callCount atomic.Int32

	mockClient := &mockPositionKeepingClient{
		initiateFn: func(_ context.Context, _ *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
			callCount.Add(1)
			return nil, status.Error(codes.InvalidArgument, "invalid request")
		},
	}

	config := clients.ResilientClientConfig{
		MaxRetries:      3,
		InitialInterval: 10 * time.Millisecond,
		Logger:          slog.Default(),
	}

	resilientClient := clients.NewResilientPositionKeepingClient(mockClient, config)
	defer func() {
		assert.NoError(t, resilientClient.Close())
	}()

	ctx := context.Background()
	_, err := resilientClient.InitiateFinancialPositionLog(ctx, &positionkeepingv1.InitiateFinancialPositionLogRequest{})

	assert.Error(t, err)
	assert.Equal(t, int32(1), callCount.Load(), "should not retry non-retryable errors")
}

// TestResilientClient_ContextCancellation verifies context cancellation stops operations
func TestResilientClient_ContextCancellation(t *testing.T) {
	t.Parallel()

	mockClient := &mockPositionKeepingClient{
		initiateFn: func(_ context.Context, _ *positionkeepingv1.InitiateFinancialPositionLogRequest) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
			// Simulate slow operation
			time.Sleep(100 * time.Millisecond)
			return &positionkeepingv1.InitiateFinancialPositionLogResponse{}, nil
		},
	}

	config := clients.ResilientClientConfig{
		MaxRetries:      3,
		InitialInterval: 10 * time.Millisecond,
		Logger:          slog.Default(),
	}

	resilientClient := clients.NewResilientPositionKeepingClient(mockClient, config)
	defer func() {
		assert.NoError(t, resilientClient.Close())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := resilientClient.InitiateFinancialPositionLog(ctx, &positionkeepingv1.InitiateFinancialPositionLogRequest{})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

// mockFinancialAccountingClient for testing
type mockFinancialAccountingClient struct {
	closeFn func() error
}

func (m *mockFinancialAccountingClient) InitiateFinancialBookingLog(_ context.Context, _ *financialaccountingv1.InitiateFinancialBookingLogRequest) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	return nil, nil
}

func (m *mockFinancialAccountingClient) UpdateFinancialBookingLog(_ context.Context, _ *financialaccountingv1.UpdateFinancialBookingLogRequest) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	return nil, nil
}

func (m *mockFinancialAccountingClient) RetrieveFinancialBookingLog(_ context.Context, _ *financialaccountingv1.RetrieveFinancialBookingLogRequest) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
	return nil, nil
}

func (m *mockFinancialAccountingClient) ListFinancialBookingLogs(_ context.Context, _ *financialaccountingv1.ListFinancialBookingLogsRequest) (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
	return nil, nil
}

func (m *mockFinancialAccountingClient) CaptureLedgerPosting(_ context.Context, _ *financialaccountingv1.CaptureLedgerPostingRequest) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	return nil, nil
}

func (m *mockFinancialAccountingClient) RetrieveLedgerPosting(_ context.Context, _ *financialaccountingv1.RetrieveLedgerPostingRequest) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
	return nil, nil
}

func (m *mockFinancialAccountingClient) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}
