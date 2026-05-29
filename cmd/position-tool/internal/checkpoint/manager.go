// Package checkpoint provides checkpoint persistence for bulk import operations.
//
// The checkpoint manager tracks import progress using the import_manifest table,
// enabling resume capability for interrupted imports. It handles:
//
//   - File checksum calculation for duplicate import detection
//   - Progress tracking (processed rows, success/failure counts)
//   - Rollback SQL storage for atomic recovery
//   - Resume from last checkpoint
package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status represents the status of an import operation.
type Status string

const (
	// StatusRunning indicates the import is in progress.
	StatusRunning Status = "RUNNING"
	// StatusCompleted indicates the import completed successfully.
	StatusCompleted Status = "COMPLETED"
	// StatusFailed indicates the import failed with an error.
	StatusFailed Status = "FAILED"
	// StatusCancelled indicates the import was cancelled by the user.
	StatusCancelled Status = "CANCELLED"
)

// Checkpoint errors.
var (
	// ErrNilPool is returned when a nil connection pool is passed.
	ErrNilPool = errors.New("database connection pool cannot be nil")
	// ErrCheckpointNotFound is returned when no checkpoint exists for resume.
	ErrCheckpointNotFound = errors.New("no checkpoint found for the specified file")
	// ErrDuplicateImport is returned when attempting to import a file that has already been successfully imported.
	ErrDuplicateImport = errors.New("this file has already been successfully imported")
	// ErrImportInProgress is returned when an import is already running for the file.
	ErrImportInProgress = errors.New("an import is already in progress for this file")
	// ErrInvalidManifestID is returned when a manifest ID is invalid.
	ErrInvalidManifestID = errors.New("invalid manifest ID")
	// ErrFileNotFound is returned when the source file does not exist.
	ErrFileNotFound = errors.New("source file not found")
	// ErrChecksumMismatch is returned when file checksum doesn't match checkpoint.
	ErrChecksumMismatch = errors.New("file checksum mismatch - file has been modified since checkpoint")
	// ErrNilCheckpoint is returned when a nil checkpoint is passed to a method.
	ErrNilCheckpoint = errors.New("checkpoint cannot be nil")
)

// SQL query constants to avoid repetition.
const (
	updateManifestSQL = `
		UPDATE import_manifest
		SET status = $2,
			total_rows = $3,
			processed_rows = $4,
			success_count = $5,
			failure_count = $6,
			rollback_sql = $7,
			updated_at = NOW()
		WHERE id = $1
	`
)

// Checkpoint represents the state of an import operation.
type Checkpoint struct {
	// ManifestID is the unique identifier for this import operation.
	ManifestID uuid.UUID

	// TenantID is the tenant this import belongs to.
	TenantID string

	// SourceFile is the path to the source file being imported.
	SourceFile string

	// FileChecksum is the SHA256 checksum of the source file.
	FileChecksum string

	// TotalRows is the total number of rows in the import file (0 if unknown).
	TotalRows int

	// ProcessedRows is the number of rows processed so far.
	ProcessedRows int

	// SuccessCount is the number of rows successfully imported.
	SuccessCount int

	// FailureCount is the number of rows that failed to import.
	FailureCount int

	// Status is the current status of the import.
	Status Status

	// LastProcessedLine is the last line number processed (1-indexed, for resume).
	// This allows resumption from the exact line where import was interrupted.
	LastProcessedLine int

	// RollbackSQL contains SQL statements to undo the import (for partial rollback).
	// Typically DELETE statements with the IDs of imported records.
	RollbackSQL []string

	// ErrorMessage contains the error message if the import failed.
	ErrorMessage string

	// CreatedAt is when the import was initiated.
	CreatedAt time.Time

	// UpdatedAt is the last update timestamp.
	UpdatedAt time.Time
}

