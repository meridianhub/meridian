// Package repository provides PostgreSQL persistence implementation for Position Keeping domain.
package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/internal/position-keeping/domain"
	"github.com/shopspring/decimal"
)

var (
	// ErrNilLog is returned when a nil log is passed to a repository method
	ErrNilLog = errors.New("log cannot be nil")
	// ErrInvalidLimit is returned when limit is not greater than 0
	ErrInvalidLimit = errors.New("limit must be greater than 0")
	// ErrBulkInsertMismatch is returned when bulk insert count doesn't match expected
	ErrBulkInsertMismatch = errors.New("bulk insert count mismatch")
	// ErrDatabaseIDNotFound is returned when database ID mapping fails
	ErrDatabaseIDNotFound = errors.New("database ID not found for log_id")
)

// PostgresRepository implements domain.FinancialPositionLogRepository using PostgreSQL.
// It provides full CRUD operations with connection pooling, bulk operations, and transaction support.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository creates a new PostgreSQL repository with the given connection pool.
// The pool should be pre-configured with appropriate connection limits and timeouts.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// Create persists a new FinancialPositionLog aggregate to the database.
// Returns domain.ErrConflict if a log with the same LogID already exists.
func (r *PostgresRepository) Create(ctx context.Context, log *domain.FinancialPositionLog) error {
	if log == nil {
		return ErrNilLog
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Insert main financial_position_log
	// TODO: Consider extracting user ID from context for proper audit trails instead of hardcoding "system"
	logQuery := `
		INSERT INTO position_keeping.financial_position_logs (
			id, created_at, created_by, updated_at, updated_by,
			log_id, account_id, version,
			current_status, previous_status, status_updated_at, status_reason, failure_reason,
			reconciliation_status
		) VALUES (
			gen_random_uuid(), $1, $2, $3, $4,
			$5, $6, $7,
			$8, $9, $10, $11, $12,
			$13
		) RETURNING id`

	var dbID uuid.UUID
	err = tx.QueryRow(ctx, logQuery,
		log.CreatedAt, "system", log.UpdatedAt, "system",
		log.LogID, log.AccountID, log.Version,
		log.StatusTracking.CurrentStatus.String(), nullString(log.StatusTracking.PreviousStatus),
		log.StatusTracking.StatusUpdatedAt, log.StatusTracking.StatusReason,
		nullStringValue(log.StatusTracking.FailureReason),
		log.StatusTracking.ReconciliationStatus.String(),
	).Scan(&dbID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return domain.ErrConflict
		}
		return fmt.Errorf("failed to insert financial position log: %w", err)
	}

	// Insert transaction log entries
	if err := r.insertTransactionLogEntries(ctx, tx, dbID, log.TransactionLogEntries); err != nil {
		return err
	}

	// Insert transaction lineage (if present)
	if log.TransactionLineage != nil {
		if err := r.insertTransactionLineage(ctx, tx, dbID, log.TransactionLineage); err != nil {
			return err
		}
	}

	// Insert audit trail entries
	if err := r.insertAuditTrailEntries(ctx, tx, dbID, log.AuditTrail); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// CreateBatch persists multiple FinancialPositionLog aggregates atomically using efficient bulk operations.
// If any log fails to persist, the entire batch is rolled back.
func (r *PostgresRepository) CreateBatch(ctx context.Context, logs []*domain.FinancialPositionLog) error {
	if len(logs) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Use COPY for bulk insert of financial_position_logs
	copyCount, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"position_keeping", "financial_position_logs"},
		[]string{
			"id", "created_at", "created_by", "updated_at", "updated_by",
			"log_id", "account_id", "version",
			"current_status", "previous_status", "status_updated_at", "status_reason", "failure_reason",
			"reconciliation_status",
		},
		pgx.CopyFromSlice(len(logs), func(i int) ([]any, error) {
			log := logs[i]
			return []any{
				uuid.New(), // Generate new DB ID
				log.CreatedAt, "system", log.UpdatedAt, "system",
				log.LogID, log.AccountID, log.Version,
				log.StatusTracking.CurrentStatus.String(), nullString(log.StatusTracking.PreviousStatus),
				log.StatusTracking.StatusUpdatedAt, log.StatusTracking.StatusReason,
				nullStringValue(log.StatusTracking.FailureReason),
				log.StatusTracking.ReconciliationStatus.String(),
			}, nil
		}),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return domain.ErrConflict
		}
		return fmt.Errorf("failed to bulk insert financial position logs: %w", err)
	}

	if copyCount != int64(len(logs)) {
		return fmt.Errorf("%w: expected %d logs but inserted %d", ErrBulkInsertMismatch, len(logs), copyCount)
	}

	// Now insert related entities for each log
	// First, we need to map LogID to database ID
	logIDMap, err := r.getLogIDMap(ctx, tx, logs)
	if err != nil {
		return err
	}

	// Insert all transaction log entries
	for _, log := range logs {
		dbID, ok := logIDMap[log.LogID]
		if !ok {
			return fmt.Errorf("%w: %s", ErrDatabaseIDNotFound, log.LogID)
		}

		if err := r.insertTransactionLogEntries(ctx, tx, dbID, log.TransactionLogEntries); err != nil {
			return err
		}

		if log.TransactionLineage != nil {
			if err := r.insertTransactionLineage(ctx, tx, dbID, log.TransactionLineage); err != nil {
				return err
			}
		}

		if err := r.insertAuditTrailEntries(ctx, tx, dbID, log.AuditTrail); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit batch transaction: %w", err)
	}

	return nil
}

