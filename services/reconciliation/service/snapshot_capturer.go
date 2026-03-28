package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"golang.org/x/sync/errgroup"
)

const (
	// defaultPageSize is the number of positions fetched per page from PK.
	defaultPageSize int32 = 500

	// maxPages is the upper bound on pages to prevent runaway pagination.
	maxPages = 1000

	// snapshotBatchSize is the number of snapshots inserted per batch.
	snapshotBatchSize = 100

	// captureTimeout is the maximum time allowed for the entire capture operation.
	captureTimeout = 10 * time.Minute

	// maxConcurrentInserts limits the number of concurrent batch insert goroutines.
	maxConcurrentInserts = 5
)

// SnapshotCapturer orchestrates capturing point-in-time position snapshots from
// Position Keeping during a settlement run. It fetches positions using cursor-based
// pagination, transforms them to SettlementSnapshot domain objects, and batch-inserts
// them into the database.
type SnapshotCapturer struct {
	provider PositionDataProvider
	runRepo  domain.SettlementRunRepository
	snapRepo domain.SettlementSnapshotRepository
}

// NewSnapshotCapturer creates a new SnapshotCapturer with the given dependencies.
func NewSnapshotCapturer(
	provider PositionDataProvider,
	runRepo domain.SettlementRunRepository,
	snapRepo domain.SettlementSnapshotRepository,
) *SnapshotCapturer {
	return &SnapshotCapturer{
		provider: provider,
		runRepo:  runRepo,
		snapRepo: snapRepo,
	}
}

// CaptureSnapshots executes the snapshot capture phase of a settlement run.
//
// It performs the following steps:
//  1. Loads the settlement run and validates it is in PENDING state
//  2. Transitions the run to RUNNING (capturing)
//  3. Cleans up any existing snapshots for idempotent retry
//  4. Paginates through PK positions, converting each page to snapshots
//  5. Batch-inserts snapshots using bounded concurrency
//  6. On success: transitions the run to COMPLETED
//  7. On failure: transitions the run to FAILED with the error reason
func (sc *SnapshotCapturer) CaptureSnapshots(ctx context.Context, runID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(ctx, captureTimeout)
	defer cancel()

	run, err := sc.runRepo.FindByID(ctx, runID)
	if err != nil {
		return fmt.Errorf("failed to find settlement run %s: %w", runID, err)
	}

	if err := run.Start(); err != nil {
		return fmt.Errorf("failed to start settlement run %s: %w", runID, err)
	}
	if err := sc.runRepo.Update(ctx, run); err != nil {
		return fmt.Errorf("failed to update settlement run to RUNNING: %w", err)
	}

	slog.InfoContext(ctx, "snapshot capture started",
		"run_id", runID,
		"account_id", run.AccountID,
		"scope", run.Scope,
	)

	captureErr := sc.capturePositions(ctx, run)
	if captureErr != nil {
		slog.ErrorContext(ctx, "snapshot capture failed",
			"run_id", runID,
			"error", captureErr,
		)
		if failErr := run.Fail(captureErr.Error()); failErr != nil {
			slog.ErrorContext(ctx, "failed to transition run to FAILED state",
				"run_id", runID,
				"error", failErr,
			)
			return fmt.Errorf("capture failed: %w; additionally failed to mark run as FAILED: %w", captureErr, failErr)
		}
		if updateErr := sc.runRepo.Update(ctx, run); updateErr != nil {
			slog.ErrorContext(ctx, "failed to persist FAILED state",
				"run_id", runID,
				"error", updateErr,
			)
		}
		return fmt.Errorf("snapshot capture failed: %w", captureErr)
	}

	if err := run.Complete(0); err != nil {
		return fmt.Errorf("failed to transition run to COMPLETED: %w", err)
	}
	if err := sc.runRepo.Update(ctx, run); err != nil {
		return fmt.Errorf("failed to persist COMPLETED state: %w", err)
	}

	slog.InfoContext(ctx, "snapshot capture completed",
		"run_id", runID,
	)
	return nil
}

