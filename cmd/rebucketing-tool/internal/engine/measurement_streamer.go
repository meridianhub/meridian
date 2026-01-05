package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// MeasurementStreamer provides paginated access to measurements for rebucketing.
// It streams measurements in batches to avoid loading all data into memory.
type MeasurementStreamer struct {
	pool *pgxpool.Pool
}

// NewMeasurementStreamer creates a new measurement streamer with the given connection pool.
func NewMeasurementStreamer(pool *pgxpool.Pool) *MeasurementStreamer {
	return &MeasurementStreamer{pool: pool}
}

// StreamMeasurements streams measurements in batches, calling the handler for each batch.
// The streaming stops when the handler returns false or an error, or when all measurements
// have been processed.
//
// Parameters:
//   - ctx: Context for cancellation support
//   - config: Streaming configuration (batch size, filters)
//   - handler: Callback function invoked for each batch of measurements
//
// Returns ErrMeasurementStream on query errors.
func (s *MeasurementStreamer) StreamMeasurements(
	ctx context.Context,
	config StreamConfig,
	handler func(batch []MeasurementRecord, progress Progress) (continueStreaming bool, err error),
) error {
	if config.BatchSize <= 0 {
		config.BatchSize = DefaultBatchSize
	}

	// Get total count first for progress tracking
	total, err := s.countMeasurements(ctx, config)
	if err != nil {
		return fmt.Errorf("%w: failed to count measurements: %s", ErrMeasurementStream, err.Error())
	}

	if total == 0 {
		return ErrNoMeasurementsFound
	}

	totalBatches := int((total + int64(config.BatchSize) - 1) / int64(config.BatchSize))
	var processed int64
	var lastID uuid.UUID // For keyset pagination

	for batch := 1; batch <= totalBatches; batch++ {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Fetch next batch
		records, err := s.fetchBatch(ctx, config, lastID)
		if err != nil {
			return fmt.Errorf("%w: failed to fetch batch %d: %s", ErrMeasurementStream, batch, err.Error())
		}

		if len(records) == 0 {
			break
		}

		// Update lastID for keyset pagination
		lastID = records[len(records)-1].ID
		processed += int64(len(records))

		// Calculate rate (simplified - could be more sophisticated with time tracking)
		progress := Progress{
			Processed:    processed,
			Total:        total,
			CurrentBatch: batch,
			TotalBatches: totalBatches,
			Rate:         0, // Rate calculation would require timing
		}

		// Call handler
		continueStreaming, err := handler(records, progress)
		if err != nil {
			return err
		}
		if !continueStreaming {
			break
		}
	}

	return nil
}

// countMeasurements returns the total count of measurements matching the config.
func (s *MeasurementStreamer) countMeasurements(ctx context.Context, config StreamConfig) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := s.setSearchPath(ctx, tx); err != nil {
		return 0, err
	}

	query := `
		SELECT COUNT(*)
		FROM measurement m
		WHERE m.deleted_at IS NULL`

	args := make([]any, 0)
	argNum := 1

	if config.OldBucketIDFilter != "" {
		query += fmt.Sprintf(" AND m.bucket_id = $%d", argNum)
		args = append(args, config.OldBucketIDFilter)
	}

	// Note: InstrumentCode filtering would require joining with financial_position_log
	// and then to a position or instrument table. For now, bucket_id filtering is primary.

	var count int64
	err = tx.QueryRow(ctx, query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count query: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}

	return count, nil
}

// fetchBatch fetches a batch of measurements using keyset pagination.
func (s *MeasurementStreamer) fetchBatch(ctx context.Context, config StreamConfig, afterID uuid.UUID) ([]MeasurementRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := s.setSearchPath(ctx, tx); err != nil {
		return nil, err
	}

	// Build query with keyset pagination
	query := `
		SELECT m.id, fpl.log_id, m.bucket_id, m.metadata
		FROM measurement m
		JOIN financial_position_log fpl ON m.financial_position_log_id = fpl.id
		WHERE m.deleted_at IS NULL`

	args := make([]any, 0)
	argNum := 1

	// Keyset pagination - fetch records after the last ID
	if afterID != uuid.Nil {
		query += fmt.Sprintf(" AND m.id > $%d", argNum)
		args = append(args, afterID)
		argNum++
	}

	if config.OldBucketIDFilter != "" {
		query += fmt.Sprintf(" AND m.bucket_id = $%d", argNum)
		args = append(args, config.OldBucketIDFilter)
		argNum++
	}

	query += fmt.Sprintf(" ORDER BY m.id ASC LIMIT $%d", argNum)
	args = append(args, config.BatchSize)

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query measurements: %w", err)
	}
	defer rows.Close()

	var records []MeasurementRecord
	for rows.Next() {
		var record MeasurementRecord
		var bucketID sql.NullString
		var metadataJSON sql.NullString

		err := rows.Scan(
			&record.ID,
			&record.FinancialPositionLogID,
			&bucketID,
			&metadataJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		if bucketID.Valid {
			record.CurrentBucketID = bucketID.String
		}

		if metadataJSON.Valid && metadataJSON.String != "" {
			if err := json.Unmarshal([]byte(metadataJSON.String), &record.Metadata); err != nil {
				// Log but continue - metadata parsing errors shouldn't stop the stream
				record.Metadata = make(map[string]string)
			}
		} else {
			record.Metadata = make(map[string]string)
		}

		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return records, nil
}

// CountByBucketID returns the count of measurements grouped by bucket ID.
// This is useful for understanding the distribution before rebucketing.
func (s *MeasurementStreamer) CountByBucketID(ctx context.Context) (map[string]int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := s.setSearchPath(ctx, tx); err != nil {
		return nil, err
	}

	query := `
		SELECT COALESCE(bucket_id, '') as bucket_id, COUNT(*) as count
		FROM measurement
		WHERE deleted_at IS NULL
		GROUP BY bucket_id
		ORDER BY count DESC`

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query bucket counts: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var bucketID string
		var count int64
		if err := rows.Scan(&bucketID, &count); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		result[bucketID] = count
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return result, nil
}

// setSearchPath sets the PostgreSQL search_path for the transaction.
func (s *MeasurementStreamer) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}

	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName)
	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("set tenant schema scope: %w", err)
	}

	return nil
}
