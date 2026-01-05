package executor

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AuditLogger handles writing rebucketing audit log entries to the database.
// It logs every position affected during rebucketing operations.
type AuditLogger struct{}

// NewAuditLogger creates a new audit logger.
func NewAuditLogger() *AuditLogger {
	return &AuditLogger{}
}

// LogSoftDelete logs a SOFT_DELETE operation for a position being marked as deleted.
func (l *AuditLogger) LogSoftDelete(
	ctx context.Context,
	tx pgx.Tx,
	adminUserID string,
	oldVersion string,
	newVersion string,
	positionID uuid.UUID,
	oldBucketKey string,
	newBucketKey string,
) error {
	return l.logEntry(ctx, tx, adminUserID, oldVersion, newVersion, positionID, oldBucketKey, newBucketKey, AuditOperationSoftDelete)
}

// LogInsertNew logs an INSERT_NEW operation for a new position being created.
func (l *AuditLogger) LogInsertNew(
	ctx context.Context,
	tx pgx.Tx,
	adminUserID string,
	oldVersion string,
	newVersion string,
	positionID uuid.UUID,
	oldBucketKey string,
	newBucketKey string,
) error {
	return l.logEntry(ctx, tx, adminUserID, oldVersion, newVersion, positionID, oldBucketKey, newBucketKey, AuditOperationInsertNew)
}

// logEntry writes a single audit log entry to the database.
func (l *AuditLogger) logEntry(
	ctx context.Context,
	tx pgx.Tx,
	adminUserID string,
	oldVersion string,
	newVersion string,
	positionID uuid.UUID,
	oldBucketKey string,
	newBucketKey string,
	operation AuditOperation,
) error {
	query := `
		INSERT INTO rebucketing_audit_log (
			id, timestamp, admin_user_id,
			old_instrument_version, new_instrument_version,
			position_id, old_bucket_id, new_bucket_id, operation
		) VALUES (
			$1, $2, $3,
			$4, $5,
			$6, $7, $8, $9
		)`

	_, err := tx.Exec(ctx, query,
		uuid.New(),
		time.Now().UTC(),
		adminUserID,
		oldVersion,
		newVersion,
		positionID,
		oldBucketKey,
		newBucketKey,
		operation.String(),
	)
	if err != nil {
		return &AuditLogWriteError{
			Cause:      err,
			PositionID: positionID.String(),
			Operation:  operation.String(),
		}
	}

	return nil
}

// LogBatch logs multiple audit entries efficiently using batch operations.
// Each entry creates TWO audit log records (SOFT_DELETE + INSERT_NEW).
func (l *AuditLogger) LogBatch(
	ctx context.Context,
	tx pgx.Tx,
	adminUserID string,
	oldVersion string,
	newVersion string,
	positions []AffectedPosition,
) error {
	if len(positions) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	now := time.Now().UTC()

	query := `
		INSERT INTO rebucketing_audit_log (
			id, timestamp, admin_user_id,
			old_instrument_version, new_instrument_version,
			position_id, old_bucket_id, new_bucket_id, operation
		) VALUES (
			$1, $2, $3,
			$4, $5,
			$6, $7, $8, $9
		)`

	for _, pos := range positions {
		// Log SOFT_DELETE for old position
		batch.Queue(query,
			uuid.New(),
			now,
			adminUserID,
			oldVersion,
			newVersion,
			pos.PositionID,
			pos.OldBucketKey,
			pos.NewBucketKey,
			AuditOperationSoftDelete.String(),
		)

		// Log INSERT_NEW for new position
		batch.Queue(query,
			uuid.New(),
			now,
			adminUserID,
			oldVersion,
			newVersion,
			pos.PositionID, // Original position ID for traceability
			pos.OldBucketKey,
			pos.NewBucketKey,
			AuditOperationInsertNew.String(),
		)
	}

	br := tx.SendBatch(ctx, batch)
	defer func() {
		_ = br.Close()
	}()

	// Check results for each operation (2 per position)
	expectedOps := len(positions) * 2
	for i := 0; i < expectedOps; i++ {
		_, err := br.Exec()
		if err != nil {
			posIndex := i / 2
			var opType string
			if i%2 == 0 {
				opType = AuditOperationSoftDelete.String()
			} else {
				opType = AuditOperationInsertNew.String()
			}
			return &AuditLogWriteError{
				Cause:      err,
				PositionID: positions[posIndex].PositionID.String(),
				Operation:  opType,
			}
		}
	}

	return nil
}

// GetAuditHistory retrieves audit log entries for a specific position.
func (l *AuditLogger) GetAuditHistory(ctx context.Context, tx pgx.Tx, positionID uuid.UUID) ([]AuditLogEntry, error) {
	query := `
		SELECT id, timestamp, admin_user_id,
			old_instrument_version, new_instrument_version,
			position_id, old_bucket_id, new_bucket_id, operation
		FROM rebucketing_audit_log
		WHERE position_id = $1
		ORDER BY timestamp DESC`

	rows, err := tx.Query(ctx, query, positionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query audit history: %w", err)
	}
	defer rows.Close()

	var entries []AuditLogEntry
	for rows.Next() {
		var entry AuditLogEntry
		var operation string

		err := rows.Scan(
			&entry.ID,
			&entry.Timestamp,
			&entry.AdminUserID,
			&entry.OldInstrumentVersion,
			&entry.NewInstrumentVersion,
			&entry.PositionID,
			&entry.OldBucketID,
			&entry.NewBucketID,
			&operation,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan audit entry: %w", err)
		}

		entry.Operation = AuditOperation(operation)
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating audit entries: %w", err)
	}

	return entries, nil
}
