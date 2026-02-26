package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/lib/pq"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// =============================================================================
// Tenant Isolation Integration Tests (Task 9)
//
// These tests verify that withdrawal operations are properly scoped to the
// tenant schema derived from the request context. A withdrawal created in
// tenant A must not be visible or executable from tenant B's context.
// =============================================================================

// setupMultiTenantTestDB creates two tenant schemas in the shared container,
// each containing the account, withdrawal, and event_outbox tables.
// Returns the shared gorm.DB, per-tenant contexts, and a cleanup function.
func setupMultiTenantTestDB(t *testing.T) (db *gorm.DB, ctxA context.Context, ctxB context.Context, cleanup func()) {
	t.Helper()

	db = openSharedDB(t)

	// Use unique tenant IDs so parallel tests don't collide
	tenantA := uniqueTenantID()
	tenantB := uniqueTenantID()

	for _, tid := range []tenant.TenantID{tenantA, tenantB} {
		schema := tid.SchemaName()

		err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schema))).Error
		require.NoError(t, err, "failed to create schema %s", schema)

		// Set search_path to the tenant schema so AutoMigrate creates tables there
		err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schema))).Error
		require.NoError(t, err, "failed to set search_path for %s", schema)

		err = db.AutoMigrate(
			&persistence.CurrentAccountEntity{},
			&persistence.WithdrawalEntity{},
			&events.EventOutbox{},
		)
		require.NoError(t, err, "failed to auto-migrate tables in %s", schema)
	}

	// Reset search_path to public so it does not favor either tenant.
	// The repository's WithGormTenantTransaction will set the correct
	// search_path per-transaction based on the context's tenant ID.
	err := db.Exec("SET search_path TO public").Error
	require.NoError(t, err, "failed to reset search_path")

	ctxA = tenant.WithTenant(context.Background(), tenantA)
	ctxB = tenant.WithTenant(context.Background(), tenantB)

	schemaA := tenantA.SchemaName()
	schemaB := tenantB.SchemaName()
	cleanup = func() {
		_ = db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaA)))
		_ = db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaB)))
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}

	return db, ctxA, ctxB, cleanup
}

// TestTenantIsolation_RetrieveWithdrawal_NotVisibleAcrossTenants verifies that
// a withdrawal created in tenant A cannot be retrieved from tenant B's context.
func TestTenantIsolation_RetrieveWithdrawal_NotVisibleAcrossTenants(t *testing.T) {
	db, ctxA, ctxB, cleanup := setupMultiTenantTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	// Create account and withdrawal in tenant A
	accountA := createTestAccountWithBalance(t, ctxA, repo, "ACC-ISO-A-001", 100000)
	amountA, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	withdrawalA, err := domain.NewWithdrawal(accountA.ID(), amountA, "WTH-ISO-A-001")
	require.NoError(t, err)
	require.NoError(t, withdrawalRepo.Create(ctxA, withdrawalA))

	// Build service with mocks
	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-ISO-A-001": 100000,
	})
	svc.withdrawalRepo = withdrawalRepo

	// Verify withdrawal is visible from tenant A context
	respA, err := svc.RetrieveWithdrawal(ctxA, &pb.RetrieveWithdrawalRequest{
		WithdrawalId: "WTH-ISO-A-001",
	})
	require.NoError(t, err, "Withdrawal should be visible from tenant A")
	require.Len(t, respA.Withdrawals, 1)
	assert.Equal(t, "WTH-ISO-A-001", respA.Withdrawals[0].WithdrawalId)

	// Verify withdrawal is NOT visible from tenant B context
	_, err = svc.RetrieveWithdrawal(ctxB, &pb.RetrieveWithdrawalRequest{
		WithdrawalId: "WTH-ISO-A-001",
	})
	require.Error(t, err, "Withdrawal from tenant A should not be visible to tenant B")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be a gRPC status")
	assert.Equal(t, codes.NotFound, st.Code(),
		"Cross-tenant retrieval should return NotFound, not the actual withdrawal")
}

