package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// TestListFinancialBookingLogs_EmptyResult verifies that an empty database returns
// an empty list without error.
func TestListFinancialBookingLogs_EmptyResult(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	resp, err := svc.ListFinancialBookingLogs(ctx, &financialaccountingv1.ListFinancialBookingLogsRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Empty(t, resp.FinancialBookingLogs)
}

// TestListFinancialBookingLogs_InvalidPageSize verifies that page_size of 0 returns
// InvalidArgument when pagination is explicitly provided.
func TestListFinancialBookingLogs_InvalidPageSize(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	resp, err := svc.ListFinancialBookingLogs(ctx, &financialaccountingv1.ListFinancialBookingLogsRequest{
		Pagination: &commonv1.Pagination{
			PageSize: 0,
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestListFinancialBookingLogs_PageSizeTooLarge verifies that page_size > 1000 is rejected.
func TestListFinancialBookingLogs_PageSizeTooLarge(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	resp, err := svc.ListFinancialBookingLogs(ctx, &financialaccountingv1.ListFinancialBookingLogsRequest{
		Pagination: &commonv1.Pagination{
			PageSize: 9999,
		},
	})

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "page_size")
}

// TestListFinancialBookingLogs_WithStatusFilter verifies that status filtering returns
// only matching booking logs.
func TestListFinancialBookingLogs_WithStatusFilter(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	// Create booking logs with different statuses
	insertTestBookingLog(t, db, "PENDING")
	insertTestBookingLog(t, db, "PENDING")
	insertTestBookingLog(t, db, "POSTED")

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	resp, err := svc.ListFinancialBookingLogs(ctx, &financialaccountingv1.ListFinancialBookingLogsRequest{
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.FinancialBookingLogs, 2, "should return only PENDING booking logs")
	for _, log := range resp.FinancialBookingLogs {
		assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, log.Status)
	}
}

// TestListFinancialBookingLogs_WithBusinessUnitFilter verifies that business unit
// filtering returns only matching records.
func TestListFinancialBookingLogs_WithBusinessUnitFilter(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert two booking logs with the same business unit and one with a different one
	buA := "BU-ALPHA"
	buB := "BU-BETA"
	insertBookingLogWithBU(t, db, buA)
	insertBookingLogWithBU(t, db, buA)
	insertBookingLogWithBU(t, db, buB)

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	resp, err := svc.ListFinancialBookingLogs(ctx, &financialaccountingv1.ListFinancialBookingLogsRequest{
		BusinessUnitReference: buA,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.FinancialBookingLogs, 2)
	for _, log := range resp.FinancialBookingLogs {
		assert.Equal(t, buA, log.BusinessUnitReference)
	}
}

// TestListFinancialBookingLogs_PaginationResponsePopulated verifies that the pagination
// response is populated for paginated results.
func TestListFinancialBookingLogs_PaginationResponsePopulated(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	// Create 5 booking logs
	for i := 0; i < 5; i++ {
		insertTestBookingLog(t, db, "PENDING")
	}

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	resp, err := svc.ListFinancialBookingLogs(ctx, &financialaccountingv1.ListFinancialBookingLogsRequest{
		Pagination: &commonv1.Pagination{
			PageSize: 2, // Request only 2 of 5
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.FinancialBookingLogs, 2)
	require.NotNil(t, resp.Pagination)
	assert.NotEmpty(t, resp.Pagination.NextPageToken, "next page token should be set when more results exist")
}

// TestListLedgerPostings_EmptyResult verifies that an empty database returns
// an empty list without error.
func TestListLedgerPostings_EmptyResult(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	resp, err := svc.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Empty(t, resp.LedgerPostings)
}

// TestListLedgerPostings_FilterByBookingLogID verifies that postings can be filtered
// by booking log ID.
func TestListLedgerPostings_FilterByBookingLogID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	bookingLogID := insertTestBookingLog(t, db, "PENDING")
	otherBookingLogID := insertTestBookingLog(t, db, "PENDING")

	// Insert postings for bookingLogID
	insertTestPosting(t, db, bookingLogID, "DEBIT")
	insertTestPosting(t, db, bookingLogID, "CREDIT")
	// Insert a posting for the other booking log (should not appear)
	insertTestPosting(t, db, otherBookingLogID, "DEBIT")

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	resp, err := svc.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{
		FinancialBookingLogId: bookingLogID.String(),
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.LedgerPostings, 2, "should return only postings for the specified booking log")
	for _, p := range resp.LedgerPostings {
		assert.Equal(t, bookingLogID.String(), p.FinancialBookingLogId)
	}
}

// TestListLedgerPostings_FilterByDirection verifies that postings can be filtered
// by posting direction.
func TestListLedgerPostings_FilterByDirection(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	bookingLogID := insertTestBookingLog(t, db, "PENDING")
	insertTestPosting(t, db, bookingLogID, "DEBIT")
	insertTestPosting(t, db, bookingLogID, "DEBIT")
	insertTestPosting(t, db, bookingLogID, "CREDIT")

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	resp, err := svc.ListLedgerPostings(ctx, &financialaccountingv1.ListLedgerPostingsRequest{
		PostingDirection: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.LedgerPostings, 2, "should return only DEBIT postings")
	for _, p := range resp.LedgerPostings {
		assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, p.PostingDirection)
	}
}

// insertBookingLogWithBU inserts a booking log with the specified business unit reference.
func insertBookingLogWithBU(t *testing.T, db *gorm.DB, bu string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	entity := persistence.FinancialBookingLogEntity{
		ID:                      id,
		FinancialAccountType:    "CURRENT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   bu,
		ChartOfAccountsRules:    "STANDARD",
		BaseCurrency:            "GBP",
		Status:                  "PENDING",
		IdempotencyKey:          uuid.New().String(),
		CreatedAt:               time.Now().UTC(),
		UpdatedAt:               time.Now().UTC(),
		Version:                 1,
	}
	require.NoError(t, db.Create(&entity).Error)
	return id
}

// insertTestPosting inserts a LedgerPostingEntity with the given booking log ID and direction.
func insertTestPosting(t *testing.T, db *gorm.DB, bookingLogID uuid.UUID, direction string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	entity := persistence.LedgerPostingEntity{
		ID:                    id,
		FinancialBookingLogID: bookingLogID,
		PostingDirection:      direction,
		AmountMinorUnits:      10000,
		Currency:              "GBP",
		AccountID:             "ACC-" + id.String()[:8],
		ValueDate:             time.Now().UTC(),
		PostingResult:         "success",
		Status:                "PENDING",
		CreatedAt:             time.Now().UTC(),
	}
	require.NoError(t, db.Create(&entity).Error)
	return id
}