// FindByID retrieves a FinancialPositionLog by its LogID.
// Returns domain.ErrNotFound if the log doesn't exist.
func (r *PostgresRepository) FindByID(ctx context.Context, logID uuid.UUID) (*domain.FinancialPositionLog, error) {
	query := `
		SELECT id, created_at, updated_at, log_id, account_id, version,
			current_status, previous_status, status_updated_at, status_reason, failure_reason,
			reconciliation_status
		FROM position_keeping.financial_position_logs
		WHERE log_id = $1 AND deleted_at IS NULL`

	var dbID uuid.UUID
	var log domain.FinancialPositionLog
	var statusTracking domain.StatusTracking
	var currentStatus, reconciliationStatus string
	var previousStatus sql.NullString
	var failureReason sql.NullString

	err := r.pool.QueryRow(ctx, query, logID).Scan(
		&dbID, &log.CreatedAt, &log.UpdatedAt, &log.LogID, &log.AccountID, &log.Version,
		&currentStatus, &previousStatus, &statusTracking.StatusUpdatedAt,
		&statusTracking.StatusReason, &failureReason,
		&reconciliationStatus,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to query financial position log: %w", err)
	}

	// Parse status values
	statusTracking.CurrentStatus = domain.ParseTransactionStatus(currentStatus)
	if previousStatus.Valid {
		prevStatus := domain.ParseTransactionStatus(previousStatus.String)
		statusTracking.PreviousStatus = &prevStatus
	}
	if failureReason.Valid {
		statusTracking.FailureReason = failureReason.String
	}
	statusTracking.ReconciliationStatus = domain.ParseReconciliationStatus(reconciliationStatus)

	log.StatusTracking = &statusTracking

	// Load related entities
	if err := r.loadTransactionLogEntries(ctx, dbID, &log); err != nil {
		return nil, err
	}

	if err := r.loadTransactionLineage(ctx, dbID, &log); err != nil {
		return nil, err
	}

	if err := r.loadAuditTrailEntries(ctx, dbID, &log); err != nil {
		return nil, err
	}

	return &log, nil
}

