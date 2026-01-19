// Package checkpoint provides checkpoint persistence for market data import operations.
//
// The checkpoint manager tracks import progress using the import_manifest table,
// enabling resume capability for interrupted imports.
package checkpoint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	// ErrFileNotFound is returned when the source file does not exist.
	ErrFileNotFound = errors.New("source file not found")
	// ErrChecksumMismatch is returned when file checksum doesn't match checkpoint.
	ErrChecksumMismatch = errors.New("file checksum mismatch - file has been modified since checkpoint")
	// ErrNilCheckpoint is returned when a nil checkpoint is passed to a method.
	ErrNilCheckpoint = errors.New("checkpoint cannot be nil")
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
	LastProcessedLine int

	// ErrorMessage contains the error message if the import failed.
	ErrorMessage string

	// CreatedAt is when the import was initiated.
	CreatedAt time.Time

	// UpdatedAt is the last update timestamp.
	UpdatedAt time.Time
}

// Manager provides checkpoint persistence for import operations.
type Manager struct {
	pool *pgxpool.Pool
}

// NewManager creates a new checkpoint manager with the given connection pool.
func NewManager(pool *pgxpool.Pool) (*Manager, error) {
	if pool == nil {
		return nil, ErrNilPool
	}
	return &Manager{pool: pool}, nil
}

// EnsureSchema creates the import_manifest table if it doesn't exist.
func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS import_manifest (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			source_file TEXT NOT NULL,
			file_checksum TEXT NOT NULL,
			total_rows INTEGER,
			processed_rows INTEGER NOT NULL DEFAULT 0,
			success_count INTEGER,
			failure_count INTEGER,
			status TEXT NOT NULL DEFAULT 'RUNNING',
			rollback_sql TEXT,
			error_message TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT import_manifest_tenant_file_checksum_unique
				UNIQUE (tenant_id, source_file, file_checksum)
		);

		CREATE INDEX IF NOT EXISTS idx_import_manifest_tenant_status
			ON import_manifest(tenant_id, status);
	`)
	return err
}

// StartImport creates a new import manifest entry.
func (m *Manager) StartImport(ctx context.Context, tenantID, sourceFile string) (*Checkpoint, error) {
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

	// Insert into database.
	// Note: The status check for FAILED/CANCELLED is already done in findExistingImport above.
	// The ON CONFLICT clause handles the case where a previous failed/cancelled import exists.
	// Since we only reach here when existing status is FAILED/CANCELLED (or no existing record),
	// we can safely update on conflict without a WHERE clause.
	query := `
		INSERT INTO import_manifest (
			id, tenant_id, source_file, file_checksum,
			total_rows, processed_rows, success_count, failure_count,
			status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (tenant_id, source_file, file_checksum)
		DO UPDATE SET
			id = EXCLUDED.id,
			status = EXCLUDED.status,
			processed_rows = EXCLUDED.processed_rows,
			success_count = EXCLUDED.success_count,
			failure_count = EXCLUDED.failure_count,
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
		checkpoint.CreatedAt,
		checkpoint.UpdatedAt,
	).Scan(&returnedID)
	if err != nil {
		return nil, fmt.Errorf("failed to create checkpoint: %w", err)
	}

	checkpoint.ManifestID = returnedID
	return checkpoint, nil
}

