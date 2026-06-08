package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
)

// --- applyPostingStatusTransition ---

// newTestPosting builds a minimal ledger posting in the given status for transition tests.
func newTestPosting(status domain.TransactionStatus) *domain.LedgerPosting {
	return &domain.LedgerPosting{
		ID:                    uuid.New(),
		FinancialBookingLogID: uuid.New(),
		Direction:             domain.PostingDirectionDebit,
		Status:                status,
	}
}

func TestApplyPostingStatusTransition(t *testing.T) {
	tests := []struct {
		name           string
		from           domain.TransactionStatus
		to             domain.TransactionStatus
		postingResult  string
		wantErr        bool
		wantCode       codes.Code
		wantStatus     domain.TransactionStatus
		wantResult     string // expected PostingResult when success path sets it
		checkResultSet bool
	}{
		// POSTED target
		{
			name:           "pending to posted succeeds",
			from:           domain.TransactionStatusPending,
			to:             domain.TransactionStatusPosted,
			postingResult:  "ok",
			wantStatus:     domain.TransactionStatusPosted,
			wantResult:     "ok",
			checkResultSet: true,
		},
		{
			name:     "already posted to posted fails precondition",
			from:     domain.TransactionStatusPosted,
			to:       domain.TransactionStatusPosted,
			wantErr:  true,
			wantCode: codes.FailedPrecondition,
		},

		// FAILED target
		{
			name:           "pending to failed succeeds",
			from:           domain.TransactionStatusPending,
			to:             domain.TransactionStatusFailed,
			postingResult:  "boom",
			wantStatus:     domain.TransactionStatusFailed,
			wantResult:     "boom",
			checkResultSet: true,
		},
		{
			name:     "posted to failed fails precondition",
			from:     domain.TransactionStatusPosted,
			to:       domain.TransactionStatusFailed,
			wantErr:  true,
			wantCode: codes.FailedPrecondition,
		},

		// PENDING target
		{
			name:           "pending to pending succeeds and sets result",
			from:           domain.TransactionStatusPending,
			to:             domain.TransactionStatusPending,
			postingResult:  "note",
			wantStatus:     domain.TransactionStatusPending,
			wantResult:     "note",
			checkResultSet: true,
		},
		{
			name:       "failed to pending succeeds without result override",
			from:       domain.TransactionStatusFailed,
			to:         domain.TransactionStatusPending,
			wantStatus: domain.TransactionStatusPending,
		},
		{
			name:     "posted to pending fails precondition",
			from:     domain.TransactionStatusPosted,
			to:       domain.TransactionStatusPending,
			wantErr:  true,
			wantCode: codes.FailedPrecondition,
		},

		// CANCELLED target
		{
			name:           "pending to cancelled succeeds",
			from:           domain.TransactionStatusPending,
			to:             domain.TransactionStatusCancelled,
			postingResult:  "cancel",
			wantStatus:     domain.TransactionStatusCancelled,
			wantResult:     "cancel",
			checkResultSet: true,
		},
		{
			name:     "posted to cancelled fails precondition",
			from:     domain.TransactionStatusPosted,
			to:       domain.TransactionStatusCancelled,
			wantErr:  true,
			wantCode: codes.FailedPrecondition,
		},

		// REVERSED target
		{
			name:           "posted to reversed succeeds",
			from:           domain.TransactionStatusPosted,
			to:             domain.TransactionStatusReversed,
			postingResult:  "reversal",
			wantStatus:     domain.TransactionStatusReversed,
			wantResult:     "reversal",
			checkResultSet: true,
		},
		{
			name:     "pending to reversed fails precondition",
			from:     domain.TransactionStatusPending,
			to:       domain.TransactionStatusReversed,
			wantErr:  true,
			wantCode: codes.FailedPrecondition,
		},

		// Unsupported target
		{
			name:     "unknown status fails invalid argument",
			from:     domain.TransactionStatusPending,
			to:       domain.TransactionStatus("WAT"),
			wantErr:  true,
			wantCode: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			posting := newTestPosting(tc.from)
			err := applyPostingStatusTransition(posting, tc.to, tc.postingResult)

			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantCode, status.Code(err))
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantStatus, posting.Status)
			if tc.checkResultSet {
				assert.Equal(t, tc.wantResult, posting.PostingResult)
			}
		})
	}
}

func TestApplyPostingStatusTransition_EmptyResultDoesNotOverride(t *testing.T) {
	// For PENDING/CANCELLED/REVERSED branches an empty postingResult must leave
	// the existing PostingResult untouched.
	t.Run("pending keeps prior result when empty", func(t *testing.T) {
		posting := newTestPosting(domain.TransactionStatusFailed)
		posting.PostingResult = "prior"
		require.NoError(t, applyPostingStatusTransition(posting, domain.TransactionStatusPending, ""))
		assert.Equal(t, "prior", posting.PostingResult)
	})

	t.Run("reversed keeps prior result when empty", func(t *testing.T) {
		posting := newTestPosting(domain.TransactionStatusPosted)
		posting.PostingResult = "prior"
		require.NoError(t, applyPostingStatusTransition(posting, domain.TransactionStatusReversed, ""))
		assert.Equal(t, "prior", posting.PostingResult)
	})
}

