package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	gormpkg "gorm.io/gorm"
)

// newFakeValuationFeatureRepo returns a valuation feature repo backed by a nil
// *gorm.DB. Safe for tests whose path returns before any feature-repo DB call.
func newFakeValuationFeatureRepo() *persistence.ValuationFeatureRepository {
	return persistence.NewValuationFeatureRepository((*gormpkg.DB)(nil))
}

// ---------------------------------------------------------------------------
// mapValuationError – maps sentinel valuation errors to op-status strings.
// ---------------------------------------------------------------------------

func TestMapValuationError_AllCases(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"account not found", ErrValuationAccountNotFound, opStatusAccountNotFound},
		{"no active feature", ErrNoActiveValuationFeature, opStatusNoValuationFeature},
		{"feature not active", ErrValuationFeatureNotActive, opStatusFeatureNotActive},
		{"repo not configured", ErrValuationRepoNotConfigured, opStatusValuationFeatureRepoNil},
		{"engine failed falls to default", ErrValuationEngineFailed, opStatusValuationFailed},
		{"unknown error falls to default", fmt.Errorf("boom"), opStatusValuationFailed},
		{"wrapped account not found", fmt.Errorf("ctx: %w", ErrValuationAccountNotFound), opStatusAccountNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, mapValuationError(tc.err))
		})
	}
}

// ---------------------------------------------------------------------------
// mapValuationErrorToGRPC – maps sentinel valuation errors to gRPC codes.
// ---------------------------------------------------------------------------

func TestMapValuationErrorToGRPC_AllCases(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
	}{
		{"account not found -> NotFound", ErrValuationAccountNotFound, codes.NotFound},
		{"no active feature -> FailedPrecondition", ErrNoActiveValuationFeature, codes.FailedPrecondition},
		{"feature not active -> FailedPrecondition", ErrValuationFeatureNotActive, codes.FailedPrecondition},
		{"repo not configured -> FailedPrecondition", ErrValuationRepoNotConfigured, codes.FailedPrecondition},
		{"engine failed -> Internal", ErrValuationEngineFailed, codes.Internal},
		{"unknown error -> Internal", fmt.Errorf("boom"), codes.Internal},
		{"wrapped account not found -> NotFound", fmt.Errorf("ctx: %w", ErrValuationAccountNotFound), codes.NotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := mapValuationErrorToGRPC(tc.err)
			require.Error(t, err)
			assert.Equal(t, tc.wantCode, status.Code(err))
		})
	}
}

// ---------------------------------------------------------------------------
// createSameInstrumentLien – direct helper calls (fail before lienRepo.Create)
// ---------------------------------------------------------------------------

// newSameInstrumentSvc returns a service with the given reference-data precision map
// and a fake (nil-DB) lien repo. The helper paths under test return before any DB call.
func newSameInstrumentSvc(t *testing.T, precisions map[string]int32) (*Service, *mockRepository) {
	t.Helper()
	repo := newMockRepository()
	svc, err := NewServiceFull(
		repo, nil, newInstrumentMap(precisions), testLogger(), nil,
		WithLienRepo(newFakeLienRepo()),
	)
	require.NoError(t, err)
	return svc, repo
}

func saveClearingAccount(t *testing.T, repo *mockRepository, instrument string) domain.InternalAccount {
	t.Helper()
	account, err := domain.NewInternalAccount(
		"IBA-"+instrument+"-HELPER", instrument+"-HELPER", "Helper Account",
		domain.AccountTypeClearing, domain.ClearingPurposeGeneral, instrument, "CURRENCY",
	)
	require.NoError(t, err)
	require.NoError(t, repo.Save(context.Background(), account))
	return account
}

func TestCreateSameInstrumentLien_Success(t *testing.T) {
	svc, repo := newSameInstrumentSvc(t, map[string]int32{"GBP": 2})
	account := saveClearingAccount(t, repo, "GBP")

	req := &pb.InitiateLienRequest{
		AccountId:             account.ID().String(),
		PaymentOrderReference: "PAY-SAME-OK",
		Input:                 &quantityv1.InstrumentAmount{Amount: "100.50", InstrumentCode: "GBP"},
	}

	lien, opStatus, err := svc.createSameInstrumentLien(
		context.Background(), account, decimal.RequireFromString("100.50"), "GBP", req, nil,
	)
	require.NoError(t, err)
	assert.Empty(t, opStatus)
	require.NotNil(t, lien)
	assert.Equal(t, int64(10050), lien.AmountCents)
	assert.Equal(t, "GBP", lien.InstrumentCode)
}

