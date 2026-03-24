package persistence_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPositionLogRead_FindByID_NotFound(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	_, err := tc.repo.FindByID(context.Background(), uuid.New())
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestPositionLogRead_FindByAccountID_NoResults(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	logs, err := tc.repo.FindByAccountID(context.Background(), "ACC-NONEXISTENT")
	require.NoError(t, err)
	assert.Empty(t, logs)
}

func TestPositionLogRead_FindByAccountID_MultipleResults(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	const accountID = "ACC-READ-MULTI"

	for i := 0; i < 3; i++ {
		log := buildReadTestLog(t, accountID)
		require.NoError(t, tc.repo.Create(ctx, log))
	}
	// Add one for a different account — must not appear in results
	require.NoError(t, tc.repo.Create(ctx, buildReadTestLog(t, "ACC-READ-OTHER")))

	logs, err := tc.repo.FindByAccountID(ctx, accountID)
	require.NoError(t, err)
	assert.Len(t, logs, 3)
	for _, l := range logs {
		assert.Equal(t, accountID, l.AccountID)
	}
}

func TestPositionLogRead_List_InvalidLimit(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	_, err := tc.repo.List(context.Background(), domain.PositionLogFilter{Limit: 0})
	assert.ErrorIs(t, err, persistence.ErrInvalidLimit)
}

func TestPositionLogRead_List_Pagination(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	const accountID = "ACC-READ-PAGE"

	for i := 0; i < 5; i++ {
		require.NoError(t, tc.repo.Create(ctx, buildReadTestLog(t, accountID)))
	}

	accID := accountID

	// First page: limit 2
	page1, err := tc.repo.List(ctx, domain.PositionLogFilter{AccountID: &accID, Limit: 2, Offset: 0})
	require.NoError(t, err)
	assert.Len(t, page1, 2)

	// Second page: offset 2
	page2, err := tc.repo.List(ctx, domain.PositionLogFilter{AccountID: &accID, Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Len(t, page2, 2)

	// Third page: offset 4 — only 1 remaining
	page3, err := tc.repo.List(ctx, domain.PositionLogFilter{AccountID: &accID, Limit: 2, Offset: 4})
	require.NoError(t, err)
	assert.Len(t, page3, 1)

	// Verify no duplicates across pages
	seen := make(map[uuid.UUID]bool)
	for _, page := range [][]*domain.FinancialPositionLog{page1, page2, page3} {
		for _, l := range page {
			assert.False(t, seen[l.LogID], "LogID %s appeared on multiple pages", l.LogID)
			seen[l.LogID] = true
		}
	}
}

func TestPositionLogRead_List_AccountIDsFilter(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	const acc1 = "ACC-READ-IDS-1"
	const acc2 = "ACC-READ-IDS-2"
	const acc3 = "ACC-READ-IDS-3"

	require.NoError(t, tc.repo.Create(ctx, buildReadTestLog(t, acc1)))
	require.NoError(t, tc.repo.Create(ctx, buildReadTestLog(t, acc2)))
	require.NoError(t, tc.repo.Create(ctx, buildReadTestLog(t, acc3)))

	// AccountIDs filter: should return acc1 and acc2 but not acc3
	logs, err := tc.repo.List(ctx, domain.PositionLogFilter{
		AccountIDs: []string{acc1, acc2},
		Limit:      10,
	})
	require.NoError(t, err)
	assert.Len(t, logs, 2)

	accountsFound := make(map[string]bool)
	for _, l := range logs {
		accountsFound[l.AccountID] = true
	}
	assert.True(t, accountsFound[acc1])
	assert.True(t, accountsFound[acc2])
	assert.False(t, accountsFound[acc3])
}

func TestPositionLogRead_FindPendingForReconciliation_NoLimit(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	const accountID = "ACC-READ-PENDING"

	for i := 0; i < 3; i++ {
		require.NoError(t, tc.repo.Create(ctx, buildReadTestLog(t, accountID)))
	}

	// limit=0 should return all pending records
	logs, err := tc.repo.FindPendingForReconciliation(ctx, 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(logs), 3)
	for _, l := range logs {
		assert.Equal(t, domain.TransactionStatusPending, l.StatusTracking.CurrentStatus)
		assert.Equal(t, domain.ReconciliationStatusUnreconciled, l.StatusTracking.ReconciliationStatus)
	}
}

func TestPositionLogRead_FindPendingForReconciliation_WithLimit(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()
	const accountID = "ACC-READ-PENDING-LIM"

	for i := 0; i < 4; i++ {
		require.NoError(t, tc.repo.Create(ctx, buildReadTestLog(t, accountID)))
	}

	logs, err := tc.repo.FindPendingForReconciliation(ctx, 2)
	require.NoError(t, err)
	assert.Len(t, logs, 2)
}

func TestPositionLogRead_FindByID_LoadsRelatedEntities(t *testing.T) {
	tc := setupTestContainer(t)
	defer tc.cleanup(t)

	ctx := context.Background()

	amount, err := domain.NewMoney(decimal.NewFromFloat(100), domain.CurrencyGBP)
	require.NoError(t, err)

	entry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		"ACC-READ-RELATED",
		amount,
		domain.PostingDirectionDebit,
		time.Now().UTC(),
		"related test",
		"REF-RELATED",
		domain.TransactionSourceManual,
	)
	require.NoError(t, err)

	parentID := uuid.New()
	lineage, err := domain.NewTransactionLineage(
		uuid.New(), "payment", &parentID,
		[]uuid.UUID{uuid.New()},
		[]uuid.UUID{uuid.New()},
	)
	require.NoError(t, err)

	log, err := domain.NewFinancialPositionLog("ACC-READ-RELATED", entry, lineage)
	require.NoError(t, err)

	auditEntry, err := domain.NewAuditTrailEntry("user", "create", "created", "127.0.0.1", map[string]string{"k": "v"})
	require.NoError(t, err)
	require.NoError(t, log.AddAuditEntry(auditEntry))

	require.NoError(t, tc.repo.Create(ctx, log))

	retrieved, err := tc.repo.FindByID(ctx, log.LogID)
	require.NoError(t, err)

	// Transaction log entries loaded
	assert.Len(t, retrieved.TransactionLogEntries, 1)

	// Transaction lineage loaded with parent
	require.NotNil(t, retrieved.TransactionLineage)
	require.NotNil(t, retrieved.TransactionLineage.ParentTransactionID())
	assert.Equal(t, parentID, *retrieved.TransactionLineage.ParentTransactionID())

	// Audit trail loaded
	assert.Len(t, retrieved.AuditTrail, 1)
}

// buildReadTestLog creates a minimal FinancialPositionLog for read tests.
func buildReadTestLog(t *testing.T, accountID string) *domain.FinancialPositionLog {
	t.Helper()

	amount, err := domain.NewMoney(decimal.NewFromFloat(25.00), domain.CurrencyUSD)
	require.NoError(t, err)

	entry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		amount,
		domain.PostingDirectionDebit,
		time.Now().UTC(),
		"read test",
		"REF-READ",
		domain.TransactionSourceAutomated,
	)
	require.NoError(t, err)

	log, err := domain.NewFinancialPositionLog(accountID, entry, nil)
	require.NoError(t, err)

	return log
}