// --- mapControlDomainError ---

func TestMapControlDomainError(t *testing.T) {
	bookingLogID := uuid.New()

	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
	}{
		{"not found", persistence.ErrBookingLogNotFound, codes.NotFound},
		{"invalid control action", domain.ErrInvalidControlAction, codes.InvalidArgument},
		{"reason required", domain.ErrReasonRequired, codes.InvalidArgument},
		{"cannot suspend terminal", domain.ErrCannotSuspendTerminal, codes.FailedPrecondition},
		{"cannot resume pending", domain.ErrCannotResumePending, codes.FailedPrecondition},
		{"cannot terminate terminal", domain.ErrCannotTerminateTerminal, codes.FailedPrecondition},
		{"unknown error maps to internal", errors.New("boom"), codes.Internal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := mapControlDomainError(tc.err, bookingLogID)
			require.Error(t, err)
			assert.Equal(t, tc.wantCode, status.Code(err))
		})
	}
}

func TestMapControlDomainError_WrappedError(t *testing.T) {
	// errors.Is must unwrap wrapped sentinels.
	wrapped := errors.Join(errors.New("context"), domain.ErrReasonRequired)
	err := mapControlDomainError(wrapped, uuid.New())
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// --- mapIdempotencyExecutorError ---

func TestMapIdempotencyExecutorError(t *testing.T) {
	t.Run("operation in progress maps to aborted", func(t *testing.T) {
		err := mapIdempotencyExecutorError(idempotency.ErrOperationInProgress)
		assert.Equal(t, codes.Aborted, status.Code(err))
	})

	t.Run("executor error maps to internal", func(t *testing.T) {
		execErr := &idempotency.ExecutorError{
			Op:  "check",
			Key: idempotency.Key{Namespace: "fa", Operation: "post"},
			Err: errors.New("redis down"),
		}
		err := mapIdempotencyExecutorError(execErr)
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("business status error passes through unchanged", func(t *testing.T) {
		businessErr := status.Error(codes.NotFound, "missing")
		err := mapIdempotencyExecutorError(businessErr)
		assert.Equal(t, businessErr, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

// --- marshalForCache ---

func TestMarshalForCache_Success(t *testing.T) {
	msg := &financialaccountingv1.UpdateLedgerPostingResponse{}
	data := marshalForCache(msg, "req-1", "update-posting")
	require.NotNil(t, data)
	// Round-trips back to an equivalent message.
	var decoded financialaccountingv1.UpdateLedgerPostingResponse
	require.NoError(t, proto.Unmarshal(data, &decoded))
}

// --- storeIdempotencyResult ---

func TestStoreIdempotencyResult_Success(t *testing.T) {
	mock := &mockIdempotencyService{}
	svc := &FinancialAccountingService{idempotency: mock}

	key := idempotency.Key{Namespace: "fa", Operation: "update", RequestID: "req-1"}
	resp := &financialaccountingv1.UpdateLedgerPostingResponse{}

	svc.storeIdempotencyResult(context.Background(), key, defaultIdempotencyTTL, resp, "update-posting")

	require.NotNil(t, mock.storedResult)
	assert.Equal(t, idempotency.StatusCompleted, mock.storedResult.Status)
	assert.Equal(t, key, mock.storedResult.Key)
	assert.Equal(t, defaultIdempotencyTTL, mock.storedResult.TTL)
}

func TestStoreIdempotencyResult_StoreErrorIsSwallowed(t *testing.T) {
	// Storage is best-effort: a StoreResult error must not panic or propagate.
	mock := &mockIdempotencyService{storeErr: errors.New("store failed")}
	svc := &FinancialAccountingService{idempotency: mock}

	key := idempotency.Key{Namespace: "fa", Operation: "update", RequestID: "req-2"}
	resp := &financialaccountingv1.UpdateLedgerPostingResponse{}

	assert.NotPanics(t, func() {
		svc.storeIdempotencyResult(context.Background(), key, defaultIdempotencyTTL, resp, "update-posting")
	})
	require.NotNil(t, mock.storedResult)
}

// --- toProtoMoney boundary (already partially covered; assert exact nanos at the fraction edge) ---

func TestToProtoMoney_MaxFractionNanos(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount, _ := decimal.NewFromString("1.999999999")
	m := domain.NewMoney(amount, inst)

	result := toProtoMoney(m)
	assert.Equal(t, int64(1), result.Units)
	assert.Equal(t, int32(999999999), result.Nanos)
}
