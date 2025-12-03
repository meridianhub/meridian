// Package main provides the sabotage mechanism for simulating network failures.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	money "google.golang.org/genproto/googleapis/type/money"
)

// SabotageConfig holds configuration for the sabotage attempt.
type SabotageConfig struct {
	// DebtorAccountID is the account from which funds are debited
	DebtorAccountID string
	// AmountPence is the payment amount in pence (e.g., 10000 = GBP 100.00)
	AmountPence int64
	// InitialTimeout is the starting timeout duration for the sabotage attempt
	InitialTimeout time.Duration
	// MinTimeout is the minimum timeout duration (adaptive calibration floor)
	MinTimeout time.Duration
	// MaxAttempts is the maximum number of calibration attempts before giving up
	MaxAttempts int
	// CreditorReference is the external account to credit (IBAN format)
	CreditorReference string
	// Logger for structured logging
	Logger *slog.Logger
}

// SabotageResult captures the outcome of a sabotage attempt.
type SabotageResult struct {
	// IdempotencyKey is the key used for this payment attempt
	IdempotencyKey string
	// CorrelationID is the distributed tracing ID
	CorrelationID string
	// Attempts records each attempt made during calibration
	Attempts []SabotageAttempt
	// FinalTimeout is the timeout that achieved the desired sabotage
	FinalTimeout time.Duration
	// PaymentOrderID is the ID of the created payment order (if any)
	PaymentOrderID string
	// Success indicates whether sabotage was achieved (client timeout with server processing)
	Success bool
}

// SabotageAttempt records a single calibration attempt.
type SabotageAttempt struct {
	// AttemptNumber is the sequence number (1, 2, etc.)
	AttemptNumber int
	// Timeout is the timeout used for this attempt
	Timeout time.Duration
	// Duration is how long the attempt actually took
	Duration time.Duration
	// TimedOut indicates if the client context deadline was exceeded
	TimedOut bool
	// Error is the error message if the attempt failed (nil for success)
	Error error
	// PaymentOrderID is set if a response was received (empty if timed out)
	PaymentOrderID string
}

// Sabotage errors.
var (
	ErrSabotageConfigInvalid   = errors.New("invalid sabotage configuration")
	ErrSabotageCalibrationFail = errors.New("failed to calibrate sabotage timeout")
	ErrSabotagePaymentFailed   = errors.New("payment failed unexpectedly")
)

// Default sabotage configuration values.
const (
	DefaultSabotageTimeout     = 30 * time.Millisecond
	DefaultMinSabotageTimeout  = 10 * time.Millisecond
	DefaultMaxSabotageAttempts = 5
	DefaultCreditorReference   = "GB82WEST12345698765432" // Test IBAN
)

// GenerateIdempotencyKey creates a unique idempotency key for the demo.
// Format: HORIZON-TXN-{timestamp}-{short-uuid}
func GenerateIdempotencyKey() string {
	timestamp := time.Now().Unix()
	shortUUID := uuid.New().String()[:8]
	return fmt.Sprintf("HORIZON-TXN-%d-%s", timestamp, shortUUID)
}

// GenerateCorrelationID creates a unique correlation ID for distributed tracing.
// Format: horizon-demo-{uuid}
func GenerateCorrelationID() string {
	return fmt.Sprintf("horizon-demo-%s", uuid.New().String())
}

