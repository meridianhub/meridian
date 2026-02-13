package service

import (
	"fmt"
	"testing"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// =============================================================================
// UpdateWithdrawal Integration Tests (Task 6)
// =============================================================================

// TestUpdateWithdrawal_Integration_RetrievesPendingWithdrawal verifies that
// UpdateWithdrawal returns the current state of a pending withdrawal.
// The handler looks up the withdrawal by reference and returns it with no
// validation warnings when no field updates are requested.
func TestUpdateWithdrawal_Integration_RetrievesPendingWithdrawal(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	// Create withdrawal table
	err := db.AutoMigrate(&persistence.WithdrawalEntity{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-UPD-001", 100000)

	// Create a pending withdrawal directly in the database
	amount, err := domain.NewMoney("GBP", 5000) // 50.00
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(account.ID(), amount, "WTH-UPD-001")
	require.NoError(t, err)
	require.NoError(t, withdrawalRepo.Create(ctx, withdrawal))

	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-UPD-001": 100000,
	})
	svc.withdrawalRepo = withdrawalRepo

	// Update with no field changes - should retrieve successfully
	resp, err := svc.UpdateWithdrawal(ctx, &pb.UpdateWithdrawalRequest{
		WithdrawalId: "WTH-UPD-001",
	})

	require.NoError(t, err, "UpdateWithdrawal should succeed for pending withdrawal")
	require.NotNil(t, resp.Withdrawal)
	assert.Equal(t, "WTH-UPD-001", resp.Withdrawal.WithdrawalId)
	assert.Equal(t, "ACC-UPD-001", resp.Withdrawal.AccountId)
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_INITIATED, resp.Withdrawal.Status)
	assert.True(t, resp.ValidationPassed, "No warnings expected when no updates requested")
	assert.Empty(t, resp.ValidationMessages)
}

// TestUpdateWithdrawal_Integration_ValidationWarningsForUnsupportedFields verifies that
// UpdateWithdrawal returns validation warnings when attempting to update fields that
// are not yet supported (amount, description, reference).
func TestUpdateWithdrawal_Integration_ValidationWarningsForUnsupportedFields(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	err := db.AutoMigrate(&persistence.WithdrawalEntity{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-UPD-002", 100000)

	amount, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(account.ID(), amount, "WTH-UPD-002")
	require.NoError(t, err)
	require.NoError(t, withdrawalRepo.Create(ctx, withdrawal))

	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-UPD-002": 100000,
	})
	svc.withdrawalRepo = withdrawalRepo

	// Request with unsupported field updates
	resp, err := svc.UpdateWithdrawal(ctx, &pb.UpdateWithdrawalRequest{
		WithdrawalId: "WTH-UPD-002",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        75,
				Nanos:        0,
			},
		},
		Description: "Updated description",
		Reference:   "NEW-REF",
	})

	require.NoError(t, err, "UpdateWithdrawal should succeed but with warnings")
	require.NotNil(t, resp.Withdrawal)
	assert.Equal(t, "WTH-UPD-002", resp.Withdrawal.WithdrawalId)
	assert.False(t, resp.ValidationPassed, "Validation should fail due to unsupported field warnings")
	assert.Len(t, resp.ValidationMessages, 3, "Should have 3 warnings: amount, description, reference")
	assert.Contains(t, resp.ValidationMessages[0], "amount updates are not yet supported")
	assert.Contains(t, resp.ValidationMessages[1], "description updates are not yet supported")
	assert.Contains(t, resp.ValidationMessages[2], "reference updates are not yet supported")
}

