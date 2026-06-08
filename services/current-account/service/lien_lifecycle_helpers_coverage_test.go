package service

import (
	"errors"
	"fmt"
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests exercise the pure error-mapping helpers in
// lien_lifecycle_helpers.go. They are sentinel-driven switch statements with no
// DB dependency, so they are covered with table-driven unit tests that assert
// both the returned operation-status string and the gRPC status code.

// =============================================================================
// mapValuationError
// =============================================================================

func TestMapValuationError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus string
		wantCode   codes.Code
	}{
		{
			name:       "account not found",
			err:        ErrValuationAccountNotFound,
			wantStatus: opStatusAccountNotFound,
			wantCode:   codes.NotFound,
		},
		{
			name:       "no active valuation feature",
			err:        ErrNoActiveValuationFeature,
			wantStatus: opStatusNoValuationFeature,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "valuation feature not active",
			err:        ErrValuationFeatureNotActive,
			wantStatus: opStatusFeatureNotActive,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "valuation repo not configured",
			err:        ErrValuationRepoNotConfigured,
			wantStatus: opStatusValuationFeatureRepoNil,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "valuation engine failed",
			err:        ErrValuationEngineFailed,
			wantStatus: opStatusValuationFailed,
			wantCode:   codes.Internal,
		},
		{
			name:       "unknown error falls through to default",
			err:        errors.New("boom"),
			wantStatus: opStatusValuationFailed,
			wantCode:   codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotErr := mapValuationError(tt.err)
			assert.Equal(t, tt.wantStatus, gotStatus)
			require.Error(t, gotErr)
			assert.Equal(t, tt.wantCode, status.Code(gotErr))
		})
	}
}

// Wrapped sentinels must still match via errors.Is.
func TestMapValuationError_WrappedSentinel(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", ErrValuationEngineFailed)
	gotStatus, gotErr := mapValuationError(wrapped)
	assert.Equal(t, opStatusValuationFailed, gotStatus)
	assert.Equal(t, codes.Internal, status.Code(gotErr))
}

// =============================================================================
// mapInitiateLienTxError
// =============================================================================

func TestMapInitiateLienTxError(t *testing.T) {
	const accountID = "acct-123"

	tests := []struct {
		name       string
		err        error
		wantStatus string
		wantCode   codes.Code
	}{
		{
			name:       "account not found",
			err:        persistence.ErrAccountNotFound,
			wantStatus: opStatusAccountNotFound,
			wantCode:   codes.NotFound,
		},
		{
			name:       "account not active",
			err:        errTxAccountNotActive,
			wantStatus: opStatusAccountNotActive,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "currency mismatch",
			err:        errTxCurrencyMismatch,
			wantStatus: opStatusCurrencyMismatch,
			wantCode:   codes.InvalidArgument,
		},
		{
			name:       "insufficient funds",
			err:        errTxInsufficientFunds,
			wantStatus: opStatusInsufficientFunds,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "domain error",
			err:        errTxDomainError,
			wantStatus: opStatusDomainError,
			wantCode:   codes.InvalidArgument,
		},
		{
			name:       "save lien failed",
			err:        errTxSaveLien,
			wantStatus: opStatusSaveFailed,
			wantCode:   codes.Internal,
		},
		{
			name:       "unknown error falls through to default",
			err:        errors.New("boom"),
			wantStatus: opStatusRetrieveFailed,
			wantCode:   codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotErr := mapInitiateLienTxError(tt.err, accountID)
			assert.Equal(t, tt.wantStatus, gotStatus)
			require.Error(t, gotErr)
			assert.Equal(t, tt.wantCode, status.Code(gotErr))
		})
	}
}

func TestMapInitiateLienTxError_NotFoundMessageIncludesAccountID(t *testing.T) {
	_, gotErr := mapInitiateLienTxError(persistence.ErrAccountNotFound, "acct-xyz")
	require.Error(t, gotErr)
	assert.Contains(t, gotErr.Error(), "acct-xyz")
}

// =============================================================================
// mapExecuteLienTxError
// =============================================================================

