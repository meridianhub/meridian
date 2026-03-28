package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/samber/lo"
)

// Create persists a new FinancialPositionLog aggregate to the database.
// Returns domain.ErrConflict if a log with the same LogID already exists.
// In multi-tenant mode, the context must contain the tenant ID for schema routing.
func (r *PostgresRepository) Create(ctx context.Context, log *domain.FinancialPositionLog) error {
	return r.CreateWithOutbox(ctx, log, nil)
}

// insertLogAndRelated inserts the main log row and all related entities within an existing transaction.
func (r *PostgresRepository) insertLogAndRelated(ctx context.Context, tx pgx.Tx, log *domain.FinancialPositionLog) error {
	dbID, err := r.insertLogRow(ctx, tx, log)
	if err != nil {
		return err
	}

	if err := r.insertTransactionLogEntries(ctx, tx, dbID, log.TransactionLogEntries); err != nil {
		return err
	}

	if log.TransactionLineage != nil {
		if err := r.insertTransactionLineage(ctx, tx, dbID, log.TransactionLineage); err != nil {
			return err
		}
	}

	return r.insertAuditTrailEntries(ctx, tx, dbID, log.AuditTrail)
}

// insertLogRow inserts the main financial_position_log row and returns the generated database ID.
func (r *PostgresRepository) insertLogRow(ctx context.Context, tx pgx.Tx, log *domain.FinancialPositionLog) (uuid.UUID, error) {
	userID := audit.GetUserFromContext(ctx)
	logQuery := `
		INSERT INTO financial_position_log (
			id, created_at, created_by, updated_at, updated_by,
			log_id, account_id, account_service_domain, version,
			current_status, previous_status, status_updated_at, status_reason, failure_reason,
			reconciliation_status,
			opening_balance_amount, opening_balance_currency, opening_balance_recorded_at
		) VALUES (
			gen_random_uuid(), $1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, $11, $12, $13,
			$14,
			$15, $16, $17
		) RETURNING id`

	var dbID uuid.UUID
	err := tx.QueryRow(ctx, logQuery,
		log.CreatedAt, userID, log.UpdatedAt, userID,
		log.LogID, log.AccountID, log.AccountServiceDomain, log.Version,
		log.StatusTracking.CurrentStatus.String(), nullString(log.StatusTracking.PreviousStatus),
		log.StatusTracking.StatusUpdatedAt, log.StatusTracking.StatusReason,
		nullStringValue(log.StatusTracking.FailureReason),
		log.StatusTracking.ReconciliationStatus.String(),
		log.OpeningBalance.Amount, openingBalanceCurrencyCode(log.OpeningBalance), nullTime(log.OpeningBalanceRecordedAt),
	).Scan(&dbID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return uuid.Nil, domain.ErrConflict
		}
		return uuid.Nil, fmt.Errorf("failed to insert financial position log: %w", err)
	}

	return dbID, nil
}

// Update updates an existing FinancialPositionLog aggregate.
// Uses optimistic locking via version checking.
// Returns domain.ErrNotFound if the log doesn't exist.
// Returns domain.ErrOptimisticLock if the version has changed.
// In multi-tenant mode, the context must contain the tenant ID for schema routing.
func (r *PostgresRepository) Update(ctx context.Context, log *domain.FinancialPositionLog) error {
	return r.UpdateWithOutbox(ctx, log, nil)
}

// updateLogAndRelated performs the core update: optimistic lock, replace entries/lineage, append audit.
func (r *PostgresRepository) updateLogAndRelated(ctx context.Context, tx pgx.Tx, log *domain.FinancialPositionLog) error {
	dbID, err := r.updateLogRow(ctx, tx, log)
	if err != nil {
		return err
	}

	// Delete and re-insert transaction log entries (simplest approach for aggregate updates)
	_, err = tx.Exec(ctx, "DELETE FROM transaction_log_entry WHERE financial_position_log_id = $1", dbID)
	if err != nil {
		return fmt.Errorf("failed to delete old transaction log entries: %w", err)
	}

	if err := r.insertTransactionLogEntries(ctx, tx, dbID, log.TransactionLogEntries); err != nil {
		return err
	}

	// Delete and re-insert transaction lineage
	_, err = tx.Exec(ctx, "DELETE FROM transaction_lineage WHERE financial_position_log_id = $1", dbID)
	if err != nil {
		return fmt.Errorf("failed to delete old transaction lineage: %w", err)
	}

	if log.TransactionLineage != nil {
		if err := r.insertTransactionLineage(ctx, tx, dbID, log.TransactionLineage); err != nil {
			return err
		}
	}

	return r.appendNewAuditEntries(ctx, tx, dbID, log.AuditTrail)
}

// updateLogRow performs the main log row update with optimistic locking.
func (r *PostgresRepository) updateLogRow(ctx context.Context, tx pgx.Tx, log *domain.FinancialPositionLog) (uuid.UUID, error) {
	var dbID uuid.UUID
	err := tx.QueryRow(ctx, "SELECT id FROM financial_position_log WHERE log_id = $1 AND deleted_at IS NULL", log.LogID).Scan(&dbID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, domain.ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("failed to find log for update: %w", err)
	}

	previousVersion := log.Version - 1
	userID := audit.GetUserFromContext(ctx)

	updateQuery := `
		UPDATE financial_position_log
		SET updated_at = $1, updated_by = $2, version = $3,
			current_status = $4, previous_status = $5, status_updated_at = $6,
			status_reason = $7, failure_reason = $8, reconciliation_status = $9
		WHERE id = $10 AND version = $11 AND deleted_at IS NULL`

	result, err := tx.Exec(ctx, updateQuery,
		log.UpdatedAt, userID, log.Version,
		log.StatusTracking.CurrentStatus.String(), nullString(log.StatusTracking.PreviousStatus),
		log.StatusTracking.StatusUpdatedAt, log.StatusTracking.StatusReason,
		nullStringValue(log.StatusTracking.FailureReason),
		log.StatusTracking.ReconciliationStatus.String(),
		dbID, previousVersion,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to update financial position log: %w", err)
	}

	if result.RowsAffected() == 0 {
		return uuid.Nil, domain.ErrOptimisticLock
	}

	return dbID, nil
}

