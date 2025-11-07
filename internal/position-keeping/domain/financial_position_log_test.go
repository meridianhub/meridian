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

// ==========================================
// Exhaustive State Transition Matrix Tests
// ==========================================

// Helper function to set log to specific status
func setLogToStatus(t *testing.T, log *FinancialPositionLog, targetStatus TransactionStatus) {
	t.Helper()
	audit, _ := NewAuditTrailEntry("user-123", "setup", "Test setup", "192.168.1.1", nil)

	switch targetStatus {
	case TransactionStatusPending:
		// Already pending
	case TransactionStatusReconciled:
		_ = log.MarkReconciled(ReconciliationStatusMatched, "Setup", audit)
	case TransactionStatusAmended:
		_ = log.MarkReconciled(ReconciliationStatusMatched, "Setup", audit)
		audit2, _ := NewAuditTrailEntry("user-123", "amended", "Setup", "192.168.1.1", nil)
		_ = log.Amend("Setup", audit2)
	case TransactionStatusPosted:
		_ = log.MarkPosted("Setup", audit)
	case TransactionStatusFailed:
		_ = log.Fail("Setup", audit)
	case TransactionStatusRejected:
		_ = log.Reject("Setup", audit)
	case TransactionStatusCancelled:
		_ = log.Cancel("Setup", audit)
	case TransactionStatusReversed:
		// REVERSED can only be reached from POSTED via StatusTracking.UpdateStatus
		_ = log.MarkPosted("Setup", audit)
		_ = log.StatusTracking.UpdateStatus(TransactionStatusReversed, "Setup reversal")
	}
}

// Test: Double-Transition Tests (attempting same state transition twice)

func TestFinancialPositionLog_DoubleTransition_MarkReconciled(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	audit, _ := NewAuditTrailEntry("user-123", "reconciled", "First reconcile", "192.168.1.1", nil)

	// First transition should succeed
	err := log.MarkReconciled(ReconciliationStatusMatched, "Matched", audit)
	if err != nil {
		t.Fatalf("First MarkReconciled failed: %v", err)
	}

	initialVersion := log.Version

	// Second transition should fail
	audit2, _ := NewAuditTrailEntry("user-123", "reconciled", "Second reconcile", "192.168.1.1", nil)
	err = log.MarkReconciled(ReconciliationStatusMatched, "Matched again", audit2)
	if err == nil {
		t.Error("Expected error when marking already reconciled log as reconciled again")
	}
	if !errors.Is(err, ErrInvalidStatusTransition) {
		t.Errorf("Expected ErrInvalidStatusTransition, got %v", err)
	}

	// Version should not have incremented
	if log.Version != initialVersion {
		t.Errorf("Version should remain %d after failed transition, got %d", initialVersion, log.Version)
	}
}

func TestFinancialPositionLog_DoubleTransition_MarkPosted(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	audit, _ := NewAuditTrailEntry("user-123", "posted", "First post", "192.168.1.1", nil)

	// First transition should succeed
	err := log.MarkPosted("Posted successfully", audit)
	if err != nil {
		t.Fatalf("First MarkPosted failed: %v", err)
	}

	initialVersion := log.Version

	// Second transition should fail with ErrAlreadyPosted
	audit2, _ := NewAuditTrailEntry("user-123", "posted", "Second post", "192.168.1.1", nil)
	err = log.MarkPosted("Posted again", audit2)
	if err == nil {
		t.Error("Expected error when marking already posted log as posted again")
	}
	if !errors.Is(err, ErrAlreadyPosted) {
		t.Errorf("Expected ErrAlreadyPosted, got %v", err)
	}

	// Version should not have incremented
	if log.Version != initialVersion {
		t.Errorf("Version should remain %d after failed transition, got %d", initialVersion, log.Version)
	}
}

func TestFinancialPositionLog_DoubleTransition_Reject(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	audit, _ := NewAuditTrailEntry("user-123", "rejected", "First reject", "192.168.1.1", nil)

	// First transition should succeed
	err := log.Reject("Validation failed", audit)
	if err != nil {
		t.Fatalf("First Reject failed: %v", err)
	}

	initialVersion := log.Version

	// Second transition should fail (REJECTED is final)
	audit2, _ := NewAuditTrailEntry("user-123", "rejected", "Second reject", "192.168.1.1", nil)
	err = log.Reject("Rejected again", audit2)
	if err == nil {
		t.Error("Expected error when rejecting already rejected log")
	}
	if !errors.Is(err, ErrInvalidStatusTransition) {
		t.Errorf("Expected ErrInvalidStatusTransition, got %v", err)
	}

	// Version should not have incremented
	if log.Version != initialVersion {
		t.Errorf("Version should remain %d after failed transition, got %d", initialVersion, log.Version)
	}
}

