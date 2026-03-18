package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	gormpkg "gorm.io/gorm"
)

// newFakeLienRepo returns a LienRepository backed by a nil *gorm.DB.
// Safe to use in tests that fail before any DB operation is reached.
func newFakeLienRepo() *persistence.LienRepository {
	return persistence.NewLienRepository((*gormpkg.DB)(nil))
}

// lienTestCtx returns a simple background context (no tenant required for pure-validation paths).
func lienTestCtx() context.Context {
	return context.Background()
}

// newLienTestService creates a service configured with a fake lien repo for unit testing.
func newLienTestService(repo Repository, opts ...Option) (*Service, error) {
	allOpts := append([]Option{WithLienRepo(newFakeLienRepo())}, opts...)
	return NewServiceFull(repo, nil, nil, testLogger(), nil, allOpts...)
}

// ---------------------------------------------------------------------------
// InitiateLien – validation paths
// ---------------------------------------------------------------------------

func TestInitiateLien_LienRepoNil(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil)
	require.NoError(t, err)

	_, err = svc.InitiateLien(lienTestCtx(), &pb.InitiateLienRequest{
		AccountId:            uuid.New().String(),
		PaymentOrderReference: "PAY-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "10.00",
			InstrumentCode: "GBP",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestInitiateLien_NilInput(t *testing.T) {
	repo := newMockRepository()
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.InitiateLien(lienTestCtx(), &pb.InitiateLienRequest{
		AccountId:            uuid.New().String(),
		PaymentOrderReference: "PAY-001",
		Input:                nil,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestInitiateLien_EmptyInputAmount(t *testing.T) {
	repo := newMockRepository()
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.InitiateLien(lienTestCtx(), &pb.InitiateLienRequest{
		AccountId:            uuid.New().String(),
		PaymentOrderReference: "PAY-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "",
			InstrumentCode: "GBP",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "amount")
}

func TestInitiateLien_EmptyInstrumentCode(t *testing.T) {
	repo := newMockRepository()
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.InitiateLien(lienTestCtx(), &pb.InitiateLienRequest{
		AccountId:            uuid.New().String(),
		PaymentOrderReference: "PAY-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "10.00",
			InstrumentCode: "",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "instrument_code")
}

func TestInitiateLien_EmptyPaymentOrderReference(t *testing.T) {
	repo := newMockRepository()
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.InitiateLien(lienTestCtx(), &pb.InitiateLienRequest{
		AccountId:            uuid.New().String(),
		PaymentOrderReference: "",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "10.00",
			InstrumentCode: "GBP",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "payment_order_reference")
}

func TestInitiateLien_WhitespacePaymentOrderReference(t *testing.T) {
	repo := newMockRepository()
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.InitiateLien(lienTestCtx(), &pb.InitiateLienRequest{
		AccountId:            uuid.New().String(),
		PaymentOrderReference: "   ",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "10.00",
			InstrumentCode: "GBP",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestInitiateLien_InvalidAmountString(t *testing.T) {
	repo := newMockRepository()
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.InitiateLien(lienTestCtx(), &pb.InitiateLienRequest{
		AccountId:            uuid.New().String(),
		PaymentOrderReference: "PAY-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "not-a-number",
			InstrumentCode: "GBP",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestInitiateLien_ZeroAmount(t *testing.T) {
	repo := newMockRepository()
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.InitiateLien(lienTestCtx(), &pb.InitiateLienRequest{
		AccountId:            uuid.New().String(),
		PaymentOrderReference: "PAY-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "0",
			InstrumentCode: "GBP",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "positive")
}

func TestInitiateLien_NegativeAmount(t *testing.T) {
	repo := newMockRepository()
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.InitiateLien(lienTestCtx(), &pb.InitiateLienRequest{
		AccountId:            uuid.New().String(),
		PaymentOrderReference: "PAY-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "-5.00",
			InstrumentCode: "GBP",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "positive")
}

func TestInitiateLien_AccountNotFound(t *testing.T) {
	repo := newMockRepository() // empty – account not in map
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.InitiateLien(lienTestCtx(), &pb.InitiateLienRequest{
		AccountId:            uuid.New().String(), // valid UUID not in repo
		PaymentOrderReference: "PAY-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "10.00",
			InstrumentCode: "GBP",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestInitiateLien_AccountRepositoryError(t *testing.T) {
	repo := newMockRepository()
	repo.findByIDErr = errors.New("simulated db failure")

	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.InitiateLien(lienTestCtx(), &pb.InitiateLienRequest{
		AccountId:            uuid.New().String(), // UUID format → hits FindByID → returns error
		PaymentOrderReference: "PAY-001",
		Input: &quantityv1.InstrumentAmount{
			Amount:         "10.00",
			InstrumentCode: "GBP",
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// ---------------------------------------------------------------------------
// RetrieveLien – validation paths (no DB)
// ---------------------------------------------------------------------------

func TestRetrieveLien_LienRepoNil(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil) // no lienRepo option
	require.NoError(t, err)

	_, err = svc.RetrieveLien(lienTestCtx(), &pb.RetrieveLienRequest{
		LienId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestRetrieveLien_InvalidLienID(t *testing.T) {
	repo := newMockRepository()
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.RetrieveLien(lienTestCtx(), &pb.RetrieveLienRequest{
		LienId: "not-a-uuid",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ---------------------------------------------------------------------------
// ExecuteLien – pre-idempotency validation paths
// ---------------------------------------------------------------------------

func TestExecuteLien_LienRepoNil_NoIdempotency(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil) // no lienRepo
	require.NoError(t, err)

	_, err = svc.ExecuteLien(lienTestCtx(), &pb.ExecuteLienRequest{
		LienId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestExecuteLien_InvalidLienID(t *testing.T) {
	repo := newMockRepository()
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.ExecuteLien(lienTestCtx(), &pb.ExecuteLienRequest{
		LienId: "bad-uuid",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ---------------------------------------------------------------------------
// TerminateLien – pre-idempotency validation paths
// ---------------------------------------------------------------------------

func TestTerminateLien_LienRepoNil_NoIdempotency(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil)
	require.NoError(t, err)

	_, err = svc.TerminateLien(lienTestCtx(), &pb.TerminateLienRequest{
		LienId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestTerminateLien_InvalidLienID(t *testing.T) {
	repo := newMockRepository()
	svc, err := newLienTestService(repo)
	require.NoError(t, err)

	_, err = svc.TerminateLien(lienTestCtx(), &pb.TerminateLienRequest{
		LienId: "bad-uuid",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// ---------------------------------------------------------------------------
// mapLienStatusToProto – exhaustive coverage
// ---------------------------------------------------------------------------

func TestMapLienStatusToProto_AllStatuses(t *testing.T) {
	tests := []struct {
		input    domain.LienStatus
		expected pb.LienStatus
	}{
		{domain.LienStatusActive, pb.LienStatus_LIEN_STATUS_ACTIVE},
		{domain.LienStatusExecuted, pb.LienStatus_LIEN_STATUS_EXECUTED},
		{domain.LienStatusTerminated, pb.LienStatus_LIEN_STATUS_TERMINATED},
		{domain.LienStatus("unknown"), pb.LienStatus_LIEN_STATUS_UNSPECIFIED},
	}
	for _, tc := range tests {
		got := mapLienStatusToProto(tc.input)
		assert.Equal(t, tc.expected, got, "status %q", tc.input)
	}
}

// ---------------------------------------------------------------------------
// isDuplicatePaymentOrderRef
// ---------------------------------------------------------------------------

func TestIsDuplicatePaymentOrderRef(t *testing.T) {
	assert.False(t, isDuplicatePaymentOrderRef(nil))
	assert.True(t, isDuplicatePaymentOrderRef(errors.New("idx_lien_payment_order constraint")))
	assert.True(t, isDuplicatePaymentOrderRef(errors.New("ERROR 23505: duplicate key")))
	assert.True(t, isDuplicatePaymentOrderRef(errors.New("duplicate key value violates unique")))
	assert.False(t, isDuplicatePaymentOrderRef(errors.New("some other error")))
	assert.True(t, isDuplicatePaymentOrderRef(errors.New(strings.Repeat("a", 10)+"idx_lien_payment_order"+"b")))
}
