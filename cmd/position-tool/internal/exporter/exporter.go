// Package exporter provides position export functionality for the position-tool CLI.
// It supports streaming export of positions to CSV format with optional filters.
package exporter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/infra"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/shopspring/decimal"
)

// Error types for export operations.
var (
	// ErrNoPositions is returned when no positions match the export criteria.
	ErrNoPositions = errors.New("no positions found matching criteria")

	// ErrExportInterrupted is returned when export is cancelled before completion.
	ErrExportInterrupted = errors.New("export interrupted")

	// ErrNilPool is returned when a nil database pool is provided.
	ErrNilPool = errors.New("database pool cannot be nil")
)

// ExportOptions configures the export operation.
type ExportOptions struct {
	// OutputPath is the file path for the CSV output (required).
	OutputPath string

	// TenantID is the tenant identifier (required).
	TenantID string

	// InstrumentCode filters by instrument code (optional).
	InstrumentCode string

	// AccountID filters by account ID (optional).
	AccountID string

	// FromTime filters positions created after this time (optional).
	FromTime *time.Time

	// ToTime filters positions created before this time (optional).
	ToTime *time.Time

	// BatchSize controls the number of rows fetched per database query (default: 10000).
	BatchSize int

	// DryRun performs a count-only operation without writing the file.
	DryRun bool
}

// DefaultBatchSize is the default number of rows per database query.
const DefaultBatchSize = 10000

// ExportResult contains the results of an export operation.
type ExportResult struct {
	// TotalRows is the number of rows exported.
	TotalRows int64

	// OutputFile is the path to the exported file.
	OutputFile string

	// FileSizeBytes is the size of the exported file in bytes.
	FileSizeBytes int64

	// Interrupted indicates if the export was cancelled.
	Interrupted bool

	// InterruptedRow is the row number where export stopped (if interrupted).
	InterruptedRow int64

	// AttributeKeys are the attribute keys found across all positions.
	AttributeKeys []string
}

// PositionRow represents a single position for export.
type PositionRow struct {
	AccountID      string
	InstrumentCode string
	Amount         decimal.Decimal
	BucketKey      string
	Dimension      string
	Attributes     map[string]string
	CreatedAt      time.Time
	ReferenceID    uuid.UUID
}

// Exporter handles position export operations.
type Exporter struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// New creates a new Exporter with the given database pool.
func New(pool *pgxpool.Pool, logger *slog.Logger) (*Exporter, error) {
	if pool == nil {
		return nil, ErrNilPool
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Exporter{
		pool:   pool,
		logger: logger,
	}, nil
}

// Export streams positions from the database to a CSV file.
// It supports filtering by instrument, account, and date range.
// Progress events are sent to the provided tracker.
func (e *Exporter) Export(ctx context.Context, opts ExportOptions, tracker *infra.ProgressTracker) (*ExportResult, error) {
	// Normalize options
	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultBatchSize
	}

	// Set up tenant context
	tenantCtx, err := e.withTenantContext(ctx, opts.TenantID)
	if err != nil {
		return nil, fmt.Errorf("setting tenant context: %w", err)
	}

	// First, get count for progress tracking
	totalCount, err := e.countPositions(tenantCtx, opts)
	if err != nil {
		return nil, fmt.Errorf("counting positions: %w", err)
	}

	if totalCount == 0 {
		return &ExportResult{
			TotalRows:  0,
			OutputFile: opts.OutputPath,
		}, nil
	}

	// Signal start
	if tracker != nil {
		tracker.Start(ctx, fmt.Sprintf("Exporting %d positions", totalCount))
	}

	// Dry run - just return the count
	if opts.DryRun {
		if tracker != nil {
			tracker.Complete(ctx, "Dry-run complete")
		}
		return &ExportResult{
			TotalRows:  totalCount,
			OutputFile: opts.OutputPath,
		}, nil
	}

	// Discover attribute keys by sampling first batch
	attrKeys, err := e.discoverAttributeKeys(tenantCtx, opts)
	if err != nil {
		return nil, fmt.Errorf("discovering attribute keys: %w", err)
	}

	// Create CSV writer
	writer, err := NewCSVWriter(opts.OutputPath, attrKeys)
	if err != nil {
		return nil, fmt.Errorf("creating CSV writer: %w", err)
	}

	// Stream positions and write to CSV
	result, err := e.executeExport(ctx, tenantCtx, opts, writer, tracker, totalCount, attrKeys)

	// Ensure writer is closed
	if closeErr := writer.Close(); closeErr != nil && err == nil {
		return nil, fmt.Errorf("closing CSV writer: %w", closeErr)
	}

	return result, err
}