// FindByAccountID retrieves all FinancialPositionLogs for a specific account.
func (r *PostgresRepository) FindByAccountID(ctx context.Context, accountID string) ([]*domain.FinancialPositionLog, error) {
	query := `
		SELECT id, created_at, updated_at, log_id, account_id, version,
			current_status, previous_status, status_updated_at, status_reason, failure_reason,
			reconciliation_status
		FROM position_keeping.financial_position_logs
		WHERE account_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC`

	rows, err := r.pool.Query(ctx, query, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to query financial position logs: %w", err)
	}
	defer rows.Close()

	return r.scanLogs(ctx, rows)
}

// Update updates an existing FinancialPositionLog using optimistic locking.
func (r *PostgresRepository) Update(ctx context.Context, log *domain.FinancialPositionLog) error {
	if log == nil {
		return ErrNilLog
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Get current database ID
	var dbID uuid.UUID
	err = tx.QueryRow(ctx, "SELECT id FROM position_keeping.financial_position_logs WHERE log_id = $1 AND deleted_at IS NULL", log.LogID).Scan(&dbID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("failed to find log for update: %w", err)
	}

	// Update with optimistic locking
	// Note: The domain layer increments the version, so we check against the previous version
	// (log.Version - 1) and set to the current version (log.Version)
	previousVersion := log.Version - 1

	updateQuery := `
		UPDATE position_keeping.financial_position_logs
		SET updated_at = $1, updated_by = $2, version = $3,
			current_status = $4, previous_status = $5, status_updated_at = $6,
			status_reason = $7, failure_reason = $8, reconciliation_status = $9
		WHERE id = $10 AND version = $11 AND deleted_at IS NULL`

	result, err := tx.Exec(ctx, updateQuery,
		log.UpdatedAt, "system", log.Version,
		log.StatusTracking.CurrentStatus.String(), nullString(log.StatusTracking.PreviousStatus),
		log.StatusTracking.StatusUpdatedAt, log.StatusTracking.StatusReason,
		nullStringValue(log.StatusTracking.FailureReason),
		log.StatusTracking.ReconciliationStatus.String(),
		dbID, previousVersion,
	)
	if err != nil {
		return fmt.Errorf("failed to update financial position log: %w", err)
	}

	if result.RowsAffected() == 0 {
		return domain.ErrOptimisticLock
	}

	// Delete and re-insert transaction log entries (simplest approach for aggregate updates)
	_, err = tx.Exec(ctx, "DELETE FROM position_keeping.transaction_log_entries WHERE financial_position_log_id = $1", dbID)
	if err != nil {
		return fmt.Errorf("failed to delete old transaction log entries: %w", err)
	}

	if err := r.insertTransactionLogEntries(ctx, tx, dbID, log.TransactionLogEntries); err != nil {
		return err
	}

	// Delete and re-insert transaction lineage
	_, err = tx.Exec(ctx, "DELETE FROM position_keeping.transaction_lineages WHERE financial_position_log_id = $1", dbID)
	if err != nil {
		return fmt.Errorf("failed to delete old transaction lineage: %w", err)
	}

	if log.TransactionLineage != nil {
		if err := r.insertTransactionLineage(ctx, tx, dbID, log.TransactionLineage); err != nil {
			return err
		}
	}

	// Delete and re-insert audit trail entries
	// Note: The domain aggregate owns the complete audit history. This delete/re-insert
	// approach assumes the domain layer maintains the full audit trail and we simply
	// persist the current state. If audit entries should be append-only at the persistence
	// layer, consider an alternative approach that compares and only inserts new entries.
	_, err = tx.Exec(ctx, "DELETE FROM position_keeping.audit_trail_entries WHERE financial_position_log_id = $1", dbID)
	if err != nil {
		return fmt.Errorf("failed to delete old audit trail entries: %w", err)
	}

	if err := r.insertAuditTrailEntries(ctx, tx, dbID, log.AuditTrail); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit update transaction: %w", err)
	}

	return nil
}

