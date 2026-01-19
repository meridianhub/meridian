package infra

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/checkpoint"
)

// CheckpointManagerAdapter wraps the checkpoint manager for use with market-data-tool.
type CheckpointManagerAdapter struct {
	manager *checkpoint.Manager
	pool    *pgxpool.Pool
}

// NewCheckpointManager creates a new checkpoint manager adapter.
func NewCheckpointManager(ctx context.Context, dbURL string) (*CheckpointManagerAdapter, error) {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	// Ensure the import_manifest table exists
	if err := checkpoint.EnsureSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ensure schema: %w", err)
	}

	manager, err := checkpoint.NewManager(pool)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create checkpoint manager: %w", err)
	}

	return &CheckpointManagerAdapter{
		manager: manager,
		pool:    pool,
	}, nil
}

// StartImport creates a new import manifest entry.
func (a *CheckpointManagerAdapter) StartImport(ctx context.Context, tenantID, sourceFile string) (*checkpoint.Checkpoint, error) {
	return a.manager.StartImport(ctx, tenantID, sourceFile)
}

// UpdateProgress updates the current checkpoint with progress information.
func (a *CheckpointManagerAdapter) UpdateProgress(ctx context.Context, cp *checkpoint.Checkpoint) error {
	return a.manager.UpdateProgress(ctx, cp)
}

// Complete marks the import as completed successfully.
func (a *CheckpointManagerAdapter) Complete(ctx context.Context, cp *checkpoint.Checkpoint) error {
	return a.manager.Complete(ctx, cp)
}

// Fail marks the import as failed with an error message.
func (a *CheckpointManagerAdapter) Fail(ctx context.Context, cp *checkpoint.Checkpoint, err error) error {
	return a.manager.Fail(ctx, cp, err)
}

// Cancel marks the import as cancelled.
func (a *CheckpointManagerAdapter) Cancel(ctx context.Context, cp *checkpoint.Checkpoint) error {
	return a.manager.Cancel(ctx, cp)
}

// ResumeByID finds a checkpoint by its manifest ID.
func (a *CheckpointManagerAdapter) ResumeByID(ctx context.Context, manifestID uuid.UUID) (*checkpoint.Checkpoint, error) {
	return a.manager.ResumeByID(ctx, manifestID)
}

// Close releases resources.
func (a *CheckpointManagerAdapter) Close() {
	if a.pool != nil {
		a.pool.Close()
	}
}
