package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLien_Success(t *testing.T) {
	accountID := uuid.New()
	amount, err := NewMoney("GBP", 10000)
	require.NoError(t, err)

	lien, err := NewLien(accountID, amount, "bucket-123", "PO-001", nil)

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, lien.ID)
	assert.Equal(t, accountID, lien.AccountID)
	assert.Equal(t, int64(10000), toMinorUnits(lien.Amount))
	assert.Equal(t, "GBP", lien.Amount.InstrumentCode())
	assert.Equal(t, "bucket-123", lien.BucketID)
	assert.Equal(t, LienStatusActive, lien.Status)
	assert.Equal(t, "PO-001", lien.PaymentOrderReference)
	assert.Equal(t, 1, lien.Version)
	assert.Nil(t, lien.ExpiresAt)
}

func TestNewLien_EmptyBucketID(t *testing.T) {
	accountID := uuid.New()
	amount, err := NewMoney("GBP", 10000)
	require.NoError(t, err)

	// Empty bucket ID is valid (default bucket for backward compatibility)
	lien, err := NewLien(accountID, amount, "", "PO-001", nil)

	require.NoError(t, err)
	assert.Equal(t, "", lien.BucketID)
}

func TestNewLien_WithExpiration(t *testing.T) {
	accountID := uuid.New()
	amount, err := NewMoney("GBP", 10000)
	require.NoError(t, err)
	expiresAt := time.Now().Add(24 * time.Hour)

	lien, err := NewLien(accountID, amount, "", "PO-001", &expiresAt)

	require.NoError(t, err)
	require.NotNil(t, lien.ExpiresAt)
	assert.Equal(t, expiresAt.Unix(), lien.ExpiresAt.Unix())
}

func TestNewLien_ZeroAmount_Fails(t *testing.T) {
	accountID := uuid.New()
	amount, err := NewMoney("GBP", 0)
	require.NoError(t, err)

	_, err = NewLien(accountID, amount, "", "PO-001", nil)

	assert.ErrorIs(t, err, ErrInvalidLienAmount)
}

func TestNewLien_NegativeAmount_Fails(t *testing.T) {
	accountID := uuid.New()
	amount, err := NewMoney("GBP", -10000)
	require.NoError(t, err)

	_, err = NewLien(accountID, amount, "", "PO-001", nil)

	assert.ErrorIs(t, err, ErrInvalidLienAmount)
}

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

	// Second call should be idempotent
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
	assert.Equal(t, LienStatusActive, lien.Status) // Status unchanged
}

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

	// Second call should be idempotent
	err = lien.Terminate("Different reason")
	assert.NoError(t, err)
	assert.Equal(t, LienStatusTerminated, lien.Status)
	// Original reason should be preserved
	assert.Equal(t, "Payment cancelled", lien.TerminationReason)
}

func TestLien_Terminate_FromExecuted_Fails(t *testing.T) {
	lien := createTestLien(t, LienStatusExecuted)

	err := lien.Terminate("Payment cancelled")

	assert.ErrorIs(t, err, ErrLienNotActive)
}

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

func TestNewLien_EnergyAccount(t *testing.T) {
	accountID := uuid.New()
	amount, err := NewAmountFromInstrument("KWH", "ENERGY", 0, 5000) // 5000 KWH
	require.NoError(t, err)

	lien, err := NewLien(accountID, amount, "bucket-energy", "PO-ENERGY-001", nil)

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, lien.ID)
	assert.Equal(t, accountID, lien.AccountID)
	assert.Equal(t, int64(5000), toMinorUnits(lien.Amount))
	assert.Equal(t, "KWH", lien.Amount.InstrumentCode())
	assert.Equal(t, "ENERGY", lien.Amount.Dimension())
	assert.Equal(t, "bucket-energy", lien.BucketID)
	assert.Equal(t, LienStatusActive, lien.Status)
	assert.Equal(t, "PO-ENERGY-001", lien.PaymentOrderReference)
}

// createTestLien is a helper to create a lien with a specific status for testing
func createTestLien(t *testing.T, status LienStatus) *Lien {
	t.Helper()
	amount, err := NewMoney("GBP", 10000)
	require.NoError(t, err)

	now := time.Now()
	return &Lien{
		ID:                    uuid.New(),
		AccountID:             uuid.New(),
		Amount:                amount,
		Status:                status,
		PaymentOrderReference: "PO-TEST-001",
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}

func TestNewLien_KWHAmountHelper(t *testing.T) {
	account := createKWHTestAccount(t, "ACC-KWH-LIEN-001", 10000)
	lienAmount := createKWHAmount(t, 3000)

	lien, err := NewLien(account.ID(), lienAmount, "bucket-kwh", "PO-KWH-001", nil)

	require.NoError(t, err)
	assert.Equal(t, int64(3000), toMinorUnits(lien.Amount))
	assert.Equal(t, "KWH", lien.Amount.InstrumentCode())
	assert.Equal(t, "ENERGY", lien.Amount.Dimension())
	assert.Equal(t, account.ID(), lien.AccountID)
}

func TestNewLien_CarbonCreditAmountHelper(t *testing.T) {
	creditAmount := createCarbonCreditAmount(t, 50)

	lien, err := NewLien(uuid.New(), creditAmount, "bucket-carbon", "PO-CC-001", nil)

	require.NoError(t, err)
	assert.Equal(t, int64(50), toMinorUnits(lien.Amount))
	assert.Equal(t, "CARBON_CREDIT", lien.Amount.InstrumentCode())
	assert.Equal(t, "CARBON", lien.Amount.Dimension())
}

// createKWHTestAccount creates a KWH energy account with an optional initial balance for testing.
func createKWHTestAccount(t *testing.T, accountID string, initialKWH int64) CurrentAccount {
	t.Helper()
	account, err := NewCurrentAccountWithDimension(accountID, "KWH-"+accountID, "PARTY-TEST", "KWH", "ENERGY", 0)
	require.NoError(t, err)

	if initialKWH > 0 {
		deposit, err := NewAmountFromInstrument("KWH", "ENERGY", 0, initialKWH)
		require.NoError(t, err)
		account, err = account.Deposit(deposit)
		require.NoError(t, err)
	}

	return account
}

// createKWHAmount creates a KWH energy Amount for test assertions.
func createKWHAmount(t *testing.T, minorUnits int64) Amount {
	t.Helper()
	a, err := NewAmountFromInstrument("KWH", "ENERGY", 0, minorUnits)
	require.NoError(t, err)
	return a
}

// createCarbonCreditAmount creates a CARBON_CREDIT Amount for test assertions.
func createCarbonCreditAmount(t *testing.T, minorUnits int64) Amount {
	t.Helper()
	a, err := NewAmountFromInstrument("CARBON_CREDIT", "CARBON", 0, minorUnits)
	require.NoError(t, err)
	return a
}
