package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPositionLogWrite_Create_NilLog(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	err := tc.repo.Create(context.Background(), nil)
	assert.ErrorIs(t, err, persistence.ErrNilLog)
}

func TestPositionLogWrite_Create_DuplicateConflict(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	log := buildWriteTestLog(t, "ACC-WRITE-001")

	require.NoError(t, tc.repo.Create(ctx, log))

	// Creating the same log again (same LogID) must return ErrConflict
	err := tc.repo.Create(ctx, log)
	assert.ErrorIs(t, err, domain.ErrConflict)
}

func TestPositionLogWrite_Create_Roundtrip(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	log := buildWriteTestLog(t, "ACC-WRITE-002")

	require.NoError(t, tc.repo.Create(ctx, log))

	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)
	assert.Equal(t, log.LogID, retrieved.LogID)
	assert.Equal(t, log.AccountID, retrieved.AccountID)
	assert.Equal(t, log.Version, retrieved.Version)
	assert.Equal(t, 1, len(retrieved.TransactionLogEntries))
	assert.Equal(t, 1, len(retrieved.AuditTrail))
}

func TestPositionLogWrite_Update_NilLog(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	err := tc.repo.Update(context.Background(), nil)
	assert.ErrorIs(t, err, persistence.ErrNilLog)
}

func TestPositionLogWrite_Update_NotFound(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	log := buildWriteTestLog(t, "ACC-WRITE-003")
	// Not persisted — update must return ErrNotFound
	err := tc.repo.Update(context.Background(), log)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestPositionLogWrite_Update_OptimisticLock(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	log := buildWriteTestLog(t, "ACC-WRITE-004")
	require.NoError(t, tc.repo.Create(ctx, log))

	// Simulate concurrent update: bump version in DB so our update is stale
	_, err := tc.pool.Exec(ctx,
		`UPDATE position_keeping.financial_position_log SET version = version + 1 WHERE log_id = $1`,
		log.LogID,
	)
	require.NoError(t, err)

	// Domain update increments version to 2, but DB version is already 2
	auditEntry, err := domain.NewAuditTrailEntry("user", "post", "posted", "127.0.0.1", nil)
	require.NoError(t, err)
	require.NoError(t, log.MarkPosted("Posted", auditEntry))

	err = tc.repo.Update(ctx, log)
	assert.ErrorIs(t, err, domain.ErrOptimisticLock)
}

func TestPositionLogWrite_Update_Success(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	log := buildWriteTestLog(t, "ACC-WRITE-005")
	require.NoError(t, tc.repo.Create(ctx, log))

	auditEntry, err := domain.NewAuditTrailEntry("user", "post", "posted", "127.0.0.1", nil)
	require.NoError(t, err)
	require.NoError(t, log.MarkPosted("Posted successfully", auditEntry))

	require.NoError(t, tc.repo.Update(ctx, log))

	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)
	assert.Equal(t, domain.TransactionStatusPosted, retrieved.StatusTracking.CurrentStatus)
	assert.Equal(t, int64(2), retrieved.Version)
}

func TestPositionLogWrite_CreateWithOutbox_Success(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	log := buildWriteTestLog(t, "ACC-WRITE-006")

	postFnCalled := false
	postFn := func(_ pgx.Tx) error {
		postFnCalled = true
		return nil
	}

	require.NoError(t, tc.repo.CreateWithOutbox(ctx, log, postFn))
	assert.True(t, postFnCalled)

	// Verify the log was persisted
	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)
	assert.Equal(t, log.LogID, retrieved.LogID)
}

func TestPositionLogWrite_CreateWithOutbox_PostFnError_RollsBack(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	log := buildWriteTestLog(t, "ACC-WRITE-007")

	postFn := func(_ pgx.Tx) error {
		return assert.AnError
	}

	err := tc.repo.CreateWithOutbox(ctx, log, postFn)
	require.Error(t, err)

	// Log must NOT be persisted because the transaction was rolled back
	_, findErr := tc.repo.FindByID(ctx, log.LogID)
	assert.ErrorIs(t, findErr, domain.ErrNotFound)
}

func TestPositionLogWrite_CreateWithOutbox_NilPostFn(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	log := buildWriteTestLog(t, "ACC-WRITE-008")

	// nil postFn should be handled gracefully
	require.NoError(t, tc.repo.CreateWithOutbox(ctx, log, nil))

	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)
	assert.Equal(t, log.LogID, retrieved.LogID)
}

func TestPositionLogWrite_UpdateWithOutbox_Success(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	log := buildWriteTestLog(t, "ACC-WRITE-009")
	require.NoError(t, tc.repo.Create(ctx, log))

	auditEntry, err := domain.NewAuditTrailEntry("user", "post", "posted", "127.0.0.1", nil)
	require.NoError(t, err)
	require.NoError(t, log.MarkPosted("Posted", auditEntry))

	outboxCalled := false
	postFn := func(_ pgx.Tx) error {
		outboxCalled = true
		return nil
	}

	require.NoError(t, tc.repo.UpdateWithOutbox(ctx, log, postFn))
	assert.True(t, outboxCalled)

	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)
	assert.Equal(t, domain.TransactionStatusPosted, retrieved.StatusTracking.CurrentStatus)
}

// buildWriteTestLog creates a minimal FinancialPositionLog for write tests.
func buildWriteTestLog(t *testing.T, accountID string) *domain.FinancialPositionLog {
	t.Helper()

	amount, err := domain.NewMoney(decimal.NewFromFloat(50.00), domain.CurrencyGBP)
	require.NoError(t, err)

	entry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		amount,
		domain.PostingDirectionCredit,
		time.Now().UTC(),
		"write test",
		"REF-WRITE",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	log, err := domain.NewFinancialPositionLog(accountID, entry, nil)
	require.NoError(t, err)

	auditEntry, err := domain.NewAuditTrailEntry("test-user", "create", "created", "127.0.0.1", nil)
	require.NoError(t, err)
	require.NoError(t, log.AddAuditEntry(auditEntry))

	return log
}
