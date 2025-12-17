// Package clients provides gRPC client wrappers with resilience patterns.
//
// This package provides service-specific resilient client wrappers that delegate
// to the shared implementation in github.com/meridianhub/meridian/shared/pkg/clients.
//
// # Service-Specific Wrappers
//
// Each wrapper maintains the domain-specific client interface while using the shared
// ResilientClient for all resilience logic (circuit breaker, retry, etc.):
//
//   - ResilientPositionKeepingClient - wraps PositionKeepingClient
//   - ResilientFinancialAccountingClient - wraps FinancialAccountingClient
//   - ResilientPartyClient - wraps PartyClient
//
// # Backward Compatibility
//
// This package re-exports types from shared/pkg/clients for backward compatibility.
// New code should import directly from github.com/meridianhub/meridian/shared/pkg/clients.
package clients

import (
	"context"
	"fmt"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
)

// ErrTypeAssertion is returned when a type assertion fails in executeWithResilience.
// Re-exported from shared package for backward compatibility.
var ErrTypeAssertion = sharedclients.ErrTypeAssertion

// ResilientPositionKeepingClient wraps PositionKeepingClient with resilience patterns
type ResilientPositionKeepingClient struct {
	client          PositionKeepingClient
	resilientClient *sharedclients.ResilientClient
}

// ResilientFinancialAccountingClient wraps FinancialAccountingClient with resilience patterns
type ResilientFinancialAccountingClient struct {
	client          FinancialAccountingClient
	resilientClient *sharedclients.ResilientClient
}

// ResilientPartyClient wraps PartyClient with resilience patterns
type ResilientPartyClient struct {
	client          PartyClient
	resilientClient *sharedclients.ResilientClient
}

// ResilientClientConfig is an alias to the shared implementation.
// Deprecated: Import directly from github.com/meridianhub/meridian/shared/pkg/clients
type ResilientClientConfig = sharedclients.ResilientClientConfig

// NewResilientPositionKeepingClient creates a resilient wrapper around PositionKeepingClient
func NewResilientPositionKeepingClient(
	client PositionKeepingClient,
	config ResilientClientConfig,
) *ResilientPositionKeepingClient {
	// Apply default name if not provided
	if config.CircuitBreakerName == "" {
		config.CircuitBreakerName = "position-keeping"
	}

	return &ResilientPositionKeepingClient{
		client:          client,
		resilientClient: sharedclients.NewResilientClient(config),
	}
}

// NewResilientFinancialAccountingClient creates a resilient wrapper around FinancialAccountingClient
func NewResilientFinancialAccountingClient(
	client FinancialAccountingClient,
	config ResilientClientConfig,
) *ResilientFinancialAccountingClient {
	// Apply default name if not provided
	if config.CircuitBreakerName == "" {
		config.CircuitBreakerName = "financial-accounting"
	}

	return &ResilientFinancialAccountingClient{
		client:          client,
		resilientClient: sharedclients.NewResilientClient(config),
	}
}

// NewResilientPartyClient creates a resilient wrapper around PartyClient
func NewResilientPartyClient(
	client PartyClient,
	config ResilientClientConfig,
) *ResilientPartyClient {
	// Apply default name if not provided
	if config.CircuitBreakerName == "" {
		config.CircuitBreakerName = "party"
	}

	return &ResilientPartyClient{
		client:          client,
		resilientClient: sharedclients.NewResilientClient(config),
	}
}

// InitiateFinancialPositionLog creates a new financial position log with resilience
func (r *ResilientPositionKeepingClient) InitiateFinancialPositionLog(
	ctx context.Context,
	req *positionkeepingv1.InitiateFinancialPositionLogRequest,
) (*positionkeepingv1.InitiateFinancialPositionLogResponse, error) {
	return sharedclients.ExecuteWithResilience(
		ctx,
		r.resilientClient,
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
	return sharedclients.ExecuteWithResilience(
		ctx,
		r.resilientClient,
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
	return sharedclients.ExecuteWithResilience(
		ctx,
		r.resilientClient,
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
	return sharedclients.ExecuteWithResilienceNoRetry(
		ctx,
		r.resilientClient,
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
	return sharedclients.ExecuteWithResilience(
		ctx,
		r.resilientClient,
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
	return sharedclients.ExecuteWithResilience(
		ctx,
		r.resilientClient,
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
	return sharedclients.ExecuteWithResilienceNoRetry(
		ctx,
		r.resilientClient,
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
	return sharedclients.ExecuteWithResilience(
		ctx,
		r.resilientClient,
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
	return sharedclients.ExecuteWithResilience(
		ctx,
		r.resilientClient,
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
	return sharedclients.ExecuteWithResilienceNoRetry(
		ctx,
		r.resilientClient,
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
	return sharedclients.ExecuteWithResilience(
		ctx,
		r.resilientClient,
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
	_, err := sharedclients.ExecuteWithResilience(
		ctx,
		r.resilientClient,
		"ValidateParty",
		func() (struct{}, error) {
			return struct{}{}, r.client.ValidateParty(ctx, partyID)
		},
	)
	return err
}

// GetParty retrieves full party details by ID with resilience
func (r *ResilientPartyClient) GetParty(ctx context.Context, partyID string) (*partyv1.Party, error) {
	return sharedclients.ExecuteWithResilience(
		ctx,
		r.resilientClient,
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