// executeExport handles the core export streaming logic.
func (e *Exporter) executeExport(
	ctx context.Context,
	tenantCtx context.Context,
	opts ExportOptions,
	writer *CSVWriter,
	tracker *infra.ProgressTracker,
	totalCount int64,
	attrKeys []string,
) (*ExportResult, error) {
	var exported int64
	var interrupted bool

	err := e.streamPositions(tenantCtx, opts, func(positions []PositionRow) error {
		// Check for cancellation
		select {
		case <-ctx.Done():
			interrupted = true
			return ctx.Err()
		default:
		}

		// Write batch to CSV
		for _, pos := range positions {
			if writeErr := writer.WriteRow(pos); writeErr != nil {
				return fmt.Errorf("writing row: %w", writeErr)
			}
			exported++
		}

		// Report progress
		if tracker != nil {
			tracker.BatchComplete(ctx, len(positions), fmt.Sprintf("Exported %d/%d rows", exported, totalCount))
		}

		return nil
	})

	// Handle interruption
	if interrupted || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if tracker != nil {
			tracker.Complete(ctx, fmt.Sprintf("Export interrupted at row %d", exported))
		}
		return &ExportResult{
			TotalRows:      exported,
			OutputFile:     opts.OutputPath,
			FileSizeBytes:  writer.BytesWritten(),
			Interrupted:    true,
			InterruptedRow: exported,
			AttributeKeys:  attrKeys,
		}, ErrExportInterrupted
	}

	if err != nil {
		if tracker != nil {
			tracker.Error(ctx, err, "Export failed")
		}
		return nil, err
	}

	// Signal completion
	if tracker != nil {
		tracker.Complete(ctx, fmt.Sprintf("Exported %d positions", exported))
	}

	return &ExportResult{
		TotalRows:     exported,
		OutputFile:    opts.OutputPath,
		FileSizeBytes: writer.BytesWritten(),
		AttributeKeys: attrKeys,
	}, nil
}

// withTenantContext adds tenant context to the context.
func (e *Exporter) withTenantContext(ctx context.Context, tenantID string) (context.Context, error) {
	tid, err := tenant.NewTenantID(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant ID %q: %w", tenantID, err)
	}
	return tenant.WithTenant(ctx, tid), nil
}

// countPositions returns the total count of positions matching the filter.
func (e *Exporter) countPositions(ctx context.Context, opts ExportOptions) (int64, error) {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := e.setSearchPath(ctx, tx); err != nil {
		return 0, err
	}

	query, args := e.buildCountQuery(opts)
	var count int64
	if err := tx.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("executing count query: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit transaction: %w", err)
	}

	return count, nil
}

// discoverAttributeKeys samples positions to find all unique attribute keys.
// It respects the same filters as the export to ensure attribute columns match the exported data.
func (e *Exporter) discoverAttributeKeys(ctx context.Context, opts ExportOptions) ([]string, error) {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := e.setSearchPath(ctx, tx); err != nil {
		return nil, err
	}

	// Build query with same filters as export to discover only relevant attribute keys
	query := `
		SELECT DISTINCT jsonb_object_keys(attributes)
		FROM (
			SELECT attributes
			FROM position
			WHERE deleted_at IS NULL
				AND attributes IS NOT NULL
				AND attributes != '{}'::jsonb`

	var args []any
	argNum := 1

	if opts.InstrumentCode != "" {
		query += fmt.Sprintf(" AND instrument_code = $%d", argNum)
		args = append(args, opts.InstrumentCode)
		argNum++
	}

	if opts.AccountID != "" {
		query += fmt.Sprintf(" AND account_id = $%d", argNum)
		args = append(args, opts.AccountID)
		argNum++
	}

	if opts.FromTime != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", argNum)
		args = append(args, *opts.FromTime)
		argNum++
	}

	if opts.ToTime != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", argNum)
		args = append(args, *opts.ToTime)
	}

	query += " LIMIT 1000) subq"

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying attribute keys: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scanning attribute key: %w", err)
		}
		keys = append(keys, key)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating attribute keys: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return keys, nil
}