// List retrieves FinancialPositionLogs matching the given filter with pagination.
func (r *PostgresRepository) List(ctx context.Context, filter domain.PositionLogFilter) ([]*domain.FinancialPositionLog, error) {
	if filter.Limit <= 0 {
		return nil, ErrInvalidLimit
	}

	query := `
		SELECT id, created_at, updated_at, log_id, account_id, version,
			current_status, previous_status, status_updated_at, status_reason, failure_reason,
			reconciliation_status
		FROM position_keeping.financial_position_logs
		WHERE deleted_at IS NULL`

	args := []any{}
	argPos := 1

	// Build WHERE clauses dynamically
	if filter.AccountID != nil {
		query += fmt.Sprintf(" AND account_id = $%d", argPos)
		args = append(args, *filter.AccountID)
		argPos++
	}

	if filter.Status != nil {
		query += fmt.Sprintf(" AND current_status = $%d", argPos)
		args = append(args, filter.Status.String())
		argPos++
	}

	if filter.ReconciliationStatus != nil {
		query += fmt.Sprintf(" AND reconciliation_status = $%d", argPos)
		args = append(args, filter.ReconciliationStatus.String())
		argPos++
	}

	if filter.FromDate != nil {
		query += fmt.Sprintf(" AND updated_at >= $%d", argPos)
		args = append(args, *filter.FromDate)
		argPos++
	}

	if filter.ToDate != nil {
		query += fmt.Sprintf(" AND updated_at <= $%d", argPos)
		args = append(args, *filter.ToDate)
		argPos++
	}

	// Add pagination
	query += " ORDER BY created_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argPos, argPos+1)
	args = append(args, filter.Limit, filter.Offset)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query financial position logs: %w", err)
	}
	defer rows.Close()

	return r.scanLogs(ctx, rows)
}

// FindPendingForReconciliation retrieves logs that are pending reconciliation.
func (r *PostgresRepository) FindPendingForReconciliation(ctx context.Context, limit int) ([]*domain.FinancialPositionLog, error) {
	query := `
		SELECT id, created_at, updated_at, log_id, account_id, version,
			current_status, previous_status, status_updated_at, status_reason, failure_reason,
			reconciliation_status
		FROM position_keeping.financial_position_logs
		WHERE deleted_at IS NULL
			AND current_status = 'PENDING'
			AND reconciliation_status = 'UNRECONCILED'
		ORDER BY created_at ASC`

	args := []any{}
	if limit > 0 {
		query += " LIMIT $1"
		args = append(args, limit)
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending logs: %w", err)
	}
	defer rows.Close()

	return r.scanLogs(ctx, rows)
}

// Helper methods

func (r *PostgresRepository) insertTransactionLogEntries(ctx context.Context, tx pgx.Tx, financialPosLogID uuid.UUID, entries []*domain.TransactionLogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	query := `
		INSERT INTO position_keeping.transaction_log_entries (
			id, created_at, created_by, updated_at, updated_by,
			entry_id, financial_position_log_id, transaction_id, account_id,
			amount_cents, currency, direction, timestamp, description, reference, source
		) VALUES (
			gen_random_uuid(), $1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15
		)`

	batch := &pgx.Batch{}

	for _, entry := range entries {
		// Convert decimal amount to cents (int64)
		amountCents := decimalToCents(entry.Amount.Amount())

		batch.Queue(query,
			entry.CreatedAt, "system", entry.CreatedAt, "system",
			entry.EntryID, financialPosLogID, entry.TransactionID, entry.AccountID,
			amountCents, entry.Amount.Currency().String(), entry.Direction.String(),
			entry.Timestamp, nullStringValue(entry.Description), nullStringValue(entry.Reference), entry.Source.String(),
		)
	}

	br := tx.SendBatch(ctx, batch)
	defer func() {
		_ = br.Close()
	}()

	for range entries {
		_, err := br.Exec()
		if err != nil {
			return fmt.Errorf("failed to insert transaction log entry: %w", err)
		}
	}

	return nil
}

