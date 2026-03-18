package persistence

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// newTestBookingLog creates a domain FinancialBookingLog for tests.
func newTestBookingLog() *domain.FinancialBookingLog {
	return domain.NewFinancialBookingLog(
		"ASSET",
		"PROD-001",
		"BU-TREASURY",
		"UK-GAAP-2024",
		domain.CurrencyGBP,
	)
}

// TestSaveBookingLog_Success verifies booking logs can be persisted.
func TestSaveBookingLog_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	log := newTestBookingLog()
	idempotencyKey := "save-test-" + uuid.New().String()

	err := repo.SaveBookingLog(ctx, log, idempotencyKey)
	assert.NoError(t, err)

	// Verify it was persisted by retrieving it
	retrieved, err := repo.GetBookingLog(ctx, log.ID)
	require.NoError(t, err)
	assert.Equal(t, log.ID, retrieved.ID)
	assert.Equal(t, log.FinancialAccountType, retrieved.FinancialAccountType)
	assert.Equal(t, log.BaseCurrency, retrieved.BaseCurrency)
	assert.Equal(t, domain.TransactionStatusPending, retrieved.Status)
}

// TestGetBookingLog_NotFound verifies ErrBookingLogNotFound is returned for missing IDs.
func TestGetBookingLog_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	_, err := repo.GetBookingLog(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrBookingLogNotFound)
}

// TestSaveBookingLog_DuplicateIdempotencyKey verifies idempotency key collision is caught.
func TestSaveBookingLog_DuplicateIdempotencyKey(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	idempotencyKey := "idempotency-dup-" + uuid.New().String()

	log1 := newTestBookingLog()
	log2 := newTestBookingLog()

	require.NoError(t, repo.SaveBookingLog(ctx, log1, idempotencyKey))

	err := repo.SaveBookingLog(ctx, log2, idempotencyKey)
	assert.ErrorIs(t, err, ErrDuplicateIdempotencyKey)
}

// TestUpdateBookingLog_Success verifies booking logs can be updated.
func TestUpdateBookingLog_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	log := newTestBookingLog()
	idempotencyKey := "update-test-" + uuid.New().String()

	require.NoError(t, repo.SaveBookingLog(ctx, log, idempotencyKey))

	// Update status to POSTED and chart of accounts rules
	updated := log.WithStatus(domain.TransactionStatusPosted).
		WithChartOfAccountsRules("IFRS-2024")

	err := repo.UpdateBookingLog(ctx, &updated)
	require.NoError(t, err)

	// Verify the update persisted
	retrieved, err := repo.GetBookingLog(ctx, log.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TransactionStatusPosted, retrieved.Status)
	assert.Equal(t, "IFRS-2024", retrieved.ChartOfAccountsRules)
}

// TestUpdateBookingLog_NotFound verifies ErrBookingLogNotFound when updating missing log.
func TestUpdateBookingLog_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	log := newTestBookingLog()

	err := repo.UpdateBookingLog(ctx, log)
	assert.ErrorIs(t, err, ErrBookingLogNotFound)
}

// TestUpdatePosting_Success verifies postings can be updated.
func TestUpdatePosting_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	// Create a booking log first
	log := newTestBookingLog()
	require.NoError(t, repo.SaveBookingLog(ctx, log, "update-posting-"+uuid.New().String()))

	// Create a posting
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	money := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)
	posting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: log.ID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                money,
		AccountID:             "ACC-001",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPending,
		CorrelationID:         "CORR-001",
		CreatedAt:             time.Now(),
	}

	require.NoError(t, repo.SavePosting(ctx, posting))

	// Update the posting status
	posting.Status = domain.TransactionStatusPosted
	posting.PostingResult = "Processed successfully"
	err := repo.UpdatePosting(ctx, posting)
	require.NoError(t, err)

	// Verify update was persisted
	retrieved, err := repo.GetPosting(ctx, posting.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TransactionStatusPosted, retrieved.Status)
	assert.Equal(t, "Processed successfully", retrieved.PostingResult)
}

// TestUpdatePosting_NotFound verifies ErrPostingNotFound when updating missing posting.
func TestUpdatePosting_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	money := domain.NewMoney(decimal.NewFromInt(50), gbpInstrument)

	nonExistentPosting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: uuid.New(),
		Direction:             domain.PostingDirectionCredit,
		Amount:                money,
		AccountID:             "ACC-999",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPosted,
	}

	err := repo.UpdatePosting(ctx, nonExistentPosting)
	assert.ErrorIs(t, err, ErrPostingNotFound)
}

// TestListBookingLogs_EmptyResult verifies empty result when no logs exist.
func TestListBookingLogs_EmptyResult(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	result, err := repo.ListBookingLogs(ctx, ListBookingLogsParams{})
	require.NoError(t, err)
	assert.Empty(t, result.BookingLogs)
	assert.Equal(t, int64(0), result.TotalCount)
	assert.Empty(t, result.NextPageToken)
}

