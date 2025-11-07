package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestNewFinancialPositionLog(t *testing.T) {
	validMoney, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	validEntry, _ := NewTransactionLogEntry(
		uuid.New(),
		"ACC-001",
		validMoney,
		PostingDirectionDebit,
		time.Now(),
		"Initial entry",
		"REF-001",
		TransactionSourceManual,
	)
	validLineage, _ := NewTransactionLineage(uuid.New(), "payment")

	t.Run("valid log with initial entry", func(t *testing.T) {
		log, err := NewFinancialPositionLog("ACC-001", validEntry, validLineage)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if log.LogID == uuid.Nil {
			t.Error("Expected non-nil log ID")
		}
		if log.EntryCount() != 1 {
			t.Errorf("Expected 1 entry, got %d", log.EntryCount())
		}
	})

	t.Run("valid log without initial entry", func(t *testing.T) {
		log, err := NewFinancialPositionLog("ACC-001", nil, validLineage)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if log.EntryCount() != 0 {
			t.Errorf("Expected 0 entries, got %d", log.EntryCount())
		}
	})

	t.Run("empty account ID", func(t *testing.T) {
		_, err := NewFinancialPositionLog("", validEntry, validLineage)
		if err == nil {
			t.Error("Expected error but got nil")
		}
		if !errors.Is(err, ErrEmptyAccountID) {
			t.Errorf("Expected ErrEmptyAccountID, got %v", err)
		}
	})
}

func TestFinancialPositionLog_AddEntry(t *testing.T) {
	validMoney, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)

	entry, _ := NewTransactionLogEntry(
		uuid.New(),
		"ACC-001",
		validMoney,
		PostingDirectionDebit,
		time.Now(),
		"Payment",
		"REF-001",
		TransactionSourceManual,
	)

	// Test adding entry to pending log
	err := log.AddEntry(entry)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if log.EntryCount() != 1 {
		t.Errorf("Expected 1 entry, got %d", log.EntryCount())
	}

	// Test cannot add entry to posted log
	auditEntry, _ := NewAuditTrailEntry("user-123", "posted", "Posted to ledger", "192.168.1.1", nil)
	_ = log.MarkPosted("Posted successfully", auditEntry)

	entry2, _ := NewTransactionLogEntry(
		uuid.New(),
		"ACC-001",
		validMoney,
		PostingDirectionCredit,
		time.Now(),
		"Payment 2",
		"REF-002",
		TransactionSourceManual,
	)

	err = log.AddEntry(entry2)
	if err == nil {
		t.Error("Expected error when adding entry to posted log")
	}
	if !errors.Is(err, ErrAlreadyPosted) {
		t.Errorf("Expected ErrAlreadyPosted, got %v", err)
	}
}

func TestFinancialPositionLog_MarkReconciled(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	auditEntry, _ := NewAuditTrailEntry("user-123", "reconciled", "Reconciled with external source", "192.168.1.1", nil)

	err := log.MarkReconciled(ReconciliationStatusMatched, "Matched with bank statement", auditEntry)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if log.StatusTracking.CurrentStatus != TransactionStatusReconciled {
		t.Errorf("Expected status RECONCILED, got %v", log.StatusTracking.CurrentStatus)
	}

	if log.StatusTracking.ReconciliationStatus != ReconciliationStatusMatched {
		t.Errorf("Expected reconciliation status MATCHED, got %v", log.StatusTracking.ReconciliationStatus)
	}

	if log.AuditEntryCount() != 1 {
		t.Errorf("Expected 1 audit entry, got %d", log.AuditEntryCount())
	}

	// Test cannot reconcile posted log
	log2, _ := NewFinancialPositionLog("ACC-002", nil, nil)
	auditEntry2, _ := NewAuditTrailEntry("user-123", "posted", "Posted to ledger", "192.168.1.1", nil)
	_ = log2.MarkPosted("Posted successfully", auditEntry2)

	err = log2.MarkReconciled(ReconciliationStatusMatched, "Attempt to reconcile", nil)
	if err == nil {
		t.Error("Expected error when reconciling posted log")
	}
}

