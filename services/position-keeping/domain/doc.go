// Package domain implements the Position Keeping bounded context following the
// BIAN (Banking Industry Architecture Network) Transaction Log service domain pattern.
//
// # Overview
//
// Position Keeping maintains a comprehensive, immutable audit trail of all financial
// transactions affecting customer accounts. It provides a single source of truth for
// transaction history, supporting reconciliation, dispute resolution, regulatory reporting,
// and forensic analysis.
//
// The implementation follows Domain-Driven Design (DDD) principles with a clear aggregate
// root (FinancialPositionLog), value objects (Money, ReconciliationStatus), and entities
// (TransactionLogEntry, TransactionLineage, AuditTrailEntry) that enforce domain invariants
// and maintain consistency.
//
// # BIAN Position Keeping Pattern
//
// This service implements the BIAN Transaction Log pattern, which defines Position Keeping as:
//
//	"The service domain captures, classifies and stores transaction activity to
//	 support account position reporting, transaction reconciliation and history
//	 retrieval for analysis and dispute resolution."
//
// Key responsibilities:
//   - Capture all transaction details with timestamps and amounts
//   - Track parent-child relationships between transactions (reversals, amendments)
//   - Maintain comprehensive audit trail for compliance
//   - Support reconciliation workflows with status tracking
//   - Enforce capacity limits to prevent unbounded growth
//   - Provide optimistic concurrency control for safe updates
//
// # Core Concepts
//
// ## Aggregate Root: FinancialPositionLog
//
// The FinancialPositionLog is the aggregate root that encapsulates all transaction
// history for a single account. It enforces the following invariants:
//
//   - Maximum 10,000 transaction entries per log
//   - Maximum 10,000 audit entries per log
//   - Posted logs cannot accept new entries (immutable after posting)
//   - State transitions follow a finite state machine (FSM)
//   - Version field increments on every state transition (optimistic locking)
//
// Example:
//
//	lineage, err := domain.NewTransactionLineage(txID, "payment")
//	if err != nil {
//	    return err
//	}
//
//	log, err := domain.NewFinancialPositionLog("ACCT-123", nil, lineage)
//	if err != nil {
//	    return err
//	}
//
//	// Add transaction entry
//	entry, _ := domain.NewTransactionLogEntry(
//	    txID,
//	    "ACCT-123",
//	    money,
//	    domain.PostingDirectionDebit,
//	    time.Now().UTC(),
//	    "Monthly payment",
//	    "REF-001",
//	    domain.TransactionSourceAPI,
//	)
//	log.AddEntry(entry)
//
// ## Entities
//
// TransactionLogEntry represents a single financial transaction with:
//   - Unique EntryID and TransactionID (UUIDs)
//   - Money amount with currency
//   - Posting direction (DEBIT/CREDIT)
//   - Timestamp, description, reference
//   - Transaction source (API, Batch, Manual, etc.)
//
// TransactionLineage tracks parent-child relationships:
//   - Parent transaction (for reversals, amendments)
//   - Child transactions (transactions spawned from this one)
//   - Related transactions (corrections, adjustments)
//   - Immutable after construction (defensive copying)
//
// AuditTrailEntry provides compliance audit trail:
//   - User ID and action performed
//   - Timestamp (UTC)
//   - IP address (optional)
//   - System context metadata (immutable map)
//   - Automatically generated audit ID
//
// StatusTracking manages lifecycle state:
//   - Current status (Pending, Reconciled, Posted, etc.)
//   - Previous status (for audit trail)
//   - Reconciliation status (Matched, Mismatched, Resolved)
//   - Status reason (free text explanation)
//
// ## Value Objects
//
// Money represents currency-aware monetary amounts:
//   - Immutable (fields unexported, accessors only)
//   - Decimal precision (arbitrary precision via shopspring/decimal)
//   - Currency validation (7 supported currencies: GBP, USD, EUR, JPY, CHF, CAD, AUD)
//   - Arithmetic operations (Add, Subtract)
//   - Currency mismatch prevention
//
// Example:
//
//	money, err := domain.NewMoney(decimal.NewFromFloat(100.50), domain.CurrencyGBP)
//	if err != nil {
//	    return err
//	}
//
//	amount := money.Amount()    // Get amount (accessor)
//	currency := money.Currency() // Get currency (accessor)
//
// ReconciliationStatus enum tracks reconciliation state:
//   - Unreconciled: Initial state
//   - Matched: Successfully matched with external system
//   - Mismatched: Discrepancy detected
//   - Resolved: Discrepancy resolved
//
// TransactionStatus enum defines lifecycle states:
//   - Pending: Initial state
//   - Reconciled: Matched and verified
//   - Posted: Final committed state
//   - Failed: Processing failure
//   - Rejected: Business rule violation
//   - Amended: Modified after initial creation
//   - Cancelled: User-initiated cancellation
//   - Reversed: Posted transaction reversal (only valid from Posted)
//
// PostingDirection enum:
//   - Debit: Funds leaving account
//   - Credit: Funds entering account
//
// TransactionSource enum:
//   - API: Via REST/gRPC API
//   - Batch: Batch file processing
//   - Manual: Manual entry
//   - System: System-generated
//   - Import: Data import
//   - Migration: Legacy system migration
//   - Correction: Error correction
//
// # Capacity Limits
//
// To prevent unbounded growth and maintain performance, the following limits are enforced:
//
//	MaxTransactionEntries = 10,000 entries per FinancialPositionLog
//	MaxAuditEntries      = 10,000 entries per FinancialPositionLog
//
// When limits are reached:
//   - AddEntry() returns ErrTooManyEntries
//   - State transition methods return ErrTooManyAuditEntries
//   - The aggregate prevents partial state updates (audit check before status change)
//
// Best practices:
//   - Archive logs that reach capacity limits
//   - Create new FinancialPositionLog for continued activity
//   - Monitor capacity metrics in production
//
// # Optimistic Concurrency Control
//
// The Version field implements optimistic locking to prevent lost updates in concurrent scenarios:
//
//	type FinancialPositionLog struct {
//	    // ... other fields
//	    Version int64  // Incremented on status transitions
//	}
//
// Version increments on state-changing operations:
//   - MarkReconciled()
//   - MarkPosted()
//   - Reject()
//   - Amend()
//   - Fail()
//   - Cancel()
//
// Version does NOT increment on:
//   - AddEntry() - accumulation within draft state
//   - AddAuditEntry() - metadata only
//
// Persistence layer should use Version in UPDATE statements:
//
//	UPDATE financial_position_logs
//	SET status = $1, version = version + 1, updated_at = $2
//	WHERE log_id = $3 AND version = $4
//
// If no rows are updated (version mismatch), a concurrent modification occurred.
//
// # Immutability Patterns
//
// This package follows strict immutability principles for data integrity and thread safety:
//
// ## Value Objects (Fully Immutable)
//
// Money:
//   - Fields unexported (amount, currency)
//   - Accessor methods return copies
//   - Arithmetic methods return new instances
//
// AuditTrailEntry:
//   - SystemContext map is cloned in constructor (defensive copy)
//   - External mutations cannot affect audit data
//
// TransactionLineage:
//   - Fields are currently exported (TransactionID, ParentTransactionID, etc.)
//   - Mutation methods available: SetParent(), AddChild(), AddRelated()
//   - Validates UUID inputs (rejects uuid.Nil)
//   - Prevents duplicate children/related IDs
//
// ## Entities (Controlled Mutability)
//
// FinancialPositionLog:
//   - Mutable via controlled lifecycle methods only
//   - AddEntry() appends entries (draft state)
//   - State transition methods validate before mutation
//   - Posted logs become immutable (ErrAlreadyPosted)
//
// # State Machine
//
// TransactionStatus follows a finite state machine with strict transition rules:
//
//	PENDING
//	  ├─→ RECONCILED
//	  │     ├─→ POSTED ────→ REVERSED (only valid final transition)
//	  │     ├─→ AMENDED ──┬─→ RECONCILED (loop back)
//	  │     │             └─→ POSTED
//	  │     └─→ REJECTED (final)
//	  ├─→ POSTED (direct posting)
//	  ├─→ FAILED (final)
//	  ├─→ REJECTED (final)
//	  └─→ CANCELLED (final)
//
// Final states (cannot transition further except POSTED → REVERSED):
//   - Posted (can only go to Reversed)
//   - Failed
//   - Rejected
//   - Cancelled
//   - Reversed
//
// Example state transitions:
//
//	// Pending → Reconciled
//	err := log.MarkReconciled(
//	    domain.ReconciliationStatusMatched,
//	    "Matched with bank statement",
//	    auditEntry,
//	)
//
//	// Reconciled → Posted
//	err = log.MarkPosted("Final posting", auditEntry)
//
//	// Posted → Reversed (special case)
//	// Note: This would typically be a new transaction with lineage
//
// Invalid transitions return errors:
//   - ErrInvalidStatusTransition
//   - ErrAlreadyPosted
//   - ErrInvalidReconciliationStatus
//
// # Common Workflows
//
// ## Workflow 1: Create New Position Log with Transaction
//
//	// 1. Create transaction lineage
//	lineage, err := domain.NewTransactionLineage(
//	    uuid.New(),
//	    "payment",
//	)
//	if err != nil {
//	    return err
//	}
//
//	// 2. Create position log
//	log, err := domain.NewFinancialPositionLog("ACCT-123", nil, lineage)
//
//	// 3. Create transaction entry
//	money, _ := domain.NewMoney(decimal.NewFromFloat(100.00), domain.CurrencyGBP)
//	entry, err := domain.NewTransactionLogEntry(
//	    uuid.New(),
//	    "ACCT-123",
//	    money,
//	    domain.PostingDirectionDebit,
//	    time.Now().UTC(),
//	    "Monthly subscription",
//	    "SUB-2025-01",
//	    domain.TransactionSourceAPI,
//	)
//
//	// 4. Add entry to log
//	err = log.AddEntry(entry)
//
// ## Workflow 2: Reconcile and Post Transaction
//
//	// 1. Create audit entry for reconciliation
//	auditEntry, err := domain.NewAuditTrailEntry(
//	    "user-123",
//	    "RECONCILE",
//	    "Matched with bank statement",
//	    "192.168.1.1",
//	    map[string]string{
//	        "reconciliation_id": "REC-001",
//	        "statement_ref":     "STMT-2025-01",
//	    },
//	)
//
//	// 2. Mark as reconciled
//	err = log.MarkReconciled(
//	    domain.ReconciliationStatusMatched,
//	    "Matched with external system",
//	    auditEntry,
//	)
//
//	// 3. Create audit entry for posting
//	postAudit, err := domain.NewAuditTrailEntry(
//	    "system",
//	    "POST",
//	    "Final posting",
//	    "",
//	    nil,
//	)
//
//	// 4. Post the transaction
//	err = log.MarkPosted("Final posting approved", postAudit)
//
// ## Workflow 3: Handle Transaction Reversal
//
//	// 1. Get original transaction ID from posted log
//	originalTxID := postedLog.TransactionLineage.TransactionID
//
//	// 2. Create lineage for reversal (parent = original)
//	reversalLineage, err := domain.NewTransactionLineage(
//	    uuid.New(),
//	    "reversal",
//	)
//	if err != nil {
//	    return err
//	}
//	reversalLineage.SetParent(originalTxID)  // Set parent relationship
//
//	// 3. Create new position log for reversal
//	reversalLog, err := domain.NewFinancialPositionLog(
//	    accountID,
//	    nil,
//	    reversalLineage,
//	)
//
//	// 4. Add reversal entry (opposite direction)
//	reversalMoney, _ := domain.NewMoney(originalAmount, currency)
//	reversalEntry, err := domain.NewTransactionLogEntry(
//	    reversalLineage.TransactionID,
//	    accountID,
//	    reversalMoney,
//	    domain.PostingDirectionCredit,  // Opposite of original debit
//	    time.Now().UTC(),
//	    "Reversal of "+originalRef,
//	    "REV-"+originalRef,
//	    domain.TransactionSourceManual,
//	)
//	reversalLog.AddEntry(reversalEntry)
//
// ## Workflow 4: Amend Draft Transaction
//
//	// 1. Check if log is in amendable state
//	if log.StatusTracking.CurrentStatus == domain.TransactionStatusPosted {
//	    return domain.ErrAlreadyPosted
//	}
//	// Note: StatusTracking fields are currently exported for direct access
//
//	// 2. Create audit entry
//	auditEntry, _ := domain.NewAuditTrailEntry(
//	    "user-123",
//	    "AMEND",
//	    "Correcting amount",
//	    "192.168.1.1",
//	    nil,
//	)
//
//	// 3. Amend the log
//	err := log.Amend("Corrected transaction amount", auditEntry)
//
//	// 4. Add corrected entry (business logic determines how)
//	// Option A: Clear and re-add entries
//	// Option B: Add adjustment entry
//	// This depends on business requirements
//
// # Thread Safety
//
// This package provides the following thread-safety guarantees:
//
// Value Objects (Thread-Safe):
//   - Money: Immutable, safe for concurrent reads
//   - AuditTrailEntry: Immutable after construction
//   - TransactionLineage: Immutable after construction (defensive copies)
//
// Entities (NOT Thread-Safe):
//   - FinancialPositionLog: Mutable aggregate, requires external synchronization
//   - Concurrent modifications must be coordinated by the application layer
//   - Use optimistic locking (Version field) in persistence layer
//
// # Error Handling
//
// Common errors returned by this package:
//
//   - ErrInvalidTransactionID: Transaction ID is uuid.Nil
//   - ErrInvalidAccountID: Account ID is empty
//   - ErrInvalidEntryAmount: Amount is not positive
//   - ErrInvalidPostingDirection: Posting direction is invalid
//   - ErrTooManyEntries: Transaction entry limit (10,000) reached
//   - ErrTooManyAuditEntries: Audit entry limit (10,000) reached
//   - ErrAlreadyPosted: Cannot modify posted log
//   - ErrInvalidStatusTransition: Invalid state machine transition
//   - ErrInvalidReconciliationStatus: ReconciliationStatusUnreconciled on MarkReconciled
//   - ErrInvalidCurrency: Currency not in supported list
//   - ErrCurrencyMismatch: Arithmetic on different currencies
//
// # Testing
//
// The package includes comprehensive test coverage:
//
//   - Unit tests for all constructors and methods
//   - State transition matrix tests (all valid/invalid transitions)
//   - Capacity boundary tests (9,999 / 10,000 / 10,001 entries)
//   - String length edge cases
//   - Currency validation and arithmetic
//   - Immutability verification (defensive copying)
//   - Concurrent access patterns (with -race flag)
//
// Run tests:
//
//	go test ./internal/position-keeping/domain/... -v
//	go test ./internal/position-keeping/domain/... -v -cover
//	go test ./internal/position-keeping/domain/... -v -race
//
// # References
//
// BIAN Standards:
//   - BIAN Service Landscape v12.0
//   - Transaction Log Service Domain
//   - Position Keeping Control Record
//
// Design Patterns:
//   - Domain-Driven Design (Eric Evans)
//   - Aggregate Pattern
//   - Value Object Pattern
//   - Finite State Machine
//   - Optimistic Concurrency Control
//
// Related Packages:
//   - github.com/meridianhub/meridian/services/position-keeping/service (gRPC service layer)
//   - github.com/meridianhub/meridian/services/position-keeping/adapters/persistence (persistence)
//   - github.com/meridianhub/meridian/api/proto/meridian/positionkeeping/v1 (protobuf contracts)
package domain
