package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWithdrawal_Success(t *testing.T) {
	accountID := uuid.New()
	amount, err := NewMoney("GBP", 5000)
	require.NoError(t, err)

	w, err := NewWithdrawal(accountID, amount, "REF-001")

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, w.ID)
	assert.Equal(t, accountID, w.AccountID)
	assert.Equal(t, amount, w.Amount)
	assert.Equal(t, WithdrawalStatusPending, w.Status)
	assert.Equal(t, "REF-001", w.Reference)
	assert.Equal(t, 1, w.Version)
	assert.True(t, w.IsPending())
	assert.False(t, w.IsTerminal())
}

func TestNewWithdrawal_InvalidAmount(t *testing.T) {
	accountID := uuid.New()
	amount, err := NewMoney("GBP", 0)
	require.NoError(t, err)

	_, err = NewWithdrawal(accountID, amount, "REF-001")
	assert.ErrorIs(t, err, ErrInvalidWithdrawalAmount)
}

func TestWithdrawal_Complete(t *testing.T) {
	w := createPendingWithdrawal(t)

	err := w.Complete()
	require.NoError(t, err)
	assert.Equal(t, WithdrawalStatusCompleted, w.Status)
	assert.False(t, w.IsPending())
	assert.True(t, w.IsTerminal())
}

func TestWithdrawal_Complete_Idempotent(t *testing.T) {
	w := createPendingWithdrawal(t)
	require.NoError(t, w.Complete())

	// Calling again should be idempotent
	err := w.Complete()
	assert.NoError(t, err)
	assert.Equal(t, WithdrawalStatusCompleted, w.Status)
}

func TestWithdrawal_Complete_FromNonPending(t *testing.T) {
	w := createPendingWithdrawal(t)
	require.NoError(t, w.Fail())

	err := w.Complete()
	assert.ErrorIs(t, err, ErrWithdrawalNotPending)
}

func TestWithdrawal_Fail(t *testing.T) {
	w := createPendingWithdrawal(t)

	err := w.Fail()
	require.NoError(t, err)
	assert.Equal(t, WithdrawalStatusFailed, w.Status)
	assert.True(t, w.IsTerminal())
}

func TestWithdrawal_Fail_Idempotent(t *testing.T) {
	w := createPendingWithdrawal(t)
	require.NoError(t, w.Fail())

	err := w.Fail()
	assert.NoError(t, err)
}

func TestWithdrawal_Fail_FromNonPending(t *testing.T) {
	w := createPendingWithdrawal(t)
	require.NoError(t, w.Complete())

	err := w.Fail()
	assert.ErrorIs(t, err, ErrWithdrawalNotPending)
}

func TestWithdrawal_Cancel(t *testing.T) {
	w := createPendingWithdrawal(t)

	err := w.Cancel()
	require.NoError(t, err)
	assert.Equal(t, WithdrawalStatusCancelled, w.Status)
	assert.True(t, w.IsTerminal())
}

func TestWithdrawal_Cancel_Idempotent(t *testing.T) {
	w := createPendingWithdrawal(t)
	require.NoError(t, w.Cancel())

	err := w.Cancel()
	assert.NoError(t, err)
}

func TestWithdrawal_Cancel_FromNonPending(t *testing.T) {
	w := createPendingWithdrawal(t)
	require.NoError(t, w.Complete())

	err := w.Cancel()
	assert.ErrorIs(t, err, ErrWithdrawalNotPending)
}

func TestWithdrawal_IsTerminal_AllStates(t *testing.T) {
	tests := []struct {
		name     string
		status   WithdrawalStatus
		terminal bool
	}{
		{"pending", WithdrawalStatusPending, false},
		{"completed", WithdrawalStatusCompleted, true},
		{"failed", WithdrawalStatusFailed, true},
		{"cancelled", WithdrawalStatusCancelled, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &Withdrawal{Status: tt.status}
			assert.Equal(t, tt.terminal, w.IsTerminal())
			assert.Equal(t, tt.status == WithdrawalStatusPending, w.IsPending())
		})
	}
}

func createPendingWithdrawal(t *testing.T) *Withdrawal {
	t.Helper()
	accountID := uuid.New()
	amount, err := NewMoney("GBP", 5000)
	require.NoError(t, err)
	w, err := NewWithdrawal(accountID, amount, "REF-001")
	require.NoError(t, err)
	return w
}
