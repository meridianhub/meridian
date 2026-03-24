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
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// validCaptureRequest returns a fully populated valid CaptureLedgerPostingRequest.
func validCaptureRequest(bookingLogID uuid.UUID) *financialaccountingv1.CaptureLedgerPostingRequest {
	return &financialaccountingv1.CaptureLedgerPostingRequest{
		FinancialBookingLogId: bookingLogID.String(),
		PostingAmount: &money.Money{
			CurrencyCode: "GBP",
			Units:        100,
		},
		PostingDirection: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		AccountId:        "ACC-001",
		ValueDate:        timestamppb.New(time.Now().UTC()),
	}
}

// TestCaptureLedgerPosting_InvalidBookingLogID verifies that a malformed booking log ID
// returns InvalidArgument.
func TestCaptureLedgerPosting_InvalidBookingLogID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validCaptureRequest(uuid.New())
	req.FinancialBookingLogId = "not-a-uuid"

	resp, err := svc.CaptureLedgerPosting(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "financial_booking_log_id")
}

// TestCaptureLedgerPosting_MissingAccountID verifies that a missing account ID
// returns InvalidArgument.
func TestCaptureLedgerPosting_MissingAccountID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validCaptureRequest(uuid.New())
	req.AccountId = ""

	resp, err := svc.CaptureLedgerPosting(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "account_id")
}

// TestCaptureLedgerPosting_UnspecifiedDirection verifies that an unspecified posting direction
// returns InvalidArgument.
func TestCaptureLedgerPosting_UnspecifiedDirection(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validCaptureRequest(uuid.New())
	req.PostingDirection = commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED

	resp, err := svc.CaptureLedgerPosting(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "posting_direction")
}

// TestCaptureLedgerPosting_MissingValueDate verifies that a nil value date
// returns InvalidArgument.
func TestCaptureLedgerPosting_MissingValueDate(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validCaptureRequest(uuid.New())
	req.ValueDate = nil

	resp, err := svc.CaptureLedgerPosting(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "value_date")
}

// TestCaptureLedgerPosting_ZeroAmount verifies that a zero posting amount
// returns InvalidArgument (domain validation rejects zero amounts).
func TestCaptureLedgerPosting_ZeroAmount(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validCaptureRequest(uuid.New())
	req.PostingAmount = &money.Money{
		CurrencyCode: "GBP",
		Units:        0,
		Nanos:        0,
	}

	resp, err := svc.CaptureLedgerPosting(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestCaptureLedgerPosting_SuccessDebit verifies that a valid DEBIT posting is persisted and returned.
func TestCaptureLedgerPosting_SuccessDebit(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	// Create the booking log first so the foreign key constraint is satisfied
	bookingLogID := insertTestBookingLog(t, db, "PENDING")

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validCaptureRequest(bookingLogID)
	resp, err := svc.CaptureLedgerPosting(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.LedgerPosting)
	assert.NotEmpty(t, resp.LedgerPosting.Id)
	assert.Equal(t, bookingLogID.String(), resp.LedgerPosting.FinancialBookingLogId)
	assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, resp.LedgerPosting.PostingDirection)
	assert.Equal(t, req.AccountId, resp.LedgerPosting.AccountId)
}

// TestCaptureLedgerPosting_SuccessCredit verifies that a CREDIT posting is accepted.
func TestCaptureLedgerPosting_SuccessCredit(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	bookingLogID := insertTestBookingLog(t, db, "PENDING")

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validCaptureRequest(bookingLogID)
	req.PostingDirection = commonv1.PostingDirection_POSTING_DIRECTION_CREDIT

	resp, err := svc.CaptureLedgerPosting(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_CREDIT, resp.LedgerPosting.PostingDirection)
}

// TestUpdateLedgerPosting_MissingIdempotencyKey verifies that a missing idempotency key
// returns InvalidArgument before any business logic executes.
func TestUpdateLedgerPosting_MissingIdempotencyKey(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	cases := []struct {
		name string
		req  *financialaccountingv1.UpdateLedgerPostingRequest
	}{
		{
			name: "nil idempotency key",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:             uuid.New().String(),
				IdempotencyKey: nil,
				Status:         commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			},
		},
		{
			name: "empty idempotency key string",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:             uuid.New().String(),
				IdempotencyKey: &commonv1.IdempotencyKey{Key: ""},
				Status:         commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.UpdateLedgerPosting(ctx, tc.req)
			require.Error(t, err)
			assert.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
		})
	}
}

// TestUpdateLedgerPosting_InvalidPostingID verifies that a malformed posting ID
// returns InvalidArgument.
func TestUpdateLedgerPosting_InvalidPostingID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := &financialaccountingv1.UpdateLedgerPostingRequest{
		Id: "not-a-valid-uuid",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
	}

	resp, err := svc.UpdateLedgerPosting(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestUpdateLedgerPosting_NotFound verifies that updating a non-existent posting
// returns NotFound.
func TestUpdateLedgerPosting_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := &financialaccountingv1.UpdateLedgerPostingRequest{
		Id: uuid.New().String(), // Non-existent
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
	}

	resp, err := svc.UpdateLedgerPosting(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestUpdateLedgerPosting_UnspecifiedStatus verifies that an unspecified status
// returns InvalidArgument.
func TestUpdateLedgerPosting_UnspecifiedStatus(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := &financialaccountingv1.UpdateLedgerPostingRequest{
		Id: uuid.New().String(),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED,
	}

	resp, err := svc.UpdateLedgerPosting(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

