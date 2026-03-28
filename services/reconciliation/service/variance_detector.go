package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
)

// VarianceDetector compares current snapshots against previous run data to detect
// variances for a settlement run. For D+1 runs it compares against initial booking entries,
// for subsequent runs (D+5, M+3, M+14) it compares against the previous run's snapshots.
type VarianceDetector struct {
	runRepo      domain.SettlementRunRepository
	snapRepo     domain.SettlementSnapshotRepository
	varianceRepo domain.VarianceRepository
}

// NewVarianceDetector creates a new VarianceDetector with the given dependencies.
func NewVarianceDetector(
	runRepo domain.SettlementRunRepository,
	snapRepo domain.SettlementSnapshotRepository,
	varianceRepo domain.VarianceRepository,
) *VarianceDetector {
	return &VarianceDetector{
		runRepo:      runRepo,
		snapRepo:     snapRepo,
		varianceRepo: varianceRepo,
	}
}

// DetectVariances compares the current run's snapshots against previous data and
// creates variance records for any discrepancies found.
//
// The method is idempotent: running it twice on the same run produces the same results
// by deleting any existing variances before re-detecting.
func (vd *VarianceDetector) DetectVariances(ctx context.Context, runID uuid.UUID) ([]*domain.Variance, error) {
	run, err := vd.runRepo.FindByID(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to find settlement run %s: %w", runID, err)
	}

	if run.Status != domain.RunStatusRunning {
		return nil, fmt.Errorf("settlement run %s is not in RUNNING state (current: %s): %w",
			runID, run.Status, domain.ErrRunNotRunning)
	}

	// Idempotent cleanup: remove any variances from a previous detection attempt
	if err := vd.varianceRepo.DeleteByRunID(ctx, runID); err != nil {
		return nil, fmt.Errorf("failed to clean up existing variances: %w", err)
	}

	// Fetch current run's snapshots
	currentSnapshots, err := vd.snapRepo.FindByRunID(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch snapshots for run %s: %w", runID, err)
	}

	if len(currentSnapshots) == 0 {
		slog.InfoContext(ctx, "no snapshots found for variance detection",
			"run_id", runID,
		)
		return nil, nil
	}

	// Find previous run for comparison (same account, completed, most recent before this)
	previousSnapshots, err := vd.findPreviousSnapshots(ctx, run)
	if err != nil {
		return nil, fmt.Errorf("failed to find previous snapshots: %w", err)
	}

	variances := vd.compareSnapshots(runID, currentSnapshots, previousSnapshots)

	if len(variances) > 0 {
		if err := vd.varianceRepo.CreateBatch(ctx, variances); err != nil {
			return nil, fmt.Errorf("failed to persist variances: %w", err)
		}
	}

	slog.InfoContext(ctx, "variance detection completed",
		"run_id", runID,
		"snapshots_compared", len(currentSnapshots),
		"variances_detected", len(variances),
	)

	return variances, nil
}

