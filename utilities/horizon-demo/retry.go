// Package main provides the idempotency retry mechanism for the Horizon Integrity Proof demo.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	money "google.golang.org/genproto/googleapis/type/money"
)

// RetryConfig holds configuration for the idempotency retry attempt.
type RetryConfig struct {
	// IdempotencyKey is the key from the original sabotage attempt
	IdempotencyKey string
	// CorrelationID is the distributed tracing ID from the original attempt
	CorrelationID string
	// DebtorAccountID is the account from which funds are debited
	DebtorAccountID string
	// AmountPence is the payment amount in pence (must match original)
	AmountPence int64
	// CreditorReference is the external account to credit (must match original)
	CreditorReference string
	// WaitBeforeRetry is the duration to wait before sending retry (allows saga completion)
	WaitBeforeRetry time.Duration
	// Timeout is the timeout for the retry request (should be generous)
	Timeout time.Duration
	// Logger for structured logging
	Logger *slog.Logger
}

// RetryResult captures the outcome of an idempotency retry attempt.
type RetryResult struct {
	// IdempotencyKey is the key used for this retry
	IdempotencyKey string
	// CorrelationID is the distributed tracing ID
	CorrelationID string
	// PaymentOrderID is the ID returned by the server
	PaymentOrderID string
	// PaymentStatus is the status of the payment order
	PaymentStatus string
	// Duration is how long the retry took
	Duration time.Duration
	// IdempotencyHit indicates if the server returned an existing payment order
	IdempotencyHit bool
	// Success indicates if the retry completed successfully
	Success bool
	// Error captures any error that occurred (nil on success)
	Error error
}

// Retry errors.
var (
	ErrRetryConfigInvalid     = errors.New("invalid retry configuration")
	ErrRetryFailed            = errors.New("retry request failed")
	ErrRetryNoPaymentOrder    = errors.New("retry returned no payment order")
	ErrRetryPaymentOrderIDNil = errors.New("retry returned nil payment order ID")
)

// Default retry configuration values.
const (
	// DefaultRetryWait is the default wait time before retry (2 seconds allows saga completion).
	DefaultRetryWait = 2 * time.Second
	// DefaultRetryTimeout is the default timeout for the retry request (30 seconds is generous).
	DefaultRetryTimeout = defaults.DefaultRPCTimeout
)

// RunRetry executes the idempotency retry after waiting for saga completion.
// This sends an identical payment request with the same IdempotencyKey and expects
// the server to return the existing PaymentOrder (idempotency hit) rather than
// creating a duplicate.
//
// The retry flow:
// 1. Wait for WaitBeforeRetry duration to allow server-side saga to complete
// 2. Send identical InitiatePaymentOrderRequest with same idempotency_key
// 3. Verify response contains valid PaymentOrder
// 4. Log "idempotency key match detected" on success
func RunRetry(ctx context.Context, clients *Clients, cfg *RetryConfig) (*RetryResult, error) {
	if err := validateRetryConfig(cfg); err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	result := &RetryResult{
		IdempotencyKey: cfg.IdempotencyKey,
		CorrelationID:  cfg.CorrelationID,
	}

	// Wait for server-side saga to complete
	logger.Info("retry: waiting before retry to allow saga completion",
		"idempotency_key", cfg.IdempotencyKey,
		"wait_duration", cfg.WaitBeforeRetry,
	)

	select {
	case <-time.After(cfg.WaitBeforeRetry):
		// Wait completed
	case <-ctx.Done():
		result.Error = fmt.Errorf("context cancelled while waiting: %w", ctx.Err())
		return result, result.Error
	}

	// Create context with generous timeout for retry
	retryCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	// Add correlation ID to context for distributed tracing
	retryCtx = ContextWithCorrelationID(retryCtx, cfg.CorrelationID)

	logger.Info("retry: sending retry request with same idempotency key",
		"idempotency_key", cfg.IdempotencyKey,
		"correlation_id", cfg.CorrelationID,
		"timeout", cfg.Timeout,
	)

	// Build identical payment request
	req := buildRetryRequest(cfg)

	// Execute the retry
	start := time.Now()
	resp, err := clients.PaymentOrder.InitiatePaymentOrder(retryCtx, req)
	result.Duration = time.Since(start)

	if err != nil {
		result.Error = fmt.Errorf("%w: %w", ErrRetryFailed, err)
		logger.Error("retry: request failed",
			"idempotency_key", cfg.IdempotencyKey,
			"error", err,
			"duration", result.Duration,
		)
		return result, result.Error
	}

	// Validate response
	if resp.GetPaymentOrder() == nil {
		result.Error = ErrRetryNoPaymentOrder
		logger.Error("retry: response contains no payment order",
			"idempotency_key", cfg.IdempotencyKey,
		)
		return result, result.Error
	}

	paymentOrder := resp.GetPaymentOrder()
	if paymentOrder.GetPaymentOrderId() == "" {
		result.Error = ErrRetryPaymentOrderIDNil
		logger.Error("retry: payment order has empty ID",
			"idempotency_key", cfg.IdempotencyKey,
		)
		return result, result.Error
	}

	// Success - populate result
	result.PaymentOrderID = paymentOrder.GetPaymentOrderId()
	result.PaymentStatus = paymentOrder.GetStatus().String()
	result.IdempotencyHit = true // Server returned existing order (idempotency behavior)
	result.Success = true

	logger.Info("retry: idempotency key match detected",
		"idempotency_key", cfg.IdempotencyKey,
		"payment_order_id", result.PaymentOrderID,
		"payment_status", result.PaymentStatus,
		"duration", result.Duration,
	)

	return result, nil
}