// ManagerInterface provides checkpoint persistence for import operations.
// Note: This interface was intentionally named ManagerInterface rather than
// CheckpointManager to avoid stuttering when used as checkpoint.ManagerInterface.
type ManagerInterface interface {
	// StartImport creates a new import manifest entry.
	// Returns ErrDuplicateImport if the file (same checksum) was already imported successfully.
	// Returns ErrImportInProgress if an import is already running for this file.
	StartImport(ctx context.Context, tenantID, sourceFile string) (*Checkpoint, error)

	// UpdateProgress updates the current checkpoint with progress information.
	UpdateProgress(ctx context.Context, checkpoint *Checkpoint) error

	// Complete marks the import as completed successfully.
	Complete(ctx context.Context, checkpoint *Checkpoint) error

	// Fail marks the import as failed with an error message.
	Fail(ctx context.Context, checkpoint *Checkpoint, err error) error

	// Cancel marks the import as cancelled.
	Cancel(ctx context.Context, checkpoint *Checkpoint) error

	// Resume finds the last checkpoint for a file to enable resumption.
	// Returns ErrCheckpointNotFound if no checkpoint exists.
	// Returns ErrChecksumMismatch if the file has been modified.
	Resume(ctx context.Context, tenantID, sourceFile string) (*Checkpoint, error)

	// ResumeByID finds a checkpoint by its manifest ID.
	// Returns ErrCheckpointNotFound if the checkpoint doesn't exist.
	ResumeByID(ctx context.Context, manifestID uuid.UUID) (*Checkpoint, error)

	// Rollback executes stored rollback SQL to undo a partial import.
	Rollback(ctx context.Context, checkpoint *Checkpoint) error

	// GetHistory returns the import history for a tenant.
	GetHistory(ctx context.Context, tenantID string, limit int) ([]*Checkpoint, error)
}

// PostgresManager implements ManagerInterface using PostgreSQL.
type PostgresManager struct {
	pool *pgxpool.Pool
}

// Compile-time check that PostgresManager implements ManagerInterface.
var _ ManagerInterface = (*PostgresManager)(nil)

// NewManager creates a new checkpoint manager with the given connection pool.
func NewManager(pool *pgxpool.Pool) (*PostgresManager, error) {
	if pool == nil {
		return nil, ErrNilPool
	}
	return &PostgresManager{pool: pool}, nil
}

// StartImport creates a new import manifest entry.
func (m *PostgresManager) StartImport(ctx context.Context, tenantID, sourceFile string) (*Checkpoint, error) {
	// Calculate file checksum
	checksum, err := calculateFileChecksum(sourceFile)
	if err != nil {
		return nil, err
	}

	// Check for existing imports with same file and checksum
	existing, err := m.findExistingImport(ctx, tenantID, sourceFile, checksum)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing imports: %w", err)
	}

	if existing != nil {
		switch existing.Status {
		case StatusCompleted:
			return nil, fmt.Errorf("%w: manifest_id=%s", ErrDuplicateImport, existing.ManifestID)
		case StatusRunning:
			return nil, fmt.Errorf("%w: manifest_id=%s", ErrImportInProgress, existing.ManifestID)
		case StatusFailed, StatusCancelled:
			// Allow re-import for failed/cancelled imports
		}
	}

	// Create new checkpoint
	checkpoint := &Checkpoint{
		ManifestID:   uuid.New(),
		TenantID:     tenantID,
		SourceFile:   sourceFile,
		FileChecksum: checksum,
		Status:       StatusRunning,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Insert into database. Use ON CONFLICT to handle re-import of failed/cancelled imports.
	// The unique constraint is on (tenant_id, source_file, file_checksum).
	query := `
		INSERT INTO import_manifest (
			id, tenant_id, source_file, file_checksum,
			total_rows, processed_rows, success_count, failure_count,
			status, rollback_sql, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (tenant_id, source_file, file_checksum)
		WHERE status IN ('FAILED', 'CANCELLED')
		DO UPDATE SET
			status = EXCLUDED.status,
			processed_rows = EXCLUDED.processed_rows,
			success_count = EXCLUDED.success_count,
			failure_count = EXCLUDED.failure_count,
			rollback_sql = EXCLUDED.rollback_sql,
			updated_at = EXCLUDED.updated_at
		RETURNING id
	`

	var returnedID uuid.UUID
	err = m.pool.QueryRow(ctx, query,
		checkpoint.ManifestID,
		checkpoint.TenantID,
		checkpoint.SourceFile,
		checkpoint.FileChecksum,
		nil, // total_rows initially unknown
		checkpoint.ProcessedRows,
		nil, // success_count initially unknown
		nil, // failure_count initially unknown
		string(checkpoint.Status),
		nil, // rollback_sql initially empty
		checkpoint.CreatedAt,
		checkpoint.UpdatedAt,
	).Scan(&returnedID)
	if err != nil {
		return nil, fmt.Errorf("failed to create checkpoint: %w", err)
	}

	// Update checkpoint ID if we reused an existing row
	checkpoint.ManifestID = returnedID

	return checkpoint, nil
}

