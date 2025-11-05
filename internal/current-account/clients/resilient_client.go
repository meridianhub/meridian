package clients

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/sony/gobreaker/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ResilientPositionKeepingClient wraps PositionKeepingClient with resilience patterns
type ResilientPositionKeepingClient struct {
	client         PositionKeepingClient
	circuitBreaker *CircuitBreaker
	retryConfig    RetryConfig
	logger         *slog.Logger
}

// ResilientFinancialAccountingClient wraps FinancialAccountingClient with resilience patterns
type ResilientFinancialAccountingClient struct {
	client         FinancialAccountingClient
	circuitBreaker *CircuitBreaker
	retryConfig    RetryConfig
	logger         *slog.Logger
}

// ResilientClientConfig holds configuration for resilient service clients
type ResilientClientConfig struct {
	// Circuit breaker configuration
	CircuitBreakerName    string
	CircuitBreakerTimeout time.Duration
	MaxRequests           uint32
	FailureThreshold      uint32

	// Retry configuration
	MaxRetries          int
	InitialInterval     time.Duration
	MaxInterval         time.Duration
	Multiplier          float64
	RandomizationFactor float64

	// Observability
	Logger *slog.Logger
}

// NewResilientPositionKeepingClient creates a resilient wrapper around PositionKeepingClient
func NewResilientPositionKeepingClient(
	client PositionKeepingClient,
	config ResilientClientConfig,
) *ResilientPositionKeepingClient {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Apply defaults
	if config.CircuitBreakerName == "" {
		config.CircuitBreakerName = "position-keeping"
	}
	if config.CircuitBreakerTimeout == 0 {
		config.CircuitBreakerTimeout = 30 * time.Second
	}
	if config.MaxRequests == 0 {
		config.MaxRequests = 1
	}
	if config.FailureThreshold == 0 {
		config.FailureThreshold = 5
	}

	// Create circuit breaker
	cbConfig := CircuitBreakerConfig{
		Name:        config.CircuitBreakerName,
		MaxRequests: config.MaxRequests,
		Interval:    60 * time.Second,
		Timeout:     config.CircuitBreakerTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= config.FailureThreshold
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			config.Logger.Info("circuit breaker state changed",
				"service", name,
				"from", from.String(),
				"to", to.String())
		},
	}

	cb := NewCircuitBreaker(cbConfig, config.Logger)

	// Create retry config
	retryConfig := RetryConfig{
		MaxRetries:          config.MaxRetries,
		InitialInterval:     config.InitialInterval,
		MaxInterval:         config.MaxInterval,
		Multiplier:          config.Multiplier,
		RandomizationFactor: config.RandomizationFactor,
	}

	return &ResilientPositionKeepingClient{
		client:         client,
		circuitBreaker: cb,
		retryConfig:    retryConfig,
		logger:         config.Logger,
	}
}

// NewResilientFinancialAccountingClient creates a resilient wrapper around FinancialAccountingClient
func NewResilientFinancialAccountingClient(
	client FinancialAccountingClient,
	config ResilientClientConfig,
) *ResilientFinancialAccountingClient {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Apply defaults
	if config.CircuitBreakerName == "" {
		config.CircuitBreakerName = "financial-accounting"
	}
	if config.CircuitBreakerTimeout == 0 {
		config.CircuitBreakerTimeout = 30 * time.Second
	}
	if config.MaxRequests == 0 {
		config.MaxRequests = 1
	}
	if config.FailureThreshold == 0 {
		config.FailureThreshold = 5
	}

	// Create circuit breaker
	cbConfig := CircuitBreakerConfig{
		Name:        config.CircuitBreakerName,
		MaxRequests: config.MaxRequests,
		Interval:    60 * time.Second,
		Timeout:     config.CircuitBreakerTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= config.FailureThreshold
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			config.Logger.Info("circuit breaker state changed",
				"service", name,
				"from", from.String(),
				"to", to.String())
		},
	}

	cb := NewCircuitBreaker(cbConfig, config.Logger)

	// Create retry config
	retryConfig := RetryConfig{
		MaxRetries:          config.MaxRetries,
		InitialInterval:     config.InitialInterval,
		MaxInterval:         config.MaxInterval,
		Multiplier:          config.Multiplier,
		RandomizationFactor: config.RandomizationFactor,
	}

	return &ResilientFinancialAccountingClient{
		client:         client,
		circuitBreaker: cb,
		retryConfig:    retryConfig,
		logger:         config.Logger,
	}
}

// executeWithResilience wraps a call with circuit breaker and retry logic
func executeWithResilience[T any](
	ctx context.Context,
	cb *CircuitBreaker,
	retryConfig RetryConfig,
	logger *slog.Logger,
	operationName string,
	fn func() (T, error),
) (T, error) {
	var result T

	// Wrap the operation with retry logic
	err := Retry(ctx, retryConfig, func() error {
		// Execute through circuit breaker
		res, err := cb.Execute(ctx, func() (any, error) {
			return fn()
		})
		if err != nil {
			logger.Debug("operation failed",
				"operation", operationName,
				"error", err)
			return fmt.Errorf("circuit breaker execution failed: %w", err)
		}

		result = res.(T)
		return nil
	})
	if err != nil {
		// Check if circuit breaker is open
		if status.Code(err) == codes.Unavailable {
			logger.Warn("circuit breaker open",
				"operation", operationName)
		}
		return result, fmt.Errorf("resilient operation failed for %s: %w", operationName, err)
	}

	return result, nil
}