func (r *PostgresRepository) insertTransactionLineage(ctx context.Context, tx pgx.Tx, financialPosLogID uuid.UUID, lineage *domain.TransactionLineage) error {
	childIDs, err := json.Marshal(lineage.ChildTransactionIDs())
	if err != nil {
		return fmt.Errorf("failed to marshal child transaction IDs: %w", err)
	}

	relatedIDs, err := json.Marshal(lineage.RelatedTransactionIDs())
	if err != nil {
		return fmt.Errorf("failed to marshal related transaction IDs: %w", err)
	}

	query := `
		INSERT INTO position_keeping.transaction_lineages (
			id, created_at, created_by, updated_at, updated_by,
			financial_position_log_id, transaction_id, parent_transaction_id,
			child_transaction_ids, related_transaction_ids, transaction_type
		) VALUES (
			gen_random_uuid(), CURRENT_TIMESTAMP, $1, CURRENT_TIMESTAMP, $2,
			$3, $4, $5,
			$6, $7, $8
		)`

	_, err = tx.Exec(ctx, query,
		"system", "system",
		financialPosLogID, lineage.TransactionID(), lineage.ParentTransactionID(),
		childIDs, relatedIDs, lineage.TransactionType(),
	)
	if err != nil {
		return fmt.Errorf("failed to insert transaction lineage: %w", err)
	}

	return nil
}

func (r *PostgresRepository) insertAuditTrailEntries(ctx context.Context, tx pgx.Tx, financialPosLogID uuid.UUID, entries []*domain.AuditTrailEntry) error {
	if len(entries) == 0 {
		return nil
	}

	query := `
		INSERT INTO position_keeping.audit_trail_entries (
			id, created_at, created_by, updated_at, updated_by,
			audit_id, financial_position_log_id, timestamp, user_id,
			action, details, ip_address, system_context
		) VALUES (
			gen_random_uuid(), CURRENT_TIMESTAMP, $1, CURRENT_TIMESTAMP, $2,
			$3, $4, $5, $6,
			$7, $8, $9, $10
		)`

	batch := &pgx.Batch{}

	for _, entry := range entries {
		sysContext, err := json.Marshal(entry.SystemContext)
		if err != nil {
			return fmt.Errorf("failed to marshal system context: %w", err)
		}

		batch.Queue(query,
			"system", "system",
			entry.AuditID, financialPosLogID, entry.Timestamp, entry.UserID,
			entry.Action, nullStringValue(entry.Details), nullStringValue(entry.IPAddress), sysContext,
		)
	}

	br := tx.SendBatch(ctx, batch)
	defer func() {
		_ = br.Close()
	}()

	for range entries {
		_, err := br.Exec()
		if err != nil {
			return fmt.Errorf("failed to insert audit trail entry: %w", err)
		}
	}

	return nil
}

func (r *PostgresRepository) loadTransactionLogEntries(ctx context.Context, financialPosLogID uuid.UUID, log *domain.FinancialPositionLog) error {
	query := `
		SELECT entry_id, transaction_id, account_id, amount_cents, currency,
			direction, timestamp, description, reference, source
		FROM position_keeping.transaction_log_entries
		WHERE financial_position_log_id = $1 AND deleted_at IS NULL
		ORDER BY timestamp ASC`

	rows, err := r.pool.Query(ctx, query, financialPosLogID)
	if err != nil {
		return fmt.Errorf("failed to load transaction log entries: %w", err)
	}
	defer rows.Close()

	entries := []*domain.TransactionLogEntry{}
	for rows.Next() {
		var entry domain.TransactionLogEntry
		var amountCents int64
		var currency, direction, source string
		var description, reference sql.NullString

		err := rows.Scan(
			&entry.EntryID, &entry.TransactionID, &entry.AccountID,
			&amountCents, &currency, &direction, &entry.Timestamp,
			&description, &reference, &source,
		)
		if err != nil {
			return fmt.Errorf("failed to scan transaction log entry: %w", err)
		}

		// Convert cents to decimal and create Money
		amount := centsToDecimal(amountCents)
		money, err := domain.NewMoney(amount, domain.Currency(currency))
		if err != nil {
			return fmt.Errorf("failed to create Money value: %w", err)
		}
		entry.Amount = money
		entry.Direction = domain.ParsePostingDirection(direction)
		entry.Source = domain.ParseTransactionSource(source)

		if description.Valid {
			entry.Description = description.String
		}
		if reference.Valid {
			entry.Reference = reference.String
		}

		entries = append(entries, &entry)
	}

	log.TransactionLogEntries = entries
	return nil
}