// UpdateProgress updates the current checkpoint with progress information.
func (m *PostgresManager) UpdateProgress(ctx context.Context, checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return ErrNilCheckpoint
	}

	rollbackSQL := encodeRollbackSQL(checkpoint.RollbackSQL)

	query := `
		UPDATE import_manifest
		SET total_rows = $2,
			processed_rows = $3,
			success_count = $4,
			failure_count = $5,
			rollback_sql = $6,
			updated_at = NOW()
		WHERE id = $1
	`

	result, err := m.pool.Exec(ctx, query,
		checkpoint.ManifestID,
		nullableInt(checkpoint.TotalRows),
		checkpoint.ProcessedRows,
		nullableInt(checkpoint.SuccessCount),
		nullableInt(checkpoint.FailureCount),
		rollbackSQL,
	)
	if err != nil {
		return fmt.Errorf("failed to update checkpoint progress: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrCheckpointNotFound
	}

	return nil
}

// Complete marks the import as completed successfully.
func (m *PostgresManager) Complete(ctx context.Context, checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return ErrNilCheckpoint
	}

	checkpoint.Status = StatusCompleted
	rollbackSQL := encodeRollbackSQL(checkpoint.RollbackSQL)

	result, err := m.pool.Exec(ctx, updateManifestSQL,
		checkpoint.ManifestID,
		string(StatusCompleted),
		nullableInt(checkpoint.TotalRows),
		checkpoint.ProcessedRows,
		nullableInt(checkpoint.SuccessCount),
		nullableInt(checkpoint.FailureCount),
		rollbackSQL,
	)
	if err != nil {
		return fmt.Errorf("failed to complete checkpoint: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrCheckpointNotFound
	}

	return nil
}