// RunSabotage executes the sabotage attempt with adaptive timeout calibration.
// The goal is to trigger a client-side timeout while ensuring the server still
// processes the payment request, creating a "phantom" situation where the client
// doesn't know if the payment succeeded.
//
// Calibration Strategy:
// 1. Start with InitialTimeout
// 2. If attempt succeeds (no timeout), reduce timeout by half and retry
// 3. If attempt times out, verify server-side state and return success
// 4. Continue until MaxAttempts is reached or sabotage is achieved
func RunSabotage(ctx context.Context, clients *Clients, cfg *SabotageConfig) (*SabotageResult, error) {
	if err := validateSabotageConfig(cfg); err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Generate unique identifiers for this sabotage run
	idempotencyKey := GenerateIdempotencyKey()
	correlationID := GenerateCorrelationID()

	logger.Info("sabotage: starting calibration",
		"idempotency_key", idempotencyKey,
		"correlation_id", correlationID,
		"initial_timeout", cfg.InitialTimeout,
		"min_timeout", cfg.MinTimeout,
		"max_attempts", cfg.MaxAttempts,
	)

	result := &SabotageResult{
		IdempotencyKey: idempotencyKey,
		CorrelationID:  correlationID,
		Attempts:       make([]SabotageAttempt, 0, cfg.MaxAttempts),
	}

	currentTimeout := cfg.InitialTimeout

	for attemptNum := 1; attemptNum <= cfg.MaxAttempts; attemptNum++ {
		logger.Info("sabotage: attempting payment with timeout",
			"attempt", attemptNum,
			"timeout", currentTimeout,
		)

		attempt := executeSabotageAttempt(ctx, clients, cfg, idempotencyKey, correlationID, attemptNum, currentTimeout)
		result.Attempts = append(result.Attempts, attempt)

		if attempt.TimedOut {
			// Success! Client timed out, which is what we want
			logger.Info("sabotage: client timeout achieved",
				"attempt", attemptNum,
				"timeout", currentTimeout,
				"duration", attempt.Duration,
			)
			result.Success = true
			result.FinalTimeout = currentTimeout
			// Note: PaymentOrderID will be empty because we timed out
			// The retry phase will retrieve it
			return result, nil
		}

		if attempt.Error != nil && !errors.Is(attempt.Error, context.DeadlineExceeded) {
			// Unexpected error (not a timeout)
			logger.Error("sabotage: unexpected error during payment",
				"attempt", attemptNum,
				"error", attempt.Error,
			)
			return result, fmt.Errorf("%w: %w", ErrSabotagePaymentFailed, attempt.Error)
		}

		// Payment succeeded without timeout - need to reduce timeout
		if attempt.PaymentOrderID != "" {
			result.PaymentOrderID = attempt.PaymentOrderID
			logger.Info("sabotage: payment completed without timeout, reducing timeout",
				"attempt", attemptNum,
				"payment_order_id", attempt.PaymentOrderID,
				"duration", attempt.Duration,
			)
		}

		// Reduce timeout for next attempt
		newTimeout := currentTimeout / 2
		if newTimeout < cfg.MinTimeout {
			logger.Warn("sabotage: reached minimum timeout, calibration failed",
				"min_timeout", cfg.MinTimeout,
				"last_timeout", currentTimeout,
			)
			return result, fmt.Errorf("%w: minimum timeout %v reached without achieving sabotage", ErrSabotageCalibrationFail, cfg.MinTimeout)
		}
		currentTimeout = newTimeout
	}

	logger.Warn("sabotage: max attempts reached without achieving timeout")
	return result, fmt.Errorf("%w: reached max attempts (%d) without triggering timeout", ErrSabotageCalibrationFail, cfg.MaxAttempts)
}

// executeSabotageAttempt performs a single payment attempt with the specified timeout.
func executeSabotageAttempt(
	ctx context.Context,
	clients *Clients,
	cfg *SabotageConfig,
	idempotencyKey string,
	correlationID string,
	attemptNum int,
	timeout time.Duration,
) SabotageAttempt {
	attempt := SabotageAttempt{
		AttemptNumber: attemptNum,
		Timeout:       timeout,
	}

	// Create context with aggressive timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Add correlation ID to context for distributed tracing
	timeoutCtx = ContextWithCorrelationID(timeoutCtx, correlationID)

	// Build the payment request
	req := buildPaymentRequest(cfg, idempotencyKey, correlationID)

	// Execute the payment
	start := time.Now()
	resp, err := clients.PaymentOrder.InitiatePaymentOrder(timeoutCtx, req)
	attempt.Duration = time.Since(start)

	if err != nil {
		attempt.Error = err
		// Check if this was a timeout
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			attempt.TimedOut = true
		}
		return attempt
	}

	// Success - payment completed within timeout
	if resp.GetPaymentOrder() != nil {
		attempt.PaymentOrderID = resp.GetPaymentOrder().GetPaymentOrderId()
	}

	return attempt
}

