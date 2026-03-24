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

		var dbID uuid.UUID
		var log domain.FinancialPositionLog
		var statusTracking domain.StatusTracking
		var currentStatus, reconciliationStatus string
		var previousStatus sql.NullString
		var failureReason sql.NullString
		var openingBalanceAmount decimal.Decimal
		var openingBalanceCurrency string
		var openingBalanceRecordedAt sql.NullTime

		err := tx.QueryRow(ctx, query, logID).Scan(
			&dbID, &log.CreatedAt, &log.UpdatedAt, &log.LogID, &log.AccountID, &log.AccountServiceDomain, &log.Version,
			&currentStatus, &previousStatus, &statusTracking.StatusUpdatedAt,
			&statusTracking.StatusReason, &failureReason,
			&reconciliationStatus,
			&openingBalanceAmount, &openingBalanceCurrency, &openingBalanceRecordedAt,
		)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return fmt.Errorf("failed to query financial position log: %w", err)
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

		// Parse opening balance (supports both currency and non-currency codes)
		openingBalance, err := domain.NewMoneyFromInstrumentCode(openingBalanceAmount, openingBalanceCurrency)
		if err != nil {
			return fmt.Errorf("failed to create opening balance Money for instrument %q: %w", openingBalanceCurrency, err)
		}
		log.OpeningBalance = openingBalance
		if openingBalanceRecordedAt.Valid {
			log.OpeningBalanceRecordedAt = openingBalanceRecordedAt.Time
		}

		// Load related entities
		if err := r.loadTransactionLogEntriesTx(ctx, tx, dbID, &log); err != nil {
			return err
		}
		if err := r.loadTransactionLineageTx(ctx, tx, dbID, &log); err != nil {
			return err
		}
		if err := r.loadAuditTrailEntriesTx(ctx, tx, dbID, &log); err != nil {
			return err
		}

		result = &log
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
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
		query := `
			SELECT id, created_at, updated_at, log_id, account_id, account_service_domain, version,
				current_status, previous_status, status_updated_at, status_reason, failure_reason,
				reconciliation_status,
				opening_balance_amount, opening_balance_currency, opening_balance_recorded_at
			FROM financial_position_log
			WHERE deleted_at IS NULL`

		args := []any{}
		argPos := 1

		// Build WHERE clauses dynamically
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

		// Add pagination
		query += " ORDER BY created_at DESC"
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argPos, argPos+1)
		args = append(args, filter.Limit, filter.Offset)

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
	logs := []*domain.FinancialPositionLog{}
	dbIDToLog := make(map[uuid.UUID]*domain.FinancialPositionLog)
	dbIDs := []uuid.UUID{}

	for rows.Next() {
		var dbID uuid.UUID
		var log domain.FinancialPositionLog
		var statusTracking domain.StatusTracking
		var currentStatus, reconciliationStatus string
		var previousStatus sql.NullString
		var failureReason sql.NullString
		var openingBalanceAmount decimal.Decimal
		var openingBalanceCurrency string
		var openingBalanceRecordedAt sql.NullTime

		err := rows.Scan(
			&dbID, &log.CreatedAt, &log.UpdatedAt, &log.LogID, &log.AccountID, &log.AccountServiceDomain, &log.Version,
			&currentStatus, &previousStatus, &statusTracking.StatusUpdatedAt,
			&statusTracking.StatusReason, &failureReason,
			&reconciliationStatus,
			&openingBalanceAmount, &openingBalanceCurrency, &openingBalanceRecordedAt,
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

		// Parse opening balance (supports both currency and non-currency codes)
		openingBalance, err := domain.NewMoneyFromInstrumentCode(openingBalanceAmount, openingBalanceCurrency)
		if err != nil {
			return nil, fmt.Errorf("failed to create opening balance Money for instrument %q: %w", openingBalanceCurrency, err)
		}
		log.OpeningBalance = openingBalance
		if openingBalanceRecordedAt.Valid {
			log.OpeningBalanceRecordedAt = openingBalanceRecordedAt.Time
		}

		logs = append(logs, &log)
		dbIDToLog[dbID] = &log
		dbIDs = append(dbIDs, dbID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating financial position logs: %w", err)
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

		// Convert cents to decimal and create Money (supports both currency and non-currency codes)
		amount := centsToDecimal(amountCents)
		money, err := domain.NewMoneyFromInstrumentCode(amount, currency)
		if err != nil {
			return fmt.Errorf("failed to create Money value for instrument %q: %w", currency, err)
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

	var transactionID uuid.UUID
	var transactionType string
	var parentID sql.NullString
	var childIDsJSON, relatedIDsJSON []byte

	err := tx.QueryRow(ctx, query, financialPosLogID).Scan(
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

	// Build IN clause with placeholders
	placeholders := make([]string, len(dbIDs))
	args := make([]any, len(dbIDs))
	for i, id := range dbIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

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
		var entry domain.TransactionLogEntry
		var amountCents int64
		var currency, direction, source string
		var description, reference sql.NullString

		err := rows.Scan(
			&financialPosLogID,
			&entry.EntryID, &entry.TransactionID, &entry.AccountID,
			&amountCents, &currency, &direction, &entry.Timestamp,
			&description, &reference, &source,
		)
		if err != nil {
			return fmt.Errorf("failed to scan transaction log entry in batch: %w", err)
		}

		// Convert cents to decimal and create Money (supports both currency and non-currency codes)
		amount := centsToDecimal(amountCents)
		money, err := domain.NewMoneyFromInstrumentCode(amount, currency)
		if err != nil {
			return fmt.Errorf("failed to create Money value for instrument %q in batch: %w", currency, err)
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

		// Append to the appropriate log
		if log, ok := dbIDToLog[financialPosLogID]; ok {
			log.TransactionLogEntries = append(log.TransactionLogEntries, &entry)
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating transaction log entries batch: %w", err)
	}

	return nil
}

// loadTransactionLineageBatchTx loads transaction lineages for multiple logs in a single query.
// This avoids N+1 query issues when loading many logs.
func (r *PostgresRepository) loadTransactionLineageBatchTx(ctx context.Context, tx pgx.Tx, dbIDs []uuid.UUID, dbIDToLog map[uuid.UUID]*domain.FinancialPositionLog) error {
	if len(dbIDs) == 0 {
		return nil
	}

	// Build IN clause with placeholders
	placeholders := make([]string, len(dbIDs))
	args := make([]any, len(dbIDs))
	for i, id := range dbIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

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
		var transactionID uuid.UUID
		var transactionType string
		var parentID sql.NullString
		var childIDsJSON, relatedIDsJSON []byte

		err := rows.Scan(
			&financialPosLogID,
			&transactionID, &parentID,
			&childIDsJSON, &relatedIDsJSON, &transactionType,
		)
		if err != nil {
			return fmt.Errorf("failed to scan transaction lineage in batch: %w", err)
		}

		var parent *uuid.UUID
		if parentID.Valid {
			pid, err := uuid.Parse(parentID.String)
			if err != nil {
				return fmt.Errorf("failed to parse parent transaction ID in batch: %w", err)
			}
			parent = &pid
		}

		var childIDs []uuid.UUID
		if err := json.Unmarshal(childIDsJSON, &childIDs); err != nil {
			return fmt.Errorf("failed to unmarshal child transaction IDs in batch: %w", err)
		}

		var relatedIDs []uuid.UUID
		if err := json.Unmarshal(relatedIDsJSON, &relatedIDs); err != nil {
			return fmt.Errorf("failed to unmarshal related transaction IDs in batch: %w", err)
		}

		// Create the immutable TransactionLineage
		lineage, err := domain.NewTransactionLineage(transactionID, transactionType, parent, childIDs, relatedIDs)
		if err != nil {
			return fmt.Errorf("failed to create transaction lineage in batch: %w", err)
		}

		// Assign to the appropriate log
		if log, ok := dbIDToLog[financialPosLogID]; ok {
			log.TransactionLineage = lineage
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating transaction lineage batch: %w", err)
	}

	return nil
}

// loadAuditTrailEntriesBatchTx loads audit trail entries for multiple logs in a single query.
// This avoids N+1 query issues when loading many logs.
func (r *PostgresRepository) loadAuditTrailEntriesBatchTx(ctx context.Context, tx pgx.Tx, dbIDs []uuid.UUID, dbIDToLog map[uuid.UUID]*domain.FinancialPositionLog) error {
	if len(dbIDs) == 0 {
		return nil
	}

	// Build IN clause with placeholders
	placeholders := make([]string, len(dbIDs))
	args := make([]any, len(dbIDs))
	for i, id := range dbIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

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
		var entry domain.AuditTrailEntry
		var details, ipAddress sql.NullString
		var sysContext []byte

		err := rows.Scan(
			&financialPosLogID,
			&entry.AuditID, &entry.Timestamp, &entry.UserID, &entry.Action,
			&details, &ipAddress, &sysContext,
		)
		if err != nil {
			return fmt.Errorf("failed to scan audit trail entry in batch: %w", err)
		}

		if details.Valid {
			entry.Details = details.String
		}
		if ipAddress.Valid {
			entry.IPAddress = ipAddress.String
		}

		if len(sysContext) > 0 {
			if err := json.Unmarshal(sysContext, &entry.SystemContext); err != nil {
				return fmt.Errorf("failed to unmarshal system context in batch: %w", err)
			}
		}

		// Append to the appropriate log
		if log, ok := dbIDToLog[financialPosLogID]; ok {
			log.AuditTrail = append(log.AuditTrail, &entry)
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating audit trail entries batch: %w", err)
	}

	return nil
}
