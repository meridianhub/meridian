package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// MeasurementRepository implements domain.MeasurementRepository using PostgreSQL.
type MeasurementRepository struct {
	pool *pgxpool.Pool
}

// ErrNilMeasurement is returned when a nil measurement is provided to repository methods.
var ErrNilMeasurement = errors.New("measurement cannot be nil")

// NewMeasurementRepository creates a new PostgreSQL measurement repository with the given connection pool.
func NewMeasurementRepository(pool *pgxpool.Pool) *MeasurementRepository {
	return &MeasurementRepository{pool: pool}
}

// setSearchPath sets the PostgreSQL search_path for the transaction.
// In multi-tenant mode, it sets the search_path to the tenant's schema.
// In single-tenant mode (no tenant context), it does nothing.
func (r *MeasurementRepository) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil
	}

	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName)
	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}

	return nil
}

// Create persists a new Measurement to the database.
// Returns domain.ErrConflict if a measurement with the same ID already exists.
func (r *MeasurementRepository) Create(ctx context.Context, measurement *domain.Measurement) error {
	if measurement == nil {
		return ErrNilMeasurement
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

	// Find the database ID (primary key) for the financial_position_log_id
	dbPositionLogID, err := r.lookupDBPositionLogID(ctx, tx, measurement.FinancialPositionLogID)
	if err != nil {
		return err
	}

	if err := r.CreateWithTx(ctx, tx, measurement, dbPositionLogID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// lookupDBPositionLogID finds the database primary key for a given domain log_id within a transaction.
func (r *MeasurementRepository) lookupDBPositionLogID(ctx context.Context, tx pgx.Tx, logID uuid.UUID) (uuid.UUID, error) {
	var dbID uuid.UUID
	err := tx.QueryRow(ctx,
		"SELECT id FROM financial_position_log WHERE log_id = $1 AND deleted_at IS NULL",
		logID,
	).Scan(&dbID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, domain.ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("failed to find financial position log: %w", err)
	}
	return dbID, nil
}

// FindByID retrieves a Measurement by its ID.
// Returns domain.ErrNotFound if the measurement doesn't exist.
func (r *MeasurementRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Measurement, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return nil, err
	}

	query := `
		SELECT m.id, fpl.log_id, m.measurement_type, m.value, m.unit, m.timestamp, m.metadata, m.bucket_id,
			m.created_at, m.created_by, m.updated_at, m.updated_by
		FROM measurement m
		JOIN financial_position_log fpl ON m.financial_position_log_id = fpl.id
		WHERE m.id = $1 AND m.deleted_at IS NULL`

	measurement, err := r.scanMeasurementRow(tx.QueryRow(ctx, query, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to query measurement: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return measurement, nil
}

// scanMeasurementRow scans a single measurement row and applies post-scan parsing.
func (r *MeasurementRepository) scanMeasurementRow(row pgx.Row) (*domain.Measurement, error) {
	var measurement domain.Measurement
	var measurementType string
	var metadataJSON sql.NullString
	var bucketID sql.NullString

	err := row.Scan(
		&measurement.ID,
		&measurement.FinancialPositionLogID,
		&measurementType,
		&measurement.Value,
		&measurement.Unit,
		&measurement.Timestamp,
		&metadataJSON,
		&bucketID,
		&measurement.CreatedAt,
		&measurement.CreatedBy,
		&measurement.UpdatedAt,
		&measurement.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	measurement.MeasurementType = domain.ParseMeasurementType(measurementType)

	if metadataJSON.Valid && metadataJSON.String != "" {
		if err := json.Unmarshal([]byte(metadataJSON.String), &measurement.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	if bucketID.Valid {
		measurement.BucketID = bucketID.String
	}

	return &measurement, nil
}

// FindByPositionLogID retrieves all Measurements for a specific financial position log.
// Returns an empty slice if no measurements exist for the log.
func (r *MeasurementRepository) FindByPositionLogID(ctx context.Context, positionLogID uuid.UUID) ([]*domain.Measurement, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return nil, err
	}

	// First find the db ID from the log_id
	var dbPositionLogID uuid.UUID
	err = tx.QueryRow(ctx,
		"SELECT id FROM financial_position_log WHERE log_id = $1 AND deleted_at IS NULL",
		positionLogID,
	).Scan(&dbPositionLogID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []*domain.Measurement{}, nil
		}
		return nil, fmt.Errorf("failed to find financial position log: %w", err)
	}

	query := `
		SELECT m.id, $1::uuid as log_id, m.measurement_type, m.value, m.unit, m.timestamp, m.metadata, m.bucket_id,
			m.created_at, m.created_by, m.updated_at, m.updated_by
		FROM measurement m
		WHERE m.financial_position_log_id = $2 AND m.deleted_at IS NULL
		ORDER BY m.timestamp DESC`

	rows, err := tx.Query(ctx, query, positionLogID, dbPositionLogID)
	if err != nil {
		return nil, fmt.Errorf("failed to query measurements: %w", err)
	}
	defer rows.Close()

	measurements, err := r.scanMeasurementRows(rows)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return measurements, nil
}

// scanMeasurementRows scans multiple measurement rows from a query result.
func (r *MeasurementRepository) scanMeasurementRows(rows pgx.Rows) ([]*domain.Measurement, error) {
	var measurements []*domain.Measurement
	for rows.Next() {
		measurement, err := r.scanMeasurementRow(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan measurement: %w", err)
		}
		measurements = append(measurements, measurement)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating measurements: %w", err)
	}

	return measurements, nil
}

// CreateWithTx persists a new Measurement to the database within an existing transaction.
// This is useful for transactional operations where measurement creation
// should be atomic with other database operations.
func (r *MeasurementRepository) CreateWithTx(ctx context.Context, tx pgx.Tx, measurement *domain.Measurement, dbPositionLogID uuid.UUID) error {
	if measurement == nil {
		return ErrNilMeasurement
	}

	// Marshal metadata to JSON
	var metadataJSON []byte
	var err error
	if measurement.Metadata != nil {
		metadataJSON, err = json.Marshal(measurement.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}
	}

	userID := audit.GetUserFromContext(ctx)

	query := `
		INSERT INTO measurement (
			id, created_at, created_by, updated_at, updated_by,
			financial_position_log_id, measurement_type, value, unit, timestamp, metadata, bucket_id
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11, $12
		)`

	// Convert empty bucket_id to NULL for database storage
	var bucketID *string
	if measurement.BucketID != "" {
		bucketID = &measurement.BucketID
	}

	_, err = tx.Exec(ctx, query,
		measurement.ID, measurement.CreatedAt, userID, measurement.UpdatedAt, userID,
		dbPositionLogID, measurement.MeasurementType.String(), measurement.Value,
		measurement.Unit, measurement.Timestamp, metadataJSON, bucketID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505": // unique_violation
				return domain.ErrConflict
			case "23503": // foreign_key_violation
				return domain.ErrNotFound
			}
		}
		return fmt.Errorf("failed to insert measurement: %w", err)
	}

	return nil
}

// GetDBPositionLogID retrieves the database ID for a given log_id within a transaction.
// This is useful for transactional operations.
func (r *MeasurementRepository) GetDBPositionLogID(ctx context.Context, tx pgx.Tx, logID uuid.UUID) (uuid.UUID, error) {
	var dbID uuid.UUID
	err := tx.QueryRow(ctx,
		"SELECT id FROM financial_position_log WHERE log_id = $1 AND deleted_at IS NULL",
		logID,
	).Scan(&dbID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, domain.ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("failed to find financial position log: %w", err)
	}
	return dbID, nil
}

// BeginTx starts a new transaction with tenant scoping.
func (r *MeasurementRepository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	if err := r.setSearchPath(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}

	return tx, nil
}

// Ensure MeasurementRepository implements the interface
var _ interface {
	Create(ctx context.Context, measurement *domain.Measurement) error
	FindByID(ctx context.Context, id uuid.UUID) (*domain.Measurement, error)
	FindByPositionLogID(ctx context.Context, positionLogID uuid.UUID) ([]*domain.Measurement, error)
} = (*MeasurementRepository)(nil)
