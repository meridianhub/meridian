package clients

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/sony/gobreaker/v2"
)

// ErrTypeAssertion is returned when a type assertion fails in executeWithResilience.
// Re-exported from shared package for backward compatibility.
var ErrTypeAssertion = sharedclients.ErrTypeAssertion

// ResilientPositionKeepingClient wraps PositionKeepingClient with resilience patterns
type ResilientPositionKeepingClient struct {
	client         PositionKeepingClient
	circuitBreaker *sharedclients.CircuitBreaker
	retryConfig    sharedclients.RetryConfig
	logger         *slog.Logger
}

// ResilientFinancialAccountingClient wraps FinancialAccountingClient with resilience patterns
type ResilientFinancialAccountingClient struct {
	client         FinancialAccountingClient
	circuitBreaker *sharedclients.CircuitBreaker
	retryConfig    sharedclients.RetryConfig
	logger         *slog.Logger
}

// ResilientPartyClient wraps PartyClient with resilience patterns
type ResilientPartyClient struct {
	client         PartyClient
	circuitBreaker *sharedclients.CircuitBreaker
	retryConfig    sharedclients.RetryConfig
	logger         *slog.Logger
}

// ResilientClientConfig is an alias to the shared implementation.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
type ResilientClientConfig = sharedclients.ResilientClientConfig

// applyConfigDefaults applies default values to ResilientClientConfig and returns circuit breaker and retry configs
func applyConfigDefaults(config *ResilientClientConfig, defaultName string) (sharedclients.CircuitBreakerConfig, sharedclients.RetryConfig) {
	// Apply logger default
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Apply circuit breaker defaults
	if config.CircuitBreakerName == "" {
		config.CircuitBreakerName = defaultName
	}
	if config.CircuitBreakerTimeout == 0 {
		config.CircuitBreakerTimeout = 30 * time.Second
	}
	if config.CircuitBreakerInterval == 0 {
		config.CircuitBreakerInterval = 60 * time.Second
	}
	if config.MaxRequests == 0 {
		config.MaxRequests = 1
	}
	if config.FailureThreshold == 0 {
		config.FailureThreshold = 5
	}

	// Create circuit breaker config
	cbConfig := sharedclients.CircuitBreakerConfig{
		Name:        config.CircuitBreakerName,
		MaxRequests: config.MaxRequests,
		Interval:    config.CircuitBreakerInterval,
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

	// Apply retry defaults
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.InitialInterval == 0 {
		config.InitialInterval = 100 * time.Millisecond
	}
	if config.MaxInterval == 0 {
		config.MaxInterval = 5 * time.Second
	}
	if config.Multiplier == 0 {
		config.Multiplier = 2.0
	}
	if config.RandomizationFactor == 0 {
		config.RandomizationFactor = 0.5
	}

	// Create retry config
	retryConfig := sharedclients.RetryConfig{
		MaxRetries:          config.MaxRetries,
		InitialInterval:     config.InitialInterval,
		MaxInterval:         config.MaxInterval,
		Multiplier:          config.Multiplier,
		RandomizationFactor: config.RandomizationFactor,
	}

	return cbConfig, retryConfig
}

// NewResilientPositionKeepingClient creates a resilient wrapper around PositionKeepingClient
func NewResilientPositionKeepingClient(
	client PositionKeepingClient,
	config ResilientClientConfig,
) *ResilientPositionKeepingClient {
	cbConfig, retryConfig := applyConfigDefaults(&config, "position-keeping")
	cb := sharedclients.NewCircuitBreaker(cbConfig, config.Logger)

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
	cbConfig, retryConfig := applyConfigDefaults(&config, "financial-accounting")
	cb := sharedclients.NewCircuitBreaker(cbConfig, config.Logger)

	return &ResilientFinancialAccountingClient{
		client:         client,
		circuitBreaker: cb,
		retryConfig:    retryConfig,
		logger:         config.Logger,
	}
}

// NewResilientPartyClient creates a resilient wrapper around PartyClient
func NewResilientPartyClient(
	client PartyClient,
	config ResilientClientConfig,
) *ResilientPartyClient {
	cbConfig, retryConfig := applyConfigDefaults(&config, "party")
	cb := sharedclients.NewCircuitBreaker(cbConfig, config.Logger)

	return &ResilientPartyClient{
		client:         client,
		circuitBreaker: cb,
		retryConfig:    retryConfig,
		logger:         config.Logger,
	}
}

// executeWithResilience wraps a call with circuit breaker and retry logic
func executeWithResilience[T any](
	ctx context.Context,
	cb *sharedclients.CircuitBreaker,
	retryConfig sharedclients.RetryConfig,
	logger *slog.Logger,
	operationName string,
	fn func() (T, error),
) (T, error) {
	var result T

	// Wrap the operation with retry logic
	err := sharedclients.Retry(ctx, retryConfig, func() error {
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

		// Type assertion with check
		var ok bool
		result, ok = res.(T)
		if !ok {
			return fmt.Errorf("%w: expected %T, got %T", ErrTypeAssertion, result, res)
		}
		return nil
	})
	if err != nil {
		// Check if circuit breaker is open
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
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
// NOTE: Retries are disabled for this operation because it lacks an idempotency_key.
// The operation relies on optimistic concurrency control (version field) to prevent duplicates.
func (r *ResilientPositionKeepingClient) BulkImportTransactions(
	ctx context.Context,
	req *positionkeepingv1.BulkImportTransactionsRequest,
) (*positionkeepingv1.BulkImportTransactionsResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		sharedclients.NoRetryConfig(), // No retries - not idempotent
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
// NOTE: Retries are disabled for this operation because it lacks an idempotency_key.
// Updates should be handled idempotently by the caller if retries are needed.
func (r *ResilientFinancialAccountingClient) UpdateFinancialBookingLog(
	ctx context.Context,
	req *financialaccountingv1.UpdateFinancialBookingLogRequest,
) (*financialaccountingv1.UpdateFinancialBookingLogResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		sharedclients.NoRetryConfig(), // No retries - not idempotent
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
// NOTE: Retries are disabled until server-side idempotency deduplication is confirmed.
// The protobuf includes idempotency_key, but retries are disabled to prevent duplicate
// ledger postings until the server implementation is verified to use it for deduplication.
func (r *ResilientFinancialAccountingClient) CaptureLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.CaptureLedgerPostingRequest,
) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		sharedclients.NoRetryConfig(), // No retries - server-side idempotency not yet confirmed
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

// ValidateParty checks if a party exists and is active with resilience
func (r *ResilientPartyClient) ValidateParty(ctx context.Context, partyID string) error {
	_, err := executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"ValidateParty",
		func() (struct{}, error) {
			return struct{}{}, r.client.ValidateParty(ctx, partyID)
		},
	)
	return err
}

// GetParty retrieves full party details by ID with resilience
func (r *ResilientPartyClient) GetParty(ctx context.Context, partyID string) (*partyv1.Party, error) {
	return executeWithResilience(
		ctx,
		r.circuitBreaker,
		r.retryConfig,
		r.logger,
		"GetParty",
		func() (*partyv1.Party, error) {
			return r.client.GetParty(ctx, partyID)
		},
	)
}

// Close closes the underlying client connection
func (r *ResilientPartyClient) Close() error {
	if err := r.client.Close(); err != nil {
		return fmt.Errorf("failed to close party client: %w", err)
	}
	return nil
}
