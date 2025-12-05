// Package main implements the Horizon Integrity Proof CLI tool.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Report represents the complete integrity proof report.
// This matches the PRD specification in FR-5.
type Report struct {
	// DemoID is a unique identifier for this demo run (e.g., "horizon-proof-{timestamp}")
	DemoID string `json:"demo_id"`
	// Timestamp is when the demo was executed in RFC3339 format
	Timestamp string `json:"timestamp"`
	// Account holds the test account details and balance verification
	Account AccountReport `json:"account"`
	// Attempts records each payment attempt made during the demo
	Attempts []AttemptReport `json:"attempts"`
	// Verification contains the forensic audit results
	Verification VerificationReport `json:"verification"`
	// Verdict is PASSED if all verification checks pass, otherwise FAILED
	Verdict string `json:"verdict"`
}

// AccountReport captures the test account state.
type AccountReport struct {
	// ID is the account identifier
	ID string `json:"id"`
	// InitialBalanceCents is the balance after initial deposit (e.g., 100000 for GBP 1,000.00)
	InitialBalanceCents int64 `json:"initial_balance_cents"`
	// FinalBalanceCents is the balance after all payment attempts
	FinalBalanceCents int64 `json:"final_balance_cents"`
	// ExpectedBalanceCents is what the balance should be (initial - payment amount)
	ExpectedBalanceCents int64 `json:"expected_balance_cents"`
}

// AttemptReport records details of a single payment attempt.
type AttemptReport struct {
	// Attempt is the sequence number (1, 2, etc.)
	Attempt int `json:"attempt"`
	// IdempotencyKey is the key used for this attempt
	IdempotencyKey string `json:"idempotency_key"`
	// Status is the attempt outcome: "CLIENT_TIMEOUT", "SUCCESS", "ERROR"
	Status string `json:"status"`
	// Error contains the error message if the attempt failed (omitted if empty)
	Error string `json:"error,omitempty"`
	// DurationMs is how long the attempt took in milliseconds
	DurationMs int64 `json:"duration_ms"`
	// PaymentOrderID is the payment order ID on success (omitted if empty)
	PaymentOrderID string `json:"payment_order_id,omitempty"`
}

// VerificationReport contains the forensic audit results.
type VerificationReport struct {
	// RequestsSent is the total number of payment requests made
	RequestsSent int `json:"requests_sent"`
	// TransactionsRecorded is the number of actual transactions created
	TransactionsRecorded int `json:"transactions_recorded"`
	// BalanceCorrect is true if final balance matches expected balance
	BalanceCorrect bool `json:"balance_correct"`
	// NoDoubleSpend is true if exactly one transaction was recorded
	NoDoubleSpend bool `json:"no_double_spend"`
	// LienVerification contains optional lien state verification (omitted if not performed)
	LienVerification *LienVerificationReport `json:"lien_verification,omitempty"`
}

// LienVerificationReport contains the optional lien state verification results.
// This is a stretch goal that verifies no orphaned liens exist after payment completion.
type LienVerificationReport struct {
	// LienID is the lien that was verified
	LienID string `json:"lien_id"`
	// Status is the current lien status (EXECUTED, ACTIVE, TERMINATED)
	Status string `json:"status"`
	// IsOrphaned is true if the lien is still ACTIVE (should have been executed/terminated)
	IsOrphaned bool `json:"is_orphaned"`
	// Verified is true if the lien verification completed successfully
	Verified bool `json:"verified"`
}

// Report verdict string constants for JSON output.
const (
	ReportVerdictPassed = "PASSED"
	ReportVerdictFailed = "FAILED"
)

// Attempt status string constants for JSON output.
const (
	AttemptStatusClientTimeout = "CLIENT_TIMEOUT"
	AttemptStatusSuccess       = "SUCCESS"
	AttemptStatusError         = "ERROR"
)

// NewReport creates a new Report with a unique demo ID and current timestamp.
func NewReport() *Report {
	now := time.Now().UTC()
	return &Report{
		DemoID:    fmt.Sprintf("horizon-proof-%d", now.Unix()),
		Timestamp: now.Format(time.RFC3339),
		Attempts:  make([]AttemptReport, 0),
	}
}

// CalculateVerdict determines the verdict based on verification results.
// Returns ReportVerdictPassed if:
// - BalanceCorrect is true
// - TransactionsRecorded equals 1
// - NoDoubleSpend is true
// Otherwise returns ReportVerdictFailed.
func (r *Report) CalculateVerdict() string {
	if r.Verification.BalanceCorrect &&
		r.Verification.TransactionsRecorded == 1 &&
		r.Verification.NoDoubleSpend {
		return ReportVerdictPassed
	}
	return ReportVerdictFailed
}

// AddAttempt adds a payment attempt to the report.
func (r *Report) AddAttempt(attempt AttemptReport) {
	r.Attempts = append(r.Attempts, attempt)
	r.Verification.RequestsSent = len(r.Attempts)
}

// SetAccountInfo sets the account information in the report.
func (r *Report) SetAccountInfo(id string, initialBalanceCents, expectedBalanceCents int64) {
	r.Account = AccountReport{
		ID:                   id,
		InitialBalanceCents:  initialBalanceCents,
		ExpectedBalanceCents: expectedBalanceCents,
	}
}

// SetFinalBalance sets the final balance and calculates verification results.
func (r *Report) SetFinalBalance(finalBalanceCents int64, transactionsRecorded int) {
	r.Account.FinalBalanceCents = finalBalanceCents
	r.Verification.TransactionsRecorded = transactionsRecorded
	r.Verification.BalanceCorrect = finalBalanceCents == r.Account.ExpectedBalanceCents
	r.Verification.NoDoubleSpend = transactionsRecorded == 1
	r.Verdict = r.CalculateVerdict()
}

// SetLienVerification sets the optional lien verification results.
// This is a non-blocking check that doesn't affect the overall verdict.
func (r *Report) SetLienVerification(lienResult *LienAuditResult) {
	if lienResult == nil {
		return
	}

	r.Verification.LienVerification = &LienVerificationReport{
		LienID:     lienResult.LienID,
		Status:     lienResult.LienStatus,
		IsOrphaned: lienResult.IsOrphaned,
		Verified:   lienResult.Verdict != AuditVerdictError,
	}
}

// ToJSON converts the report to a formatted JSON string.
func (r *Report) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// WriteToFile writes the report to the specified file path.
func (r *Report) WriteToFile(path string) error {
	data, err := r.ToJSON()
	if err != nil {
		return fmt.Errorf("marshaling report to JSON: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing report to %s: %w", path, err)
	}

	return nil
}
