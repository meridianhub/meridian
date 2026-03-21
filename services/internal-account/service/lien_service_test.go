package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func lienTestServiceWithRefData(t *testing.T, refData ReferenceDataClient) *Service {
	t.Helper()
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, refData, testLogger(), nil, WithLienRepo(newFakeLienRepo()))
	require.NoError(t, err)
	return svc
}

func TestBuildInitiateLienResponse_NoValuation(t *testing.T) {
	svc := lienTestServiceWithRefData(t, newInstrumentMap(map[string]int32{"GBP": 2}))

	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10050,
		InstrumentCode:        "GBP",
		PaymentOrderReference: "PAY-TEST",
		Status:                domain.LienStatusActive,
		Version:               1,
	}

	resp, err := svc.buildInitiateLienResponse(context.Background(), lien)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Lien)
	assert.Equal(t, "100.5", resp.Lien.Amount.Amount)
	assert.Equal(t, "GBP", resp.Lien.Amount.InstrumentCode)
	assert.Nil(t, resp.ValuedAmount)
	assert.Nil(t, resp.Basis)
}

func TestBuildInitiateLienResponse_WithValuation(t *testing.T) {
	svc := lienTestServiceWithRefData(t, newInstrumentMap(map[string]int32{"GBP": 2}))

	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10050,
		InstrumentCode:        "GBP",
		PaymentOrderReference: "PAY-TEST-2",
		Status:                domain.LienStatusActive,
		Version:               1,
		ReservedQuantity: &domain.InstrumentAmount{
			Amount:         decimal.NewFromFloat(5.5),
			InstrumentCode: "KWH",
		},
		ValuedAmount: &domain.InstrumentAmount{
			Amount:         decimal.NewFromFloat(100.50),
			InstrumentCode: "GBP",
		},
	}

	resp, err := svc.buildInitiateLienResponse(context.Background(), lien)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.ValuedAmount)
	assert.Equal(t, "100.5", resp.ValuedAmount.Amount)
	assert.Equal(t, "GBP", resp.ValuedAmount.InstrumentCode)
}

func TestDomainToProtoLien_TerminatedWithReason(t *testing.T) {
	svc := lienTestServiceWithRefData(t, newInstrumentMap(map[string]int32{"GBP": 2}))

	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           500,
		InstrumentCode:        "GBP",
		PaymentOrderReference: "PAY-EXP",
		Status:                domain.LienStatusTerminated,
		TerminationReason:     "expired",
		Version:               2,
	}

	protoLien, err := svc.domainToProtoLien(context.Background(), lien)
	require.NoError(t, err)
	assert.Equal(t, pb.LienStatus_LIEN_STATUS_TERMINATED, protoLien.Status)
	assert.Equal(t, "expired", protoLien.TerminationReason)
	assert.Equal(t, int32(2), protoLien.Version)
}

func TestDomainToProtoLien_WithReservedAndValuedAmounts(t *testing.T) {
	svc := lienTestServiceWithRefData(t, newInstrumentMap(map[string]int32{"GBP": 2}))

	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10050,
		InstrumentCode:        "GBP",
		PaymentOrderReference: "PAY-RQ",
		Status:                domain.LienStatusActive,
		Version:               1,
		ReservedQuantity: &domain.InstrumentAmount{
			Amount:         decimal.NewFromFloat(5.5),
			InstrumentCode: "KWH",
		},
		ValuedAmount: &domain.InstrumentAmount{
			Amount:         decimal.NewFromFloat(100.50),
			InstrumentCode: "GBP",
		},
	}

	protoLien, err := svc.domainToProtoLien(context.Background(), lien)
	require.NoError(t, err)
	require.NotNil(t, protoLien.ReservedQuantity)
	assert.Equal(t, "5.5", protoLien.ReservedQuantity.Amount)
	assert.Equal(t, "KWH", protoLien.ReservedQuantity.InstrumentCode)
	require.NotNil(t, protoLien.ValuedAmount)
	assert.Equal(t, "100.5", protoLien.ValuedAmount.Amount)
}

func TestDomainToProtoLien_NilReferenceDataClient_FailsClosed(t *testing.T) {
	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, testLogger(), nil, WithLienRepo(newFakeLienRepo()))
	require.NoError(t, err)

	lien := &domain.Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           100,
		InstrumentCode:        "GBP",
		PaymentOrderReference: "PAY-NIL",
		Status:                domain.LienStatusActive,
		Version:               1,
	}

	_, err = svc.domainToProtoLien(context.Background(), lien)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}