// TestListBookingLogs_WithResults verifies basic listing works.
func TestListBookingLogs_WithResults(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	// Create 3 booking logs
	for i := 0; i < 3; i++ {
		log := newTestBookingLog()
		require.NoError(t, repo.SaveBookingLog(ctx, log, fmt.Sprintf("list-test-%d-%s", i, uuid.New().String())))
	}

	result, err := repo.ListBookingLogs(ctx, ListBookingLogsParams{PageSize: 10})
	require.NoError(t, err)
	assert.Len(t, result.BookingLogs, 3)
	assert.Equal(t, int64(3), result.TotalCount)
	assert.Empty(t, result.NextPageToken)
}

// TestListBookingLogs_Pagination verifies pagination generates next page tokens.
func TestListBookingLogs_Pagination(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	// Create 5 booking logs with explicit timestamps to ensure deterministic ordering.
	// Timestamps are spaced 1 second apart since applyCursorPagination truncates to seconds.
	baseTime := time.Now().UTC()
	for i := 0; i < 5; i++ {
		log := newTestBookingLog()
		log.CreatedAt = baseTime.Add(time.Duration(i) * time.Second)
		log.UpdatedAt = log.CreatedAt
		require.NoError(t, repo.SaveBookingLog(ctx, log, fmt.Sprintf("paginate-%d-%s", i, uuid.New().String())))
	}

	// Get first page of 3
	firstPage, err := repo.ListBookingLogs(ctx, ListBookingLogsParams{PageSize: 3})
	require.NoError(t, err)
	assert.Len(t, firstPage.BookingLogs, 3)
	assert.Equal(t, int64(5), firstPage.TotalCount)
	assert.NotEmpty(t, firstPage.NextPageToken)

	// Get second page using next page token
	secondPage, err := repo.ListBookingLogs(ctx, ListBookingLogsParams{
		PageSize:  3,
		PageToken: firstPage.NextPageToken,
	})
	require.NoError(t, err)
	assert.Len(t, secondPage.BookingLogs, 2)
	assert.Empty(t, secondPage.NextPageToken)
}

// TestListBookingLogs_InvalidPageToken verifies invalid tokens are rejected.
func TestListBookingLogs_InvalidPageToken(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	_, err := repo.ListBookingLogs(ctx, ListBookingLogsParams{
		PageToken: "invalid-token",
	})
	assert.ErrorIs(t, err, ErrInvalidPageToken)
}

// TestListBookingLogs_StatusFilter verifies status filtering works.
func TestListBookingLogs_StatusFilter(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	// Create one PENDING log
	pendingLog := newTestBookingLog()
	require.NoError(t, repo.SaveBookingLog(ctx, pendingLog, "status-filter-pending-"+uuid.New().String()))

	// Create one POSTED log
	postedLog := newTestBookingLog()
	require.NoError(t, repo.SaveBookingLog(ctx, postedLog, "status-filter-posted-"+uuid.New().String()))
	postedUpdated := postedLog.WithStatus(domain.TransactionStatusPosted)
	require.NoError(t, repo.UpdateBookingLog(ctx, &postedUpdated))

	// Filter by PENDING
	result, err := repo.ListBookingLogs(ctx, ListBookingLogsParams{
		StatusFilter: string(domain.TransactionStatusPending),
	})
	require.NoError(t, err)
	assert.Len(t, result.BookingLogs, 1)
	assert.Equal(t, pendingLog.ID, result.BookingLogs[0].ID)
}

// TestListBookingLogs_BusinessUnitFilter verifies business unit filtering works.
func TestListBookingLogs_BusinessUnitFilter(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	// Create log for treasury
	treasuryLog := domain.NewFinancialBookingLog("ASSET", "PROD-001", "BU-TREASURY", "UK-GAAP", domain.CurrencyGBP)
	require.NoError(t, repo.SaveBookingLog(ctx, treasuryLog, "bu-treasury-"+uuid.New().String()))

	// Create log for lending
	lendingLog := domain.NewFinancialBookingLog("LIABILITY", "PROD-002", "BU-LENDING", "UK-GAAP", domain.CurrencyGBP)
	require.NoError(t, repo.SaveBookingLog(ctx, lendingLog, "bu-lending-"+uuid.New().String()))

	result, err := repo.ListBookingLogs(ctx, ListBookingLogsParams{
		BusinessUnitFilter: "BU-TREASURY",
	})
	require.NoError(t, err)
	assert.Len(t, result.BookingLogs, 1)
	assert.Equal(t, "BU-TREASURY", result.BookingLogs[0].BusinessUnitReference)
}

// TestListPostings_EmptyResult verifies empty result when no postings exist.
func TestListPostings_EmptyResult(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	result, err := repo.ListPostings(ctx, ListPostingsParams{})
	require.NoError(t, err)
	assert.Empty(t, result.Postings)
	assert.Equal(t, int64(0), result.TotalCount)
}