// capturePositions handles the paginated fetch-and-store loop.
func (sc *SnapshotCapturer) capturePositions(ctx context.Context, run *domain.SettlementRun) (retErr error) {
	// Idempotent cleanup: remove any snapshots from a previous failed attempt
	if err := sc.snapRepo.DeleteByRunID(ctx, run.RunID); err != nil {
		return fmt.Errorf("failed to clean up existing snapshots: %w", err)
	}

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentInserts)

	// Always wait for in-flight goroutines before returning, to avoid leaking
	// goroutines when the pagination loop exits early on error.
	defer func() {
		if waitErr := g.Wait(); waitErr != nil {
			if retErr != nil {
				retErr = fmt.Errorf("%w; additionally batch insert failed: %w", retErr, waitErr)
			} else {
				retErr = fmt.Errorf("batch insert failed: %w", waitErr)
			}
		}
	}()

	totalCaptured, err := sc.fetchAndInsertPages(ctx, gCtx, g, run)
	if err != nil {
		return err
	}

	slog.InfoContext(ctx, "all snapshots captured",
		"run_id", run.RunID,
		"total_snapshots", totalCaptured,
	)
	return nil
}

// fetchAndInsertPages paginates through PK positions, transforms them to snapshots,
// and schedules batch inserts on the errgroup.
func (sc *SnapshotCapturer) fetchAndInsertPages(ctx, gCtx context.Context, g *errgroup.Group, run *domain.SettlementRun) (int, error) {
	pageToken := ""
	totalCaptured := 0

	for page := 0; page < maxPages; page++ {
		positionPage, err := sc.provider.FetchPositions(gCtx, run.AccountID, defaultPageSize, pageToken)
		if err != nil {
			return totalCaptured, fmt.Errorf("failed to fetch positions (page %d): %w", page, err)
		}

		if len(positionPage.Records) == 0 {
			break
		}

		snapshots, err := sc.transformToSnapshots(run, positionPage.Records)
		if err != nil {
			return totalCaptured, fmt.Errorf("failed to transform positions to snapshots (page %d): %w", page, err)
		}

		sc.scheduleBatchInserts(gCtx, g, snapshots)
		totalCaptured += len(snapshots)

		slog.DebugContext(ctx, "captured snapshot page",
			"run_id", run.RunID,
			"page", page,
			"records", len(positionPage.Records),
			"total_captured", totalCaptured,
		)

		pageToken = positionPage.NextPageToken
		if pageToken == "" {
			break
		}
	}

	return totalCaptured, nil
}

// scheduleBatchInserts splits snapshots into batches and schedules concurrent inserts.
func (sc *SnapshotCapturer) scheduleBatchInserts(gCtx context.Context, g *errgroup.Group, snapshots []*domain.SettlementSnapshot) {
	for i := 0; i < len(snapshots); i += snapshotBatchSize {
		end := i + snapshotBatchSize
		if end > len(snapshots) {
			end = len(snapshots)
		}
		batch := snapshots[i:end]
		g.Go(func() error {
			return sc.snapRepo.CreateBatch(gCtx, batch)
		})
	}
}

// transformToSnapshots converts PK position records into SettlementSnapshot domain objects.
func (sc *SnapshotCapturer) transformToSnapshots(run *domain.SettlementRun, records []PositionRecord) ([]*domain.SettlementSnapshot, error) {
	snapshots := make([]*domain.SettlementSnapshot, 0, len(records))
	for _, rec := range records {
		// During capture, both expected and actual are set to the PK balance
		// so that variance is zero. The actual balance will be overwritten
		// during the reconciliation phase when the second source is compared.
		snap, err := domain.NewSettlementSnapshot(
			run.RunID,
			rec.AccountID,
			rec.InstrumentCode,
			rec.Balance, // expected balance from PK
			rec.Balance, // actual balance - same as expected until reconciliation
			rec.SourceSystem,
			rec.Attributes,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create snapshot for account %s: %w", rec.AccountID, err)
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots, nil
}
