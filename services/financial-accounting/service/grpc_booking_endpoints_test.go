package service

import (
	"testing"

	"github.com/google/uuid"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// validInitiateRequest returns a fully populated valid InitiateFinancialBookingLogRequest.
func validInitiateRequest() *financialaccountingv1.InitiateFinancialBookingLogRequest {
	return &financialaccountingv1.InitiateFinancialBookingLogRequest{
		FinancialAccountType:    "CURRENT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "IFRS",
		BaseInstrumentCode:      "GBP",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	}
}

// TestInitiateFinancialBookingLog_MissingIdempotencyKey validates that a missing
// idempotency key is rejected with InvalidArgument.
func TestInitiateFinancialBookingLog_MissingIdempotencyKey(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	cases := []struct {
		name string
		req  *financialaccountingv1.InitiateFinancialBookingLogRequest
	}{
		{
			name: "nil idempotency key",
			req: func() *financialaccountingv1.InitiateFinancialBookingLogRequest {
				r := validInitiateRequest()
				r.IdempotencyKey = nil
				return r
			}(),
		},
		{
			name: "empty idempotency key",
			req: func() *financialaccountingv1.InitiateFinancialBookingLogRequest {
				r := validInitiateRequest()
				r.IdempotencyKey = &commonv1.IdempotencyKey{Key: ""}
				return r
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.InitiateFinancialBookingLog(ctx, tc.req)
			require.Error(t, err)
			assert.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
		})
	}
}

// TestInitiateFinancialBookingLog_RequiredFieldValidation verifies that each required
// field is individually validated and returns InvalidArgument when missing.
func TestInitiateFinancialBookingLog_RequiredFieldValidation(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	cases := []struct {
		name    string
		mutate  func(*financialaccountingv1.InitiateFinancialBookingLogRequest)
		wantMsg string
	}{
		{
			name: "missing financial_account_type",
			mutate: func(r *financialaccountingv1.InitiateFinancialBookingLogRequest) {
				r.FinancialAccountType = ""
			},
			wantMsg: "financial_account_type",
		},
		{
			name: "missing product_service_reference",
			mutate: func(r *financialaccountingv1.InitiateFinancialBookingLogRequest) {
				r.ProductServiceReference = ""
			},
			wantMsg: "product_service_reference",
		},
		{
			name: "missing business_unit_reference",
			mutate: func(r *financialaccountingv1.InitiateFinancialBookingLogRequest) {
				r.BusinessUnitReference = ""
			},
			wantMsg: "business_unit_reference",
		},
		{
			name: "missing chart_of_accounts_rules",
			mutate: func(r *financialaccountingv1.InitiateFinancialBookingLogRequest) {
				r.ChartOfAccountsRules = ""
			},
			wantMsg: "chart_of_accounts_rules",
		},
		{
			name: "missing base_instrument_code",
			mutate: func(r *financialaccountingv1.InitiateFinancialBookingLogRequest) {
				r.BaseInstrumentCode = ""
			},
			wantMsg: "base_instrument_code",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validInitiateRequest()
			// Use unique idempotency key per sub-test
			req.IdempotencyKey = &commonv1.IdempotencyKey{Key: uuid.New().String()}
			tc.mutate(req)

			resp, err := svc.InitiateFinancialBookingLog(ctx, req)
			require.Error(t, err)
			assert.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code(), "expected InvalidArgument for %s", tc.name)
			assert.Contains(t, st.Message(), tc.wantMsg, "error message should reference the missing field")
		})
	}
}

// TestInitiateFinancialBookingLog_Success verifies that a valid request creates a booking log.
func TestInitiateFinancialBookingLog_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validInitiateRequest()
	resp, err := svc.InitiateFinancialBookingLog(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.FinancialBookingLog)
	assert.NotEmpty(t, resp.FinancialBookingLog.Id, "booking log ID must be set")
	assert.Equal(t, req.ProductServiceReference, resp.FinancialBookingLog.ProductServiceReference)
	assert.Equal(t, req.BusinessUnitReference, resp.FinancialBookingLog.BusinessUnitReference)
	assert.Equal(t, req.BaseInstrumentCode, resp.FinancialBookingLog.BaseInstrumentCode)
	// New booking logs start in PENDING status
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, resp.FinancialBookingLog.Status)
}