// buildPaymentRequest constructs the InitiatePaymentOrderRequest for the sabotage attempt.
func buildPaymentRequest(cfg *SabotageConfig, idempotencyKey, correlationID string) *paymentorderv1.InitiatePaymentOrderRequest {
	return &paymentorderv1.InitiatePaymentOrderRequest{
		DebtorAccountId:   cfg.DebtorAccountID,
		CreditorReference: cfg.CreditorReference,
		Amount: &commonv1.MoneyAmount{
			Amount: penceToPoundsPayment(cfg.AmountPence),
		},
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: idempotencyKey,
		},
		CorrelationId: correlationID,
	}
}

// penceToPoundsPayment converts pence to google.type.Money with GBP currency.
// This is similar to preflight.go's penceToPounds but kept separate for clarity.
func penceToPoundsPayment(pence int64) *money.Money {
	units := pence / 100
	fractionalPence := pence % 100
	nanos := int32(fractionalPence) * 10000000 // #nosec G115 - safe: fractionalPence is 0-99

	return &money.Money{
		CurrencyCode: "GBP",
		Units:        units,
		Nanos:        nanos,
	}
}

// validateSabotageConfig validates the sabotage configuration.
func validateSabotageConfig(cfg *SabotageConfig) error {
	if cfg == nil {
		return fmt.Errorf("%w: config is nil", ErrSabotageConfigInvalid)
	}

	if cfg.DebtorAccountID == "" {
		return fmt.Errorf("%w: DebtorAccountID is required", ErrSabotageConfigInvalid)
	}

	if cfg.AmountPence <= 0 {
		return fmt.Errorf("%w: AmountPence must be positive", ErrSabotageConfigInvalid)
	}

	if cfg.InitialTimeout <= 0 {
		return fmt.Errorf("%w: InitialTimeout must be positive", ErrSabotageConfigInvalid)
	}

	if cfg.MinTimeout <= 0 {
		return fmt.Errorf("%w: MinTimeout must be positive", ErrSabotageConfigInvalid)
	}

	if cfg.MinTimeout > cfg.InitialTimeout {
		return fmt.Errorf("%w: MinTimeout (%v) cannot exceed InitialTimeout (%v)", ErrSabotageConfigInvalid, cfg.MinTimeout, cfg.InitialTimeout)
	}

	if cfg.MaxAttempts <= 0 {
		return fmt.Errorf("%w: MaxAttempts must be positive", ErrSabotageConfigInvalid)
	}

	if cfg.CreditorReference == "" {
		return fmt.Errorf("%w: CreditorReference is required", ErrSabotageConfigInvalid)
	}

	return nil
}

// DefaultSabotageConfig returns a SabotageConfig with default values.
// Caller must set DebtorAccountID.
func DefaultSabotageConfig() *SabotageConfig {
	return &SabotageConfig{
		AmountPence:       10000, // GBP 100.00
		InitialTimeout:    DefaultSabotageTimeout,
		MinTimeout:        DefaultMinSabotageTimeout,
		MaxAttempts:       DefaultMaxSabotageAttempts,
		CreditorReference: DefaultCreditorReference,
		Logger:            slog.Default(),
	}
}

// ToAttemptReport converts a SabotageAttempt to an AttemptReport for JSON output.
func (a *SabotageAttempt) ToAttemptReport(idempotencyKey string) AttemptReport {
	report := AttemptReport{
		Attempt:        a.AttemptNumber,
		IdempotencyKey: idempotencyKey,
		DurationMs:     a.Duration.Milliseconds(),
		PaymentOrderID: a.PaymentOrderID,
	}

	if a.TimedOut {
		report.Status = AttemptStatusClientTimeout
		if a.Error != nil {
			report.Error = a.Error.Error()
		} else {
			report.Error = "context deadline exceeded"
		}
	} else if a.Error != nil {
		report.Status = AttemptStatusError
		report.Error = a.Error.Error()
	} else {
		report.Status = AttemptStatusSuccess
	}

	return report
}