func (r *PostgresRepository) loadTransactionLineage(ctx context.Context, financialPosLogID uuid.UUID, log *domain.FinancialPositionLog) error {
	query := `
		SELECT transaction_id, parent_transaction_id, child_transaction_ids,
			related_transaction_ids, transaction_type
		FROM position_keeping.transaction_lineages
		WHERE financial_position_log_id = $1 AND deleted_at IS NULL`

	var transactionID uuid.UUID
	var transactionType string
	var parentID sql.NullString
	var childIDsJSON, relatedIDsJSON []byte

	err := r.pool.QueryRow(ctx, query, financialPosLogID).Scan(
		&transactionID, &parentID,
		&childIDsJSON, &relatedIDsJSON, &transactionType,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No lineage is optional
			return nil
		}
		return fmt.Errorf("failed to load transaction lineage: %w", err)
	}

	var parent *uuid.UUID
	if parentID.Valid {
		pid, err := uuid.Parse(parentID.String)
		if err != nil {
			return fmt.Errorf("failed to parse parent transaction ID: %w", err)
		}
		parent = &pid
	}

	var childIDs []uuid.UUID
	if err := json.Unmarshal(childIDsJSON, &childIDs); err != nil {
		return fmt.Errorf("failed to unmarshal child transaction IDs: %w", err)
	}

	var relatedIDs []uuid.UUID
	if err := json.Unmarshal(relatedIDsJSON, &relatedIDs); err != nil {
		return fmt.Errorf("failed to unmarshal related transaction IDs: %w", err)
	}

	// Create the immutable TransactionLineage
	lineage, err := domain.NewTransactionLineage(transactionID, transactionType, parent, childIDs, relatedIDs)
	if err != nil {
		return fmt.Errorf("failed to create transaction lineage: %w", err)
	}

	log.TransactionLineage = lineage
	return nil
}

func (r *PostgresRepository) loadAuditTrailEntries(ctx context.Context, financialPosLogID uuid.UUID, log *domain.FinancialPositionLog) error {
	query := `
		SELECT audit_id, timestamp, user_id, action, details, ip_address, system_context
		FROM position_keeping.audit_trail_entries
		WHERE financial_position_log_id = $1 AND deleted_at IS NULL
		ORDER BY timestamp ASC`

	rows, err := r.pool.Query(ctx, query, financialPosLogID)
	if err != nil {
		return fmt.Errorf("failed to load audit trail entries: %w", err)
	}
	defer rows.Close()

	entries := []*domain.AuditTrailEntry{}
	for rows.Next() {
		var entry domain.AuditTrailEntry
		var details, ipAddress sql.NullString
		var sysContextJSON []byte

		err := rows.Scan(
			&entry.AuditID, &entry.Timestamp, &entry.UserID, &entry.Action,
			&details, &ipAddress, &sysContextJSON,
		)
		if err != nil {
			return fmt.Errorf("failed to scan audit trail entry: %w", err)
		}

		if details.Valid {
			entry.Details = details.String
		}
		if ipAddress.Valid {
			entry.IPAddress = ipAddress.String
		}

		if len(sysContextJSON) > 0 {
			if err := json.Unmarshal(sysContextJSON, &entry.SystemContext); err != nil {
				return fmt.Errorf("failed to unmarshal system context: %w", err)
			}
		}

		entries = append(entries, &entry)
	}

	log.AuditTrail = entries
	return nil
}