// buildRetryRequest constructs the InitiatePaymentOrderRequest for the retry.
// This must be identical to the original sabotage request.
func buildRetryRequest(cfg *RetryConfig) *paymentorderv1.InitiatePaymentOrderRequest {
	return &paymentorderv1.InitiatePaymentOrderRequest{
		DebtorAccountId:   cfg.DebtorAccountID,
		CreditorReference: cfg.CreditorReference,
		Amount: &commonv1.MoneyAmount{
			Amount: penceToPoundsRetry(cfg.AmountPence),
		},
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: cfg.IdempotencyKey,
		},
		CorrelationId: cfg.CorrelationID,
	}
}

// penceToPoundsRetry converts pence to google.type.Money with GBP currency.
func penceToPoundsRetry(pence int64) *money.Money {
	units := pence / 100
	fractionalPence := pence % 100
	nanos := int32(fractionalPence) * 10000000 // #nosec G115 - safe: fractionalPence is 0-99

	return &money.Money{
		CurrencyCode: "GBP",
		Units:        units,
		Nanos:        nanos,
	}
}

// validateRetryConfig validates the retry configuration.
func validateRetryConfig(cfg *RetryConfig) error {
	if cfg == nil {
		return fmt.Errorf("%w: config is nil", ErrRetryConfigInvalid)
	}

	if cfg.IdempotencyKey == "" {
		return fmt.Errorf("%w: IdempotencyKey is required", ErrRetryConfigInvalid)
	}

	if cfg.CorrelationID == "" {
		return fmt.Errorf("%w: CorrelationID is required", ErrRetryConfigInvalid)
	}

	if cfg.DebtorAccountID == "" {
		return fmt.Errorf("%w: DebtorAccountID is required", ErrRetryConfigInvalid)
	}

	if cfg.AmountPence <= 0 {
		return fmt.Errorf("%w: AmountPence must be positive", ErrRetryConfigInvalid)
	}

	if cfg.CreditorReference == "" {
		return fmt.Errorf("%w: CreditorReference is required", ErrRetryConfigInvalid)
	}

	if cfg.WaitBeforeRetry < 0 {
		return fmt.Errorf("%w: WaitBeforeRetry cannot be negative", ErrRetryConfigInvalid)
	}

	if cfg.Timeout <= 0 {
		return fmt.Errorf("%w: Timeout must be positive", ErrRetryConfigInvalid)
	}

	return nil
}

// DefaultRetryConfig returns a RetryConfig with default values.
// Caller must set IdempotencyKey, CorrelationID, DebtorAccountID, AmountPence, and CreditorReference.
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		WaitBeforeRetry: DefaultRetryWait,
		Timeout:         DefaultRetryTimeout,
		Logger:          slog.Default(),
	}
}

// NewRetryConfigFromSabotage creates a RetryConfig from SabotageConfig and SabotageResult.
// This ensures the retry uses identical parameters to the original sabotage attempt.
func NewRetryConfigFromSabotage(sabCfg *SabotageConfig, sabResult *SabotageResult) *RetryConfig {
	return &RetryConfig{
		IdempotencyKey:    sabResult.IdempotencyKey,
		CorrelationID:     sabResult.CorrelationID,
		DebtorAccountID:   sabCfg.DebtorAccountID,
		AmountPence:       sabCfg.AmountPence,
		CreditorReference: sabCfg.CreditorReference,
		WaitBeforeRetry:   DefaultRetryWait,
		Timeout:           DefaultRetryTimeout,
		Logger:            sabCfg.Logger,
	}
}

// ToAttemptReport converts a RetryResult to an AttemptReport for JSON output.
func (r *RetryResult) ToAttemptReport(attemptNum int) AttemptReport {
	report := AttemptReport{
		Attempt:        attemptNum,
		IdempotencyKey: r.IdempotencyKey,
		DurationMs:     r.Duration.Milliseconds(),
		PaymentOrderID: r.PaymentOrderID,
	}

	if r.Success {
		report.Status = AttemptStatusSuccess
	} else if r.Error != nil {
		report.Status = AttemptStatusError
		report.Error = r.Error.Error()
	}

	return report
}
