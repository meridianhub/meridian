package persistence_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// errOutboxBoom simulates a failure that occurs after the outbox row has been
// written but before the surrounding transaction commits.
var errOutboxBoom = errors.New("simulated failure after outbox write")

// countOutboxRows returns the number of event_outbox rows for a given aggregate.
func countOutboxRows(t *testing.T, db *gorm.DB, aggregateID string) int64 {
	t.Helper()
	tid := tenant.TenantID("test-tenant-01")
	quoted := fmt.Sprintf("%q", tid.SchemaName())
	var count int64
	err := db.WithContext(tenantCtx()).
		Raw(fmt.Sprintf(`SELECT COUNT(*) FROM %s."event_outbox" WHERE aggregate_id = ?`, quoted), aggregateID).
		Scan(&count).Error
	require.NoError(t, err)
	return count
}

// insertOutboxRow writes an outbox entry using the shared events repository on
// the provided transaction, exactly as the production publishers do.
func insertOutboxRow(ctx context.Context, tx *gorm.DB, aggregateID string) error {
	outboxRepo := events.NewPostgresOutboxRepository(tx)
	entry := events.NewEventOutbox(
		"reconciliation.dispute_created.v1",
		aggregateID,
		"Dispute",
		[]byte("payload"),
		"reconciliation.dispute.created",
		"reconciliation",
		aggregateID,
		"test-tenant-01",
	)
	return outboxRepo.Insert(ctx, tx, entry)
}

func TestDisputeRepository_CreateWithOutbox_CommitsBoth(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewDisputeRepository(db)
	ctx := tenantCtx()

	runID, snapshotID, varianceID := uuid.New(), uuid.New(), uuid.New()
	seedRunAndVariance(t, db, runID, snapshotID, varianceID)

	dispute, err := domain.NewDispute(varianceID, runID, "ACC-001", "Amount mismatch", "user-1")
	require.NoError(t, err)
	aggregateID := dispute.DisputeID.String()

	err = repo.CreateWithOutbox(ctx, dispute, func(tx *gorm.DB) error {
		return insertOutboxRow(ctx, tx, aggregateID)
	})
	require.NoError(t, err)

	// Both the domain row and the outbox row are persisted.
	persisted, err := repo.FindByID(ctx, dispute.DisputeID)
	require.NoError(t, err)
	assert.Equal(t, dispute.DisputeID, persisted.DisputeID)
	assert.Equal(t, int64(1), countOutboxRows(t, db, aggregateID))
}

func TestDisputeRepository_CreateWithOutbox_RollsBackBoth(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewDisputeRepository(db)
	ctx := tenantCtx()

	runID, snapshotID, varianceID := uuid.New(), uuid.New(), uuid.New()
	seedRunAndVariance(t, db, runID, snapshotID, varianceID)

	dispute, err := domain.NewDispute(varianceID, runID, "ACC-001", "Amount mismatch", "user-1")
	require.NoError(t, err)
	aggregateID := dispute.DisputeID.String()

	// The callback writes the outbox row and then fails, forcing a rollback of
	// the whole transaction after both writes have been staged.
	err = repo.CreateWithOutbox(ctx, dispute, func(tx *gorm.DB) error {
		if insErr := insertOutboxRow(ctx, tx, aggregateID); insErr != nil {
			return insErr
		}
		return errOutboxBoom
	})
	require.ErrorIs(t, err, errOutboxBoom)

	// Neither the dispute nor the outbox row survives: all-or-nothing.
	_, err = repo.FindByID(ctx, dispute.DisputeID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.Equal(t, int64(0), countOutboxRows(t, db, aggregateID))
}

func TestSettlementRunRepository_UpdateWithOutbox_CommitsBoth(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementRunRepository(db)
	ctx := tenantCtx()

	run := newTestSettlementRun(t)
	require.NoError(t, repo.Create(ctx, run))
	require.NoError(t, run.Start())
	aggregateID := run.RunID.String()

	err := repo.UpdateWithOutbox(ctx, run, func(tx *gorm.DB) error {
		return insertOutboxRow(ctx, tx, aggregateID)
	})
	require.NoError(t, err)

	found, err := repo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusRunning, found.Status)
	assert.Equal(t, int64(2), found.Version)
	assert.Equal(t, int64(1), countOutboxRows(t, db, aggregateID))
}

func TestSettlementRunRepository_UpdateWithOutbox_RollsBackBoth(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewSettlementRunRepository(db)
	ctx := tenantCtx()

	run := newTestSettlementRun(t)
	require.NoError(t, repo.Create(ctx, run))
	require.NoError(t, run.Start()) // in-memory transition to RUNNING, version 2
	aggregateID := run.RunID.String()

	err := repo.UpdateWithOutbox(ctx, run, func(tx *gorm.DB) error {
		if insErr := insertOutboxRow(ctx, tx, aggregateID); insErr != nil {
			return insErr
		}
		return errOutboxBoom
	})
	require.ErrorIs(t, err, errOutboxBoom)

	// The persisted run remains in its pre-update state and no event was written.
	found, err := repo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusPending, found.Status)
	assert.Equal(t, int64(1), found.Version)
	assert.Equal(t, int64(0), countOutboxRows(t, db, aggregateID))
}
