package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPositionLogBatch_EmptyBatch_IsNoOp(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	err := tc.repo.CreateBatch(context.Background(), nil)
	assert.NoError(t, err)

	err = tc.repo.CreateBatch(context.Background(), []*domain.FinancialPositionLog{})
	assert.NoError(t, err)
}

func TestPositionLogBatch_SingleLog(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	log := buildBatchTestLog(t, "ACC-BATCH-SINGLE")

	err := tc.repo.CreateBatch(ctx, []*domain.FinancialPositionLog{log})
	require.NoError(t, err)

	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)
	assert.Equal(t, log.LogID, retrieved.LogID)
}

func TestPositionLogBatch_MultipleLogs_AllPersisted(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	const count = 10
	const accountID = "ACC-BATCH-MULTI"

	logs := make([]*domain.FinancialPositionLog, count)
	for i := 0; i < count; i++ {
		logs[i] = buildBatchTestLog(t, accountID)
	}

	require.NoError(t, tc.repo.CreateBatch(ctx, logs))

	retrieved, err := tc.repo.FindByAccountID(ctx, accountID)
	require.NoError(t, err)
	assert.Len(t, retrieved, count)
}

func TestPositionLogBatch_DuplicateInBatch_RollsBackAll(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	log := buildBatchTestLog(t, "ACC-BATCH-DUP")

	// Include the same log twice to trigger unique_violation during COPY
	err := tc.repo.CreateBatch(ctx, []*domain.FinancialPositionLog{log, log})
	assert.ErrorIs(t, err, domain.ErrConflict)

	// Nothing should have been persisted (transaction rolled back)
	_, findErr := tc.repo.FindByID(ctx, log.LogID)
	assert.ErrorIs(t, findErr, domain.ErrNotFound)
}

func TestPositionLogBatch_WithComplexAggregates(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	const accountID = "ACC-BATCH-COMPLEX"

	// Build logs with entries + lineage + audit trail
	var logs []*domain.FinancialPositionLog
	for i := 0; i < 3; i++ {
		amount, err := domain.NewMoney(decimal.NewFromFloat(float64(100*(i+1))), domain.CurrencyGBP)
		require.NoError(t, err)

		entry, err := domain.NewTransactionLogEntry(
			uuid.New(),
			accountID,
			amount,
			domain.PostingDirectionDebit,
			time.Now().UTC(),
			"batch complex test",
			"REF-BATCH-COMPLEX",
			domain.TransactionSourceManual,
		)
		require.NoError(t, err)

		parentID := uuid.New()
		lineage, err := domain.NewTransactionLineage(
			uuid.New(), "payment", &parentID,
			[]uuid.UUID{uuid.New()},
			[]uuid.UUID{},
		)
		require.NoError(t, err)

		log, err := domain.NewFinancialPositionLog(accountID, entry, lineage)
		require.NoError(t, err)

		auditEntry, err := domain.NewAuditTrailEntry("batch-user", "create", "batch created", "10.0.0.1", nil)
		require.NoError(t, err)
		require.NoError(t, log.AddAuditEntry(auditEntry))

		logs = append(logs, log)
	}

	require.NoError(t, tc.repo.CreateBatch(ctx, logs))

	// Verify all were persisted with their related entities
	retrieved, err := tc.repo.FindByAccountID(ctx, accountID)
	require.NoError(t, err)
	assert.Len(t, retrieved, 3)

	for _, r := range retrieved {
		assert.Len(t, r.TransactionLogEntries, 1)
		require.NotNil(t, r.TransactionLineage)
		assert.NotNil(t, r.TransactionLineage.ParentTransactionID())
		assert.Len(t, r.AuditTrail, 1)
	}
}

func TestPositionLogBatch_AlreadyExistingLog_Conflict(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	log := buildBatchTestLog(t, "ACC-BATCH-EXIST")

	// Persist individually first
	require.NoError(t, tc.repo.Create(ctx, log))

	// Batch with the same log must conflict
	err := tc.repo.CreateBatch(ctx, []*domain.FinancialPositionLog{log})
	assert.ErrorIs(t, err, domain.ErrConflict)
}

// buildBatchTestLog creates a FinancialPositionLog for batch tests.
func buildBatchTestLog(t *testing.T, accountID string) *domain.FinancialPositionLog {
	t.Helper()

	amount, err := domain.NewMoney(decimal.NewFromFloat(75.00), domain.CurrencyEUR)
	require.NoError(t, err)

	entry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		amount,
		domain.PostingDirectionCredit,
		time.Now().UTC(),
		"batch test",
		"REF-BATCH",
		domain.TransactionSourceAutomated,
	)
	require.NoError(t, err)

	log, err := domain.NewFinancialPositionLog(accountID, entry, nil)
	require.NoError(t, err)

	return log
}
