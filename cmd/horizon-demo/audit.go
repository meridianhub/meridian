// Package main provides forensic audit functionality for the Horizon Integrity Proof demo.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
)

// AuditConfig holds configuration for the forensic audit phase.
type AuditConfig struct {
	// AccountID is the test account to audit
	AccountID string
	// InitialBalancePence is the account balance before any payments
	InitialBalancePence int64
	// PaymentAmountPence is the expected payment amount
	PaymentAmountPence int64
	// Logger for structured logging
	Logger *slog.Logger
}

// AuditResult captures the outcome of the forensic audit.
type AuditResult struct {
	// AccountID is the audited account
	AccountID string
	// InitialBalancePence is the starting balance
	InitialBalancePence int64
	// FinalBalancePence is the actual final balance
	FinalBalancePence int64
	// ExpectedBalancePence is what the balance should be (initial - payment)
	ExpectedBalancePence int64
	// BalanceCorrect indicates if final balance matches expected
	BalanceCorrect bool
	// BalanceStatus describes the balance verification outcome
	BalanceStatus BalanceStatus
	// TransactionsRecorded is the number of payment orders found (for future use)
	TransactionsRecorded int
	// NoDoubleSpend indicates exactly one transaction was recorded
	NoDoubleSpend bool
	// Verdict is the final audit determination
	Verdict AuditVerdict
	// Error captures any error during audit (nil on success)
	Error error
}

// BalanceStatus represents the outcome of balance verification.
type BalanceStatus int

const (
	// BalanceStatusCorrect indicates balance is exactly as expected (single payment).
	BalanceStatusCorrect BalanceStatus = iota
	// BalanceStatusDoubleSpend indicates balance shows two payments were deducted.
	BalanceStatusDoubleSpend
	// BalanceStatusNoPayment indicates no payment was executed.
	BalanceStatusNoPayment
	// BalanceStatusUnexpected indicates balance doesn't match any expected pattern.
	BalanceStatusUnexpected
)

// String constants for audit status values.
const (
	balanceStatusCorrectStr     = "CORRECT"
	balanceStatusDoubleSpendStr = "DOUBLE_SPEND"
	balanceStatusNoPaymentStr   = "NO_PAYMENT"
	balanceStatusUnexpectedStr  = "UNEXPECTED"
	statusUnknownStr            = "UNKNOWN"
)

func (s BalanceStatus) String() string {
	switch s {
	case BalanceStatusCorrect:
		return balanceStatusCorrectStr
	case BalanceStatusDoubleSpend:
		return balanceStatusDoubleSpendStr
	case BalanceStatusNoPayment:
		return balanceStatusNoPaymentStr
	case BalanceStatusUnexpected:
		return balanceStatusUnexpectedStr
	default:
		return statusUnknownStr
	}
}

// AuditVerdict represents the final audit determination.
type AuditVerdict int

const (
	// AuditVerdictPass indicates the system behaved correctly.
	AuditVerdictPass AuditVerdict = iota
	// AuditVerdictFail indicates an integrity issue was detected.
	AuditVerdictFail
	// AuditVerdictError indicates the audit could not complete.
	AuditVerdictError
)

// Audit verdict string constants.
const (
	auditVerdictPassStr  = "PASS"
	auditVerdictFailStr  = "FAIL"
	auditVerdictErrorStr = "ERROR"
)

func (v AuditVerdict) String() string {
	switch v {
	case AuditVerdictPass:
		return auditVerdictPassStr
	case AuditVerdictFail:
		return auditVerdictFailStr
	case AuditVerdictError:
		return auditVerdictErrorStr
	default:
		return statusUnknownStr
	}
}

// Audit errors.
var (
	ErrAuditConfigInvalid    = errors.New("invalid audit configuration")
	ErrAuditBalanceRetrieval = errors.New("failed to retrieve account balance")
	ErrAuditDoubleSpend      = errors.New("double-spend detected: payment executed twice")
	ErrAuditNoPayment        = errors.New("no payment executed: saga failed")
	ErrAuditUnexpectedState  = errors.New("unexpected account state")
)