// appendNewAuditEntries inserts only audit entries not already persisted (append-only semantics).
func (r *PostgresRepository) appendNewAuditEntries(ctx context.Context, tx pgx.Tx, dbID uuid.UUID, auditTrail []*domain.AuditTrailEntry) error {
	existingAuditIDs, err := r.getExistingAuditIDs(ctx, tx, dbID)
	if err != nil {
		return err
	}

	newAuditEntries := lo.Filter(auditTrail, func(entry *domain.AuditTrailEntry, _ int) bool {
		_, exists := existingAuditIDs[entry.AuditID]
		return !exists
	})

	if len(newAuditEntries) > 0 {
		return r.insertAuditTrailEntries(ctx, tx, dbID, newAuditEntries)
	}

	return nil
}

// CreateWithOutbox persists a new FinancialPositionLog and runs postFn within the same
// database transaction, enabling atomic event outbox writes.
func (r *PostgresRepository) CreateWithOutbox(ctx context.Context, log *domain.FinancialPositionLog, postFn func(pgx.Tx) error) error {
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

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	if err := r.insertLogAndRelated(ctx, tx, log); err != nil {
		return err
	}

	if postFn != nil {
		if err := postFn(tx); err != nil {
			return fmt.Errorf("post-create outbox write failed: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// UpdateWithOutbox updates an existing FinancialPositionLog and runs postFn within the same
// database transaction, enabling atomic event outbox writes.
func (r *PostgresRepository) UpdateWithOutbox(ctx context.Context, log *domain.FinancialPositionLog, postFn func(pgx.Tx) error) error {
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

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	if err := r.updateLogAndRelated(ctx, tx, log); err != nil {
		return err
	}

	if postFn != nil {
		if err := postFn(tx); err != nil {
			return fmt.Errorf("post-update outbox write failed: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit update transaction: %w", err)
	}

	return nil
}

func (r *PostgresRepository) insertTransactionLogEntries(ctx context.Context, tx pgx.Tx, financialPosLogID uuid.UUID, entries []*domain.TransactionLogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	query := `
		INSERT INTO transaction_log_entry (
			id, created_at, created_by, updated_at, updated_by,
			entry_id, financial_position_log_id, transaction_id, account_id,
			amount_cents, currency, direction, timestamp, description, reference, source
		) VALUES (
			gen_random_uuid(), $1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15
		)`

	batch := &pgx.Batch{}
	userID := audit.GetUserFromContext(ctx)

	for _, entry := range entries {
		// Convert decimal amount to cents (int64) - uses 2 decimal places for all instruments
		amountCents := decimalToCents(entry.Amount.Amount)

		batch.Queue(query,
			entry.CreatedAt, userID, entry.CreatedAt, userID,
			entry.EntryID, financialPosLogID, entry.TransactionID, entry.AccountID,
			amountCents, entry.Amount.Instrument.Code, entry.Direction.String(),
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

	userID := audit.GetUserFromContext(ctx)
	query := `
		INSERT INTO transaction_lineage (
			id, created_at, created_by, updated_at, updated_by,
			financial_position_log_id, transaction_id, parent_transaction_id,
			child_transaction_ids, related_transaction_ids, transaction_type
		) VALUES (
			gen_random_uuid(), CURRENT_TIMESTAMP, $1, CURRENT_TIMESTAMP, $2,
			$3, $4, $5,
			$6, $7, $8
		)`

	_, err = tx.Exec(ctx, query,
		userID, userID,
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
		INSERT INTO audit_trail_entry (
			id, created_at, created_by, updated_at, updated_by,
			audit_id, financial_position_log_id, timestamp, user_id,
			action, details, ip_address, system_context
		) VALUES (
			gen_random_uuid(), CURRENT_TIMESTAMP, $1, CURRENT_TIMESTAMP, $2,
			$3, $4, $5, $6,
			$7, $8, $9, $10
		)`

	batch := &pgx.Batch{}
	userID := audit.GetUserFromContext(ctx)

	for _, entry := range entries {
		sysContext, err := json.Marshal(entry.SystemContext)
		if err != nil {
			return fmt.Errorf("failed to marshal system context: %w", err)
		}

		batch.Queue(query,
			userID, userID,
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

func (r *PostgresRepository) getExistingAuditIDs(ctx context.Context, tx pgx.Tx, financialPosLogID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	query := `
		SELECT audit_id
		FROM audit_trail_entry
		WHERE financial_position_log_id = $1 AND deleted_at IS NULL`

	rows, err := tx.Query(ctx, query, financialPosLogID)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing audit IDs: %w", err)
	}
	defer rows.Close()

	existingIDs := make(map[uuid.UUID]struct{})
	for rows.Next() {
		var auditID uuid.UUID
		if err := rows.Scan(&auditID); err != nil {
			return nil, fmt.Errorf("failed to scan audit ID: %w", err)
		}
		existingIDs[auditID] = struct{}{}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating existing audit IDs: %w", err)
	}

	return existingIDs, nil
}