func TestFinancialPositionLog_MarkPosted(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	auditEntry, _ := NewAuditTrailEntry("user-123", "posted", "Posted to ledger", "192.168.1.1", nil)

	err := log.MarkPosted("Posted successfully", auditEntry)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if log.StatusTracking.CurrentStatus != TransactionStatusPosted {
		t.Errorf("Expected status POSTED, got %v", log.StatusTracking.CurrentStatus)
	}

	if !log.IsPosted() {
		t.Error("Expected IsPosted to be true")
	}

	// Test cannot post already posted log
	err = log.MarkPosted("Attempt to post again", nil)
	if err == nil {
		t.Error("Expected error when posting already posted log")
	}
	if !errors.Is(err, ErrAlreadyPosted) {
		t.Errorf("Expected ErrAlreadyPosted, got %v", err)
	}
}

func TestFinancialPositionLog_Reject(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	auditEntry, _ := NewAuditTrailEntry("user-123", "rejected", "Failed validation", "192.168.1.1", nil)

	err := log.Reject("Invalid transaction details", auditEntry)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if log.StatusTracking.CurrentStatus != TransactionStatusRejected {
		t.Errorf("Expected status REJECTED, got %v", log.StatusTracking.CurrentStatus)
	}

	if !log.IsFinal() {
		t.Error("Expected IsFinal to be true for rejected status")
	}
}

func TestFinancialPositionLog_Amend(t *testing.T) {
	// Test can amend reconciled log
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	auditEntry, _ := NewAuditTrailEntry("user-123", "reconciled", "Reconciled", "192.168.1.1", nil)
	_ = log.MarkReconciled(ReconciliationStatusMatched, "Matched", auditEntry)

	if !log.CanBeAmended() {
		t.Error("Expected CanBeAmended to be true for reconciled status")
	}

	auditEntry2, _ := NewAuditTrailEntry("user-123", "amended", "Corrected amount", "192.168.1.1", nil)
	err := log.Amend("Amount correction needed", auditEntry2)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if log.StatusTracking.CurrentStatus != TransactionStatusAmended {
		t.Errorf("Expected status AMENDED, got %v", log.StatusTracking.CurrentStatus)
	}

	// Test cannot amend pending log
	log2, _ := NewFinancialPositionLog("ACC-002", nil, nil)

	if log2.CanBeAmended() {
		t.Error("Expected CanBeAmended to be false for pending status")
	}

	err = log2.Amend("Attempt to amend", nil)
	if err == nil {
		t.Error("Expected error when amending pending log")
	}
	if !errors.Is(err, ErrCannotAmend) {
		t.Errorf("Expected ErrCannotAmend, got %v", err)
	}

	// Test cannot amend posted log
	log3, _ := NewFinancialPositionLog("ACC-003", nil, nil)
	auditEntry3, _ := NewAuditTrailEntry("user-123", "posted", "Posted", "192.168.1.1", nil)
	_ = log3.MarkPosted("Posted successfully", auditEntry3)

	if log3.CanBeAmended() {
		t.Error("Expected CanBeAmended to be false for posted status")
	}

	err = log3.Amend("Attempt to amend", nil)
	if err == nil {
		t.Error("Expected error when amending posted log")
	}
}

func TestFinancialPositionLog_Fail(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	auditEntry, _ := NewAuditTrailEntry("user-123", "failed", "Validation failed", "192.168.1.1", nil)

	err := log.Fail("Insufficient funds", auditEntry)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if log.StatusTracking.CurrentStatus != TransactionStatusFailed {
		t.Errorf("Expected status FAILED, got %v", log.StatusTracking.CurrentStatus)
	}

	if log.StatusTracking.FailureReason != "Insufficient funds" {
		t.Errorf("Expected failure reason 'Insufficient funds', got %v", log.StatusTracking.FailureReason)
	}
}

func TestFinancialPositionLog_Cancel(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	auditEntry, _ := NewAuditTrailEntry("user-123", "cancelled", "Cancelled by user", "192.168.1.1", nil)

	err := log.Cancel("User requested cancellation", auditEntry)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if log.StatusTracking.CurrentStatus != TransactionStatusCancelled {
		t.Errorf("Expected status CANCELLED, got %v", log.StatusTracking.CurrentStatus)
	}
}