func TestCreateSameInstrumentLien_TooManyDecimals(t *testing.T) {
	svc, repo := newSameInstrumentSvc(t, map[string]int32{"GBP": 2})
	account := saveClearingAccount(t, repo, "GBP")

	req := &pb.InitiateLienRequest{
		AccountId:             account.ID().String(),
		PaymentOrderReference: "PAY-SAME-PRECISION",
		Input:                 &quantityv1.InstrumentAmount{Amount: "100.123", InstrumentCode: "GBP"},
	}

	// 100.123 has 3 decimals but GBP precision is 2 -> InvalidArgument.
	lien, opStatus, err := svc.createSameInstrumentLien(
		context.Background(), account, decimal.RequireFromString("100.123"), "GBP", req, nil,
	)
	require.Error(t, err)
	assert.Nil(t, lien)
	assert.Equal(t, opStatusInvalidInputAmount, opStatus)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "decimal places")
}

func TestCreateSameInstrumentLien_PrecisionLookupFails(t *testing.T) {
	// No reference-data client -> getInstrumentPrecision fails closed.
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil, WithLienRepo(newFakeLienRepo()))
	require.NoError(t, err)
	account := saveClearingAccount(t, repo, "GBP")

	req := &pb.InitiateLienRequest{
		AccountId:             account.ID().String(),
		PaymentOrderReference: "PAY-SAME-NOREF",
		Input:                 &quantityv1.InstrumentAmount{Amount: "100.00", InstrumentCode: "GBP"},
	}

	lien, opStatus, err := svc.createSameInstrumentLien(
		context.Background(), account, decimal.RequireFromString("100.00"), "GBP", req, nil,
	)
	require.Error(t, err)
	assert.Nil(t, lien)
	assert.Equal(t, operationStatusFailed, opStatus)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

// ---------------------------------------------------------------------------
// createCrossInstrumentLien – valuation + persistence helper.
// ---------------------------------------------------------------------------

// withValuationFeatureRepo wires a real (nil-DB) valuation feature repo so that
// valuateInternal passes the nil-check and reaches the identity-conversion path.
func newCrossInstrumentSvc(t *testing.T, precisions map[string]int32) (*Service, *mockRepository) {
	t.Helper()
	repo := newMockRepository()
	svc, err := NewServiceFull(
		repo, nil, newInstrumentMap(precisions), testLogger(), nil,
		WithLienRepo(newFakeLienRepo()),
	)
	require.NoError(t, err)
	return svc, repo
}

func TestCreateCrossInstrumentLien_ValuationRepoNotConfigured(t *testing.T) {
	// valuationFeatureRepo is nil -> valuateInternal returns ErrValuationRepoNotConfigured.
	svc, repo := newCrossInstrumentSvc(t, map[string]int32{"GBP": 2, "KWH": 6})
	account := saveClearingAccount(t, repo, "GBP")

	req := &pb.InitiateLienRequest{
		AccountId:             account.ID().String(),
		PaymentOrderReference: "PAY-CROSS-NOREPO",
		Input:                 &quantityv1.InstrumentAmount{Amount: "100", InstrumentCode: "KWH"},
	}

	lien, opStatus, err := svc.createCrossInstrumentLien(
		context.Background(), account, decimal.RequireFromString("100"), "GBP", req, time.Now(), nil,
	)
	require.Error(t, err)
	assert.Nil(t, lien)
	assert.Equal(t, opStatusValuationFeatureRepoNil, opStatus)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestCreateCrossInstrumentLien_ValuationAccountNotFound(t *testing.T) {
	// Account is NOT in the repo so valuateInternal -> findAccountByID -> NotFound.
	repo := newMockRepository()
	valFeatureRepo := newFakeValuationFeatureRepo()
	svc, err := NewServiceFull(
		repo, nil, newInstrumentMap(map[string]int32{"GBP": 2, "KWH": 6}), testLogger(), nil,
		WithLienRepo(newFakeLienRepo()),
		WithValuationFeatureRepo(valFeatureRepo),
	)
	require.NoError(t, err)

	// Construct an account object that is not persisted in the repo.
	account, err := domain.NewInternalAccount(
		"IBA-GHOST", "GHOST", "Ghost Account",
		domain.AccountTypeClearing, domain.ClearingPurposeGeneral, "GBP", "CURRENCY",
	)
	require.NoError(t, err)

	req := &pb.InitiateLienRequest{
		AccountId:             account.ID().String(), // valid UUID, not in repo
		PaymentOrderReference: "PAY-CROSS-NOACCT",
		Input:                 &quantityv1.InstrumentAmount{Amount: "100", InstrumentCode: "KWH"},
	}

	lien, opStatus, err := svc.createCrossInstrumentLien(
		context.Background(), account, decimal.RequireFromString("100"), "GBP", req, time.Now(), nil,
	)
	require.Error(t, err)
	assert.Nil(t, lien)
	assert.Equal(t, opStatusAccountNotFound, opStatus)
	assert.Equal(t, codes.NotFound, status.Code(err))
}