// Fail marks the import as failed with an error message.
func (m *PostgresManager) Fail(ctx context.Context, checkpoint *Checkpoint, importErr error) error {
	if checkpoint == nil {
		return ErrNilCheckpoint
	}

	checkpoint.Status = StatusFailed
	if importErr != nil {
		checkpoint.ErrorMessage = importErr.Error()
	}

	rollbackSQL := encodeRollbackSQL(checkpoint.RollbackSQL)

	result, err := m.pool.Exec(ctx, updateManifestSQL,
		checkpoint.ManifestID,
		string(StatusFailed),
		nullableInt(checkpoint.TotalRows),
		checkpoint.ProcessedRows,
		nullableInt(checkpoint.SuccessCount),
		nullableInt(checkpoint.FailureCount),
		rollbackSQL,
	)
	if err != nil {
		return fmt.Errorf("failed to mark checkpoint as failed: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrCheckpointNotFound
	}

	return nil
}

// Cancel marks the import as cancelled.
func (m *PostgresManager) Cancel(ctx context.Context, checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return ErrNilCheckpoint
	}

	checkpoint.Status = StatusCancelled
	rollbackSQL := encodeRollbackSQL(checkpoint.RollbackSQL)

	result, err := m.pool.Exec(ctx, updateManifestSQL,
		checkpoint.ManifestID,
		string(StatusCancelled),
		nullableInt(checkpoint.TotalRows),
		checkpoint.ProcessedRows,
		nullableInt(checkpoint.SuccessCount),
		nullableInt(checkpoint.FailureCount),
		rollbackSQL,
	)
	if err != nil {
		return fmt.Errorf("failed to cancel checkpoint: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrCheckpointNotFound
	}

	return nil
}

// Resume finds the last checkpoint for a file to enable resumption.
func (m *PostgresManager) Resume(ctx context.Context, tenantID, sourceFile string) (*Checkpoint, error) {
	// Calculate current file checksum
	checksum, err := calculateFileChecksum(sourceFile)
	if err != nil {
		return nil, err
	}

	// Find the most recent RUNNING or CANCELLED checkpoint for this file
	query := `
		SELECT id, tenant_id, source_file, file_checksum,
			   COALESCE(total_rows, 0), processed_rows,
			   COALESCE(success_count, 0), COALESCE(failure_count, 0),
			   status, rollback_sql, created_at, updated_at
		FROM import_manifest
		WHERE tenant_id = $1 AND source_file = $2 AND status IN ('RUNNING', 'CANCELLED', 'FAILED')
		ORDER BY created_at DESC
		LIMIT 1
	`

	checkpoint, err := m.scanCheckpoint(ctx, query, tenantID, sourceFile)
	if err != nil {
		return nil, err
	}

	// Verify checksum matches
	if checkpoint.FileChecksum != checksum {
		return nil, fmt.Errorf("%w: expected %s, got %s",
			ErrChecksumMismatch, checkpoint.FileChecksum, checksum)
	}

	// Reset status to running for resumption and persist the change
	checkpoint.Status = StatusRunning
	_, err = m.pool.Exec(ctx, `
		UPDATE import_manifest SET status = $2, updated_at = NOW()
		WHERE id = $1
	`, checkpoint.ManifestID, string(StatusRunning))
	if err != nil {
		return nil, fmt.Errorf("failed to update checkpoint status for resumption: %w", err)
	}

	return checkpoint, nil
}

// ResumeByID finds a checkpoint by its manifest ID.
func (m *PostgresManager) ResumeByID(ctx context.Context, manifestID uuid.UUID) (*Checkpoint, error) {
	query := `
		SELECT id, tenant_id, source_file, file_checksum,
			   COALESCE(total_rows, 0), processed_rows,
			   COALESCE(success_count, 0), COALESCE(failure_count, 0),
			   status, rollback_sql, created_at, updated_at
		FROM import_manifest
		WHERE id = $1
	`

	checkpoint, err := m.scanCheckpoint(ctx, query, manifestID)
	if err != nil {
		return nil, err
	}

	// Verify the source file still exists and checksum matches
	checksum, err := calculateFileChecksum(checkpoint.SourceFile)
	if err != nil {
		return nil, err
	}

	if checkpoint.FileChecksum != checksum {
		return nil, fmt.Errorf("%w: expected %s, got %s",
			ErrChecksumMismatch, checkpoint.FileChecksum, checksum)
	}

	// Reset status to running for resumption and persist the change
	checkpoint.Status = StatusRunning
	_, err = m.pool.Exec(ctx, `
		UPDATE import_manifest SET status = $2, updated_at = NOW()
		WHERE id = $1
	`, checkpoint.ManifestID, string(StatusRunning))
	if err != nil {
		return nil, fmt.Errorf("failed to update checkpoint status for resumption: %w", err)
	}

	return checkpoint, nil
}

// Rollback executes stored rollback SQL to undo a partial import.
func (m *PostgresManager) Rollback(ctx context.Context, checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return ErrNilCheckpoint
	}

	if len(checkpoint.RollbackSQL) == 0 {
		return nil // Nothing to rollback
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin rollback transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	// Execute each rollback statement
	for i, sql := range checkpoint.RollbackSQL {
		if _, err := tx.Exec(ctx, sql); err != nil {
			return fmt.Errorf("failed to execute rollback statement %d: %w", i+1, err)
		}
	}

	// Update checkpoint status
	updateQuery := `
		UPDATE import_manifest
		SET status = 'CANCELLED',
			rollback_sql = NULL,
			updated_at = NOW()
		WHERE id = $1
	`
	if _, err := tx.Exec(ctx, updateQuery, checkpoint.ManifestID); err != nil {
		return fmt.Errorf("failed to update checkpoint after rollback: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit rollback transaction: %w", err)
	}

	checkpoint.Status = StatusCancelled
	checkpoint.RollbackSQL = nil

	return nil
}

// GetHistory returns the import history for a tenant.
func (m *PostgresManager) GetHistory(ctx context.Context, tenantID string, limit int) ([]*Checkpoint, error) {
	if limit <= 0 {
		limit = 100 // Default limit
	}

	query := `
		SELECT id, tenant_id, source_file, file_checksum,
			   COALESCE(total_rows, 0), processed_rows,
			   COALESCE(success_count, 0), COALESCE(failure_count, 0),
			   status, rollback_sql, created_at, updated_at
		FROM import_manifest
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := m.pool.Query(ctx, query, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query import history: %w", err)
	}
	defer rows.Close()

	var checkpoints []*Checkpoint
	for rows.Next() {
		cp, err := scanCheckpointRow(rows)
		if err != nil {
			return nil, err
		}
		checkpoints = append(checkpoints, cp)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating import history: %w", err)
	}

	return checkpoints, nil
}
