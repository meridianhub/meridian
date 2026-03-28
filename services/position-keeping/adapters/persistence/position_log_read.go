package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
)

// FindByID retrieves a FinancialPositionLog by its LogID.
// Returns domain.ErrNotFound if the log doesn't exist.
// In multi-tenant mode, the context must contain the tenant ID for schema routing.
func (r *PostgresRepository) FindByID(ctx context.Context, logID uuid.UUID) (*domain.FinancialPositionLog, error) {
	var result *domain.FinancialPositionLog

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, created_at, updated_at, log_id, account_id, account_service_domain, version,
				current_status, previous_status, status_updated_at, status_reason, failure_reason,
				reconciliation_status,
				opening_balance_amount, opening_balance_currency, opening_balance_recorded_at
			FROM financial_position_log
			WHERE log_id = $1 AND deleted_at IS NULL`

		dbID, log, err := r.scanLogRow(tx.QueryRow(ctx, query, logID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return fmt.Errorf("failed to query financial position log: %w", err)
		}

		// Load related entities
		if err := r.loadTransactionLogEntriesTx(ctx, tx, dbID, log); err != nil {
			return err
		}
		if err := r.loadTransactionLineageTx(ctx, tx, dbID, log); err != nil {
			return err
		}
		if err := r.loadAuditTrailEntriesTx(ctx, tx, dbID, log); err != nil {
			return err
		}

		result = log
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// scanLogRow scans a single financial position log row and applies post-scan parsing.
// Returns the database ID, the parsed log, and any error.
func (r *PostgresRepository) scanLogRow(row pgx.Row) (uuid.UUID, *domain.FinancialPositionLog, error) {
	var dbID uuid.UUID
	var log domain.FinancialPositionLog
	var statusTracking domain.StatusTracking
	var currentStatus, reconciliationStatus string
	var previousStatus sql.NullString
	var failureReason sql.NullString
	var openingBalanceAmount decimal.Decimal
	var openingBalanceCurrency string
	var openingBalanceRecordedAt sql.NullTime

	err := row.Scan(
		&dbID, &log.CreatedAt, &log.UpdatedAt, &log.LogID, &log.AccountID, &log.AccountServiceDomain, &log.Version,
		&currentStatus, &previousStatus, &statusTracking.StatusUpdatedAt,
		&statusTracking.StatusReason, &failureReason,
		&reconciliationStatus,
		&openingBalanceAmount, &openingBalanceCurrency, &openingBalanceRecordedAt,
	)
	if err != nil {
		return uuid.Nil, nil, err
	}

	mapStatusTracking(&statusTracking, currentStatus, previousStatus, failureReason, reconciliationStatus)
	log.StatusTracking = &statusTracking

	if err := mapOpeningBalance(&log, openingBalanceAmount, openingBalanceCurrency, openingBalanceRecordedAt); err != nil {
		return uuid.Nil, nil, err
	}

	return dbID, &log, nil
}

// mapStatusTracking parses raw status strings into the domain StatusTracking struct.
func mapStatusTracking(st *domain.StatusTracking, currentStatus string, previousStatus, failureReason sql.NullString, reconciliationStatus string) {
	st.CurrentStatus = domain.ParseTransactionStatus(currentStatus)
	if previousStatus.Valid {
		prevStatus := domain.ParseTransactionStatus(previousStatus.String)
		st.PreviousStatus = &prevStatus
	}
	if failureReason.Valid {
		st.FailureReason = failureReason.String
	}
	st.ReconciliationStatus = domain.ParseReconciliationStatus(reconciliationStatus)
}

// mapOpeningBalance parses and assigns the opening balance to a log.
func mapOpeningBalance(log *domain.FinancialPositionLog, amount decimal.Decimal, currency string, recordedAt sql.NullTime) error {
	openingBalance, err := domain.NewMoneyFromInstrumentCode(amount, currency)
	if err != nil {
		return fmt.Errorf("failed to create opening balance Money for instrument %q: %w", currency, err)
	}
	log.OpeningBalance = openingBalance
	if recordedAt.Valid {
		log.OpeningBalanceRecordedAt = recordedAt.Time
	}
	return nil
}

// FindByAccountID retrieves all FinancialPositionLogs for a given account ID.
// In multi-tenant mode, the context must contain the tenant ID for schema routing.
func (r *PostgresRepository) FindByAccountID(ctx context.Context, accountID string) ([]*domain.FinancialPositionLog, error) {
	var result []*domain.FinancialPositionLog

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, created_at, updated_at, log_id, account_id, account_service_domain, version,
				current_status, previous_status, status_updated_at, status_reason, failure_reason,
				reconciliation_status,
				opening_balance_amount, opening_balance_currency, opening_balance_recorded_at
			FROM financial_position_log
			WHERE account_id = $1 AND deleted_at IS NULL
			ORDER BY created_at DESC`

		rows, err := tx.Query(ctx, query, accountID)
		if err != nil {
			return fmt.Errorf("failed to query financial position logs by account: %w", err)
		}
		defer rows.Close()

		result, err = r.scanLogsTx(ctx, tx, rows)
		return err
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// List retrieves FinancialPositionLogs matching the given filter with pagination.
// In multi-tenant mode, the context must contain the tenant ID for schema routing.
func (r *PostgresRepository) List(ctx context.Context, filter domain.PositionLogFilter) ([]*domain.FinancialPositionLog, error) {
	if filter.Limit <= 0 {
		return nil, ErrInvalidLimit
	}

	var result []*domain.FinancialPositionLog

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query, args := buildListQuery(filter)

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("failed to query financial position logs: %w", err)
		}
		defer rows.Close()

		result, err = r.scanLogsTx(ctx, tx, rows)
		return err
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// buildListQuery constructs the SQL query and args for listing position logs with dynamic filters.
func buildListQuery(filter domain.PositionLogFilter) (string, []any) {
	query := `
		SELECT id, created_at, updated_at, log_id, account_id, account_service_domain, version,
			current_status, previous_status, status_updated_at, status_reason, failure_reason,
			reconciliation_status,
			opening_balance_amount, opening_balance_currency, opening_balance_recorded_at
		FROM financial_position_log
		WHERE deleted_at IS NULL`

	args := []any{}
	argPos := 1

	if len(filter.AccountIDs) > 0 {
		query += fmt.Sprintf(" AND account_id = ANY($%d)", argPos)
		args = append(args, filter.AccountIDs)
		argPos++
	} else if filter.AccountID != nil {
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

	query += " ORDER BY created_at DESC"
	query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argPos, argPos+1)
	args = append(args, filter.Limit, filter.Offset)

	return query, args
}

// FindPendingForReconciliation retrieves logs that are pending reconciliation.
// In multi-tenant mode, the context must contain the tenant ID for schema routing.
func (r *PostgresRepository) FindPendingForReconciliation(ctx context.Context, limit int) ([]*domain.FinancialPositionLog, error) {
	var result []*domain.FinancialPositionLog

	err := r.withReadTransaction(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, created_at, updated_at, log_id, account_id, account_service_domain, version,
				current_status, previous_status, status_updated_at, status_reason, failure_reason,
				reconciliation_status,
				opening_balance_amount, opening_balance_currency, opening_balance_recorded_at
			FROM financial_position_log
			WHERE deleted_at IS NULL
				AND current_status = 'PENDING'
				AND reconciliation_status = 'UNRECONCILED'
			ORDER BY created_at ASC`

		args := []any{}
		if limit > 0 {
			query += " LIMIT $1"
			args = append(args, limit)
		}

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("failed to query pending logs: %w", err)
		}
		defer rows.Close()

		result, err = r.scanLogsTx(ctx, tx, rows)
		return err
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// scanLogsTx scans log rows and loads related entities using a transaction.
// Uses batch loading to avoid N+1 queries.
func (r *PostgresRepository) scanLogsTx(ctx context.Context, tx pgx.Tx, rows pgx.Rows) ([]*domain.FinancialPositionLog, error) {
	logs, dbIDs, dbIDToLog, err := r.scanLogRows(rows)
	if err != nil {
		return nil, err
	}

	// If no logs were found, return early
	if len(logs) == 0 {
		return logs, nil
	}

	// Batch load all related entities for all logs to avoid N+1 queries
	if err := r.loadTransactionLogEntriesBatchTx(ctx, tx, dbIDs, dbIDToLog); err != nil {
		return nil, err
	}

	if err := r.loadTransactionLineageBatchTx(ctx, tx, dbIDs, dbIDToLog); err != nil {
		return nil, err
	}

	if err := r.loadAuditTrailEntriesBatchTx(ctx, tx, dbIDs, dbIDToLog); err != nil {
		return nil, err
	}

	return logs, nil
}

// scanLogRows scans multiple log rows without loading related entities.
func (r *PostgresRepository) scanLogRows(rows pgx.Rows) ([]*domain.FinancialPositionLog, []uuid.UUID, map[uuid.UUID]*domain.FinancialPositionLog, error) {
	logs := []*domain.FinancialPositionLog{}
	dbIDToLog := make(map[uuid.UUID]*domain.FinancialPositionLog)
	dbIDs := []uuid.UUID{}

	for rows.Next() {
		dbID, log, err := r.scanLogRow(rows)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to scan financial position log: %w", err)
		}

		logs = append(logs, log)
		dbIDToLog[dbID] = log
		dbIDs = append(dbIDs, dbID)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("error iterating financial position logs: %w", err)
	}

	return logs, dbIDs, dbIDToLog, nil
}

// loadTransactionLogEntriesTx is a transaction-aware version of loadTransactionLogEntries.
func (r *PostgresRepository) loadTransactionLogEntriesTx(ctx context.Context, tx pgx.Tx, financialPosLogID uuid.UUID, log *domain.FinancialPositionLog) error {
	query := `
		SELECT entry_id, transaction_id, account_id, amount_cents, currency,
			direction, timestamp, description, reference, source
		FROM transaction_log_entry
		WHERE financial_position_log_id = $1 AND deleted_at IS NULL
		ORDER BY timestamp ASC`

	rows, err := tx.Query(ctx, query, financialPosLogID)
	if err != nil {
		return fmt.Errorf("failed to load transaction log entries: %w", err)
	}
	defer rows.Close()

	entries := []*domain.TransactionLogEntry{}
	for rows.Next() {
		entry, scanErr := scanTransactionLogEntryRow(rows, nil)
		if scanErr != nil {
			return fmt.Errorf("failed to scan transaction log entry: %w", scanErr)
		}
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating transaction log entries: %w", err)
	}

	log.TransactionLogEntries = entries
	return nil
}

// loadTransactionLineageTx is a transaction-aware version of loadTransactionLineage.
func (r *PostgresRepository) loadTransactionLineageTx(ctx context.Context, tx pgx.Tx, financialPosLogID uuid.UUID, log *domain.FinancialPositionLog) error {
	query := `
		SELECT transaction_id, parent_transaction_id, child_transaction_ids,
			related_transaction_ids, transaction_type
		FROM transaction_lineage
		WHERE financial_position_log_id = $1 AND deleted_at IS NULL`

	lineage, err := scanTransactionLineageRow(tx.QueryRow(ctx, query, financialPosLogID), nil)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No lineage is optional
			return nil
		}
		return fmt.Errorf("failed to load transaction lineage: %w", err)
	}

	log.TransactionLineage = lineage
	return nil
}

// loadAuditTrailEntriesTx is a transaction-aware version of loadAuditTrailEntries.
func (r *PostgresRepository) loadAuditTrailEntriesTx(ctx context.Context, tx pgx.Tx, financialPosLogID uuid.UUID, log *domain.FinancialPositionLog) error {
	query := `
		SELECT audit_id, timestamp, user_id, action, details, ip_address, system_context
		FROM audit_trail_entry
		WHERE financial_position_log_id = $1 AND deleted_at IS NULL
		ORDER BY timestamp ASC`

	rows, err := tx.Query(ctx, query, financialPosLogID)
	if err != nil {
		return fmt.Errorf("failed to load audit trail entries: %w", err)
	}
	defer rows.Close()

	entries := []*domain.AuditTrailEntry{}
	for rows.Next() {
		entry, scanErr := scanAuditTrailEntryRow(rows, nil)
		if scanErr != nil {
			return fmt.Errorf("failed to scan audit trail entry: %w", scanErr)
		}
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating audit trail entries: %w", err)
	}

	log.AuditTrail = entries
	return nil
}

// loadTransactionLogEntriesBatchTx loads transaction log entries for multiple logs in a single query.
// This avoids N+1 query issues when loading many logs.
func (r *PostgresRepository) loadTransactionLogEntriesBatchTx(ctx context.Context, tx pgx.Tx, dbIDs []uuid.UUID, dbIDToLog map[uuid.UUID]*domain.FinancialPositionLog) error {
	if len(dbIDs) == 0 {
		return nil
	}

	placeholders, args := buildINClause(dbIDs)

	query := fmt.Sprintf(`
		SELECT financial_position_log_id, entry_id, transaction_id, account_id, amount_cents, currency,
			direction, timestamp, description, reference, source
		FROM transaction_log_entry
		WHERE financial_position_log_id IN (%s) AND deleted_at IS NULL
		ORDER BY financial_position_log_id, timestamp ASC`, strings.Join(placeholders, ","))

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to load transaction log entries batch: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var financialPosLogID uuid.UUID
		entry, err := scanTransactionLogEntryRow(rows, &financialPosLogID)
		if err != nil {
			return fmt.Errorf("failed to scan transaction log entry in batch: %w", err)
		}

		if log, ok := dbIDToLog[financialPosLogID]; ok {
			log.TransactionLogEntries = append(log.TransactionLogEntries, entry)
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating transaction log entries batch: %w", err)
	}

	return nil
}

// buildINClause builds SQL IN clause placeholders and args from a UUID slice.
func buildINClause(ids []uuid.UUID) ([]string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	return placeholders, args
}

// scanTransactionLogEntryRow scans a single transaction log entry row.
// If parentID is non-nil, it scans the parent foreign key into it first.
func scanTransactionLogEntryRow(row pgx.Row, parentID *uuid.UUID) (*domain.TransactionLogEntry, error) {
	var entry domain.TransactionLogEntry
	var amountCents int64
	var currency, direction, source string
	var description, reference sql.NullString

	var scanArgs []any
	if parentID != nil {
		scanArgs = append(scanArgs, parentID)
	}
	scanArgs = append(scanArgs,
		&entry.EntryID, &entry.TransactionID, &entry.AccountID,
		&amountCents, &currency, &direction, &entry.Timestamp,
		&description, &reference, &source,
	)

	if err := row.Scan(scanArgs...); err != nil {
		return nil, err
	}

	amount := centsToDecimal(amountCents)
	money, err := domain.NewMoneyFromInstrumentCode(amount, currency)
	if err != nil {
		return nil, fmt.Errorf("failed to create Money value for instrument %q: %w", currency, err)
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

	return &entry, nil
}

// loadTransactionLineageBatchTx loads transaction lineages for multiple logs in a single query.
// This avoids N+1 query issues when loading many logs.
func (r *PostgresRepository) loadTransactionLineageBatchTx(ctx context.Context, tx pgx.Tx, dbIDs []uuid.UUID, dbIDToLog map[uuid.UUID]*domain.FinancialPositionLog) error {
	if len(dbIDs) == 0 {
		return nil
	}

	placeholders, args := buildINClause(dbIDs)

	query := fmt.Sprintf(`
		SELECT financial_position_log_id, transaction_id, parent_transaction_id, child_transaction_ids,
			related_transaction_ids, transaction_type
		FROM transaction_lineage
		WHERE financial_position_log_id IN (%s) AND deleted_at IS NULL`, strings.Join(placeholders, ","))

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to load transaction lineage batch: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var financialPosLogID uuid.UUID
		lineage, err := scanTransactionLineageRow(rows, &financialPosLogID)
		if err != nil {
			return fmt.Errorf("failed to scan transaction lineage in batch: %w", err)
		}

		if log, ok := dbIDToLog[financialPosLogID]; ok {
			log.TransactionLineage = lineage
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating transaction lineage batch: %w", err)
	}

	return nil
}

// scanTransactionLineageRow scans a single transaction lineage row.
// If parentLogID is non-nil, it scans the parent foreign key into it first.
func scanTransactionLineageRow(row pgx.Row, parentLogID *uuid.UUID) (*domain.TransactionLineage, error) {
	var transactionID uuid.UUID
	var transactionType string
	var parentID sql.NullString
	var childIDsJSON, relatedIDsJSON []byte

	var scanArgs []any
	if parentLogID != nil {
		scanArgs = append(scanArgs, parentLogID)
	}
	scanArgs = append(scanArgs,
		&transactionID, &parentID,
		&childIDsJSON, &relatedIDsJSON, &transactionType,
	)

	if err := row.Scan(scanArgs...); err != nil {
		return nil, err
	}

	var parent *uuid.UUID
	if parentID.Valid {
		pid, err := uuid.Parse(parentID.String)
		if err != nil {
			return nil, fmt.Errorf("failed to parse parent transaction ID: %w", err)
		}
		parent = &pid
	}

	var childIDs []uuid.UUID
	if err := json.Unmarshal(childIDsJSON, &childIDs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal child transaction IDs: %w", err)
	}

	var relatedIDs []uuid.UUID
	if err := json.Unmarshal(relatedIDsJSON, &relatedIDs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal related transaction IDs: %w", err)
	}

	return domain.NewTransactionLineage(transactionID, transactionType, parent, childIDs, relatedIDs)
}

// loadAuditTrailEntriesBatchTx loads audit trail entries for multiple logs in a single query.
// This avoids N+1 query issues when loading many logs.
func (r *PostgresRepository) loadAuditTrailEntriesBatchTx(ctx context.Context, tx pgx.Tx, dbIDs []uuid.UUID, dbIDToLog map[uuid.UUID]*domain.FinancialPositionLog) error {
	if len(dbIDs) == 0 {
		return nil
	}

	placeholders, args := buildINClause(dbIDs)

	query := fmt.Sprintf(`
		SELECT financial_position_log_id, audit_id, timestamp, user_id, action, details, ip_address, system_context
		FROM audit_trail_entry
		WHERE financial_position_log_id IN (%s) AND deleted_at IS NULL
		ORDER BY financial_position_log_id, timestamp ASC`, strings.Join(placeholders, ","))

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to load audit trail entries batch: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var financialPosLogID uuid.UUID
		entry, scanErr := scanAuditTrailEntryRow(rows, &financialPosLogID)
		if scanErr != nil {
			return fmt.Errorf("failed to scan audit trail entry in batch: %w", scanErr)
		}

		if log, ok := dbIDToLog[financialPosLogID]; ok {
			log.AuditTrail = append(log.AuditTrail, entry)
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating audit trail entries batch: %w", err)
	}

	return nil
}

// scanAuditTrailEntryRow scans a single audit trail entry row.
// If parentLogID is non-nil, it scans the parent foreign key into it first.
func scanAuditTrailEntryRow(row pgx.Row, parentLogID *uuid.UUID) (*domain.AuditTrailEntry, error) {
	var entry domain.AuditTrailEntry
	var details, ipAddress sql.NullString
	var sysContext []byte

	var scanArgs []any
	if parentLogID != nil {
		scanArgs = append(scanArgs, parentLogID)
	}
	scanArgs = append(scanArgs,
		&entry.AuditID, &entry.Timestamp, &entry.UserID, &entry.Action,
		&details, &ipAddress, &sysContext,
	)

	if err := row.Scan(scanArgs...); err != nil {
		return nil, err
	}

	if details.Valid {
		entry.Details = details.String
	}
	if ipAddress.Valid {
		entry.IPAddress = ipAddress.String
	}

	if len(sysContext) > 0 {
		if err := json.Unmarshal(sysContext, &entry.SystemContext); err != nil {
			return nil, fmt.Errorf("failed to unmarshal system context: %w", err)
		}
	}

	return &entry, nil
}