func TestFinancialPositionLog_DoubleTransition_Amend(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	audit, _ := NewAuditTrailEntry("user-123", "reconciled", "Reconciled", "192.168.1.1", nil)
	_ = log.MarkReconciled(ReconciliationStatusMatched, "Matched", audit)

	audit2, _ := NewAuditTrailEntry("user-123", "amended", "First amend", "192.168.1.1", nil)
	err := log.Amend("Amendment needed", audit2)
	if err != nil {
		t.Fatalf("First Amend failed: %v", err)
	}

	initialVersion := log.Version

	// Second amend should fail (AMENDED → AMENDED not allowed)
	audit3, _ := NewAuditTrailEntry("user-123", "amended", "Second amend", "192.168.1.1", nil)
	err = log.Amend("Amendment again", audit3)
	if err == nil {
		t.Error("Expected error when amending already amended log")
	}
	if !errors.Is(err, ErrCannotAmend) {
		t.Errorf("Expected ErrCannotAmend, got %v", err)
	}

	// Version should not have incremented
	if log.Version != initialVersion {
		t.Errorf("Version should remain %d after failed transition, got %d", initialVersion, log.Version)
	}
}

func TestFinancialPositionLog_DoubleTransition_Fail(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	audit, _ := NewAuditTrailEntry("user-123", "failed", "First fail", "192.168.1.1", nil)

	// First transition should succeed
	err := log.Fail("Insufficient funds", audit)
	if err != nil {
		t.Fatalf("First Fail failed: %v", err)
	}

	initialVersion := log.Version

	// Second transition should fail (FAILED is final)
	audit2, _ := NewAuditTrailEntry("user-123", "failed", "Second fail", "192.168.1.1", nil)
	err = log.Fail("Failed again", audit2)
	if err == nil {
		t.Error("Expected error when failing already failed log")
	}
	if !errors.Is(err, ErrInvalidStatusTransition) {
		t.Errorf("Expected ErrInvalidStatusTransition, got %v", err)
	}

	// Version should not have incremented
	if log.Version != initialVersion {
		t.Errorf("Version should remain %d after failed transition, got %d", initialVersion, log.Version)
	}
}

func TestFinancialPositionLog_DoubleTransition_Cancel(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	audit, _ := NewAuditTrailEntry("user-123", "cancelled", "First cancel", "192.168.1.1", nil)

	// First transition should succeed
	err := log.Cancel("User requested", audit)
	if err != nil {
		t.Fatalf("First Cancel failed: %v", err)
	}

	initialVersion := log.Version

	// Second transition should fail (CANCELLED is final)
	audit2, _ := NewAuditTrailEntry("user-123", "cancelled", "Second cancel", "192.168.1.1", nil)
	err = log.Cancel("Cancelled again", audit2)
	if err == nil {
		t.Error("Expected error when cancelling already cancelled log")
	}
	if !errors.Is(err, ErrInvalidStatusTransition) {
		t.Errorf("Expected ErrInvalidStatusTransition, got %v", err)
	}

	// Version should not have incremented
	if log.Version != initialVersion {
		t.Errorf("Version should remain %d after failed transition, got %d", initialVersion, log.Version)
	}
}

// Test: Invalid Transitions from Each State