func TestFinancialPositionLog_StateTransitions(t *testing.T) {
	tests := []struct {
		name           string
		initialStatus  TransactionStatus
		transition     func(*FinancialPositionLog) error
		expectedStatus TransactionStatus
		shouldSucceed  bool
	}{
		{
			name:          "pending to reconciled",
			initialStatus: TransactionStatusPending,
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "reconciled", "Reconciled", "192.168.1.1", nil)
				return log.MarkReconciled(ReconciliationStatusMatched, "Matched", audit)
			},
			expectedStatus: TransactionStatusReconciled,
			shouldSucceed:  true,
		},
		{
			name:          "reconciled to amended",
			initialStatus: TransactionStatusReconciled,
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "amended", "Amended", "192.168.1.1", nil)
				return log.Amend("Amendment needed", audit)
			},
			expectedStatus: TransactionStatusAmended,
			shouldSucceed:  true,
		},
		{
			name:          "reconciled to posted",
			initialStatus: TransactionStatusReconciled,
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "posted", "Posted", "192.168.1.1", nil)
				return log.MarkPosted("Posted successfully", audit)
			},
			expectedStatus: TransactionStatusPosted,
			shouldSucceed:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, _ := NewFinancialPositionLog("ACC-001", nil, nil)

			// Set initial status if not pending
			if tt.initialStatus != TransactionStatusPending {
				audit, _ := NewAuditTrailEntry("user-123", "setup", "Setup", "192.168.1.1", nil)
				if tt.initialStatus == TransactionStatusReconciled {
					_ = log.MarkReconciled(ReconciliationStatusMatched, "Setup", audit)
				}
			}

			err := tt.transition(log)

			if tt.shouldSucceed {
				if err != nil {
					t.Errorf("Expected success but got error: %v", err)
				}
				if log.StatusTracking.CurrentStatus != tt.expectedStatus {
					t.Errorf("Expected status %v, got %v", tt.expectedStatus, log.StatusTracking.CurrentStatus)
				}
			} else {
				if err == nil {
					t.Error("Expected error but got nil")
				}
			}
		})
	}
}

func TestFinancialPositionLog_IsReconciled(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)

	if log.IsReconciled() {
		t.Error("Expected IsReconciled to be false for pending status")
	}

	auditEntry, _ := NewAuditTrailEntry("user-123", "reconciled", "Reconciled", "192.168.1.1", nil)
	_ = log.MarkReconciled(ReconciliationStatusMatched, "Matched with external source", auditEntry)

	if !log.IsReconciled() {
		t.Error("Expected IsReconciled to be true after marking as reconciled")
	}
}

// Helper functions for capacity boundary tests

// createLogWithEntries creates a log with the specified number of transaction entries
func createLogWithEntries(t *testing.T, numEntries int) *FinancialPositionLog {
	t.Helper()
	log, err := NewFinancialPositionLog("ACC-TEST", nil, nil)
	if err != nil {
		t.Fatalf("Failed to create log: %v", err)
	}

	validMoney, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)

	for i := 0; i < numEntries; i++ {
		entry, err := NewTransactionLogEntry(
			uuid.New(),
			"ACC-TEST",
			validMoney,
			PostingDirectionDebit,
			time.Now(),
			"Test entry",
			"REF-"+string(rune(i)),
			TransactionSourceManual,
		)
		if err != nil {
			t.Fatalf("Failed to create entry %d: %v", i, err)
		}

		if err := log.AddEntry(entry); err != nil {
			t.Fatalf("Failed to add entry %d: %v", i, err)
		}
	}

	return log
}

// createLogWithAuditEntries creates a log with the specified number of audit entries
func createLogWithAuditEntries(t *testing.T, numAudits int) *FinancialPositionLog {
	t.Helper()
	log, err := NewFinancialPositionLog("ACC-TEST", nil, nil)
	if err != nil {
		t.Fatalf("Failed to create log: %v", err)
	}

	for i := 0; i < numAudits; i++ {
		auditEntry, err := NewAuditTrailEntry(
			"user-123",
			"audit",
			"Test audit entry",
			"192.168.1.1",
			nil,
		)
		if err != nil {
			t.Fatalf("Failed to create audit entry %d: %v", i, err)
		}

		if err := log.AddAuditEntry(auditEntry); err != nil {
			t.Fatalf("Failed to add audit entry %d: %v", i, err)
		}
	}

	return log
}

// Transaction Entry Capacity Boundary Tests

