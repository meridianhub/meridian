package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// orgPartyIDToString
// =============================================================================

func TestOrgPartyIDToString_Nil(t *testing.T) {
	result := orgPartyIDToString(nil)
	assert.Equal(t, "", result)
}

func TestOrgPartyIDToString_NonNil(t *testing.T) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	result := orgPartyIDToString(&id)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", result)
}

func TestOrgPartyIDToString_NewUUID(t *testing.T) {
	id := uuid.New()
	result := orgPartyIDToString(&id)
	assert.Equal(t, id.String(), result)
}

// =============================================================================
// toProtoWithdrawal
// =============================================================================

func TestToProtoWithdrawal_PendingStatus(t *testing.T) {
	accountUUID := uuid.New()
	amt, err := domain.NewMoney("GBP", 5000) // £50.00
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	w := &domain.Withdrawal{
		ID:        uuid.New(),
		AccountID: accountUUID,
		Amount:    amt,
		Status:    domain.WithdrawalStatusPending,
		Reference: "WTH-001",
		CreatedAt: now,
		UpdatedAt: now,
	}

	proto := toProtoWithdrawal(w, "ACC-001")

	assert.Equal(t, "WTH-001", proto.WithdrawalId)
	assert.Equal(t, "ACC-001", proto.AccountId)
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_INITIATED, proto.Status)
	assert.Equal(t, "WTH-001", proto.Reference)
	require.NotNil(t, proto.Amount)
	require.NotNil(t, proto.Amount.Amount)
	assert.Equal(t, "GBP", proto.Amount.Amount.CurrencyCode)
	assert.Equal(t, int64(50), proto.Amount.Amount.Units)
}

func TestToProtoWithdrawal_CompletedStatus(t *testing.T) {
	amt, err := domain.NewMoney("USD", 10000)
	require.NoError(t, err)

	w := &domain.Withdrawal{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Amount:    amt,
		Status:    domain.WithdrawalStatusCompleted,
		Reference: "WTH-002",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	proto := toProtoWithdrawal(w, "ACC-002")

	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED, proto.Status)
	assert.Equal(t, "ACC-002", proto.AccountId)
}

func TestToProtoWithdrawal_FailedStatus(t *testing.T) {
	amt, err := domain.NewMoney("EUR", 7500)
	require.NoError(t, err)

	w := &domain.Withdrawal{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Amount:    amt,
		Status:    domain.WithdrawalStatusFailed,
		Reference: "WTH-003",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	proto := toProtoWithdrawal(w, "ACC-003")

	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_FAILED, proto.Status)
}

func TestToProtoWithdrawal_CancelledStatus(t *testing.T) {
	amt, err := domain.NewMoney("GBP", 2000)
	require.NoError(t, err)

	w := &domain.Withdrawal{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Amount:    amt,
		Status:    domain.WithdrawalStatusCancelled,
		Reference: "WTH-004",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	proto := toProtoWithdrawal(w, "ACC-004")

	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_CANCELLED, proto.Status)
}

func TestToProtoWithdrawal_PreservesTimestamps(t *testing.T) {
	amt, err := domain.NewMoney("GBP", 1000)
	require.NoError(t, err)

	createdAt := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2025, 1, 16, 12, 30, 0, 0, time.UTC)

	w := &domain.Withdrawal{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Amount:    amt,
		Status:    domain.WithdrawalStatusPending,
		Reference: "WTH-005",
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}

	proto := toProtoWithdrawal(w, "ACC-005")

	require.NotNil(t, proto.CreatedAt)
	require.NotNil(t, proto.UpdatedAt)
	assert.Equal(t, createdAt, proto.CreatedAt.AsTime())
	assert.Equal(t, updatedAt, proto.UpdatedAt.AsTime())
}

// =============================================================================
// mapWithdrawalStatusToProto - unknown status
// =============================================================================

func TestMapWithdrawalStatusToProto_UnknownStatus(t *testing.T) {
	result := mapWithdrawalStatusToProto(domain.WithdrawalStatus("BOGUS"))
	assert.Equal(t, pb.WithdrawalStatus_WITHDRAWAL_STATUS_UNSPECIFIED, result)
}
