package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- NewLien tests ---

func TestNewLien_Success(t *testing.T) {
	accountID := uuid.New()

	lien, err := NewLien(accountID, 10000, "GBP", "bucket-123", "PO-001", nil)

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, lien.ID)
	assert.Equal(t, accountID, lien.AccountID)
	assert.Equal(t, int64(10000), lien.AmountCents)
	assert.Equal(t, "GBP", lien.Currency)
	assert.Equal(t, "bucket-123", lien.BucketID)
	assert.Equal(t, LienStatusActive, lien.Status)
	assert.Equal(t, "PO-001", lien.PaymentOrderReference)
	assert.Equal(t, 1, lien.Version)
	assert.Nil(t, lien.ExpiresAt)
	assert.Nil(t, lien.ReservedQuantity)
	assert.Nil(t, lien.ValuedAmount)
	assert.Nil(t, lien.ValuationAnalysis)
}

func TestNewLien_EmptyBucketID(t *testing.T) {
	accountID := uuid.New()

	lien, err := NewLien(accountID, 10000, "GBP", "", "PO-001", nil)

	require.NoError(t, err)
	assert.Equal(t, "", lien.BucketID)
}

func TestNewLien_WithExpiration(t *testing.T) {
	accountID := uuid.New()
	expiresAt := time.Now().Add(24 * time.Hour)

	lien, err := NewLien(accountID, 10000, "GBP", "", "PO-001", &expiresAt)

	require.NoError(t, err)
	require.NotNil(t, lien.ExpiresAt)
	assert.Equal(t, expiresAt.Unix(), lien.ExpiresAt.Unix())
}

func TestNewLien_MultiAssetInstrument(t *testing.T) {
	accountID := uuid.New()

	// IBA supports any instrument code, not just currencies
	lien, err := NewLien(accountID, 50000, "kWh", "", "PO-ENERGY-001", nil)

	require.NoError(t, err)
	assert.Equal(t, int64(50000), lien.AmountCents)
	assert.Equal(t, "kWh", lien.Currency)
}

func TestNewLien_ZeroAmount_Fails(t *testing.T) {
	_, err := NewLien(uuid.New(), 0, "GBP", "", "PO-001", nil)
	assert.ErrorIs(t, err, ErrInvalidLienAmount)
}

func TestNewLien_NegativeAmount_Fails(t *testing.T) {
	_, err := NewLien(uuid.New(), -10000, "GBP", "", "PO-001", nil)
	assert.ErrorIs(t, err, ErrInvalidLienAmount)
}

func TestNewLien_EmptyCurrency_Fails(t *testing.T) {
	_, err := NewLien(uuid.New(), 10000, "", "", "PO-001", nil)
	assert.ErrorIs(t, err, ErrInvalidLienCurrency)
}

func TestNewLien_EmptyPaymentOrderReference_Fails(t *testing.T) {
	_, err := NewLien(uuid.New(), 10000, "GBP", "", "", nil)
	assert.ErrorIs(t, err, ErrInvalidPaymentOrderReference)
}

// --- NewValuedLien tests ---

func TestNewValuedLien_Success(t *testing.T) {
	accountID := uuid.New()
	reserved := &InstrumentAmount{
		Amount:         decimal.NewFromInt(100),
		InstrumentCode: "kWh",
	}
	valued := &InstrumentAmount{
		Amount:         decimal.NewFromFloat(35.50),
		InstrumentCode: "GBP",
	}
	analysis := json.RawMessage(`{"method_id":"test","version":"1"}`)

	lien, err := NewValuedLien(accountID, 3550, "GBP", "", "PO-VALUED-001", nil, reserved, valued, analysis)

	require.NoError(t, err)
	assert.Equal(t, int64(3550), lien.AmountCents)
	assert.Equal(t, "GBP", lien.Currency)
	assert.True(t, lien.HasValuation())
	assert.Equal(t, "kWh", lien.ReservedQuantity.InstrumentCode)
	assert.True(t, lien.ReservedQuantity.Amount.Equal(decimal.NewFromInt(100)))
	assert.Equal(t, "GBP", lien.ValuedAmount.InstrumentCode)
	assert.True(t, lien.ValuedAmount.Amount.Equal(decimal.NewFromFloat(35.50)))
	assert.NotNil(t, lien.ValuationAnalysis)
}