// RunAudit executes the forensic audit phase of the demo.
// This verifies that exactly one payment was executed (no double-spend).
//
// Assertion branches:
// 1. balance == (initial - payment): PASS - correct single deduction
// 2. balance == (initial - 2*payment): FAIL - double-spend detected
// 3. balance == initial: FAIL - no payment executed
// 4. any other balance: FAIL - unexpected state
func RunAudit(ctx context.Context, clients *Clients, cfg *AuditConfig) (*AuditResult, error) {
	if err := validateAuditConfig(cfg); err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	result := &AuditResult{
		AccountID:            cfg.AccountID,
		InitialBalancePence:  cfg.InitialBalancePence,
		ExpectedBalancePence: cfg.InitialBalancePence - cfg.PaymentAmountPence,
	}

	logger.Info("audit: starting balance verification",
		"account_id", cfg.AccountID,
		"initial_balance_pence", cfg.InitialBalancePence,
		"payment_amount_pence", cfg.PaymentAmountPence,
		"expected_balance_pence", result.ExpectedBalancePence,
	)

	// Retrieve current account balance
	retrieveResp, err := clients.CurrentAccount.RetrieveCurrentAccount(ctx, &currentaccountv1.RetrieveCurrentAccountRequest{
		AccountId: cfg.AccountID,
	})
	if err != nil {
		result.Error = fmt.Errorf("%w: %w", ErrAuditBalanceRetrieval, err)
		result.Verdict = AuditVerdictError
		logger.Error("audit: failed to retrieve account balance",
			"account_id", cfg.AccountID,
			"error", err,
		)
		return result, result.Error
	}

	// Extract balance using moneyToPence from preflight.go
	result.FinalBalancePence = moneyToPence(
		retrieveResp.GetFacility().GetCurrentBalance().GetCurrentBalance().GetAmount(),
	)

	logger.Info("audit: retrieved final balance",
		"account_id", cfg.AccountID,
		"final_balance_pence", result.FinalBalancePence,
		"final_balance_gbp", fmt.Sprintf("%.2f", float64(result.FinalBalancePence)/100),
	)

	// Calculate expected values for assertion branches
	expectedSinglePayment := cfg.InitialBalancePence - cfg.PaymentAmountPence
	expectedDoublePayment := cfg.InitialBalancePence - (2 * cfg.PaymentAmountPence)
	expectedNoPayment := cfg.InitialBalancePence

	// Perform assertion branches
	switch result.FinalBalancePence {
	case expectedSinglePayment:
		// PASS: Correct single deduction
		result.BalanceCorrect = true
		result.BalanceStatus = BalanceStatusCorrect
		result.TransactionsRecorded = 1
		result.NoDoubleSpend = true
		result.Verdict = AuditVerdictPass

		logger.Info("audit: PASS - correct single deduction",
			"account_id", cfg.AccountID,
			"final_balance_pence", result.FinalBalancePence,
			"expected_balance_pence", expectedSinglePayment,
		)

	case expectedDoublePayment:
		// FAIL: Double-spend detected
		result.BalanceCorrect = false
		result.BalanceStatus = BalanceStatusDoubleSpend
		result.TransactionsRecorded = 2
		result.NoDoubleSpend = false
		result.Verdict = AuditVerdictFail
		result.Error = ErrAuditDoubleSpend

		logger.Error("audit: FAIL - double-spend detected",
			"account_id", cfg.AccountID,
			"final_balance_pence", result.FinalBalancePence,
			"expected_balance_pence", expectedSinglePayment,
			"double_spend_balance", expectedDoublePayment,
		)

	case expectedNoPayment:
		// FAIL: No payment executed
		result.BalanceCorrect = false
		result.BalanceStatus = BalanceStatusNoPayment
		result.TransactionsRecorded = 0
		result.NoDoubleSpend = true // Technically true, but saga failed
		result.Verdict = AuditVerdictFail
		result.Error = ErrAuditNoPayment

		logger.Error("audit: FAIL - no payment executed (saga failed)",
			"account_id", cfg.AccountID,
			"final_balance_pence", result.FinalBalancePence,
			"initial_balance_pence", cfg.InitialBalancePence,
		)

	default:
		// FAIL: Unexpected state
		result.BalanceCorrect = false
		result.BalanceStatus = BalanceStatusUnexpected
		result.TransactionsRecorded = -1 // Unknown
		result.NoDoubleSpend = false
		result.Verdict = AuditVerdictFail
		result.Error = fmt.Errorf("%w: balance %d pence does not match any expected value",
			ErrAuditUnexpectedState, result.FinalBalancePence)

		logger.Error("audit: FAIL - unexpected account state",
			"account_id", cfg.AccountID,
			"final_balance_pence", result.FinalBalancePence,
			"expected_single_payment", expectedSinglePayment,
			"expected_double_payment", expectedDoublePayment,
			"expected_no_payment", expectedNoPayment,
		)
	}

	return result, result.Error
}

// validateAuditConfig validates the audit configuration.
func validateAuditConfig(cfg *AuditConfig) error {
	if cfg == nil {
		return fmt.Errorf("%w: config is nil", ErrAuditConfigInvalid)
	}

	if cfg.AccountID == "" {
		return fmt.Errorf("%w: AccountID is required", ErrAuditConfigInvalid)
	}

	if cfg.InitialBalancePence <= 0 {
		return fmt.Errorf("%w: InitialBalancePence must be positive", ErrAuditConfigInvalid)
	}

	if cfg.PaymentAmountPence <= 0 {
		return fmt.Errorf("%w: PaymentAmountPence must be positive", ErrAuditConfigInvalid)
	}

	if cfg.PaymentAmountPence > cfg.InitialBalancePence {
		return fmt.Errorf("%w: PaymentAmountPence (%d) cannot exceed InitialBalancePence (%d)",
			ErrAuditConfigInvalid, cfg.PaymentAmountPence, cfg.InitialBalancePence)
	}

	return nil
}

// DefaultAuditConfig returns an AuditConfig with default values.
// Caller must set AccountID.
func DefaultAuditConfig() *AuditConfig {
	return &AuditConfig{
		InitialBalancePence: 100000, // GBP 1,000.00
		PaymentAmountPence:  10000,  // GBP 100.00
		Logger:              slog.Default(),
	}
}

// NewAuditConfigFromPreFlight creates an AuditConfig from PreFlightResult.
// This ensures the audit uses the actual values from the pre-flight phase.
func NewAuditConfigFromPreFlight(preflight *PreFlightResult, paymentAmountPence int64, logger *slog.Logger) *AuditConfig {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuditConfig{
		AccountID:           preflight.AccountID,
		InitialBalancePence: preflight.InitialBalancePence,
		PaymentAmountPence:  paymentAmountPence,
		Logger:              logger,
	}
}

// ToVerificationReport converts an AuditResult to a VerificationReport for the JSON report.
func (r *AuditResult) ToVerificationReport(requestsSent int) VerificationReport {
	return VerificationReport{
		RequestsSent:         requestsSent,
		TransactionsRecorded: r.TransactionsRecorded,
		BalanceCorrect:       r.BalanceCorrect,
		NoDoubleSpend:        r.NoDoubleSpend,
	}
}
