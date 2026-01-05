package executor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// PositionUpdateExecutor orchestrates rebucketing operations including
// authorization, position updates, and audit logging.
type PositionUpdateExecutor struct {
	pool        *pgxpool.Pool
	config      *Config
	authorizer  *AdminAuthorizer
	auditLogger *AuditLogger
	posUpdater  *PositionUpdater
	logger      *slog.Logger
}

// NewPositionUpdateExecutor creates a new position update executor.
func NewPositionUpdateExecutor(pool *pgxpool.Pool, config *Config, logger *slog.Logger) (*PositionUpdateExecutor, error) {
	if pool == nil {
		return nil, ErrNilPool
	}

	if config == nil {
		config = DefaultConfig()
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	if logger == nil {
		logger = slog.Default()
	}

	posUpdater, err := NewPositionUpdater(pool, config.BatchSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create position updater: %w", err)
	}

	return &PositionUpdateExecutor{
		pool:        pool,
		config:      config,
		authorizer:  NewAdminAuthorizer(),
		auditLogger: NewAuditLogger(),
		posUpdater:  posUpdater,
		logger:      logger,
	}, nil
}

// Execute performs the rebucketing operation according to the provided plan.
// If DryRun mode is enabled, it returns a plan without making changes.
func (e *PositionUpdateExecutor) Execute(ctx context.Context, plan *RebucketingPlan) (*ExecutionResult, error) {
	startTime := time.Now()

	// Validate input
	if err := e.validatePlan(plan); err != nil {
		return &ExecutionResult{
			Success:  false,
			Duration: time.Since(startTime),
			Error:    err,
		}, err
	}

	// Check authorization
	adminUserID, err := e.authorizer.AuthorizeRebucketing(ctx)
	if err != nil {
		e.logger.Warn("rebucketing authorization failed",
			"error", err,
			"instrument", plan.InstrumentCode,
		)
		return &ExecutionResult{
			Success:  false,
			Duration: time.Since(startTime),
			Error:    err,
		}, err
	}

	e.logger.Info("rebucketing authorized",
		"admin_user", adminUserID,
		"instrument", plan.InstrumentCode,
		"old_version", plan.OldInstrumentVersion,
		"new_version", plan.NewInstrumentVersion,
		"position_count", len(plan.AffectedPositions),
	)

	// Handle dry-run mode
	if e.config.DryRun {
		return e.executeDryRun(plan, adminUserID, startTime)
	}

	// Execute the actual rebucketing
	return e.executeRebucketing(ctx, plan, adminUserID, startTime)
}

// DryRun generates a plan without making any changes.
func (e *PositionUpdateExecutor) DryRun(ctx context.Context, plan *RebucketingPlan) (*DryRunPlan, error) {
	// Validate input
	if err := e.validatePlan(plan); err != nil {
		return nil, err
	}

	// Check authorization (even for dry-run)
	_, err := e.authorizer.AuthorizeRebucketing(ctx)
	if err != nil {
		return nil, err
	}

	return e.generateDryRunPlan(plan), nil
}

// validatePlan validates the rebucketing plan.
func (e *PositionUpdateExecutor) validatePlan(plan *RebucketingPlan) error {
	if plan == nil {
		return ErrNilPlan
	}

	if len(plan.AffectedPositions) == 0 {
		return ErrEmptyPlan
	}

	if plan.OldInstrumentVersion == "" || plan.NewInstrumentVersion == "" {
		return ErrMissingInstrumentVersion
	}

	// Validate bucket mappings
	for oldKey, newKey := range plan.BucketMappings {
		if oldKey == "" || newKey == "" {
			return ErrInvalidBucketMapping
		}
	}

	return nil
}

// executeDryRun returns results for a dry-run execution.
func (e *PositionUpdateExecutor) executeDryRun(plan *RebucketingPlan, adminUserID string, startTime time.Time) (*ExecutionResult, error) {
	batches := e.posUpdater.SplitIntoBatches(plan.AffectedPositions)

	e.logger.Info("dry-run completed",
		"admin_user", adminUserID,
		"instrument", plan.InstrumentCode,
		"position_count", len(plan.AffectedPositions),
		"batch_count", len(batches),
	)

	return &ExecutionResult{
		Success:          true,
		PositionsUpdated: int64(len(plan.AffectedPositions)),
		BucketsAffected:  len(plan.BucketMappings),
		AuditLogEntries:  int64(len(plan.AffectedPositions) * 2), // 2 entries per position
		Duration:         time.Since(startTime),
		DryRun:           true,
	}, nil
}

// executeRebucketing performs the actual rebucketing operation.
func (e *PositionUpdateExecutor) executeRebucketing(
	ctx context.Context,
	plan *RebucketingPlan,
	adminUserID string,
	startTime time.Time,
) (*ExecutionResult, error) {
	// Split positions into batches
	batches := e.posUpdater.SplitIntoBatches(plan.AffectedPositions)
	totalPositions := int64(len(plan.AffectedPositions))

	e.logger.Info("starting rebucketing execution",
		"admin_user", adminUserID,
		"instrument", plan.InstrumentCode,
		"total_positions", totalPositions,
		"batch_count", len(batches),
		"batch_size", e.config.BatchSize,
	)

	// Process all batches in a single transaction for atomicity
	tx, err := e.posUpdater.BeginTx(ctx)
	if err != nil {
		return &ExecutionResult{
			Success:  false,
			Duration: time.Since(startTime),
			Error:    fmt.Errorf("failed to begin transaction: %w", err),
		}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var processedCount int64
	for batchNum, batch := range batches {
		e.logger.Debug("processing batch",
			"batch_number", batchNum+1,
			"batch_size", len(batch),
		)

		// Process positions in batch
		if err := e.posUpdater.ProcessBatch(ctx, tx, batch, adminUserID); err != nil {
			rollbackErr := &TransactionRollbackError{
				Cause:              err,
				PositionsProcessed: processedCount,
				BatchNumber:        batchNum + 1,
			}
			e.logger.Error("batch processing failed, rolling back",
				"batch_number", batchNum+1,
				"positions_processed", processedCount,
				"error", err,
			)
			return &ExecutionResult{
				Success:  false,
				Duration: time.Since(startTime),
				Error:    rollbackErr,
				PartialProgress: &PartialProgress{
					PositionsProcessed: processedCount,
					BatchesCompleted:   batchNum,
				},
			}, rollbackErr
		}

		// Log audit entries for batch
		if err := e.auditLogger.LogBatch(ctx, tx, adminUserID,
			plan.OldInstrumentVersion, plan.NewInstrumentVersion, batch); err != nil {
			rollbackErr := &TransactionRollbackError{
				Cause:              err,
				PositionsProcessed: processedCount,
				BatchNumber:        batchNum + 1,
			}
			e.logger.Error("audit logging failed, rolling back",
				"batch_number", batchNum+1,
				"positions_processed", processedCount,
				"error", err,
			)
			return &ExecutionResult{
				Success:  false,
				Duration: time.Since(startTime),
				Error:    rollbackErr,
				PartialProgress: &PartialProgress{
					PositionsProcessed: processedCount,
					BatchesCompleted:   batchNum,
				},
			}, rollbackErr
		}

		processedCount += int64(len(batch))
		e.logger.Debug("batch completed",
			"batch_number", batchNum+1,
			"total_processed", processedCount,
		)
	}

	// Commit the transaction
	if err := tx.Commit(ctx); err != nil {
		rollbackErr := &TransactionRollbackError{
			Cause:              err,
			PositionsProcessed: processedCount,
			BatchNumber:        len(batches),
		}
		e.logger.Error("transaction commit failed",
			"positions_processed", processedCount,
			"error", err,
		)
		return &ExecutionResult{
			Success:  false,
			Duration: time.Since(startTime),
			Error:    rollbackErr,
			PartialProgress: &PartialProgress{
				PositionsProcessed: processedCount,
				BatchesCompleted:   len(batches),
			},
		}, rollbackErr
	}

	duration := time.Since(startTime)
	auditEntries := totalPositions * 2 // SOFT_DELETE + INSERT_NEW per position

	e.logger.Info("rebucketing completed successfully",
		"admin_user", adminUserID,
		"instrument", plan.InstrumentCode,
		"positions_updated", totalPositions,
		"buckets_affected", len(plan.BucketMappings),
		"audit_entries", auditEntries,
		"duration_ms", duration.Milliseconds(),
	)

	return &ExecutionResult{
		Success:          true,
		PositionsUpdated: totalPositions,
		BucketsAffected:  len(plan.BucketMappings),
		AuditLogEntries:  auditEntries,
		Duration:         duration,
		DryRun:           false,
	}, nil
}

// generateDryRunPlan creates a detailed plan for review.
func (e *PositionUpdateExecutor) generateDryRunPlan(plan *RebucketingPlan) *DryRunPlan {
	// Build bucket summary
	bucketCounts := make(map[string]int64)
	bucketAmounts := make(map[string]decimal.Decimal)

	for _, pos := range plan.AffectedPositions {
		key := pos.OldBucketKey + "->" + pos.NewBucketKey
		bucketCounts[key]++
		if amount, exists := bucketAmounts[key]; exists {
			bucketAmounts[key] = amount.Add(pos.Amount)
		} else {
			bucketAmounts[key] = pos.Amount
		}
	}

	var summaries []BucketMappingSummary
	for _, pos := range plan.AffectedPositions {
		key := pos.OldBucketKey + "->" + pos.NewBucketKey
		// Only add each unique mapping once
		found := false
		for _, s := range summaries {
			if s.OldBucketKey == pos.OldBucketKey && s.NewBucketKey == pos.NewBucketKey {
				found = true
				break
			}
		}
		if !found {
			summaries = append(summaries, BucketMappingSummary{
				OldBucketKey:  pos.OldBucketKey,
				NewBucketKey:  pos.NewBucketKey,
				PositionCount: bucketCounts[key],
				TotalAmount:   bucketAmounts[key],
			})
		}
	}

	batches := e.posUpdater.SplitIntoBatches(plan.AffectedPositions)

	return &DryRunPlan{
		InstrumentCode:        plan.InstrumentCode,
		OldInstrumentVersion:  plan.OldInstrumentVersion,
		NewInstrumentVersion:  plan.NewInstrumentVersion,
		AffectedPositionCount: int64(len(plan.AffectedPositions)),
		BucketMappings:        plan.BucketMappings,
		BucketSummary:         summaries,
		EstimatedAuditEntries: int64(len(plan.AffectedPositions) * 2),
		EstimatedBatches:      len(batches),
	}
}

// PrintDryRunReport prints a formatted dry-run report.
func (e *PositionUpdateExecutor) PrintDryRunReport(plan *DryRunPlan) string {
	report := fmt.Sprintf(`
Rebucketing Dry-Run Report
===========================

Instrument: %s
Old Version: %s
New Version: %s

Affected Positions: %d
Estimated Batches: %d
Estimated Audit Entries: %d

Bucket Mappings:
`, plan.InstrumentCode, plan.OldInstrumentVersion, plan.NewInstrumentVersion,
		plan.AffectedPositionCount, plan.EstimatedBatches, plan.EstimatedAuditEntries)

	for _, summary := range plan.BucketSummary {
		report += fmt.Sprintf("  %s -> %s: %d positions, total amount: %s\n",
			summary.OldBucketKey, summary.NewBucketKey,
			summary.PositionCount, summary.TotalAmount.String())
	}

	return report
}

// PrintExecutionReport prints a formatted execution report.
func (e *PositionUpdateExecutor) PrintExecutionReport(result *ExecutionResult) string {
	status := "FAILED"
	if result.Success {
		status = "COMPLETED"
	}

	mode := "Live"
	if result.DryRun {
		mode = "Dry-Run"
	}

	report := fmt.Sprintf(`
Rebucketing %s (%s)
===========================

Positions Updated: %d
Buckets Affected: %d
Audit Log Entries: %d
Duration: %.2fs
`,
		status, mode,
		result.PositionsUpdated,
		result.BucketsAffected,
		result.AuditLogEntries,
		result.Duration.Seconds())

	if result.Error != nil {
		report += fmt.Sprintf("\nError: %v\n", result.Error)
	}

	if result.PartialProgress != nil {
		report += fmt.Sprintf(`
Partial Progress (before rollback):
  Positions Processed: %d
  Batches Completed: %d
`,
			result.PartialProgress.PositionsProcessed,
			result.PartialProgress.BatchesCompleted)
	}

	return report
}