// TestTenantIsolation_ListWithdrawals_ScopedToTenant verifies that listing
// withdrawals by account_id returns only the current tenant's withdrawals,
// even when both tenants have accounts with the same business identifier.
func TestTenantIsolation_ListWithdrawals_ScopedToTenant(t *testing.T) {
	db, ctxA, ctxB, cleanup := setupMultiTenantTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	// Use the same business account ID in both tenants to verify schema isolation
	sharedAccountID := "ACC-SHARED-001"

	accountA := createTestAccountWithBalance(t, ctxA, repo, sharedAccountID, 100000)
	accountB := createTestAccountWithBalance(t, ctxB, repo, sharedAccountID, 200000)

	// Create 2 withdrawals in tenant A
	for i := 1; i <= 2; i++ {
		amount, err := domain.NewMoney("GBP", int64(i*1000))
		require.NoError(t, err)
		w, err := domain.NewWithdrawal(accountA.ID(), amount, fmt.Sprintf("WTH-A-%03d", i))
		require.NoError(t, err)
		require.NoError(t, withdrawalRepo.Create(ctxA, w))
	}

	// Create 3 withdrawals in tenant B
	for i := 1; i <= 3; i++ {
		amount, err := domain.NewMoney("GBP", int64(i*2000))
		require.NoError(t, err)
		w, err := domain.NewWithdrawal(accountB.ID(), amount, fmt.Sprintf("WTH-B-%03d", i))
		require.NoError(t, err)
		require.NoError(t, withdrawalRepo.Create(ctxB, w))
	}

	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		sharedAccountID: 100000,
	})
	svc.withdrawalRepo = withdrawalRepo

	// List from tenant A context: should see only tenant A's 2 withdrawals
	respA, err := svc.RetrieveWithdrawal(ctxA, &pb.RetrieveWithdrawalRequest{
		AccountId: sharedAccountID,
	})
	require.NoError(t, err, "Listing withdrawals from tenant A should succeed")
	assert.Len(t, respA.Withdrawals, 2,
		"Tenant A should see exactly 2 withdrawals, not tenant B's 3")

	// List from tenant B context: should see only tenant B's 3 withdrawals
	respB, err := svc.RetrieveWithdrawal(ctxB, &pb.RetrieveWithdrawalRequest{
		AccountId: sharedAccountID,
	})
	require.NoError(t, err, "Listing withdrawals from tenant B should succeed")
	assert.Len(t, respB.Withdrawals, 3,
		"Tenant B should see exactly 3 withdrawals, not tenant A's 2")
}

// TestTenantIsolation_ExecuteWithdrawal_CannotExecuteAcrossTenants verifies that
// a pending withdrawal in tenant A cannot be executed from tenant B's context.
func TestTenantIsolation_ExecuteWithdrawal_CannotExecuteAcrossTenants(t *testing.T) {
	db, ctxA, ctxB, cleanup := setupMultiTenantTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create account with balance in tenant A
	_ = createTestAccountWithBalance(t, ctxA, repo, "ACC-ISO-EXEC-A", 100000)

	// Configure mocks
	mockPosKeeping := &mockPositionKeepingClient{
		accountBalances: map[string]int64{
			"ACC-ISO-EXEC-A": 100000,
		},
	}
	mockFinAcct := &mockFinancialAccountingClient{}

	svc := &Service{
		repo:                   repo,
		withdrawalRepo:         withdrawalRepo,
		outboxRepo:             outboxRepo,
		db:                     db,
		posKeepingClient:       mockPosKeeping,
		finAcctClient:          mockFinAcct,
		logger:                 testLogger(),
		withdrawalOrchestrator: testWithdrawalOrchestrator(repo, mockPosKeeping, mockFinAcct),
	}

	// Initiate withdrawal in tenant A
	initiateResp, err := svc.InitiateWithdrawal(ctxA, &pb.InitiateWithdrawalRequest{
		AccountId: "ACC-ISO-EXEC-A",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        50,
				Nanos:        0,
			},
		},
		Reference: "WTH-ISO-EXEC-001",
	})
	require.NoError(t, err, "InitiateWithdrawal should succeed in tenant A")
	require.NotNil(t, initiateResp.Withdrawal)
	withdrawalID := initiateResp.Withdrawal.WithdrawalId

	// Attempt to execute from tenant B context
	resp, err := svc.ExecuteWithdrawal(ctxB, &pb.ExecuteWithdrawalRequest{
		WithdrawalId: withdrawalID,
	})

	require.Error(t, err,
		"ExecuteWithdrawal from tenant B should fail for tenant A's withdrawal")
	assert.Nil(t, resp, "Response should be nil on cross-tenant execution attempt")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be a gRPC status")
	assert.Equal(t, codes.NotFound, st.Code(),
		"Cross-tenant execute should return NotFound, not FailedPrecondition or Internal")

	// Verify the withdrawal in tenant A is still in PENDING status (untouched)
	originalWithdrawal, err := withdrawalRepo.FindByReference(ctxA, withdrawalID)
	require.NoError(t, err, "Withdrawal should still exist in tenant A")
	assert.Equal(t, domain.WithdrawalStatusPending, originalWithdrawal.Status,
		"Withdrawal should remain PENDING after cross-tenant execute attempt")
}