func TestFinancialPositionLog_AddEntry_OneBelowLimit(t *testing.T) {
	log := createLogWithEntries(t, MaxTransactionEntries-1)

	if log.EntryCount() != MaxTransactionEntries-1 {
		t.Errorf("Expected %d entries, got %d", MaxTransactionEntries-1, log.EntryCount())
	}

	// Should be able to add one more
	validMoney, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	entry, _ := NewTransactionLogEntry(
		uuid.New(),
		"ACC-TEST",
		validMoney,
		PostingDirectionDebit,
		time.Now(),
		"Final entry",
		"REF-FINAL",
		TransactionSourceManual,
	)

	err := log.AddEntry(entry)
	if err != nil {
		t.Errorf("Expected success when adding entry at %d (one below limit), got error: %v", MaxTransactionEntries-1, err)
	}

	if log.EntryCount() != MaxTransactionEntries {
		t.Errorf("Expected %d entries after adding, got %d", MaxTransactionEntries, log.EntryCount())
	}
}

func TestFinancialPositionLog_AddEntry_AtLimit(t *testing.T) {
	log := createLogWithEntries(t, MaxTransactionEntries)

	if log.EntryCount() != MaxTransactionEntries {
		t.Errorf("Expected %d entries, got %d", MaxTransactionEntries, log.EntryCount())
	}

	// Should NOT be able to add one more
	validMoney, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	entry, _ := NewTransactionLogEntry(
		uuid.New(),
		"ACC-TEST",
		validMoney,
		PostingDirectionDebit,
		time.Now(),
		"Overflow entry",
		"REF-OVERFLOW",
		TransactionSourceManual,
	)

	err := log.AddEntry(entry)
	if err == nil {
		t.Error("Expected error when adding entry at limit (10,000 entries), got nil")
	}
	if !errors.Is(err, ErrTooManyEntries) {
		t.Errorf("Expected ErrTooManyEntries, got %v", err)
	}

	// Entry count should remain unchanged
	if log.EntryCount() != MaxTransactionEntries {
		t.Errorf("Expected %d entries after failed add, got %d", MaxTransactionEntries, log.EntryCount())
	}
}

func TestFinancialPositionLog_AddEntry_ExceedsLimit(t *testing.T) {
	log := createLogWithEntries(t, MaxTransactionEntries)

	// Attempt to add multiple entries beyond limit
	validMoney, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)

	for i := 0; i < 5; i++ {
		entry, _ := NewTransactionLogEntry(
			uuid.New(),
			"ACC-TEST",
			validMoney,
			PostingDirectionDebit,
			time.Now(),
			"Overflow entry",
			"REF-OVERFLOW",
			TransactionSourceManual,
		)

		err := log.AddEntry(entry)
		if err == nil {
			t.Errorf("Expected error when adding entry beyond limit (attempt %d), got nil", i+1)
		}
		if !errors.Is(err, ErrTooManyEntries) {
			t.Errorf("Attempt %d: Expected ErrTooManyEntries, got %v", i+1, err)
		}
	}

	// Entry count should remain at limit
	if log.EntryCount() != MaxTransactionEntries {
		t.Errorf("Expected %d entries after failed attempts, got %d", MaxTransactionEntries, log.EntryCount())
	}
}

// Audit Entry Capacity Boundary Tests

func TestFinancialPositionLog_AddAuditEntry_OneBelowLimit(t *testing.T) {
	log := createLogWithAuditEntries(t, MaxAuditEntries-1)

	if log.AuditEntryCount() != MaxAuditEntries-1 {
		t.Errorf("Expected %d audit entries, got %d", MaxAuditEntries-1, log.AuditEntryCount())
	}

	// Should be able to add one more
	auditEntry, _ := NewAuditTrailEntry(
		"user-123",
		"audit",
		"Final audit entry",
		"192.168.1.1",
		nil,
	)

	err := log.AddAuditEntry(auditEntry)
	if err != nil {
		t.Errorf("Expected success when adding audit entry at %d (one below limit), got error: %v", MaxAuditEntries-1, err)
	}

	if log.AuditEntryCount() != MaxAuditEntries {
		t.Errorf("Expected %d audit entries after adding, got %d", MaxAuditEntries, log.AuditEntryCount())
	}
}

