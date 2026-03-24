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

// validControlRequest returns a fully populated valid ControlFinancialBookingLogRequest.
func validControlRequest(bookingLogID uuid.UUID) *financialaccountingv1.ControlFinancialBookingLogRequest {
	return &financialaccountingv1.ControlFinancialBookingLogRequest{
		Id:            bookingLogID.String(),
		ControlAction: financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Compliance hold",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	}
}

// TestControlFinancialBookingLog_ResumeSuccess verifies that a suspended (FAILED status)
// booking log can be resumed to PENDING.
func TestControlFinancialBookingLog_ResumeSuccess(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	// Create a booking log in FAILED status (suspended state)
	bookingLogID := insertTestBookingLog(t, db, "FAILED")

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validControlRequest(bookingLogID)
	req.ControlAction = financialaccountingv1.ControlAction_CONTROL_ACTION_RESUME
	req.Reason = "Compliance hold lifted"

	resp, err := svc.ControlFinancialBookingLog(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.FinancialBookingLog)
	// RESUME transitions FAILED -> PENDING
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, resp.FinancialBookingLog.Status)
}

// TestControlFinancialBookingLog_TerminateFromFailed verifies that a FAILED booking log
// can be terminated (FAILED -> CANCELLED).
func TestControlFinancialBookingLog_TerminateFromFailed(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	bookingLogID := insertTestBookingLog(t, db, "FAILED")

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validControlRequest(bookingLogID)
	req.ControlAction = financialaccountingv1.ControlAction_CONTROL_ACTION_TERMINATE
	req.Reason = "Permanently closing this booking log"

	resp, err := svc.ControlFinancialBookingLog(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED, resp.FinancialBookingLog.Status)
}

// TestControlFinancialBookingLog_CannotResumePosted verifies that RESUME cannot be applied
// to a POSTED booking log (only FAILED/suspended can be resumed).
func TestControlFinancialBookingLog_CannotResumePosted(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	bookingLogID := insertTestBookingLog(t, db, "POSTED")

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validControlRequest(bookingLogID)
	req.ControlAction = financialaccountingv1.ControlAction_CONTROL_ACTION_RESUME
	req.Reason = "Attempting invalid resume"

	resp, err := svc.ControlFinancialBookingLog(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// TestControlFinancialBookingLog_CannotSuspendCancelled verifies that SUSPEND cannot be
// applied to a CANCELLED booking log (terminal state).
func TestControlFinancialBookingLog_CannotSuspendCancelled(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	bookingLogID := insertTestBookingLog(t, db, "CANCELLED")

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validControlRequest(bookingLogID)
	req.ControlAction = financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND
	req.Reason = "Attempting invalid suspend"

	resp, err := svc.ControlFinancialBookingLog(ctx, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// TestControlFinancialBookingLog_RespondsWithAuditInfo verifies that the control response
// includes the reason and updated status.
func TestControlFinancialBookingLog_RespondsWithAuditInfo(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	bookingLogID := insertTestBookingLog(t, db, "PENDING")

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	req := validControlRequest(bookingLogID)
	req.ControlAction = financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND
	req.Reason = "Audit review required"

	resp, err := svc.ControlFinancialBookingLog(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.FinancialBookingLog)
	// SUSPEND transitions PENDING -> FAILED
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED, resp.FinancialBookingLog.Status)
	assert.Equal(t, bookingLogID.String(), resp.FinancialBookingLog.Id)
}

// TestControlFinancialBookingLog_IdempotencyKeyVariants verifies multiple idempotency
// key validation scenarios.
func TestControlFinancialBookingLog_IdempotencyKeyVariants(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	cases := []struct {
		name string
		req  *financialaccountingv1.ControlFinancialBookingLogRequest
	}{
		{
			name: "nil idempotency key",
			req: &financialaccountingv1.ControlFinancialBookingLogRequest{
				Id:             uuid.New().String(),
				ControlAction:  financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND,
				Reason:         "test",
				IdempotencyKey: nil,
			},
		},
		{
			name: "empty idempotency key",
			req: &financialaccountingv1.ControlFinancialBookingLogRequest{
				Id:            uuid.New().String(),
				ControlAction: financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND,
				Reason:        "test",
				IdempotencyKey: &commonv1.IdempotencyKey{
					Key: "",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.ControlFinancialBookingLog(ctx, tc.req)
			require.Error(t, err)
			assert.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
		})
	}
}