// TestUpdateWithdrawal_Integration_CannotUpdateNonPendingWithdrawal verifies that
// UpdateWithdrawal returns FailedPrecondition for withdrawals that are not in PENDING status.
func TestUpdateWithdrawal_Integration_CannotUpdateNonPendingWithdrawal(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	err := db.AutoMigrate(&persistence.WithdrawalEntity{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-UPD-003", 100000)

	// Create a withdrawal and transition it to COMPLETED
	amount, err := domain.NewMoney("GBP", 5000)
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(account.ID(), amount, "WTH-UPD-003")
	require.NoError(t, err)
	require.NoError(t, withdrawal.Complete())
	require.NoError(t, withdrawalRepo.Create(ctx, withdrawal))

	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-UPD-003": 100000,
	})
	svc.withdrawalRepo = withdrawalRepo

	// Attempt to update a completed withdrawal
	_, err = svc.UpdateWithdrawal(ctx, &pb.UpdateWithdrawalRequest{
		WithdrawalId: "WTH-UPD-003",
	})

	require.Error(t, err, "UpdateWithdrawal should fail for completed withdrawal")
	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "not in pending status")
	assert.Contains(t, st.Message(), "COMPLETED")
}

// TestUpdateWithdrawal_Integration_NotFound verifies that UpdateWithdrawal returns
// NotFound for a non-existent withdrawal_id.
func TestUpdateWithdrawal_Integration_NotFound(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	err := db.AutoMigrate(&persistence.WithdrawalEntity{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	svc := mustNewService(t, repo, nil)
	svc.withdrawalRepo = withdrawalRepo

	_, err = svc.UpdateWithdrawal(ctx, &pb.UpdateWithdrawalRequest{
		WithdrawalId: "WTH-NONEXISTENT",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "withdrawal not found")
}

// TestUpdateWithdrawal_Integration_MissingWithdrawalID verifies that UpdateWithdrawal
// returns InvalidArgument when withdrawal_id is empty.
func TestUpdateWithdrawal_Integration_MissingWithdrawalID(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	_, err := svc.UpdateWithdrawal(ctx, &pb.UpdateWithdrawalRequest{
		WithdrawalId: "",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "withdrawal_id is required")
}

// =============================================================================
// RetrieveWithdrawal Integration Tests (Task 7)
// =============================================================================

// TestRetrieveWithdrawal_Integration_SingleByWithdrawalID verifies retrieval
// of a single withdrawal by its withdrawal_id (reference).
func TestRetrieveWithdrawal_Integration_SingleByWithdrawalID(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	err := db.AutoMigrate(&persistence.WithdrawalEntity{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-RET-SINGLE-001", 100000)

	// Create a withdrawal
	amount, err := domain.NewMoney("GBP", 7500) // 75.00
	require.NoError(t, err)
	withdrawal, err := domain.NewWithdrawal(account.ID(), amount, "WTH-RET-001")
	require.NoError(t, err)
	require.NoError(t, withdrawalRepo.Create(ctx, withdrawal))

	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-RET-SINGLE-001": 100000,
	})
	svc.withdrawalRepo = withdrawalRepo

	resp, err := svc.RetrieveWithdrawal(ctx, &pb.RetrieveWithdrawalRequest{
		WithdrawalId: "WTH-RET-001",
	})

	require.NoError(t, err, "RetrieveWithdrawal by ID should succeed")
	require.NotNil(t, resp.Withdrawals)
	require.Len(t, resp.Withdrawals, 1)

	w := resp.Withdrawals[0]
	assert.Equal(t, "WTH-RET-001", w.WithdrawalId)
	assert.Equal(t, "ACC-RET-SINGLE-001", w.AccountId)
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_INITIATED, w.Status)
	assert.NotNil(t, w.Amount)
	assert.Equal(t, int64(75), w.Amount.Amount.Units)

	// Pagination should indicate single result
	require.NotNil(t, resp.Pagination)
	assert.Equal(t, int64(1), resp.Pagination.TotalCount)
}

// TestRetrieveWithdrawal_Integration_ListByAccountWithPagination verifies listing
// withdrawals by account_id with pagination (page_size=2, verify NextPageToken).
func TestRetrieveWithdrawal_Integration_ListByAccountWithPagination(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	err := db.AutoMigrate(&persistence.WithdrawalEntity{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	account := createTestAccountWithBalance(t, ctx, repo, "ACC-RET-PAGE-001", 100000)

	// Create 3 withdrawals for the account
	for i := 1; i <= 3; i++ {
		amount, err := domain.NewMoney("GBP", int64(i*1000))
		require.NoError(t, err)
		w, err := domain.NewWithdrawal(account.ID(), amount, fmt.Sprintf("WTH-PAGE-%03d", i))
		require.NoError(t, err)
		require.NoError(t, withdrawalRepo.Create(ctx, w))
	}

	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-RET-PAGE-001": 100000,
	})
	svc.withdrawalRepo = withdrawalRepo

	// Page 1: Request first 2 withdrawals
	resp1, err := svc.RetrieveWithdrawal(ctx, &pb.RetrieveWithdrawalRequest{
		AccountId: "ACC-RET-PAGE-001",
		Pagination: &commonpb.Pagination{
			PageSize: 2,
		},
	})

	require.NoError(t, err, "First page should succeed")
	require.Len(t, resp1.Withdrawals, 2, "First page should have 2 withdrawals")
	assert.Equal(t, int64(2), resp1.Pagination.TotalCount)
	assert.NotEmpty(t, resp1.Pagination.NextPageToken, "Should have next page token when more results exist")

	// Page 2: Use the next page token
	resp2, err := svc.RetrieveWithdrawal(ctx, &pb.RetrieveWithdrawalRequest{
		AccountId: "ACC-RET-PAGE-001",
		Pagination: &commonpb.Pagination{
			PageSize:  2,
			PageToken: resp1.Pagination.NextPageToken,
		},
	})

	require.NoError(t, err, "Second page should succeed")
	require.Len(t, resp2.Withdrawals, 1, "Second page should have 1 withdrawal (remainder)")
	assert.Equal(t, int64(1), resp2.Pagination.TotalCount)
	assert.Empty(t, resp2.Pagination.NextPageToken, "No next page token when this is the last page")

	// Verify all 3 withdrawal IDs are distinct across both pages
	allIDs := make(map[string]bool)
	for _, w := range resp1.Withdrawals {
		allIDs[w.WithdrawalId] = true
	}
	for _, w := range resp2.Withdrawals {
		allIDs[w.WithdrawalId] = true
	}
	assert.Len(t, allIDs, 3, "All 3 withdrawals should have distinct IDs across pages")
}

// TestRetrieveWithdrawal_Integration_EmptyListForAccountWithNoWithdrawals verifies
// that listing withdrawals for an account with no withdrawals returns an empty list.
func TestRetrieveWithdrawal_Integration_EmptyListForAccountWithNoWithdrawals(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	err := db.AutoMigrate(&persistence.WithdrawalEntity{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)
	_ = createTestAccountWithBalance(t, ctx, repo, "ACC-RET-EMPTY-001", 100000)

	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-RET-EMPTY-001": 100000,
	})
	svc.withdrawalRepo = withdrawalRepo

	resp, err := svc.RetrieveWithdrawal(ctx, &pb.RetrieveWithdrawalRequest{
		AccountId: "ACC-RET-EMPTY-001",
	})

	require.NoError(t, err, "Empty list should succeed")
	require.NotNil(t, resp.Withdrawals)
	assert.Len(t, resp.Withdrawals, 0, "Should return empty list")
	assert.Equal(t, int64(0), resp.Pagination.TotalCount)
	assert.Empty(t, resp.Pagination.NextPageToken)
}

// TestRetrieveWithdrawal_Integration_NotFoundForNonExistentWithdrawalID verifies
// that retrieving a non-existent withdrawal_id returns NotFound.
func TestRetrieveWithdrawal_Integration_NotFoundForNonExistentWithdrawalID(t *testing.T) {
	db, ctx, cleanup := setupIntegrationTestDB(t)
	defer cleanup()

	err := db.AutoMigrate(&persistence.WithdrawalEntity{})
	require.NoError(t, err)

	repo := persistence.NewRepository(db)
	withdrawalRepo := persistence.NewWithdrawalRepository(db)

	svc := mustNewService(t, repo, nil)
	svc.withdrawalRepo = withdrawalRepo

	_, err = svc.RetrieveWithdrawal(ctx, &pb.RetrieveWithdrawalRequest{
		WithdrawalId: "WTH-DOES-NOT-EXIST",
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "withdrawal not found")
}