func TestFinancialPositionLog_AddAuditEntry_AtLimit(t *testing.T) {
	log := createLogWithAuditEntries(t, MaxAuditEntries)

	if log.AuditEntryCount() != MaxAuditEntries {
		t.Errorf("Expected %d audit entries, got %d", MaxAuditEntries, log.AuditEntryCount())
	}

	// Should NOT be able to add one more
	auditEntry, _ := NewAuditTrailEntry(
		"user-123",
		"audit",
		"Overflow audit entry",
		"192.168.1.1",
		nil,
	)

	err := log.AddAuditEntry(auditEntry)
	if err == nil {
		t.Error("Expected error when adding audit entry at limit (10,000 entries), got nil")
	}
	if !errors.Is(err, ErrTooManyAuditEntries) {
		t.Errorf("Expected ErrTooManyAuditEntries, got %v", err)
	}

	// Audit entry count should remain unchanged
	if log.AuditEntryCount() != MaxAuditEntries {
		t.Errorf("Expected %d audit entries after failed add, got %d", MaxAuditEntries, log.AuditEntryCount())
	}
}

func TestFinancialPositionLog_AddAuditEntry_ExceedsLimit(t *testing.T) {
	log := createLogWithAuditEntries(t, MaxAuditEntries)

	// Attempt to add multiple audit entries beyond limit
	for i := 0; i < 5; i++ {
		auditEntry, _ := NewAuditTrailEntry(
			"user-123",
			"audit",
			"Overflow audit entry",
			"192.168.1.1",
			nil,
		)

		err := log.AddAuditEntry(auditEntry)
		if err == nil {
			t.Errorf("Expected error when adding audit entry beyond limit (attempt %d), got nil", i+1)
		}
		if !errors.Is(err, ErrTooManyAuditEntries) {
			t.Errorf("Attempt %d: Expected ErrTooManyAuditEntries, got %v", i+1, err)
		}
	}

	// Audit entry count should remain at limit
	if log.AuditEntryCount() != MaxAuditEntries {
		t.Errorf("Expected %d audit entries after failed attempts, got %d", MaxAuditEntries, log.AuditEntryCount())
	}
}

// State Transition at Audit Capacity Tests

func TestFinancialPositionLog_MarkReconciled_AtAuditCapacity(t *testing.T) {
	log := createLogWithAuditEntries(t, MaxAuditEntries)

	initialStatus := log.StatusTracking.CurrentStatus
	initialVersion := log.Version

	auditEntry, _ := NewAuditTrailEntry(
		"user-123",
		"reconciled",
		"Attempt to reconcile",
		"192.168.1.1",
		nil,
	)

	err := log.MarkReconciled(ReconciliationStatusMatched, "Reconciled", auditEntry)

	// Should fail with ErrTooManyAuditEntries
	if err == nil {
		t.Error("Expected error when marking reconciled with audit trail at capacity, got nil")
	}
	if !errors.Is(err, ErrTooManyAuditEntries) {
		t.Errorf("Expected ErrTooManyAuditEntries, got %v", err)
	}

	// CRITICAL: Status must NOT have changed
	if log.StatusTracking.CurrentStatus != initialStatus {
		t.Errorf("Expected status to remain %v after failed MarkReconciled, got %v",
			initialStatus, log.StatusTracking.CurrentStatus)
	}

	// CRITICAL: Version must NOT have incremented
	if log.Version != initialVersion {
		t.Errorf("Expected version to remain %d after failed MarkReconciled, got %d",
			initialVersion, log.Version)
	}

	// Reconciliation status should not have changed
	if log.StatusTracking.ReconciliationStatus != ReconciliationStatusUnreconciled {
		t.Errorf("Expected reconciliation status to remain UNRECONCILED, got %v",
			log.StatusTracking.ReconciliationStatus)
	}

	// Audit trail count should remain at limit
	if log.AuditEntryCount() != MaxAuditEntries {
		t.Errorf("Expected %d audit entries, got %d", MaxAuditEntries, log.AuditEntryCount())
	}
}