// findPreviousSnapshots finds the snapshots from the most recent completed run
// for the same account. Returns nil (no error) if no previous run exists,
// meaning this is a D+1 run comparing against initial booking.
func (vd *VarianceDetector) findPreviousSnapshots(ctx context.Context, run *domain.SettlementRun) ([]*domain.SettlementSnapshot, error) {
	completedStatus := domain.RunStatusCompleted
	runs, err := vd.runRepo.List(ctx, domain.RunFilter{
		AccountID: &run.AccountID,
		Status:    &completedStatus,
		ToDate:    &run.CreatedAt,
		Limit:     1,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list previous runs: %w", err)
	}

	if len(runs) == 0 {
		slog.DebugContext(ctx, "no previous completed run found, treating as D+1 run",
			"run_id", run.RunID,
			"account_id", run.AccountID,
		)
		return nil, nil
	}

	prevRun := runs[0]
	snapshots, err := vd.snapRepo.FindByRunID(ctx, prevRun.RunID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch previous run snapshots: %w", err)
	}

	slog.DebugContext(ctx, "found previous run for comparison",
		"current_run_id", run.RunID,
		"previous_run_id", prevRun.RunID,
		"previous_snapshots", len(snapshots),
	)

	return snapshots, nil
}

// snapshotKey generates a composite key for snapshot matching.
func snapshotKey(accountID, instrumentCode, sourceSystem string) string {
	return accountID + "|" + instrumentCode + "|" + sourceSystem
}

// compareSnapshots compares current and previous snapshots to detect variances.
// For D+1 runs (no previous snapshots), it compares each snapshot's expected vs actual balance.
// For subsequent runs, it compares current vs previous settled amounts.
func (vd *VarianceDetector) compareSnapshots(
	runID uuid.UUID,
	current []*domain.SettlementSnapshot,
	previous []*domain.SettlementSnapshot,
) []*domain.Variance {
	if len(previous) == 0 {
		return vd.compareInitialRun(runID, current)
	}
	return vd.compareAgainstPrevious(runID, current, previous)
}

// compareInitialRun detects variances for D+1 runs by comparing expected vs actual
// within each snapshot.
func (vd *VarianceDetector) compareInitialRun(runID uuid.UUID, current []*domain.SettlementSnapshot) []*domain.Variance {
	var variances []*domain.Variance
	for _, snap := range current {
		if snap.HasVariance() {
			reason := classifyVarianceReason(snap, nil)
			v, err := domain.NewVariance(
				runID,
				snap.SnapshotID,
				snap.AccountID,
				snap.InstrumentCode,
				snap.ExpectedBalance,
				snap.ActualBalance,
				reason,
			)
			if err != nil {
				slog.Warn("failed to create variance from snapshot",
					"snapshot_id", snap.SnapshotID,
					"error", err,
				)
				continue
			}
			variances = append(variances, v)
		}
	}
	return variances
}

// compareAgainstPrevious detects variances by comparing current snapshots against
// the previous run's snapshots, including missing entries in both directions.
func (vd *VarianceDetector) compareAgainstPrevious(
	runID uuid.UUID,
	current []*domain.SettlementSnapshot,
	previous []*domain.SettlementSnapshot,
) []*domain.Variance {
	var variances []*domain.Variance

	prevMap := make(map[string]*domain.SettlementSnapshot, len(previous))
	for _, snap := range previous {
		key := snapshotKey(snap.AccountID, snap.InstrumentCode, snap.SourceSystem)
		prevMap[key] = snap
	}

	for _, snap := range current {
		key := snapshotKey(snap.AccountID, snap.InstrumentCode, snap.SourceSystem)
		prevSnap, found := prevMap[key]

		if !found {
			if v := buildMissingEntryVariance(runID, snap.SnapshotID, snap.AccountID, snap.InstrumentCode, decimal.Zero, snap.ActualBalance); v != nil {
				variances = append(variances, v)
			}
			continue
		}

		delta := snap.ActualBalance.Sub(prevSnap.ActualBalance)
		if !delta.IsZero() {
			reason := classifyVarianceReason(snap, prevSnap)
			v, err := domain.NewVariance(
				runID,
				snap.SnapshotID,
				snap.AccountID,
				snap.InstrumentCode,
				prevSnap.ActualBalance,
				snap.ActualBalance,
				reason,
			)
			if err != nil {
				slog.Warn("failed to create variance for delta",
					"snapshot_id", snap.SnapshotID,
					"error", err,
				)
			} else {
				variances = append(variances, v)
			}
		}

		delete(prevMap, key)
	}

	// Entries in previous but not in current are missing
	for _, prevSnap := range prevMap {
		if v := buildMissingEntryVariance(runID, prevSnap.SnapshotID, prevSnap.AccountID, prevSnap.InstrumentCode, prevSnap.ActualBalance, decimal.Zero); v != nil {
			variances = append(variances, v)
		}
	}

	return variances
}

// buildMissingEntryVariance creates a variance for a missing entry, returning nil
// if the balance is zero or creation fails.
func buildMissingEntryVariance(runID, snapshotID uuid.UUID, accountID, instrumentCode string, expected, actual decimal.Decimal) *domain.Variance {
	if actual.IsZero() && expected.IsZero() {
		return nil
	}
	v, err := domain.NewVariance(
		runID,
		snapshotID,
		accountID,
		instrumentCode,
		expected,
		actual,
		domain.VarianceReasonMissingEntry,
	)
	if err != nil {
		slog.Warn("failed to create variance for missing entry",
			"snapshot_id", snapshotID,
			"error", err,
		)
		return nil
	}
	return v
}

// classifyVarianceReason determines the reason for a variance based on snapshot data.
func classifyVarianceReason(current *domain.SettlementSnapshot, previous *domain.SettlementSnapshot) domain.VarianceReason {
	if previous == nil {
		// D+1 run: compare within a single snapshot
		return domain.VarianceReasonAmountMismatch
	}

	// Check for source system mismatch
	if current.SourceSystem != previous.SourceSystem {
		return domain.VarianceReasonExternalMismatch
	}

	// Check quality attributes for upgrade detection
	currentQuality := current.Attributes["quality"]
	previousQuality := previous.Attributes["quality"]
	if currentQuality != "" && previousQuality != "" && isQualityUpgrade(previousQuality, currentQuality) {
		return domain.VarianceReasonQualityUpgrade
	}

	// Check for correction attribute
	if current.Attributes["correction"] == "wash_and_reload" {
		return domain.VarianceReasonCorrectionApplied
	}

	return domain.VarianceReasonAmountMismatch
}

// qualityRank maps quality levels to numeric ranks for comparison.
var qualityRank = map[string]int{
	"ESTIMATE":    1,
	"COEFFICIENT": 2,
	"ACTUAL":      3,
	"REVISED":     4,
}

// isQualityUpgrade returns true if the current quality is higher than the previous.
func isQualityUpgrade(previous, current string) bool {
	return qualityRank[current] > qualityRank[previous]
}