func TestNewValuedLien_NilReservedQuantity_Fails(t *testing.T) {
	valued := &InstrumentAmount{Amount: decimal.NewFromInt(35), InstrumentCode: "GBP"}

	_, err := NewValuedLien(uuid.New(), 3500, "GBP", "", "PO-001", nil, nil, valued, nil)

	assert.ErrorIs(t, err, ErrInvalidInstrumentAmount)
}

func TestNewValuedLien_NilValuedAmount_Fails(t *testing.T) {
	reserved := &InstrumentAmount{Amount: decimal.NewFromInt(100), InstrumentCode: "kWh"}

	_, err := NewValuedLien(uuid.New(), 3500, "GBP", "", "PO-001", nil, reserved, nil, nil)

	assert.ErrorIs(t, err, ErrInvalidInstrumentAmount)
}

func TestNewValuedLien_EmptyReservedInstrumentCode_Fails(t *testing.T) {
	reserved := &InstrumentAmount{Amount: decimal.NewFromInt(100), InstrumentCode: ""}
	valued := &InstrumentAmount{Amount: decimal.NewFromInt(35), InstrumentCode: "GBP"}

	_, err := NewValuedLien(uuid.New(), 3500, "GBP", "", "PO-001", nil, reserved, valued, nil)

	assert.ErrorIs(t, err, ErrInvalidInstrumentAmount)
}

func TestNewValuedLien_ZeroReservedAmount_Fails(t *testing.T) {
	reserved := &InstrumentAmount{Amount: decimal.Zero, InstrumentCode: "kWh"}
	valued := &InstrumentAmount{Amount: decimal.NewFromInt(35), InstrumentCode: "GBP"}

	_, err := NewValuedLien(uuid.New(), 3500, "GBP", "", "PO-001", nil, reserved, valued, nil)

	assert.ErrorIs(t, err, ErrInvalidInstrumentAmount)
}

func TestNewValuedLien_ZeroValuedAmount_Fails(t *testing.T) {
	reserved := &InstrumentAmount{Amount: decimal.NewFromInt(100), InstrumentCode: "kWh"}
	valued := &InstrumentAmount{Amount: decimal.Zero, InstrumentCode: "GBP"}

	_, err := NewValuedLien(uuid.New(), 3500, "GBP", "", "PO-001", nil, reserved, valued, nil)

	assert.ErrorIs(t, err, ErrInvalidInstrumentAmount)
}

// --- Execute tests ---

func TestLien_Execute_FromActive(t *testing.T) {
	lien := createTestLien(t, LienStatusActive)

	err := lien.Execute()

	assert.NoError(t, err)
	assert.Equal(t, LienStatusExecuted, lien.Status)
}

func TestLien_Execute_Idempotent(t *testing.T) {
	lien := createTestLien(t, LienStatusActive)

	err := lien.Execute()
	require.NoError(t, err)

	err = lien.Execute()
	assert.NoError(t, err)
	assert.Equal(t, LienStatusExecuted, lien.Status)
}

func TestLien_Execute_FromTerminated_Fails(t *testing.T) {
	lien := createTestLien(t, LienStatusTerminated)

	err := lien.Execute()

	assert.ErrorIs(t, err, ErrLienNotActive)
}

func TestLien_Execute_Expired_Fails(t *testing.T) {
	lien := createTestLien(t, LienStatusActive)
	past := time.Now().Add(-1 * time.Hour)
	lien.ExpiresAt = &past

	err := lien.Execute()

	assert.ErrorIs(t, err, ErrLienExpired)
	assert.Equal(t, LienStatusActive, lien.Status)
}

// --- Terminate tests ---

func TestLien_Terminate_FromActive(t *testing.T) {
	lien := createTestLien(t, LienStatusActive)

	err := lien.Terminate("Payment cancelled")

	assert.NoError(t, err)
	assert.Equal(t, LienStatusTerminated, lien.Status)
	assert.Equal(t, "Payment cancelled", lien.TerminationReason)
}

func TestLien_Terminate_Idempotent(t *testing.T) {
	lien := createTestLien(t, LienStatusActive)

	err := lien.Terminate("Payment cancelled")
	require.NoError(t, err)

	err = lien.Terminate("Different reason")
	assert.NoError(t, err)
	assert.Equal(t, LienStatusTerminated, lien.Status)
	assert.Equal(t, "Payment cancelled", lien.TerminationReason)
}

func TestLien_Terminate_FromExecuted_Fails(t *testing.T) {
	lien := createTestLien(t, LienStatusExecuted)

	err := lien.Terminate("Payment cancelled")

	assert.ErrorIs(t, err, ErrLienNotActive)
}

// --- Status helper tests ---

