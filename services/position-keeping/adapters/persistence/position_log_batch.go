package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
)

// CreateBatch persists multiple FinancialPositionLog aggregates atomically using efficient bulk operations.
// If any log fails to persist, the entire batch is rolled back.
// In multi-tenant mode, the context must contain the tenant ID for schema routing.
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

	// Set tenant scope if in multi-tenant mode
	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}

	// Use COPY for bulk insert of financial_position_log
	userID := audit.GetUserFromContext(ctx)
	copyCount, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"financial_position_log"},
		[]string{
			"id", "created_at", "created_by", "updated_at", "updated_by",
			"log_id", "account_id", "account_service_domain", "version",
			"current_status", "previous_status", "status_updated_at", "status_reason", "failure_reason",
			"reconciliation_status",
			"opening_balance_amount", "opening_balance_currency", "opening_balance_recorded_at",
		},
		pgx.CopyFromSlice(len(logs), func(i int) ([]any, error) {
			log := logs[i]
			return []any{
				uuid.New(), // Generate new DB ID
				log.CreatedAt, userID, log.UpdatedAt, userID,
				log.LogID, log.AccountID, log.AccountServiceDomain, log.Version,
				log.StatusTracking.CurrentStatus.String(), nullString(log.StatusTracking.PreviousStatus),
				log.StatusTracking.StatusUpdatedAt, log.StatusTracking.StatusReason,
				nullStringValue(log.StatusTracking.FailureReason),
				log.StatusTracking.ReconciliationStatus.String(),
				log.OpeningBalance.Amount, openingBalanceCurrencyCode(log.OpeningBalance), nullTime(log.OpeningBalanceRecordedAt),
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
		FROM financial_position_log
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

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating log ID mappings: %w", err)
	}

	return idMap, nil
}
