package domain

import (
	"testing"
	"time"
)

// These tests target mutation-testing survivors in the state-transition
// lifecycle methods of FinancialPositionLog (MarkReconciled, MarkPosted,
// Reject, Amend, Fail, Cancel).
//
// Two classes of mutant previously survived the suite:
//
//  1. INCREMENT_DECREMENT on `l.Version++` (Version-- ): the Version field is
//     the optimistic-concurrency token (see the struct doc comment). A decrement
//     instead of an increment silently breaks lost-update detection, allowing
//     concurrent writers to overwrite each other - a correctness defect that
//     loses ledger data. No prior test asserted Version advances on transition.
//
//  2. CONDITIONALS_NEGATION on the audit-entry guard / error checks
//     (`if auditEntry != nil`, `if err := l.AddAuditEntry(...); err != nil`):
//     negating these either skips appending the audit entry or returns early
//     before Version++/UpdatedAt run. Asserting both the audit-trail growth and
//     the version bump on the same successful transition pins this behavior.
//
// Each sub-test captures Version and AuditEntryCount immediately before the
// transition and asserts they advance by exactly one, which kills the survivors
// without depending on the absolute starting Version (currently 1).

// transitionCase describes one state-transition method under test. Amend is the
// only case with a precondition (a RECONCILED log), handled inline in the test.
type transitionCase struct {
	name           string
	transition     func(t *testing.T, log *FinancialPositionLog) error
	expectedStatus TransactionStatus
}

func newAudit(t *testing.T, action string) *AuditTrailEntry {
	t.Helper()
	a, err := NewAuditTrailEntry("user-123", action, action+" reason", "192.168.1.1", nil)
	if err != nil {
		t.Fatalf("failed to build audit entry: %v", err)
	}
	return a
}

func TestFinancialPositionLog_Transitions_BumpVersionAndAudit(t *testing.T) {
	cases := []transitionCase{
		{
			name: "MarkReconciled",
			transition: func(t *testing.T, log *FinancialPositionLog) error {
				return log.MarkReconciled(ReconciliationStatusMatched, "matched", newAudit(t, "reconciled"))
			},
			expectedStatus: TransactionStatusReconciled,
		},
		{
			name: "MarkPosted",
			transition: func(t *testing.T, log *FinancialPositionLog) error {
				return log.MarkPosted("posted", newAudit(t, "posted"))
			},
			expectedStatus: TransactionStatusPosted,
		},
		{
			name: "Reject",
			transition: func(t *testing.T, log *FinancialPositionLog) error {
				return log.Reject("rejected", newAudit(t, "rejected"))
			},
			expectedStatus: TransactionStatusRejected,
		},
		{
			name: "Fail",
			transition: func(t *testing.T, log *FinancialPositionLog) error {
				return log.Fail("failed", newAudit(t, "failed"))
			},
			expectedStatus: TransactionStatusFailed,
		},
		{
			name: "Cancel",
			transition: func(t *testing.T, log *FinancialPositionLog) error {
				return log.Cancel("cancelled", newAudit(t, "cancelled"))
			},
			expectedStatus: TransactionStatusCancelled,
		},
		{
			name: "Amend",
			// Amend requires a RECONCILED log first (set up inline below)
			transition: func(t *testing.T, log *FinancialPositionLog) error {
				return log.Amend("amended", newAudit(t, "amended"))
			},
			expectedStatus: TransactionStatusAmended,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log, err := NewFinancialPositionLog("ACC-001", nil, nil)
			if err != nil {
				t.Fatalf("failed to create log: %v", err)
			}

			// Bring the log to the required precondition state.
			if tc.name == "Amend" {
				if err := log.MarkReconciled(ReconciliationStatusMatched, "setup", newAudit(t, "setup")); err != nil {
					t.Fatalf("setup MarkReconciled failed: %v", err)
				}
			}

			versionBefore := log.Version
			auditsBefore := log.AuditEntryCount()

			// Force UpdatedAt into the past so the post-transition assertion that
			// the timestamp strictly advanced is meaningful and non-flaky (no
			// dependence on sub-nanosecond timing between two time.Now calls). The
			// Version and AuditEntryCount assertions are the mutation killers; this
			// is a monotonicity sanity check on the timestamp the transition writes.
			staleMarker := time.Now().UTC().Add(-time.Hour)
			log.UpdatedAt = staleMarker

			if err := tc.transition(t, log); err != nil {
				t.Fatalf("transition returned unexpected error: %v", err)
			}

			// Kills the status mutants and confirms the transition actually ran.
			if log.StatusTracking.CurrentStatus != tc.expectedStatus {
				t.Errorf("status = %v, want %v", log.StatusTracking.CurrentStatus, tc.expectedStatus)
			}

			// Kills INCREMENT_DECREMENT on Version++ and the early-return
			// CONDITIONALS_NEGATION mutants (which skip the increment).
			if got, want := log.Version, versionBefore+1; got != want {
				t.Errorf("Version = %d, want %d (must increment by exactly 1 for optimistic concurrency)", got, want)
			}

			// Kills the `if auditEntry != nil` / AddAuditEntry error-check
			// negations (which skip appending the provided audit entry).
			if got, want := log.AuditEntryCount(), auditsBefore+1; got != want {
				t.Errorf("AuditEntryCount = %d, want %d (provided audit entry must be appended)", got, want)
			}

			// UpdatedAt must strictly advance past the stale marker; the
			// early-return mutants return before any timestamp write runs.
			if !log.UpdatedAt.After(staleMarker) {
				t.Errorf("UpdatedAt did not advance: marker=%v after=%v", staleMarker, log.UpdatedAt)
			}
		})
	}
}

// TestFinancialPositionLog_RejectedTransition_DoesNotBumpVersion guards the
// other direction: a transition that is rejected (returns an error) must leave
// the Version untouched. This pins the early-return semantics so a mutant that
// bumps the version before validating cannot survive.
func TestFinancialPositionLog_RejectedTransition_DoesNotBumpVersion(t *testing.T) {
	log, err := NewFinancialPositionLog("ACC-001", nil, nil)
	if err != nil {
		t.Fatalf("failed to create log: %v", err)
	}

	// Post the log so it reaches a final state.
	if err := log.MarkPosted("posted", newAudit(t, "posted")); err != nil {
		t.Fatalf("MarkPosted failed: %v", err)
	}

	versionAfterPost := log.Version
	auditsAfterPost := log.AuditEntryCount()

	// A posted log cannot be cancelled; the call must fail and change nothing.
	if err := log.Cancel("attempt", newAudit(t, "cancel")); err == nil {
		t.Fatal("expected error cancelling a posted log, got nil")
	}

	if log.Version != versionAfterPost {
		t.Errorf("Version changed on rejected transition: got %d, want %d", log.Version, versionAfterPost)
	}
	if log.AuditEntryCount() != auditsAfterPost {
		t.Errorf("AuditEntryCount changed on rejected transition: got %d, want %d", log.AuditEntryCount(), auditsAfterPost)
	}
}
