// Package checkpoint helper functions and Checkpoint value methods extracted from manager.go.
package checkpoint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
)

// findExistingImport checks for existing imports with the same file and checksum.
// Returns (nil, nil) when no existing import is found - this is intentional
// to distinguish "not found" from actual errors.
//
//nolint:nilnil // Returning (nil, nil) is intentional - nil checkpoint means no existing import
func (m *PostgresManager) findExistingImport(ctx context.Context, tenantID, sourceFile, checksum string) (*Checkpoint, error) {
	query := `
		SELECT id, tenant_id, source_file, file_checksum,
			   COALESCE(total_rows, 0), processed_rows,
			   COALESCE(success_count, 0), COALESCE(failure_count, 0),
			   status, rollback_sql, created_at, updated_at
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
func (m *PostgresManager) scanCheckpoint(ctx context.Context, query string, args ...any) (*Checkpoint, error) {
	row := m.pool.QueryRow(ctx, query, args...)

	var cp Checkpoint
	var statusStr string
	var rollbackSQL *string

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
		&rollbackSQL,
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
	cp.RollbackSQL = decodeRollbackSQL(rollbackSQL)
	cp.LastProcessedLine = cp.ProcessedRows // Use processed_rows as last line marker

	return &cp, nil
}

// scanCheckpointRow scans a checkpoint from a rows iterator.
func scanCheckpointRow(rows pgx.Rows) (*Checkpoint, error) {
	var cp Checkpoint
	var statusStr string
	var rollbackSQL *string

	err := rows.Scan(
		&cp.ManifestID,
		&cp.TenantID,
		&cp.SourceFile,
		&cp.FileChecksum,
		&cp.TotalRows,
		&cp.ProcessedRows,
		&cp.SuccessCount,
		&cp.FailureCount,
		&statusStr,
		&rollbackSQL,
		&cp.CreatedAt,
		&cp.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan checkpoint row: %w", err)
	}

	cp.Status = Status(statusStr)
	cp.RollbackSQL = decodeRollbackSQL(rollbackSQL)
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

// encodeRollbackSQL encodes rollback statements for storage.
// Uses newline as separator since SQL statements don't typically contain bare newlines.
func encodeRollbackSQL(statements []string) *string {
	if len(statements) == 0 {
		return nil
	}
	encoded := strings.Join(statements, "\n")
	return &encoded
}

// decodeRollbackSQL decodes rollback statements from storage.
func decodeRollbackSQL(encoded *string) []string {
	if encoded == nil || *encoded == "" {
		return nil
	}
	return strings.Split(*encoded, "\n")
}

// nullableInt returns nil for zero values, otherwise the value pointer.
func nullableInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// AddRollbackStatement appends a rollback SQL statement to the checkpoint.
// This is a convenience method for building up rollback information.
func (c *Checkpoint) AddRollbackStatement(sql string) {
	c.RollbackSQL = append(c.RollbackSQL, sql)
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
