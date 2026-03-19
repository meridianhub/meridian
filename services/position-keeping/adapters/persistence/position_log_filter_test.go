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

func TestPostgresRepository_List_ReconciliationStatusFilter(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	log := createTestLog(t, testAccountID)
	err := tc.repo.Create(ctx, log)
	require.NoError(t, err)

	// Default reconciliation status is UNRECONCILED
	reconciled := domain.ReconciliationStatusUnreconciled
	filter := domain.PositionLogFilter{
		ReconciliationStatus: &reconciled,
		Limit:                10,
		Offset:               0,
	}
	logs, err := tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, logs, 1)

	// Filter for a status with no matches
	matched := domain.ReconciliationStatusMatched
	filter.ReconciliationStatus = &matched
	logs, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Empty(t, logs)
}

func TestPostgresRepository_List_DateFilters(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	log := createTestLog(t, testAccountID)
	err := tc.repo.Create(ctx, log)
	require.NoError(t, err)

	// FromDate in the past should include the log
	pastDate := time.Now().UTC().Add(-1 * time.Hour)
	filter := domain.PositionLogFilter{
		FromDate: &pastDate,
		Limit:    10,
	}
	logs, err := tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, logs, 1)

	// FromDate in the future should exclude everything
	futureDate := time.Now().UTC().Add(24 * time.Hour)
	filter.FromDate = &futureDate
	logs, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Empty(t, logs)

	// ToDate in the past should exclude the log
	veryPast := time.Now().UTC().Add(-24 * time.Hour)
	filter = domain.PositionLogFilter{
		ToDate: &veryPast,
		Limit:  10,
	}
	logs, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Empty(t, logs)

	// ToDate in the future should include
	filter.ToDate = &futureDate
	logs, err = tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, logs, 1)
}

func TestPostgresRepository_List_CombinedFilters(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create logs with different accounts and statuses
	log1 := createTestLog(t, testAccountID)
	log2 := createTestLog(t, "GB33BUKB20201555555556")
	err := log2.MarkPosted("Posted", nil)
	require.NoError(t, err)

	err = tc.repo.Create(ctx, log1)
	require.NoError(t, err)
	err = tc.repo.Create(ctx, log2)
	require.NoError(t, err)

	// Combine account + status + date
	accountID := testAccountID
	statusPending := domain.TransactionStatusPending
	pastDate := time.Now().UTC().Add(-1 * time.Hour)
	futureDate := time.Now().UTC().Add(24 * time.Hour)

	filter := domain.PositionLogFilter{
		AccountID: &accountID,
		Status:    &statusPending,
		FromDate:  &pastDate,
		ToDate:    &futureDate,
		Limit:     10,
	}
	logs, err := tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, logs, 1)
	assert.Equal(t, testAccountID, logs[0].AccountID)
}

func TestPostgresRepository_CreateBatch_DuplicateLogIDs(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	log := createTestLog(t, testAccountID)
	logs := []*domain.FinancialPositionLog{log, log}

	err := tc.repo.CreateBatch(ctx, logs)
	assert.ErrorIs(t, err, domain.ErrConflict)
}

func TestPostgresRepository_TransactionLineageWithParent(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create a log with a parent transaction in the lineage
	amount, err := domain.NewMoney(decimal.NewFromFloat(100), domain.CurrencyGBP)
	require.NoError(t, err)

	entry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		testAccountID,
		amount,
		domain.PostingDirectionDebit,
		time.Now().UTC(),
		"Test",
		"REF-PARENT",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	parentID := uuid.New()
	childID := uuid.New()
	lineage, err := domain.NewTransactionLineage(
		uuid.New(),
		"refund",
		&parentID,
		[]uuid.UUID{childID},
		[]uuid.UUID{uuid.New()},
	)
	require.NoError(t, err)

	log, err := domain.NewFinancialPositionLog(testAccountID, entry, lineage)
	require.NoError(t, err)

	auditEntry, err := domain.NewAuditTrailEntry(
		"test-user", "create", "Test", "127.0.0.1",
		map[string]string{"system": "test"},
	)
	require.NoError(t, err)
	err = log.AddAuditEntry(auditEntry)
	require.NoError(t, err)

	err = tc.repo.Create(ctx, log)
	require.NoError(t, err)

	// Verify parent lineage round-trips (FindByID uses loadTransactionLineageTx)
	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)
	require.NotNil(t, retrieved.TransactionLineage)
	assert.Equal(t, parentID, *retrieved.TransactionLineage.ParentTransactionID())
	assert.Len(t, retrieved.TransactionLineage.ChildTransactionIDs(), 1)
	assert.Len(t, retrieved.TransactionLineage.RelatedTransactionIDs(), 1)
}

func TestPostgresRepository_TransactionLineageWithParent_BatchList(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create two logs with parent lineage to exercise loadTransactionLineageBatchTx
	for i := 0; i < 2; i++ {
		amount, err := domain.NewMoney(decimal.NewFromFloat(50), domain.CurrencyGBP)
		require.NoError(t, err)

		entry, err := domain.NewTransactionLogEntry(
			uuid.New(), testAccountID, amount,
			domain.PostingDirectionCredit, time.Now().UTC(),
			"Batch test", "REF-BATCH",
			domain.TransactionSourceAutomated,
		)
		require.NoError(t, err)

		parentID := uuid.New()
		lineage, err := domain.NewTransactionLineage(
			uuid.New(), "payment", &parentID, []uuid.UUID{}, []uuid.UUID{},
		)
		require.NoError(t, err)

		log, err := domain.NewFinancialPositionLog(testAccountID, entry, lineage)
		require.NoError(t, err)

		auditEntry, err := domain.NewAuditTrailEntry(
			"test-user", "create", "Test", "127.0.0.1",
			map[string]string{"system": "test"},
		)
		require.NoError(t, err)
		err = log.AddAuditEntry(auditEntry)
		require.NoError(t, err)

		err = tc.repo.Create(ctx, log)
		require.NoError(t, err)
	}

	// List exercises batch loading including lineage with parent
	filter := domain.PositionLogFilter{
		Limit: 10,
	}
	logs, err := tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, logs, 2)
	for _, log := range logs {
		require.NotNil(t, log.TransactionLineage)
		assert.NotNil(t, log.TransactionLineage.ParentTransactionID())
	}
}

func TestPostgresRepository_Update_NotFound(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	log := createTestLog(t, testAccountID)
	// Don't persist — update should fail
	err := tc.repo.Update(ctx, log)
	assert.ErrorIs(t, err, domain.ErrOptimisticLock)
}

func TestPostgresRepository_CreateBatch_LargeBatch(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	// Create a batch of 5 logs to exercise the batch load paths
	var logs []*domain.FinancialPositionLog
	for i := 0; i < 5; i++ {
		log := createTestLog(t, testAccountID)
		logs = append(logs, log)
	}

	err := tc.repo.CreateBatch(ctx, logs)
	require.NoError(t, err)

	// Verify via List with account filter
	accountID := testAccountID
	filter := domain.PositionLogFilter{
		AccountID: &accountID,
		Limit:     10,
	}
	listed, err := tc.repo.List(ctx, filter)
	require.NoError(t, err)
	assert.Len(t, listed, 5)
}