func TestFinancialPositionLog_InvalidTransitions_FromPending(t *testing.T) {
	tests := []struct {
		name       string
		transition func(*FinancialPositionLog) error
		wantErr    error
	}{
		{
			name: "pending to pending (no-op via MarkReconciled with same status)",
			transition: func(log *FinancialPositionLog) error {
				// Note: PENDING → PENDING isn't directly testable as there's no method for it
				// This tests that we can't go PENDING → AMENDED
				audit, _ := NewAuditTrailEntry("user-123", "amended", "Amend", "192.168.1.1", nil)
				return log.Amend("Amendment", audit)
			},
			wantErr: ErrCannotAmend,
		},
		{
			name: "pending to amended",
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "amended", "Amend", "192.168.1.1", nil)
				return log.Amend("Amendment", audit)
			},
			wantErr: ErrCannotAmend,
		},
		{
			name: "pending to reversed",
			transition: func(log *FinancialPositionLog) error {
				return log.StatusTracking.UpdateStatus(TransactionStatusReversed, "Invalid reversal")
			},
			wantErr: ErrInvalidStatusTransition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
			initialVersion := log.Version

			err := tt.transition(log)
			if err == nil {
				t.Error("Expected error but got nil")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Expected %v, got %v", tt.wantErr, err)
			}

			// Status should remain PENDING
			if log.StatusTracking.CurrentStatus != TransactionStatusPending {
				t.Errorf("Expected status PENDING, got %v", log.StatusTracking.CurrentStatus)
			}

			// Version should not increment
			if log.Version != initialVersion {
				t.Errorf("Version should remain %d, got %d", initialVersion, log.Version)
			}
		})
	}
}

func TestFinancialPositionLog_InvalidTransitions_FromReconciled(t *testing.T) {
	tests := []struct {
		name       string
		transition func(*FinancialPositionLog) error
		wantErr    error
	}{
		{
			name: "reconciled to reconciled (double reconcile)",
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "reconciled", "Reconcile", "192.168.1.1", nil)
				return log.MarkReconciled(ReconciliationStatusMatched, "Matched", audit)
			},
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name: "reconciled to pending (backward)",
			transition: func(log *FinancialPositionLog) error {
				// No direct method, but would fail via UpdateStatus
				return log.StatusTracking.UpdateStatus(TransactionStatusPending, "Back to pending")
			},
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name: "reconciled to failed",
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "failed", "Fail", "192.168.1.1", nil)
				return log.Fail("Failure", audit)
			},
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name: "reconciled to cancelled",
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "cancelled", "Cancel", "192.168.1.1", nil)
				return log.Cancel("Cancellation", audit)
			},
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name: "reconciled to reversed",
			transition: func(log *FinancialPositionLog) error {
				return log.StatusTracking.UpdateStatus(TransactionStatusReversed, "Invalid reversal")
			},
			wantErr: ErrInvalidStatusTransition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
			audit, _ := NewAuditTrailEntry("user-123", "reconciled", "Setup", "192.168.1.1", nil)
			_ = log.MarkReconciled(ReconciliationStatusMatched, "Setup", audit)

			initialVersion := log.Version

			err := tt.transition(log)
			if err == nil {
				t.Error("Expected error but got nil")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Expected %v, got %v", tt.wantErr, err)
			}

			// Status should remain RECONCILED
			if log.StatusTracking.CurrentStatus != TransactionStatusReconciled {
				t.Errorf("Expected status RECONCILED, got %v", log.StatusTracking.CurrentStatus)
			}

			// Version should not increment
			if log.Version != initialVersion {
				t.Errorf("Version should remain %d, got %d", initialVersion, log.Version)
			}
		})
	}
}

func TestFinancialPositionLog_InvalidTransitions_FromAmended(t *testing.T) {
	tests := []struct {
		name       string
		transition func(*FinancialPositionLog) error
		wantErr    error
	}{
		{
			name: "amended to amended (double amend)",
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "amended", "Amend", "192.168.1.1", nil)
				return log.Amend("Amendment", audit)
			},
			wantErr: ErrCannotAmend,
		},
		{
			name: "amended to pending (backward)",
			transition: func(log *FinancialPositionLog) error {
				return log.StatusTracking.UpdateStatus(TransactionStatusPending, "Back to pending")
			},
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name: "amended to failed",
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "failed", "Fail", "192.168.1.1", nil)
				return log.Fail("Failure", audit)
			},
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name: "amended to cancelled",
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "cancelled", "Cancel", "192.168.1.1", nil)
				return log.Cancel("Cancellation", audit)
			},
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name: "amended to reversed",
			transition: func(log *FinancialPositionLog) error {
				return log.StatusTracking.UpdateStatus(TransactionStatusReversed, "Invalid reversal")
			},
			wantErr: ErrInvalidStatusTransition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
			audit, _ := NewAuditTrailEntry("user-123", "reconciled", "Setup", "192.168.1.1", nil)
			_ = log.MarkReconciled(ReconciliationStatusMatched, "Setup", audit)
			audit2, _ := NewAuditTrailEntry("user-123", "amended", "Setup", "192.168.1.1", nil)
			_ = log.Amend("Setup", audit2)

			initialVersion := log.Version

			err := tt.transition(log)
			if err == nil {
				t.Error("Expected error but got nil")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Expected %v, got %v", tt.wantErr, err)
			}

			// Status should remain AMENDED
			if log.StatusTracking.CurrentStatus != TransactionStatusAmended {
				t.Errorf("Expected status AMENDED, got %v", log.StatusTracking.CurrentStatus)
			}

			// Version should not increment
			if log.Version != initialVersion {
				t.Errorf("Version should remain %d, got %d", initialVersion, log.Version)
			}
		})
	}
}