// TestListPostings_WithResults verifies basic posting listing works.
func TestListPostings_WithResults(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	// Create a booking log and two postings
	log := newTestBookingLog()
	require.NoError(t, repo.SaveBookingLog(ctx, log, "list-postings-"+uuid.New().String()))

	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	money := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)

	debitPosting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: log.ID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                money,
		AccountID:             "ACC-001",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPosted,
		CreatedAt:             time.Now(),
	}
	creditPosting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: log.ID,
		Direction:             domain.PostingDirectionCredit,
		Amount:                money,
		AccountID:             "ACC-002",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPosted,
		CreatedAt:             time.Now(),
	}

	require.NoError(t, repo.SavePosting(ctx, debitPosting))
	require.NoError(t, repo.SavePosting(ctx, creditPosting))

	result, err := repo.ListPostings(ctx, ListPostingsParams{})
	require.NoError(t, err)
	assert.Len(t, result.Postings, 2)
	assert.Equal(t, int64(2), result.TotalCount)
}

// TestListPostings_ByBookingLogID verifies filtering by booking log ID.
func TestListPostings_ByBookingLogID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	// Create two booking logs with postings
	log1 := newTestBookingLog()
	log2 := newTestBookingLog()
	require.NoError(t, repo.SaveBookingLog(ctx, log1, "bl-filter-1-"+uuid.New().String()))
	require.NoError(t, repo.SaveBookingLog(ctx, log2, "bl-filter-2-"+uuid.New().String()))

	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	money := domain.NewMoney(decimal.NewFromInt(50), gbpInstrument)

	posting1 := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: log1.ID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                money,
		AccountID:             "ACC-001",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPosted,
		CreatedAt:             time.Now(),
	}
	posting2 := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: log2.ID,
		Direction:             domain.PostingDirectionCredit,
		Amount:                money,
		AccountID:             "ACC-002",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPosted,
		CreatedAt:             time.Now(),
	}

	require.NoError(t, repo.SavePosting(ctx, posting1))
	require.NoError(t, repo.SavePosting(ctx, posting2))

	// Filter by log1 ID
	result, err := repo.ListPostings(ctx, ListPostingsParams{BookingLogID: &log1.ID})
	require.NoError(t, err)
	assert.Len(t, result.Postings, 1)
	assert.Equal(t, posting1.ID, result.Postings[0].ID)
}

// TestListPostings_InvalidPageToken verifies invalid tokens are rejected.
func TestListPostings_InvalidPageToken(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	_, err := repo.ListPostings(ctx, ListPostingsParams{PageToken: "bad-token"})
	assert.ErrorIs(t, err, ErrInvalidPageToken)
}

// TestListPostings_DirectionFilter verifies posting direction filter works.
func TestListPostings_DirectionFilter(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)

	log := newTestBookingLog()
	require.NoError(t, repo.SaveBookingLog(ctx, log, "dir-filter-"+uuid.New().String()))

	gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	money := domain.NewMoney(decimal.NewFromInt(200), gbpInstrument)

	debitPosting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: log.ID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                money,
		AccountID:             "ACC-D",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPosted,
		CreatedAt:             time.Now(),
	}
	creditPosting := &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: log.ID,
		Direction:             domain.PostingDirectionCredit,
		Amount:                money,
		AccountID:             "ACC-C",
		ValueDate:             time.Now(),
		Status:                domain.TransactionStatusPosted,
		CreatedAt:             time.Now(),
	}

	require.NoError(t, repo.SavePosting(ctx, debitPosting))
	require.NoError(t, repo.SavePosting(ctx, creditPosting))

	// Filter for DEBIT only
	result, err := repo.ListPostings(ctx, ListPostingsParams{
		PostingDirection: string(domain.PostingDirectionDebit),
	})
	require.NoError(t, err)
	assert.Len(t, result.Postings, 1)
	assert.Equal(t, domain.PostingDirectionDebit, result.Postings[0].Direction)
}

// TestWithTransaction_Success verifies the WithTransaction helper executes within a transaction.
func TestWithTransaction_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	log := newTestBookingLog()

	err := repo.WithTransaction(ctx, func(tx *gorm.DB) error {
		entity := toBookingLogEntity(log, "wt-test-"+uuid.New().String())
		return tx.Create(&entity).Error
	})
	require.NoError(t, err)

	// Verify the booking log was actually saved
	retrieved, err := repo.GetBookingLog(ctx, log.ID)
	require.NoError(t, err)
	assert.Equal(t, log.ID, retrieved.ID)
}

// TestWithTransaction_Rollback verifies that errors cause transaction rollback.
func TestWithTransaction_Rollback(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	log := newTestBookingLog()
	idempotencyKey := "rollback-test-" + uuid.New().String()

	testErr := fmt.Errorf("forced error for rollback test")
	err := repo.WithTransaction(ctx, func(tx *gorm.DB) error {
		entity := toBookingLogEntity(log, idempotencyKey)
		if err := tx.Create(&entity).Error; err != nil {
			return err
		}
		return testErr // Force rollback
	})
	assert.ErrorIs(t, err, testErr)

	// Booking log should NOT have been saved (rolled back)
	_, err = repo.GetBookingLog(ctx, log.ID)
	assert.ErrorIs(t, err, ErrBookingLogNotFound)
}

// TestDB_ReturnsDatabase verifies DB() returns a non-nil *gorm.DB.
func TestDB_ReturnsDatabase(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewLedgerRepository(db)
	assert.NotNil(t, repo.DB())
}