func TestFinancialPositionLog_MarkPosted_AtAuditCapacity(t *testing.T) {
	log := createLogWithAuditEntries(t, MaxAuditEntries)

	initialStatus := log.StatusTracking.CurrentStatus
	initialVersion := log.Version

	auditEntry, _ := NewAuditTrailEntry(
		"user-123",
		"posted",
		"Attempt to post",
		"192.168.1.1",
		nil,
	)

	err := log.MarkPosted("Posted successfully", auditEntry)

	// Should fail with ErrTooManyAuditEntries
	if err == nil {
		t.Error("Expected error when marking posted with audit trail at capacity, got nil")
	}
	if !errors.Is(err, ErrTooManyAuditEntries) {
		t.Errorf("Expected ErrTooManyAuditEntries, got %v", err)
	}

	// CRITICAL: Status must NOT have changed
	if log.StatusTracking.CurrentStatus != initialStatus {
		t.Errorf("Expected status to remain %v after failed MarkPosted, got %v",
			initialStatus, log.StatusTracking.CurrentStatus)
	}

	// CRITICAL: Version must NOT have incremented
	if log.Version != initialVersion {
		t.Errorf("Expected version to remain %d after failed MarkPosted, got %d",
			initialVersion, log.Version)
	}

	// Should not be marked as posted
	if log.IsPosted() {
		t.Error("Expected IsPosted to be false after failed MarkPosted")
	}

	// Audit trail count should remain at limit
	if log.AuditEntryCount() != MaxAuditEntries {
		t.Errorf("Expected %d audit entries, got %d", MaxAuditEntries, log.AuditEntryCount())
	}
}

func TestFinancialPositionLog_StateTransitions_AtAuditCapacity(t *testing.T) {
	tests := []struct {
		name                  string
		setupLog              func() *FinancialPositionLog
		transition            func(*FinancialPositionLog) error
		expectedInitialStatus TransactionStatus
	}{
		{
			name: "Reject at audit capacity",
			setupLog: func() *FinancialPositionLog {
				return createLogWithAuditEntries(t, MaxAuditEntries)
			},
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "rejected", "Rejected", "192.168.1.1", nil)
				return log.Reject("Validation failed", audit)
			},
			expectedInitialStatus: TransactionStatusPending,
		},
		{
			name: "Fail at audit capacity",
			setupLog: func() *FinancialPositionLog {
				return createLogWithAuditEntries(t, MaxAuditEntries)
			},
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "failed", "Failed", "192.168.1.1", nil)
				return log.Fail("Insufficient funds", audit)
			},
			expectedInitialStatus: TransactionStatusPending,
		},
		{
			name: "Cancel at audit capacity",
			setupLog: func() *FinancialPositionLog {
				return createLogWithAuditEntries(t, MaxAuditEntries)
			},
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "cancelled", "Cancelled", "192.168.1.1", nil)
				return log.Cancel("User requested cancellation", audit)
			},
			expectedInitialStatus: TransactionStatusPending,
		},
		{
			name: "Amend at audit capacity",
			setupLog: func() *FinancialPositionLog {
				log := createLogWithAuditEntries(t, MaxAuditEntries-1)
				// Mark as reconciled first (this will add one audit entry, bringing us to MaxAuditEntries)
				audit, _ := NewAuditTrailEntry("user-123", "reconciled", "Reconciled", "192.168.1.1", nil)
				_ = log.MarkReconciled(ReconciliationStatusMatched, "Matched", audit)
				return log
			},
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "amended", "Amended", "192.168.1.1", nil)
				return log.Amend("Amendment needed", audit)
			},
			expectedInitialStatus: TransactionStatusReconciled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := tt.setupLog()

			initialStatus := log.StatusTracking.CurrentStatus
			initialVersion := log.Version

			// Verify initial status matches expected
			if initialStatus != tt.expectedInitialStatus {
				t.Errorf("Initial status expected to be %v, got %v", tt.expectedInitialStatus, initialStatus)
			}

			// Verify audit trail is at capacity
			if log.AuditEntryCount() != MaxAuditEntries {
				t.Errorf("Expected %d audit entries before transition, got %d", MaxAuditEntries, log.AuditEntryCount())
			}

			err := tt.transition(log)

			// Should fail with ErrTooManyAuditEntries
			if err == nil {
				t.Error("Expected error when attempting state transition with audit trail at capacity, got nil")
			}
			if !errors.Is(err, ErrTooManyAuditEntries) {
				t.Errorf("Expected ErrTooManyAuditEntries, got %v", err)
			}

			// CRITICAL: Status must NOT have changed
			if log.StatusTracking.CurrentStatus != initialStatus {
				t.Errorf("Expected status to remain %v after failed transition, got %v",
					initialStatus, log.StatusTracking.CurrentStatus)
			}

			// CRITICAL: Version must NOT have incremented
			if log.Version != initialVersion {
				t.Errorf("Expected version to remain %d after failed transition, got %d",
					initialVersion, log.Version)
			}

			// Audit trail count should remain at limit
			if log.AuditEntryCount() != MaxAuditEntries {
				t.Errorf("Expected %d audit entries after failed transition, got %d", MaxAuditEntries, log.AuditEntryCount())
			}
		})
	}
}