func TestLien_IsActive(t *testing.T) {
	tests := []struct {
		status   LienStatus
		expected bool
	}{
		{LienStatusActive, true},
		{LienStatusExecuted, false},
		{LienStatusTerminated, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			lien := createTestLien(t, tc.status)
			assert.Equal(t, tc.expected, lien.IsActive())
		})
	}
}

func TestLien_IsTerminal(t *testing.T) {
	tests := []struct {
		status   LienStatus
		expected bool
	}{
		{LienStatusActive, false},
		{LienStatusExecuted, true},
		{LienStatusTerminated, true},
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			lien := createTestLien(t, tc.status)
			assert.Equal(t, tc.expected, lien.IsTerminal())
		})
	}
}

func TestLien_IsExpired(t *testing.T) {
	t.Run("no expiration", func(t *testing.T) {
		lien := createTestLien(t, LienStatusActive)
		lien.ExpiresAt = nil
		assert.False(t, lien.IsExpired())
	})

	t.Run("future expiration", func(t *testing.T) {
		lien := createTestLien(t, LienStatusActive)
		future := time.Now().Add(24 * time.Hour)
		lien.ExpiresAt = &future
		assert.False(t, lien.IsExpired())
	})

	t.Run("past expiration", func(t *testing.T) {
		lien := createTestLien(t, LienStatusActive)
		past := time.Now().Add(-1 * time.Hour)
		lien.ExpiresAt = &past
		assert.True(t, lien.IsExpired())
	})
}

func TestLien_CanExecute(t *testing.T) {
	t.Run("active not expired", func(t *testing.T) {
		lien := createTestLien(t, LienStatusActive)
		assert.True(t, lien.CanExecute())
	})

	t.Run("active but expired", func(t *testing.T) {
		lien := createTestLien(t, LienStatusActive)
		past := time.Now().Add(-1 * time.Hour)
		lien.ExpiresAt = &past
		assert.False(t, lien.CanExecute())
	})

	t.Run("executed", func(t *testing.T) {
		lien := createTestLien(t, LienStatusExecuted)
		assert.False(t, lien.CanExecute())
	})

	t.Run("terminated", func(t *testing.T) {
		lien := createTestLien(t, LienStatusTerminated)
		assert.False(t, lien.CanExecute())
	})
}

func TestLien_CanTerminate(t *testing.T) {
	tests := []struct {
		status   LienStatus
		expected bool
	}{
		{LienStatusActive, true},
		{LienStatusExecuted, false},
		{LienStatusTerminated, false},
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			lien := createTestLien(t, tc.status)
			assert.Equal(t, tc.expected, lien.CanTerminate())
		})
	}
}

// --- HasValuation tests ---

func TestLien_HasValuation(t *testing.T) {
	t.Run("without valuation", func(t *testing.T) {
		lien := createTestLien(t, LienStatusActive)
		assert.False(t, lien.HasValuation())
	})

	t.Run("with valuation", func(t *testing.T) {
		lien := createTestLien(t, LienStatusActive)
		lien.ValuedAmount = &InstrumentAmount{
			Amount:         decimal.NewFromFloat(35.50),
			InstrumentCode: "GBP",
		}
		assert.True(t, lien.HasValuation())
	})

	t.Run("with zero valued amount", func(t *testing.T) {
		lien := createTestLien(t, LienStatusActive)
		lien.ValuedAmount = &InstrumentAmount{
			Amount:         decimal.Zero,
			InstrumentCode: "",
		}
		assert.False(t, lien.HasValuation())
	})
}

// --- InstrumentAmount tests ---

func TestInstrumentAmount_IsZero(t *testing.T) {
	t.Run("zero amount empty code", func(t *testing.T) {
		ia := InstrumentAmount{}
		assert.True(t, ia.IsZero())
	})

	t.Run("non-zero amount", func(t *testing.T) {
		ia := InstrumentAmount{Amount: decimal.NewFromInt(100), InstrumentCode: "kWh"}
		assert.False(t, ia.IsZero())
	})

	t.Run("zero amount with code", func(t *testing.T) {
		ia := InstrumentAmount{Amount: decimal.Zero, InstrumentCode: "kWh"}
		assert.False(t, ia.IsZero())
	})
}

// --- Test helper ---

func createTestLien(t *testing.T, status LienStatus) *Lien {
	t.Helper()
	now := time.Now()
	return &Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		AmountCents:           10000,
		Currency:              "GBP",
		Status:                status,
		PaymentOrderReference: "PO-TEST-001",
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}
