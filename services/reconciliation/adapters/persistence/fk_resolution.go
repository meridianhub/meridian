package persistence

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// resolveRunSurrogate looks up the settlement_run surrogate PK (settlement_run.id)
// for a business run_id (settlement_run.run_id). The surrogate is what child tables
// (settlement_snapshot, variance) store in their run_id FK columns, since those FKs
// reference settlement_run.id, not the business identifier.
//
// The returned error wraps gorm.ErrRecordNotFound when no run matches, so callers can
// branch on errors.Is(err, gorm.ErrRecordNotFound) to treat a missing run as a no-op.
func resolveRunSurrogate(tx *gorm.DB, businessRunID uuid.UUID) (uuid.UUID, error) {
	var run SettlementRunEntity
	if err := tx.Select("id").Where("run_id = ?", businessRunID).First(&run).Error; err != nil {
		return uuid.Nil, fmt.Errorf("resolving surrogate ID for run %s: %w", businessRunID, err)
	}
	return run.ID, nil
}

// resolveSnapshotSurrogate looks up the settlement_snapshot surrogate PK
// (settlement_snapshot.id) for a business snapshot_id (settlement_snapshot.snapshot_id).
// The surrogate is what variance.snapshot_id stores, since that FK references
// settlement_snapshot.id, not the business identifier.
func resolveSnapshotSurrogate(tx *gorm.DB, businessSnapshotID uuid.UUID) (uuid.UUID, error) {
	var snap SettlementSnapshotEntity
	if err := tx.Select("id").Where("snapshot_id = ?", businessSnapshotID).First(&snap).Error; err != nil {
		return uuid.Nil, fmt.Errorf("resolving surrogate ID for snapshot %s: %w", businessSnapshotID, err)
	}
	return snap.ID, nil
}