// InitiateFinancialPositionLog creates a new financial position log with resilience
func (r *ResilientPositionKeepingClient) InitiateFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.InitiateFinancialPositionLogRequest,
) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"InitiateFinancialPositionLog",
		func() (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
			return r.client.InitiateFinancialPositionLog(ctx, req)
		},
	)
}

// UpdateFinancialPositionLog updates an existing financial position log with resilience
func (r *ResilientPositionKeepingClient) UpdateFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.UpdateFinancialPositionLogRequest,
) (*positionkeepingv1.UpdateFinancialPositionLogResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"UpdateFinancialPositionLog",
		func() (*positionkeepingv1.UpdateFinancialPositionLogResponse, error) {
			return r.client.UpdateFinancialPositionLog(ctx, req)
		},
	)
}

// RetrieveFinancialPositionLog retrieves a specific financial position log with resilience
func (r *ResilientPositionKeepingClient) RetrieveFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.RetrieveFinancialPositionLogRequest,
) (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"RetrieveFinancialPositionLog",
		func() (*positionkeepingv1.RetrieveFinancialPositionLogResponse, error) {
			return r.client.RetrieveFinancialPositionLog(ctx, req)
		},
	)
}

// BulkImportTransactions imports multiple transactions with resilience
func (r *ResilientPositionKeepingClient) BulkImportTransactions(
	ctx context.Context,
	req *positionkeepingv1.BulkImportTransactionsRequest,
) (*positionkeepingv1.BulkImportTransactionsResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"BulkImportTransactions",
		func() (*positionkeepingv1.BulkImportTransactionsResponse, error) {
			return r.client.BulkImportTransactions(ctx, req)
		},
	)
}

// ListFinancialPositionLogs lists financial position logs with resilience
func (r *ResilientPositionKeepingClient) ListFinancialPositionLogs(
	ctx context.Context,
	req *positionkeepingv1.ListFinancialPositionLogsRequest,
) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"ListFinancialPositionLogs",
		func() (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
			return r.client.ListFinancialPositionLogs(ctx, req)
		},
	)
}

// Close closes the underlying client connection
func (r *ResilientPositionKeepingClient) Close() error {
	if err := r.client.Close(); err != nil {
		return fmt.Errorf("failed to close position keeping client: %w", err)
	}
	return nil
}

// InitiateFinancialBookingLog creates a new financial booking log with resilience
func (r *ResilientFinancialAccountingClient) InitiateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.InitiateFinancialBookingLogRequest,
) (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"InitiateFinancialBookingLog",
		func() (*financialaccountingv1.InitiateFinancialBookingLogResponse, error) {
			return r.client.InitiateFinancialBookingLog(ctx, req)
		},
	)
}

// UpdateFinancialBookingLog updates an existing financial booking log with resilience
func (r *ResilientFinancialAccountingClient) UpdateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.UpdateFinancialBookingLogRequest,
) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"UpdateFinancialBookingLog",
		func() (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
			return r.client.UpdateFinancialBookingLog(ctx, req)
		},
	)
}

// RetrieveFinancialBookingLog retrieves a specific financial booking log with resilience
func (r *ResilientFinancialAccountingClient) RetrieveFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.RetrieveFinancialBookingLogRequest,
) (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"RetrieveFinancialBookingLog",
		func() (*financialaccountingv1.RetrieveFinancialBookingLogResponse, error) {
			return r.client.RetrieveFinancialBookingLog(ctx, req)
		},
	)
}

// ListFinancialBookingLogs lists financial booking logs with resilience
func (r *ResilientFinancialAccountingClient) ListFinancialBookingLogs(
	ctx context.Context,
	req *financialaccountingv1.ListFinancialBookingLogsRequest,
) (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"ListFinancialBookingLogs",
		func() (*financialaccountingv1.ListFinancialBookingLogsResponse, error) {
			return r.client.ListFinancialBookingLogs(ctx, req)
		},
	)
}

// CaptureLedgerPosting creates a new ledger posting with resilience
func (r *ResilientFinancialAccountingClient) CaptureLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.CaptureLedgerPostingRequest,
) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"CaptureLedgerPosting",
		func() (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
			return r.client.CaptureLedgerPosting(ctx, req)
		},
	)
}

// RetrieveLedgerPosting retrieves a specific ledger posting with resilience
func (r *ResilientFinancialAccountingClient) RetrieveLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.RetrieveLedgerPostingRequest,
) (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"RetrieveLedgerPosting",
		func() (*financialaccountingv1.RetrieveLedgerPostingResponse, error) {
			return r.client.RetrieveLedgerPosting(ctx, req)
		},
	)
}

// Close closes the underlying client connection
func (r *ResilientFinancialAccountingClient) Close() error {
	if err := r.client.Close(); err != nil {
		return fmt.Errorf("failed to close financial accounting client: %w", err)
	}
	return nil
}