func (r *PostgresRepository) scanLogs(ctx context.Context, rows pgx.Rows) ([]*domain.FinancialPositionLog, error) {
	logs := []*domain.FinancialPositionLog{}

	for rows.Next() {
		var dbID uuid.UUID
		var log domain.FinancialPositionLog
		var statusTracking domain.StatusTracking
		var currentStatus, reconciliationStatus string
		var previousStatus sql.NullString
		var failureReason sql.NullString

		err := rows.Scan(
			&dbID, &log.CreatedAt, &log.UpdatedAt, &log.LogID, &log.AccountID, &log.Version,
			&currentStatus, &previousStatus, &statusTracking.StatusUpdatedAt,
			&statusTracking.StatusReason, &failureReason,
			&reconciliationStatus,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan financial position log: %w", err)
		}

		// Parse status values
		statusTracking.CurrentStatus = domain.ParseTransactionStatus(currentStatus)
		if previousStatus.Valid {
			prevStatus := domain.ParseTransactionStatus(previousStatus.String)
			statusTracking.PreviousStatus = &prevStatus
		}
		if failureReason.Valid {
			statusTracking.FailureReason = failureReason.String
		}
		statusTracking.ReconciliationStatus = domain.ParseReconciliationStatus(reconciliationStatus)

		log.StatusTracking = &statusTracking

		// Load related entities
		if err := r.loadTransactionLogEntries(ctx, dbID, &log); err != nil {
			return nil, err
		}

		if err := r.loadTransactionLineage(ctx, dbID, &log); err != nil {
			return nil, err
		}

		if err := r.loadAuditTrailEntries(ctx, dbID, &log); err != nil {
			return nil, err
		}

		logs = append(logs, &log)
	}

	return logs, nil
}

func (r *PostgresRepository) getLogIDMap(ctx context.Context, tx pgx.Tx, logs []*domain.FinancialPositionLog) (map[uuid.UUID]uuid.UUID, error) {
	logIDs := make([]uuid.UUID, len(logs))
	for i, log := range logs {
		logIDs[i] = log.LogID
	}

	// Build placeholders for IN clause
	placeholders := make([]string, len(logIDs))
	args := make([]any, len(logIDs))
	for i, id := range logIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT id, log_id
		FROM position_keeping.financial_position_logs
		WHERE log_id IN (%s)`, strings.Join(placeholders, ","))

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get log ID map: %w", err)
	}
	defer rows.Close()

	idMap := make(map[uuid.UUID]uuid.UUID)
	for rows.Next() {
		var dbID, logID uuid.UUID
		if err := rows.Scan(&dbID, &logID); err != nil {
			return nil, fmt.Errorf("failed to scan log ID mapping: %w", err)
		}
		idMap[logID] = dbID
	}

	return idMap, nil
}

// nullString converts a pointer to TransactionStatus to sql.NullString for PreviousStatus
func nullString(status *domain.TransactionStatus) sql.NullString {
	if status == nil {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: status.String(), Valid: true}
}

// nullStringValue converts a string to sql.NullString, treating empty strings as NULL
func nullStringValue(s string) sql.NullString {
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

// decimalToCents converts a decimal amount to cents (int64) for database storage.
// This function assumes 2 decimal places which is appropriate for most currencies
// (GBP, USD, EUR, etc.). Note that some currencies have different decimal place
// requirements (e.g., JPY has 0, some cryptocurrencies have more). The domain
// layer currently supports currencies with 2 decimal places as defined in
// domain.Currency constants.
// Example: 123.45 -> 12345 cents
func decimalToCents(d decimal.Decimal) int64 {
	cents := d.Mul(decimal.NewFromInt(100))
	return cents.IntPart()
}

// centsToDecimal converts cents (int64) from database storage to a decimal amount.
// This function assumes 2 decimal places which is appropriate for most currencies
// (GBP, USD, EUR, etc.). See decimalToCents for currency decimal place notes.
// Example: 12345 cents -> 123.45
func centsToDecimal(cents int64) decimal.Decimal {
	return decimal.NewFromInt(cents).Div(decimal.NewFromInt(100))
}