// Compound Scenario Test

func TestFinancialPositionLog_CompoundCapacity(t *testing.T) {
	// Create log with maximum transaction entries
	log := createLogWithEntries(t, MaxTransactionEntries)

	// Add maximum audit entries
	for i := 0; i < MaxAuditEntries; i++ {
		auditEntry, _ := NewAuditTrailEntry(
			"user-123",
			"audit",
			"Test audit entry",
			"192.168.1.1",
			nil,
		)
		if err := log.AddAuditEntry(auditEntry); err != nil {
			t.Fatalf("Failed to add audit entry %d: %v", i, err)
		}
	}

	// Verify both capacities are at limit
	if log.EntryCount() != MaxTransactionEntries {
		t.Errorf("Expected %d transaction entries, got %d", MaxTransactionEntries, log.EntryCount())
	}
	if log.AuditEntryCount() != MaxAuditEntries {
		t.Errorf("Expected %d audit entries, got %d", MaxAuditEntries, log.AuditEntryCount())
	}

	// Attempt to add another transaction entry - should fail
	validMoney, _ := NewMoney(decimal.NewFromInt(100), CurrencyGBP)
	entry, _ := NewTransactionLogEntry(
		uuid.New(),
		"ACC-TEST",
		validMoney,
		PostingDirectionDebit,
		time.Now(),
		"Overflow entry",
		"REF-OVERFLOW",
		TransactionSourceManual,
	)

	err := log.AddEntry(entry)
	if err == nil {
		t.Error("Expected error when adding transaction entry at compound capacity, got nil")
	}
	if !errors.Is(err, ErrTooManyEntries) {
		t.Errorf("Expected ErrTooManyEntries, got %v", err)
	}

	// Attempt to add another audit entry - should fail
	auditEntry, _ := NewAuditTrailEntry(
		"user-123",
		"audit",
		"Overflow audit entry",
		"192.168.1.1",
		nil,
	)

	err = log.AddAuditEntry(auditEntry)
	if err == nil {
		t.Error("Expected error when adding audit entry at compound capacity, got nil")
	}
	if !errors.Is(err, ErrTooManyAuditEntries) {
		t.Errorf("Expected ErrTooManyAuditEntries, got %v", err)
	}

	// Attempt state transition with audit entry - should fail
	auditEntry2, _ := NewAuditTrailEntry(
		"user-123",
		"posted",
		"Attempt to post",
		"192.168.1.1",
		nil,
	)

	initialStatus := log.StatusTracking.CurrentStatus
	initialVersion := log.Version

	err = log.MarkPosted("Posted successfully", auditEntry2)
	if err == nil {
		t.Error("Expected error when marking posted at compound capacity, got nil")
	}
	if !errors.Is(err, ErrTooManyAuditEntries) {
		t.Errorf("Expected ErrTooManyAuditEntries, got %v", err)
	}

	// Verify state did not change
	if log.StatusTracking.CurrentStatus != initialStatus {
		t.Errorf("Expected status to remain %v, got %v", initialStatus, log.StatusTracking.CurrentStatus)
	}
	if log.Version != initialVersion {
		t.Errorf("Expected version to remain %d, got %d", initialVersion, log.Version)
	}

	// Verify counts remain at limits
	if log.EntryCount() != MaxTransactionEntries {
		t.Errorf("Expected %d transaction entries after compound test, got %d", MaxTransactionEntries, log.EntryCount())
	}
	if log.AuditEntryCount() != MaxAuditEntries {
		t.Errorf("Expected %d audit entries after compound test, got %d", MaxAuditEntries, log.AuditEntryCount())
	}
}