func TestFinancialPositionLog_InvalidTransitions_FromPosted(t *testing.T) {
	tests := []struct {
		name       string
		transition func(*FinancialPositionLog) error
		wantErr    error
	}{
		{
			name: "posted to pending",
			transition: func(log *FinancialPositionLog) error {
				return log.StatusTracking.UpdateStatus(TransactionStatusPending, "Back to pending")
			},
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name: "posted to reconciled",
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "reconciled", "Reconcile", "192.168.1.1", nil)
				return log.MarkReconciled(ReconciliationStatusMatched, "Matched", audit)
			},
			wantErr: ErrAlreadyPosted,
		},
		{
			name: "posted to amended",
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "amended", "Amend", "192.168.1.1", nil)
				return log.Amend("Amendment", audit)
			},
			wantErr: ErrCannotAmend,
		},
		{
			name: "posted to failed",
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "failed", "Fail", "192.168.1.1", nil)
				return log.Fail("Failure", audit)
			},
			wantErr: ErrAlreadyPosted,
		},
		{
			name: "posted to cancelled",
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "cancelled", "Cancel", "192.168.1.1", nil)
				return log.Cancel("Cancellation", audit)
			},
			wantErr: ErrAlreadyPosted,
		},
		{
			name: "posted to posted (double post)",
			transition: func(log *FinancialPositionLog) error {
				audit, _ := NewAuditTrailEntry("user-123", "posted", "Post", "192.168.1.1", nil)
				return log.MarkPosted("Posted", audit)
			},
			wantErr: ErrAlreadyPosted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
			audit, _ := NewAuditTrailEntry("user-123", "posted", "Setup", "192.168.1.1", nil)
			_ = log.MarkPosted("Setup", audit)

			initialVersion := log.Version

			err := tt.transition(log)
			if err == nil {
				t.Error("Expected error but got nil")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("Expected %v, got %v", tt.wantErr, err)
			}

			// Status should remain POSTED
			if log.StatusTracking.CurrentStatus != TransactionStatusPosted {
				t.Errorf("Expected status POSTED, got %v", log.StatusTracking.CurrentStatus)
			}

			// Version should not increment
			if log.Version != initialVersion {
				t.Errorf("Version should remain %d, got %d", initialVersion, log.Version)
			}
		})
	}
}

// Test: All transitions from final states (FAILED, REJECTED, CANCELLED, REVERSED)

func TestFinancialPositionLog_InvalidTransitions_FromFinalStates(t *testing.T) {
	finalStates := []TransactionStatus{
		TransactionStatusFailed,
		TransactionStatusRejected,
		TransactionStatusCancelled,
		TransactionStatusReversed,
	}

	allTargetStates := []TransactionStatus{
		TransactionStatusPending,
		TransactionStatusReconciled,
		TransactionStatusAmended,
		TransactionStatusPosted,
		TransactionStatusFailed,
		TransactionStatusRejected,
		TransactionStatusCancelled,
		TransactionStatusReversed,
	}

	for _, fromStatus := range finalStates {
		for _, toStatus := range allTargetStates {
			testName := "from " + string(fromStatus) + " to " + string(toStatus)
			t.Run(testName, func(t *testing.T) {
				log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
				setLogToStatus(t, log, fromStatus)

				initialVersion := log.Version

				// Try to transition using UpdateStatus directly
				err := log.StatusTracking.UpdateStatus(toStatus, "Attempt invalid transition")

				// All transitions from final states should fail
				if err == nil {
					t.Errorf("Expected error when transitioning from %v to %v", fromStatus, toStatus)
				}
				if !errors.Is(err, ErrInvalidStatusTransition) {
					t.Errorf("Expected ErrInvalidStatusTransition, got %v", err)
				}

				// Status should remain unchanged
				if log.StatusTracking.CurrentStatus != fromStatus {
					t.Errorf("Expected status %v, got %v", fromStatus, log.StatusTracking.CurrentStatus)
				}

				// Version should not increment
				if log.Version != initialVersion {
					t.Errorf("Version should remain %d, got %d", initialVersion, log.Version)
				}
			})
		}
	}
}