func TestMapExecuteLienTxError(t *testing.T) {
	const lienID = "lien-123"
	// A zero-value lien is sufficient: only the invalid-status branch reads
	// lien.Status / lien.IsExpired(), and a fresh lien is unexpired with an
	// empty status, which is exactly the "cannot be executed" condition.
	lien := &domain.Lien{}

	tests := []struct {
		name       string
		err        error
		wantStatus string
		wantCode   codes.Code
	}{
		{
			name:       "lien not found",
			err:        persistence.ErrLienNotFound,
			wantStatus: opStatusLienNotFound,
			wantCode:   codes.NotFound,
		},
		{
			name:       "lien version conflict",
			err:        persistence.ErrLienVersionConflict,
			wantStatus: opStatusVersionConflict,
			wantCode:   codes.Aborted,
		},
		{
			name:       "generic version conflict",
			err:        persistence.ErrVersionConflict,
			wantStatus: opStatusVersionConflict,
			wantCode:   codes.Aborted,
		},
		{
			name:       "invalid lien status",
			err:        errTxInvalidLienStatus,
			wantStatus: opStatusInvalidLienStatus,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "save account failed",
			err:        errTxSaveAccount,
			wantStatus: opStatusSaveAccountFailed,
			wantCode:   codes.Internal,
		},
		{
			name:       "update lien failed",
			err:        errTxUpdateLien,
			wantStatus: opStatusUpdateLienFailed,
			wantCode:   codes.Internal,
		},
		{
			name:       "execute failed",
			err:        errTxExecuteFailed,
			wantStatus: opStatusExecuteFailed,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "withdraw failed",
			err:        errTxWithdrawFailed,
			wantStatus: opStatusWithdrawFailed,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "unknown error falls through to default",
			err:        errors.New("boom"),
			wantStatus: opStatusRetrieveAccountFailed,
			wantCode:   codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotErr := mapExecuteLienTxError(tt.err, lienID, lien)
			assert.Equal(t, tt.wantStatus, gotStatus)
			require.Error(t, gotErr)
			assert.Equal(t, tt.wantCode, status.Code(gotErr))
		})
	}
}

func TestMapExecuteLienTxError_NotFoundMessageIncludesLienID(t *testing.T) {
	_, gotErr := mapExecuteLienTxError(persistence.ErrLienNotFound, "lien-xyz", &domain.Lien{})
	require.Error(t, gotErr)
	assert.Contains(t, gotErr.Error(), "lien-xyz")
}

// =============================================================================
// mapTerminateLienTxError
// =============================================================================

func TestMapTerminateLienTxError(t *testing.T) {
	const lienID = "lien-456"
	lien := &domain.Lien{}

	tests := []struct {
		name       string
		err        error
		wantStatus string
		wantCode   codes.Code
	}{
		{
			name:       "lien not found",
			err:        persistence.ErrLienNotFound,
			wantStatus: opStatusLienNotFound,
			wantCode:   codes.NotFound,
		},
		{
			name:       "lien version conflict",
			err:        persistence.ErrLienVersionConflict,
			wantStatus: opStatusVersionConflict,
			wantCode:   codes.Aborted,
		},
		{
			name:       "invalid lien status",
			err:        errTxInvalidLienStatus,
			wantStatus: opStatusInvalidLienStatus,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "terminate failed",
			err:        errTxTerminateFailed,
			wantStatus: opStatusTerminateFailed,
			wantCode:   codes.FailedPrecondition,
		},
		{
			name:       "update lien failed",
			err:        errTxUpdateLien,
			wantStatus: opStatusUpdateFailed,
			wantCode:   codes.Internal,
		},
		{
			name:       "unknown error falls through to default",
			err:        errors.New("boom"),
			wantStatus: opStatusRetrieveFailed,
			wantCode:   codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotErr := mapTerminateLienTxError(tt.err, lienID, lien)
			assert.Equal(t, tt.wantStatus, gotStatus)
			require.Error(t, gotErr)
			assert.Equal(t, tt.wantCode, status.Code(gotErr))
		})
	}
}

func TestMapTerminateLienTxError_NotFoundMessageIncludesLienID(t *testing.T) {
	_, gotErr := mapTerminateLienTxError(persistence.ErrLienNotFound, "lien-zzz", &domain.Lien{})
	require.Error(t, gotErr)
	assert.Contains(t, gotErr.Error(), "lien-zzz")
}

// =============================================================================
// computeLegacyLienAmount - error paths
// =============================================================================

func TestComputeLegacyLienAmount_NilAmount(t *testing.T) {
	// req.Amount == nil hits the first guard ("amount is required") before any
	// account fields are dereferenced, so a zero-value account is safe here.
	req := &pb.InitiateLienRequest{}
	_, gotStatus, gotErr := computeLegacyLienAmount(req, domain.CurrentAccount{})
	assert.Equal(t, opStatusInvalidAmount, gotStatus)
	require.Error(t, gotErr)
	assert.Equal(t, codes.InvalidArgument, status.Code(gotErr))
	assert.Contains(t, gotErr.Error(), "amount is required")
}
