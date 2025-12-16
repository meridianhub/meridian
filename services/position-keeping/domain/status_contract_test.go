package domain_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/stretchr/testify/assert"
)

// Contract tests validate that domain values align with database constraints.
// These tests catch mismatches between Go code and SQL CHECK constraints.

// AllTransactionStatuses returns all valid transaction statuses.
// This must stay in sync with the CHECK constraint in migrations.
var AllTransactionStatuses = []domain.TransactionStatus{
	domain.TransactionStatusPending,
	domain.TransactionStatusReconciled,
	domain.TransactionStatusPosted,
	domain.TransactionStatusCancelled,
	domain.TransactionStatusFailed,
	domain.TransactionStatusRejected,
	domain.TransactionStatusAmended,
	domain.TransactionStatusReversed,
}

// AllReconciliationStatuses returns all valid reconciliation statuses.
// This must stay in sync with the CHECK constraint in migrations.
var AllReconciliationStatuses = []domain.ReconciliationStatus{
	domain.ReconciliationStatusUnreconciled,
	domain.ReconciliationStatusMatched,
	domain.ReconciliationStatusMismatched,
	domain.ReconciliationStatusResolved,
}

// TestTransactionStatusValuesMatchDBConstraint validates that all domain status
// values will pass the database CHECK constraint.
//
// The DB constraint is:
// CHECK ("current_status" IN ('PENDING', 'RECONCILED', 'POSTED', 'CANCELLED', 'FAILED', 'REJECTED', 'AMENDED', 'REVERSED'))
//
// This test was added after discovering the domain used UPPERCASE but the
// original DB constraint used lowercase values.
func TestTransactionStatusValuesMatchDBConstraint(t *testing.T) {
	// These are the exact values allowed by the DB CHECK constraint
	dbAllowedValues := map[string]bool{
		"PENDING":    true,
		"RECONCILED": true,
		"POSTED":     true,
		"CANCELLED":  true,
		"FAILED":     true,
		"REJECTED":   true,
		"AMENDED":    true,
		"REVERSED":   true,
	}

	for _, status := range AllTransactionStatuses {
		statusStr := status.String()
		assert.True(t, dbAllowedValues[statusStr],
			"Transaction status %q must be allowed by DB CHECK constraint", statusStr)
	}
}

// TestTransactionStatusIsUppercase ensures all status values are uppercase.
// The DB constraint uses uppercase, so we must ensure consistency.
func TestTransactionStatusIsUppercase(t *testing.T) {
	for _, status := range AllTransactionStatuses {
		statusStr := status.String()
		assert.Equal(t, statusStr, string(status),
			"Status string representation should match the constant value")

		// Verify uppercase
		for _, char := range statusStr {
			if char >= 'a' && char <= 'z' {
				t.Errorf("Status %q contains lowercase characters - DB constraint requires uppercase", statusStr)
			}
		}
	}
}

// TestReconciliationStatusValuesMatchDBConstraint validates reconciliation statuses.
//
// The DB constraint is:
// CHECK ("reconciliation_status" IN ('UNRECONCILED', 'MATCHED', 'MISMATCHED', 'RESOLVED'))
func TestReconciliationStatusValuesMatchDBConstraint(t *testing.T) {
	dbAllowedValues := map[string]bool{
		"UNRECONCILED": true,
		"MATCHED":      true,
		"MISMATCHED":   true,
		"RESOLVED":     true,
	}

	for _, status := range AllReconciliationStatuses {
		statusStr := status.String()
		assert.True(t, dbAllowedValues[statusStr],
			"Reconciliation status %q must be allowed by DB CHECK constraint", statusStr)
	}
}

// TestNewStatusTrackingCreatesValidStatus ensures that the factory function
// creates status values that will pass DB constraints.
func TestNewStatusTrackingCreatesValidStatus(t *testing.T) {
	st := domain.NewStatusTracking()

	// The initial status must be valid for the DB
	assert.Equal(t, domain.TransactionStatusPending, st.CurrentStatus,
		"New status tracking should start with PENDING status")
	assert.Equal(t, "PENDING", st.CurrentStatus.String(),
		"PENDING status string must be uppercase for DB")

	assert.Equal(t, domain.ReconciliationStatusUnreconciled, st.ReconciliationStatus,
		"New status tracking should start with UNRECONCILED reconciliation status")
	assert.Equal(t, "UNRECONCILED", st.ReconciliationStatus.String(),
		"UNRECONCILED status string must be uppercase for DB")
}