// UpdateProgress updates the current checkpoint with progress information.
func (m *Manager) UpdateProgress(ctx context.Context, checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return ErrNilCheckpoint
	}

	query := `
		UPDATE import_manifest
		SET total_rows = $2,
			processed_rows = $3,
			success_count = $4,
			failure_count = $5,
			updated_at = NOW()
		WHERE id = $1
	`

	result, err := m.pool.Exec(ctx, query,
		checkpoint.ManifestID,
		nullableInt(checkpoint.TotalRows),
		checkpoint.ProcessedRows,
		nullableInt(checkpoint.SuccessCount),
		nullableInt(checkpoint.FailureCount),
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
func (m *Manager) Complete(ctx context.Context, checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return ErrNilCheckpoint
	}

	checkpoint.Status = StatusCompleted

	result, err := m.pool.Exec(ctx, `
		UPDATE import_manifest
		SET status = $2,
			total_rows = $3,
			processed_rows = $4,
			success_count = $5,
			failure_count = $6,
			updated_at = NOW()
		WHERE id = $1
	`,
		checkpoint.ManifestID,
		string(StatusCompleted),
		nullableInt(checkpoint.TotalRows),
		checkpoint.ProcessedRows,
		nullableInt(checkpoint.SuccessCount),
		nullableInt(checkpoint.FailureCount),
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
func (m *Manager) Fail(ctx context.Context, checkpoint *Checkpoint, importErr error) error {
	if checkpoint == nil {
		return ErrNilCheckpoint
	}

	checkpoint.Status = StatusFailed
	if importErr != nil {
		checkpoint.ErrorMessage = importErr.Error()
	}

	result, err := m.pool.Exec(ctx, `
		UPDATE import_manifest
		SET status = $2,
			total_rows = $3,
			processed_rows = $4,
			success_count = $5,
			failure_count = $6,
			error_message = $7,
			updated_at = NOW()
		WHERE id = $1
	`,
		checkpoint.ManifestID,
		string(StatusFailed),
		nullableInt(checkpoint.TotalRows),
		checkpoint.ProcessedRows,
		nullableInt(checkpoint.SuccessCount),
		nullableInt(checkpoint.FailureCount),
		checkpoint.ErrorMessage,
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
func (m *Manager) Cancel(ctx context.Context, checkpoint *Checkpoint) error {
	if checkpoint == nil {
		return ErrNilCheckpoint
	}

	checkpoint.Status = StatusCancelled

	result, err := m.pool.Exec(ctx, `
		UPDATE import_manifest
		SET status = $2,
			total_rows = $3,
			processed_rows = $4,
			success_count = $5,
			failure_count = $6,
			updated_at = NOW()
		WHERE id = $1
	`,
		checkpoint.ManifestID,
		string(StatusCancelled),
		nullableInt(checkpoint.TotalRows),
		checkpoint.ProcessedRows,
		nullableInt(checkpoint.SuccessCount),
		nullableInt(checkpoint.FailureCount),
	)
	if err != nil {
		return fmt.Errorf("failed to cancel checkpoint: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrCheckpointNotFound
	}

	return nil
}

// ResumeByID finds a checkpoint by its manifest ID.
func (m *Manager) ResumeByID(ctx context.Context, manifestID uuid.UUID) (*Checkpoint, error) {
	query := `
		SELECT id, tenant_id, source_file, file_checksum,
			   COALESCE(total_rows, 0), processed_rows,
			   COALESCE(success_count, 0), COALESCE(failure_count, 0),
			   status, created_at, updated_at
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

	// Reset status to running for resumption
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

// findExistingImport checks for existing imports with the same file and checksum.
//
//nolint:nilnil // Returning (nil, nil) is intentional - nil checkpoint means no existing import
func (m *Manager) findExistingImport(ctx context.Context, tenantID, sourceFile, checksum string) (*Checkpoint, error) {
	query := `
		SELECT id, tenant_id, source_file, file_checksum,
			   COALESCE(total_rows, 0), processed_rows,
			   COALESCE(success_count, 0), COALESCE(failure_count, 0),
			   status, created_at, updated_at
		FROM import_manifest
		WHERE tenant_id = $1 AND source_file = $2 AND file_checksum = $3
		ORDER BY created_at DESC
		LIMIT 1
	`

	checkpoint, err := m.scanCheckpoint(ctx, query, tenantID, sourceFile, checksum)
	if errors.Is(err, ErrCheckpointNotFound) {
		return nil, nil
	}
	return checkpoint, err
}

// scanCheckpoint executes a query and scans a single checkpoint.
func (m *Manager) scanCheckpoint(ctx context.Context, query string, args ...any) (*Checkpoint, error) {
	row := m.pool.QueryRow(ctx, query, args...)

	var cp Checkpoint
	var statusStr string

	err := row.Scan(
		&cp.ManifestID,
		&cp.TenantID,
		&cp.SourceFile,
		&cp.FileChecksum,
		&cp.TotalRows,
		&cp.ProcessedRows,
		&cp.SuccessCount,
		&cp.FailureCount,
		&statusStr,
		&cp.CreatedAt,
		&cp.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCheckpointNotFound
		}
		return nil, fmt.Errorf("failed to scan checkpoint: %w", err)
	}

	cp.Status = Status(statusStr)
	cp.LastProcessedLine = cp.ProcessedRows

	return &cp, nil
}

// calculateFileChecksum computes the SHA256 checksum of a file.
func calculateFileChecksum(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrFileNotFound, filePath)
		}
		return "", fmt.Errorf("failed to open file for checksum: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to calculate checksum: %w", err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// nullableInt returns nil for zero values, otherwise the value pointer.
func nullableInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// IncrementSuccess increments the success count and processed rows.
func (c *Checkpoint) IncrementSuccess(count int) {
	c.SuccessCount += count
	c.ProcessedRows += count
	c.LastProcessedLine = c.ProcessedRows
}

// IncrementFailure increments the failure count and processed rows.
func (c *Checkpoint) IncrementFailure(count int) {
	c.FailureCount += count
	c.ProcessedRows += count
	c.LastProcessedLine = c.ProcessedRows
}

// SetTotalRows sets the total row count (usually after file parsing).
func (c *Checkpoint) SetTotalRows(total int) {
	c.TotalRows = total
}

// Progress returns the completion percentage (0-100).
func (c *Checkpoint) Progress() float64 {
	if c.TotalRows == 0 {
		return 0
	}
	return float64(c.ProcessedRows) / float64(c.TotalRows) * 100
}

// IsResumable returns true if the checkpoint can be resumed.
func (c *Checkpoint) IsResumable() bool {
	return c.Status == StatusRunning || c.Status == StatusCancelled || c.Status == StatusFailed
}
