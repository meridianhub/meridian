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
	validLineage := NewTransactionLineage(uuid.New(), "payment")

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