// Test: ReconciliationStatus Edge Cases

func TestFinancialPositionLog_ReconciliationStatus_ValidStatuses(t *testing.T) {
	validStatuses := []ReconciliationStatus{
		ReconciliationStatusMatched,
		ReconciliationStatusMismatched,
		ReconciliationStatusResolved,
	}

	for _, status := range validStatuses {
		testName := "mark reconciled with " + string(status)
		t.Run(testName, func(t *testing.T) {
			log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
			audit, _ := NewAuditTrailEntry("user-123", "reconciled", "Reconciled", "192.168.1.1", nil)

			err := log.MarkReconciled(status, "Reconciled with "+string(status), audit)
			if err != nil {
				t.Errorf("Expected success with status %v, got error: %v", status, err)
			}

			if log.StatusTracking.ReconciliationStatus != status {
				t.Errorf("Expected reconciliation status %v, got %v", status, log.StatusTracking.ReconciliationStatus)
			}

			if log.StatusTracking.CurrentStatus != TransactionStatusReconciled {
				t.Errorf("Expected transaction status RECONCILED, got %v", log.StatusTracking.CurrentStatus)
			}
		})
	}
}

func TestFinancialPositionLog_ReconciliationStatus_Unreconciled(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	audit, _ := NewAuditTrailEntry("user-123", "reconciled", "Reconciled", "192.168.1.1", nil)

	initialVersion := log.Version

	// Attempting to mark as reconciled with UNRECONCILED status should fail
	err := log.MarkReconciled(ReconciliationStatusUnreconciled, "Invalid reconciliation", audit)
	if err == nil {
		t.Error("Expected error when marking reconciled with UNRECONCILED status")
	}
	if !errors.Is(err, ErrInvalidReconciliationStatus) {
		t.Errorf("Expected ErrInvalidReconciliationStatus, got %v", err)
	}

	// Status should remain PENDING
	if log.StatusTracking.CurrentStatus != TransactionStatusPending {
		t.Errorf("Expected status PENDING, got %v", log.StatusTracking.CurrentStatus)
	}

	// Reconciliation status should remain UNRECONCILED
	if log.StatusTracking.ReconciliationStatus != ReconciliationStatusUnreconciled {
		t.Errorf("Expected reconciliation status UNRECONCILED, got %v", log.StatusTracking.ReconciliationStatus)
	}

	// Version should not increment
	if log.Version != initialVersion {
		t.Errorf("Version should remain %d, got %d", initialVersion, log.Version)
	}
}

func TestFinancialPositionLog_ReconciliationStatus_MultipleMarkReconciled(t *testing.T) {
	log, _ := NewFinancialPositionLog("ACC-001", nil, nil)
	audit, _ := NewAuditTrailEntry("user-123", "reconciled", "First reconcile", "192.168.1.1", nil)

	// First MarkReconciled with MATCHED
	err := log.MarkReconciled(ReconciliationStatusMatched, "Matched", audit)
	if err != nil {
		t.Fatalf("First MarkReconciled failed: %v", err)
	}

	initialVersion := log.Version

	// Second MarkReconciled with different status should fail
	audit2, _ := NewAuditTrailEntry("user-123", "reconciled", "Second reconcile", "192.168.1.1", nil)
	err = log.MarkReconciled(ReconciliationStatusMismatched, "Mismatched", audit2)
	if err == nil {
		t.Error("Expected error when marking already reconciled log")
	}

	// Should be invalid state transition, not reconciliation status error
	if !errors.Is(err, ErrInvalidStatusTransition) {
		t.Errorf("Expected ErrInvalidStatusTransition, got %v", err)
	}

	// Reconciliation status should remain at first value
	if log.StatusTracking.ReconciliationStatus != ReconciliationStatusMatched {
		t.Errorf("Expected reconciliation status MATCHED, got %v", log.StatusTracking.ReconciliationStatus)
	}

	// Version should not increment
	if log.Version != initialVersion {
		t.Errorf("Version should remain %d, got %d", initialVersion, log.Version)
	}
}
