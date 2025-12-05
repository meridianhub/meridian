// Package main provides lien verification for the Horizon Integrity Proof demo.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
)

// LienAuditConfig holds configuration for the lien verification audit.
type LienAuditConfig struct {
	// LienID is the lien to verify (obtained from PaymentOrder)
	LienID string
	// AccountID is the expected account for the lien
	AccountID string
	// Logger for structured logging
	Logger *slog.Logger
}

// LienAuditResult captures the outcome of the lien verification.
type LienAuditResult struct {
	// LienID is the audited lien
	LienID string
	// AccountID is the lien's account
	AccountID string
	// LienStatus is the current status of the lien
	LienStatus string
	// ExpectedStatus is what the status should be (EXECUTED for completed payments)
	ExpectedStatus string
	// IsOrphaned indicates if the lien is still ACTIVE (orphaned reservation)
	IsOrphaned bool
	// Verdict is the audit determination for this check
	Verdict AuditVerdict
	// Error captures any error during audit (nil on success)
	Error error
}

// Lien audit errors.
var (
	ErrLienAuditConfigInvalid    = errors.New("invalid lien audit configuration")
	ErrLienAuditRetrieveFailed   = errors.New("failed to retrieve lien")
	ErrLienAuditOrphaned         = errors.New("orphaned lien detected: lien still ACTIVE after payment completion")
	ErrLienAuditUnexpectedStatus = errors.New("lien in unexpected status")
)

// RunLienAudit executes the lien verification.
// This retrieves the lien by ID and verifies it is in EXECUTED status (not ACTIVE).
//
// Assertion branches:
// 1. Lien status == EXECUTED: PASS - lien was properly converted to debit
// 2. Lien status == ACTIVE: WARN - orphaned lien (reservation not released)
// 3. Lien status == TERMINATED: INFO - lien was released without execution (saga compensation)
// 4. Lien not found or error: ERROR - cannot verify
//
// This is a non-blocking check - orphaned liens are logged as warnings but don't fail the demo.
func RunLienAudit(ctx context.Context, clients *Clients, cfg *LienAuditConfig) (*LienAuditResult, error) {
	if err := validateLienAuditConfig(cfg); err != nil {
		return nil, err
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	result := &LienAuditResult{
		LienID:         cfg.LienID,
		AccountID:      cfg.AccountID,
		ExpectedStatus: currentaccountv1.LienStatus_LIEN_STATUS_EXECUTED.String(),
	}

	logger.Info("lien audit: starting verification",
		"lien_id", cfg.LienID,
		"account_id", cfg.AccountID,
	)

	// Retrieve lien details
	retrieveResp, err := clients.CurrentAccount.RetrieveLien(ctx, &currentaccountv1.RetrieveLienRequest{
		LienId: cfg.LienID,
	})
	if err != nil {
		result.Error = fmt.Errorf("%w: %w", ErrLienAuditRetrieveFailed, err)
		result.Verdict = AuditVerdictError
		logger.Error("lien audit: failed to retrieve lien",
			"lien_id", cfg.LienID,
			"error", err,
		)
		return result, result.Error
	}

	lien := retrieveResp.GetLien()
	if lien == nil {
		result.Error = fmt.Errorf("%w: response has nil lien", ErrLienAuditRetrieveFailed)
		result.Verdict = AuditVerdictError
		logger.Error("lien audit: nil lien in response", "lien_id", cfg.LienID)
		return result, result.Error
	}

	result.LienStatus = lien.GetStatus().String()

	// Verify lien belongs to expected account
	if lien.GetAccountId() != cfg.AccountID {
		logger.Warn("lien audit: lien account mismatch",
			"lien_id", cfg.LienID,
			"expected_account", cfg.AccountID,
			"actual_account", lien.GetAccountId(),
		)
	}

	logger.Info("lien audit: retrieved lien",
		"lien_id", cfg.LienID,
		"status", result.LienStatus,
		"account_id", lien.GetAccountId(),
	)

	// Perform assertion branches
	switch lien.GetStatus() {
	case currentaccountv1.LienStatus_LIEN_STATUS_EXECUTED:
		// PASS: Lien was properly converted to debit
		result.IsOrphaned = false
		result.Verdict = AuditVerdictPass

		logger.Info("lien audit: PASS - lien executed correctly",
			"lien_id", cfg.LienID,
			"status", result.LienStatus,
		)

	case currentaccountv1.LienStatus_LIEN_STATUS_ACTIVE:
		// WARN: Orphaned lien - reservation not released
		result.IsOrphaned = true
		result.Verdict = AuditVerdictPass // Non-blocking, just informational
		result.Error = ErrLienAuditOrphaned

		logger.Warn("lien audit: WARN - orphaned lien detected",
			"lien_id", cfg.LienID,
			"status", result.LienStatus,
			"message", "lien still ACTIVE after payment, may indicate saga compensation bug",
		)

	case currentaccountv1.LienStatus_LIEN_STATUS_TERMINATED:
		// INFO: Lien was released without execution (saga compensation path)
		result.IsOrphaned = false
		result.Verdict = AuditVerdictPass // Valid state for compensated transactions

		logger.Info("lien audit: INFO - lien terminated (saga compensation)",
			"lien_id", cfg.LienID,
			"status", result.LienStatus,
		)

	case currentaccountv1.LienStatus_LIEN_STATUS_UNSPECIFIED:
		// WARN: Unspecified status (should not occur in practice)
		result.IsOrphaned = false
		result.Verdict = AuditVerdictPass // Non-blocking
		result.Error = fmt.Errorf("%w: got %s", ErrLienAuditUnexpectedStatus, result.LienStatus)

		logger.Warn("lien audit: WARN - unexpected lien status",
			"lien_id", cfg.LienID,
			"status", result.LienStatus,
		)
	}

	return result, nil // Return nil error for non-blocking checks
}

// validateLienAuditConfig validates the lien audit configuration.
func validateLienAuditConfig(cfg *LienAuditConfig) error {
	if cfg == nil {
		return fmt.Errorf("%w: config is nil", ErrLienAuditConfigInvalid)
	}

	if cfg.LienID == "" {
		return fmt.Errorf("%w: LienID is required", ErrLienAuditConfigInvalid)
	}

	if cfg.AccountID == "" {
		return fmt.Errorf("%w: AccountID is required", ErrLienAuditConfigInvalid)
	}

	return nil
}

// NewLienAuditConfig creates a LienAuditConfig with the given parameters.
func NewLienAuditConfig(lienID, accountID string, logger *slog.Logger) *LienAuditConfig {
	if logger == nil {
		logger = slog.Default()
	}
	return &LienAuditConfig{
		LienID:    lienID,
		AccountID: accountID,
		Logger:    logger,
	}
}

// NewLienAuditConfigFromOrderAudit creates a LienAuditConfig from an OrderAuditResult.
// Returns nil if no matching orders were found or if the order has no lien ID.
func NewLienAuditConfigFromOrderAudit(orderResult *OrderAuditResult, logger *slog.Logger) *LienAuditConfig {
	if orderResult == nil || len(orderResult.MatchingOrders) == 0 {
		return nil
	}

	// Use the first matching order's lien ID
	lienID := orderResult.MatchingOrders[0].LienID
	if lienID == "" {
		return nil
	}

	if logger == nil {
		logger = slog.Default()
	}

	return &LienAuditConfig{
		LienID:    lienID,
		AccountID: orderResult.AccountID,
		Logger:    logger,
	}
}