// TestInitiateFinancialBookingLog_DuplicateIdempotencyKey verifies that a duplicate
// idempotency key results in AlreadyExists.
func TestInitiateFinancialBookingLog_DuplicateIdempotencyKey(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validInitiateRequest()

	// First request should succeed
	resp1, err := svc.InitiateFinancialBookingLog(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp1)

	// Second request with same idempotency key should fail
	resp2, err := svc.InitiateFinancialBookingLog(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp2)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.AlreadyExists, st.Code())
}

// TestUpdateFinancialBookingLog_MissingIdempotencyKey verifies that a missing idempotency key
// is rejected before any business logic executes.
func TestUpdateFinancialBookingLog_MissingIdempotencyKey(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	cases := []struct {
		name string
		req  *financialaccountingv1.UpdateFinancialBookingLogRequest
	}{
		{
			name: "nil idempotency key",
			req: &financialaccountingv1.UpdateFinancialBookingLogRequest{
				Id:             uuid.New().String(),
				IdempotencyKey: nil,
				Status:         commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			},
		},
		{
			name: "empty idempotency key string",
			req: &financialaccountingv1.UpdateFinancialBookingLogRequest{
				Id:             uuid.New().String(),
				IdempotencyKey: &commonv1.IdempotencyKey{Key: ""},
				Status:         commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.UpdateFinancialBookingLog(ctx, tc.req)
			require.Error(t, err)
			assert.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
		})
	}
}

// TestUpdateFinancialBookingLog_InvalidID verifies that a malformed booking log ID
// returns InvalidArgument.
func TestUpdateFinancialBookingLog_InvalidID(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id: "not-a-valid-uuid",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
	}

	resp, err := svc.UpdateFinancialBookingLog(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestUpdateFinancialBookingLog_UnspecifiedStatus verifies that an unspecified status
// returns InvalidArgument.
func TestUpdateFinancialBookingLog_UnspecifiedStatus(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id: uuid.New().String(),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED,
	}

	resp, err := svc.UpdateFinancialBookingLog(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestUpdateFinancialBookingLog_NotFound verifies that updating a non-existent booking log
// returns NotFound.
func TestUpdateFinancialBookingLog_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id: uuid.New().String(), // Non-existent ID
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
	}

	resp, err := svc.UpdateFinancialBookingLog(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestUpdateFinancialBookingLog_InvalidTransition verifies that invalid state transitions
// return FailedPrecondition.
func TestUpdateFinancialBookingLog_InvalidTransition(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	// Create a booking log in CANCELLED state (terminal)
	bookingLogID := insertTestBookingLog(t, db, "CANCELLED")

	req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id: bookingLogID.String(),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
	}

	resp, err := svc.UpdateFinancialBookingLog(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// TestUpdateFinancialBookingLog_PendingToFailed verifies a valid PENDING->FAILED transition.
func TestUpdateFinancialBookingLog_PendingToFailed(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	bookingLogID := insertTestBookingLog(t, db, "PENDING")

	req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id: bookingLogID.String(),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED,
	}

	resp, err := svc.UpdateFinancialBookingLog(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.FinancialBookingLog)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED, resp.FinancialBookingLog.Status)
}

// TestUpdateFinancialBookingLog_PendingToCancelled verifies a valid PENDING->CANCELLED transition.
func TestUpdateFinancialBookingLog_PendingToCancelled(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	bookingLogID := insertTestBookingLog(t, db, "PENDING")

	req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id: bookingLogID.String(),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
		Status: commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
	}

	resp, err := svc.UpdateFinancialBookingLog(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED, resp.FinancialBookingLog.Status)
}

// TestUpdateFinancialBookingLog_UpdatesChartOfAccountsRules verifies that chart of accounts
// rules can be updated during a status transition.
func TestUpdateFinancialBookingLog_UpdatesChartOfAccountsRules(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	bookingLogID := insertTestBookingLog(t, db, "PENDING")
	newRules := "UPDATED-GAAP-RULES"

	req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
		Id: bookingLogID.String(),
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
		Status:               commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED,
		ChartOfAccountsRules: newRules,
	}

	resp, err := svc.UpdateFinancialBookingLog(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, newRules, resp.FinancialBookingLog.ChartOfAccountsRules)
}