// streamPositions streams positions in batches using cursor-based pagination.
func (e *Exporter) streamPositions(ctx context.Context, opts ExportOptions, handler func([]PositionRow) error) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := e.setSearchPath(ctx, tx); err != nil {
		return err
	}

	// Use server-side cursor for memory efficiency
	cursorName := "export_cursor"
	query, args := e.buildSelectQuery(opts)

	// Declare cursor
	_, err = tx.Exec(ctx, fmt.Sprintf("DECLARE %s CURSOR FOR %s", cursorName, query), args...)
	if err != nil {
		return fmt.Errorf("declaring cursor: %w", err)
	}

	// Fetch in batches
	fetchQuery := fmt.Sprintf("FETCH %d FROM %s", opts.BatchSize, cursorName)

	for {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rows, err := tx.Query(ctx, fetchQuery)
		if err != nil {
			return fmt.Errorf("fetching from cursor: %w", err)
		}

		positions, err := e.scanPositions(rows)
		rows.Close()
		if err != nil {
			return fmt.Errorf("scanning positions: %w", err)
		}

		if len(positions) == 0 {
			break // No more rows
		}

		if err := handler(positions); err != nil {
			return err
		}
	}

	// Close cursor and commit
	_, _ = tx.Exec(ctx, fmt.Sprintf("CLOSE %s", cursorName))

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// buildCountQuery builds the COUNT query with filters.
func (e *Exporter) buildCountQuery(opts ExportOptions) (string, []any) {
	query := "SELECT COUNT(*) FROM position WHERE deleted_at IS NULL"
	var args []any
	argNum := 1

	if opts.InstrumentCode != "" {
		query += fmt.Sprintf(" AND instrument_code = $%d", argNum)
		args = append(args, opts.InstrumentCode)
		argNum++
	}

	if opts.AccountID != "" {
		query += fmt.Sprintf(" AND account_id = $%d", argNum)
		args = append(args, opts.AccountID)
		argNum++
	}

	if opts.FromTime != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", argNum)
		args = append(args, *opts.FromTime)
		argNum++
	}

	if opts.ToTime != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", argNum)
		args = append(args, *opts.ToTime)
	}

	return query, args
}

// buildSelectQuery builds the SELECT query with filters and ordering.
func (e *Exporter) buildSelectQuery(opts ExportOptions) (string, []any) {
	query := `
		SELECT id, account_id, instrument_code, bucket_key, amount, dimension,
			attributes, reference_id, created_at
		FROM position
		WHERE deleted_at IS NULL`

	var args []any
	argNum := 1

	if opts.InstrumentCode != "" {
		query += fmt.Sprintf(" AND instrument_code = $%d", argNum)
		args = append(args, opts.InstrumentCode)
		argNum++
	}

	if opts.AccountID != "" {
		query += fmt.Sprintf(" AND account_id = $%d", argNum)
		args = append(args, opts.AccountID)
		argNum++
	}

	if opts.FromTime != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", argNum)
		args = append(args, *opts.FromTime)
		argNum++
	}

	if opts.ToTime != nil {
		query += fmt.Sprintf(" AND created_at <= $%d", argNum)
		args = append(args, *opts.ToTime)
	}

	// Order by created_at for deterministic output
	query += " ORDER BY created_at ASC, id ASC"

	return query, args
}

// scanPositions scans query results into PositionRow structs.
func (e *Exporter) scanPositions(rows pgx.Rows) ([]PositionRow, error) {
	var positions []PositionRow

	for rows.Next() {
		var (
			id             uuid.UUID
			accountID      string
			instrumentCode string
			bucketKey      string
			amount         decimal.Decimal
			dimension      string
			attributesJSON sql.NullString
			referenceID    sql.NullString
			createdAt      time.Time
		)

		err := rows.Scan(
			&id, &accountID, &instrumentCode, &bucketKey, &amount, &dimension,
			&attributesJSON, &referenceID, &createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		pos := PositionRow{
			AccountID:      accountID,
			InstrumentCode: instrumentCode,
			Amount:         amount,
			BucketKey:      bucketKey,
			Dimension:      dimension,
			CreatedAt:      createdAt,
		}

		// Parse attributes JSON
		if attributesJSON.Valid && attributesJSON.String != "" {
			if err := json.Unmarshal([]byte(attributesJSON.String), &pos.Attributes); err != nil {
				e.logger.Warn("failed to unmarshal attributes",
					"id", id,
					"error", err,
				)
				pos.Attributes = make(map[string]string)
			}
		}

		// Parse reference ID
		if referenceID.Valid {
			pos.ReferenceID, _ = uuid.Parse(referenceID.String)
		}

		positions = append(positions, pos)
	}

	return positions, rows.Err()
}

// setSearchPath sets the PostgreSQL search_path for the transaction.
func (e *Exporter) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil // Single-tenant mode
	}

	schemaName := pgx.Identifier{tenantID.SchemaName()}.Sanitize()
	query := fmt.Sprintf("SET LOCAL search_path TO %s", schemaName)

	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	return nil
}
