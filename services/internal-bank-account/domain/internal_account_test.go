package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewInternalBankAccount_Success(t *testing.T) {
	tests := []struct {
		name           string
		accountID      string
		accountCode    string
		accountName    string
		accountType    AccountType
		instrumentCode string
		dimension      string
	}{
		{
			name:           "clearing account",
			accountID:      "IBA-001",
			accountCode:    "GBP_CLEARING",
			accountName:    "GBP Clearing Account",
			accountType:    AccountTypeClearing,
			instrumentCode: "GBP",
			dimension:      "CURRENCY",
		},
		{
			name:           "nostro account",
			accountID:      "IBA-002",
			accountCode:    "USD_NOSTRO_CHASE",
			accountName:    "USD Nostro at Chase",
			accountType:    AccountTypeNostro,
			instrumentCode: "USD",
			dimension:      "CURRENCY",
		},
		{
			name:           "vostro account",
			accountID:      "IBA-003",
			accountCode:    "EUR_VOSTRO_DB",
			accountName:    "EUR Vostro for Deutsche Bank",
			accountType:    AccountTypeVostro,
			instrumentCode: "EUR",
			dimension:      "CURRENCY",
		},
		{
			name:           "suspense account",
			accountID:      "IBA-004",
			accountCode:    "SUSPENSE_001",
			accountName:    "General Suspense",
			accountType:    AccountTypeSuspense,
			instrumentCode: "USD",
			dimension:      "CURRENCY",
		},
		{
			name:           "energy holding account",
			accountID:      "IBA-005",
			accountCode:    "KWH_HOLDING",
			accountName:    "Energy Holding Account",
			accountType:    AccountTypeHolding,
			instrumentCode: "KWH",
			dimension:      "ENERGY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beforeCreate := time.Now()

			account, err := NewInternalBankAccount(
				tt.accountID,
				tt.accountCode,
				tt.accountName,
				tt.accountType,
				tt.instrumentCode,
				tt.dimension,
			)

			require.NoError(t, err)

			// Verify all fields are set correctly
			assert.NotEqual(t, uuid.Nil, account.ID(), "ID should be generated")
			assert.Equal(t, tt.accountID, account.AccountID())
			assert.Equal(t, tt.accountCode, account.AccountCode())
			assert.Equal(t, tt.accountName, account.Name())
			assert.Equal(t, tt.accountType, account.AccountType())
			assert.Equal(t, tt.instrumentCode, account.InstrumentCode())
			assert.Equal(t, tt.dimension, account.Dimension())

			// Verify initial state
			assert.Equal(t, AccountStatusActive, account.Status(), "initial status should be ACTIVE")
			assert.Equal(t, int64(1), account.Version(), "initial version should be 1")
			assert.Nil(t, account.Correspondent(), "correspondent should be nil initially")
			assert.Nil(t, account.Attributes(), "attributes should be nil initially")

			// Verify timestamps
			assert.False(t, account.CreatedAt().Before(beforeCreate), "createdAt should be >= beforeCreate")
			assert.False(t, account.UpdatedAt().Before(beforeCreate), "updatedAt should be >= beforeCreate")
			assert.Equal(t, account.CreatedAt(), account.UpdatedAt(), "createdAt and updatedAt should match initially")
		})
	}
}

func TestNewInternalBankAccount_ValidationErrors(t *testing.T) {
	tests := []struct {
		name           string
		accountID      string
		accountCode    string
		accountName    string
		accountType    AccountType
		instrumentCode string
		dimension      string
		expectedErr    error
	}{
		{
			name:           "empty account ID",
			accountID:      "",
			accountCode:    "GBP_CLEARING",
			accountName:    "GBP Clearing Account",
			accountType:    AccountTypeClearing,
			instrumentCode: "GBP",
			dimension:      "CURRENCY",
			expectedErr:    ErrAccountIDRequired,
		},
		{
			name:           "empty account code",
			accountID:      "IBA-001",
			accountCode:    "",
			accountName:    "GBP Clearing Account",
			accountType:    AccountTypeClearing,
			instrumentCode: "GBP",
			dimension:      "CURRENCY",
			expectedErr:    ErrAccountCodeRequired,
		},
		{
			name:           "empty name",
			accountID:      "IBA-001",
			accountCode:    "GBP_CLEARING",
			accountName:    "",
			accountType:    AccountTypeClearing,
			instrumentCode: "GBP",
			dimension:      "CURRENCY",
			expectedErr:    ErrNameRequired,
		},
		{
			name:           "invalid account type",
			accountID:      "IBA-001",
			accountCode:    "GBP_CLEARING",
			accountName:    "GBP Clearing Account",
			accountType:    AccountType("INVALID"),
			instrumentCode: "GBP",
			dimension:      "CURRENCY",
			expectedErr:    ErrInvalidAccountType,
		},
		{
			name:           "empty account type",
			accountID:      "IBA-001",
			accountCode:    "GBP_CLEARING",
			accountName:    "GBP Clearing Account",
			accountType:    AccountType(""),
			instrumentCode: "GBP",
			dimension:      "CURRENCY",
			expectedErr:    ErrInvalidAccountType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := NewInternalBankAccount(
				tt.accountID,
				tt.accountCode,
				tt.accountName,
				tt.accountType,
				tt.instrumentCode,
				tt.dimension,
			)

			require.Error(t, err)
			assert.ErrorIs(t, err, tt.expectedErr)
			assert.Equal(t, InternalBankAccount{}, account, "should return zero value on error")
		})
	}
}

func TestLifecycleTransitions_Valid(t *testing.T) {
	account := createTestAccount(t, AccountTypeClearing)

	// ACTIVE -> SUSPENDED
	suspended, err := account.Suspend("Maintenance period")
	require.NoError(t, err)
	assert.Equal(t, AccountStatusSuspended, suspended.Status())
	assert.Equal(t, int64(2), suspended.Version())

	// SUSPENDED -> ACTIVE
	reactivated, err := suspended.Activate()
	require.NoError(t, err)
	assert.Equal(t, AccountStatusActive, reactivated.Status())
	assert.Equal(t, int64(3), reactivated.Version())

	// ACTIVE -> CLOSED
	closed, err := reactivated.Close("Account decommissioned")
	require.NoError(t, err)
	assert.Equal(t, AccountStatusClosed, closed.Status())
	assert.Equal(t, int64(4), closed.Version())
}

func TestLifecycleTransitions_DirectToClose(t *testing.T) {
	// Test ACTIVE -> CLOSED directly
	account := createTestAccount(t, AccountTypeClearing)
	closed, err := account.Close("No longer needed")
	require.NoError(t, err)
	assert.Equal(t, AccountStatusClosed, closed.Status())

	// Test SUSPENDED -> CLOSED
	suspended, err := createTestAccount(t, AccountTypeClearing).Suspend("Temporary")
	require.NoError(t, err)
	closedFromSuspended, err := suspended.Close("Permanently closed")
	require.NoError(t, err)
	assert.Equal(t, AccountStatusClosed, closedFromSuspended.Status())
}

func TestLifecycleTransitions_Invalid(t *testing.T) {
	account := createTestAccount(t, AccountTypeClearing)

	// Close the account first
	closed, err := account.Close("Decommissioned")
	require.NoError(t, err)

	// Try to suspend a closed account
	_, err = closed.Suspend("Try to suspend")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)

	// Try to activate a closed account
	_, err = closed.Activate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)

	// Try to close again
	_, err = closed.Close("Close again")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestLifecycleTransitions_SameStatus(t *testing.T) {
	account := createTestAccount(t, AccountTypeClearing)

	// Try to activate an already active account
	_, err := account.Activate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)

	// Try to suspend an already suspended account
	suspended, err := account.Suspend("Maintenance")
	require.NoError(t, err)
	_, err = suspended.Suspend("Suspend again")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
}

func TestImmutability(t *testing.T) {
	original := createTestAccount(t, AccountTypeClearing)
	originalID := original.ID()
	originalVersion := original.Version()
	originalStatus := original.Status()
	originalUpdatedAt := original.UpdatedAt()

	// Give some time for timestamp difference
	time.Sleep(1 * time.Millisecond)

	// Perform a status change
	suspended, err := original.Suspend("Testing immutability")
	require.NoError(t, err)

	// Verify original is unchanged
	assert.Equal(t, originalID, original.ID(), "original ID should be unchanged")
	assert.Equal(t, originalVersion, original.Version(), "original version should be unchanged")
	assert.Equal(t, originalStatus, original.Status(), "original status should be unchanged")
	assert.Equal(t, originalUpdatedAt, original.UpdatedAt(), "original updatedAt should be unchanged")

	// Verify new instance has changes
	assert.Equal(t, originalID, suspended.ID(), "ID should be preserved")
	assert.Equal(t, originalVersion+1, suspended.Version(), "version should be incremented")
	assert.Equal(t, AccountStatusSuspended, suspended.Status(), "status should be updated")
	assert.True(t, suspended.UpdatedAt().After(originalUpdatedAt), "updatedAt should be newer")
}

func TestImmutability_AttributesNotShared(t *testing.T) {
	// Use builder to create account with attributes
	attrs := map[string]string{"key": "value"}
	account := NewInternalBankAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-001").
		WithAccountCode("TEST_CLEARING").
		WithName("Test Account").
		WithAccountType(AccountTypeClearing).
		WithInstrumentCode("USD").
		WithDimension("CURRENCY").
		WithStatus(AccountStatusActive).
		WithAttributes(attrs).
		WithVersion(1).
		WithCreatedAt(time.Now()).
		WithUpdatedAt(time.Now()).
		Build()

	// Get attributes from account
	retrievedAttrs := account.Attributes()
	require.NotNil(t, retrievedAttrs)

	// Modify the retrieved attributes
	retrievedAttrs["new_key"] = "new_value"

	// Verify original attributes are unchanged
	originalAttrs := account.Attributes()
	assert.NotContains(t, originalAttrs, "new_key", "modifying returned attributes should not affect internal state")
}

func TestUpdateCorrespondent_Success(t *testing.T) {
	// Create a NOSTRO account
	nostroAccount := createTestAccount(t, AccountTypeNostro)

	// Create correspondent details
	correspondent, err := NewCorrespondentDetails(
		"CHASE001",
		"JPMorgan Chase Bank",
		"ACC-12345",
	)
	require.NoError(t, err)

	// Update correspondent
	updated, err := nostroAccount.UpdateCorrespondent(correspondent)
	require.NoError(t, err)

	assert.NotNil(t, updated.Correspondent())
	assert.Equal(t, "CHASE001", updated.Correspondent().BankID())
	assert.Equal(t, int64(2), updated.Version())

	// Verify original is unchanged
	assert.Nil(t, nostroAccount.Correspondent())
	assert.Equal(t, int64(1), nostroAccount.Version())
}

func TestUpdateCorrespondent_VostroAccount(t *testing.T) {
	// Create a VOSTRO account
	vostroAccount := createTestAccount(t, AccountTypeVostro)

	// Create correspondent details
	correspondent, err := NewCorrespondentDetails(
		"DB001",
		"Deutsche Bank",
		"VOSTRO-REF-001",
	)
	require.NoError(t, err)

	// Update correspondent
	updated, err := vostroAccount.UpdateCorrespondent(correspondent)
	require.NoError(t, err)

	assert.NotNil(t, updated.Correspondent())
	assert.Equal(t, "DB001", updated.Correspondent().BankID())
}

func TestUpdateCorrespondent_ValidationForType(t *testing.T) {
	t.Run("NOSTRO requires correspondent", func(t *testing.T) {
		nostro := createTestAccount(t, AccountTypeNostro)

		// Try to set nil correspondent on NOSTRO
		_, err := nostro.UpdateCorrespondent(nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCorrespondentRequired)
	})

	t.Run("VOSTRO requires correspondent", func(t *testing.T) {
		vostro := createTestAccount(t, AccountTypeVostro)

		// Try to set nil correspondent on VOSTRO
		_, err := vostro.UpdateCorrespondent(nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCorrespondentRequired)
	})

	t.Run("CLEARING rejects correspondent", func(t *testing.T) {
		clearing := createTestAccount(t, AccountTypeClearing)

		correspondent, err := NewCorrespondentDetails(
			"BANK001",
			"Some Bank",
			"REF-001",
		)
		require.NoError(t, err)

		// Try to set correspondent on CLEARING account
		_, err = clearing.UpdateCorrespondent(correspondent)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCorrespondentNotAllowed)
	})

	t.Run("HOLDING rejects correspondent", func(t *testing.T) {
		holding := createTestAccount(t, AccountTypeHolding)

		correspondent, err := NewCorrespondentDetails(
			"BANK001",
			"Some Bank",
			"REF-001",
		)
		require.NoError(t, err)

		// Try to set correspondent on HOLDING account
		_, err = holding.UpdateCorrespondent(correspondent)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCorrespondentNotAllowed)
	})

	t.Run("SUSPENSE rejects correspondent", func(t *testing.T) {
		suspense := createTestAccount(t, AccountTypeSuspense)

		correspondent, err := NewCorrespondentDetails(
			"BANK001",
			"Some Bank",
			"REF-001",
		)
		require.NoError(t, err)

		_, err = suspense.UpdateCorrespondent(correspondent)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCorrespondentNotAllowed)
	})
}

func TestUpdateCorrespondent_ClosedAccount(t *testing.T) {
	nostro := createTestAccount(t, AccountTypeNostro)

	// Close the account
	closed, err := nostro.Close("Decommissioned")
	require.NoError(t, err)

	// Try to update correspondent on closed account
	correspondent, err := NewCorrespondentDetails(
		"BANK001",
		"Some Bank",
		"REF-001",
	)
	require.NoError(t, err)

	_, err = closed.UpdateCorrespondent(correspondent)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountClosed)
}

func TestBuilder_Reconstruction(t *testing.T) {
	// Simulate values from persistence
	id := uuid.New()
	createdAt := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC)
	correspondent, err := NewCorrespondentDetails(
		"CHASE001",
		"JPMorgan Chase Bank",
		"ACC-12345",
	)
	require.NoError(t, err)

	attributes := map[string]string{
		"category":   "trading",
		"department": "treasury",
	}

	// Reconstruct using builder
	account := NewInternalBankAccountBuilder().
		WithID(id).
		WithAccountID("IBA-001").
		WithAccountCode("USD_NOSTRO_CHASE").
		WithName("USD Nostro at Chase").
		WithAccountType(AccountTypeNostro).
		WithInstrumentCode("USD").
		WithDimension("CURRENCY").
		WithStatus(AccountStatusSuspended).
		WithCorrespondent(correspondent).
		WithAttributes(attributes).
		WithVersion(5).
		WithCreatedAt(createdAt).
		WithUpdatedAt(updatedAt).
		Build()

	// Verify all fields were set correctly
	assert.Equal(t, id, account.ID())
	assert.Equal(t, "IBA-001", account.AccountID())
	assert.Equal(t, "USD_NOSTRO_CHASE", account.AccountCode())
	assert.Equal(t, "USD Nostro at Chase", account.Name())
	assert.Equal(t, AccountTypeNostro, account.AccountType())
	assert.Equal(t, "USD", account.InstrumentCode())
	assert.Equal(t, "CURRENCY", account.Dimension())
	assert.Equal(t, AccountStatusSuspended, account.Status())
	assert.NotNil(t, account.Correspondent())
	assert.Equal(t, "CHASE001", account.Correspondent().BankID())
	assert.Equal(t, int64(5), account.Version())
	assert.Equal(t, createdAt, account.CreatedAt())
	assert.Equal(t, updatedAt, account.UpdatedAt())

	// Verify attributes
	attrs := account.Attributes()
	assert.Equal(t, "trading", attrs["category"])
	assert.Equal(t, "treasury", attrs["department"])
}

func TestBuilder_AttributesDeepCopy(t *testing.T) {
	originalAttrs := map[string]string{"key": "value"}

	account := NewInternalBankAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-001").
		WithAccountCode("TEST").
		WithName("Test").
		WithAccountType(AccountTypeClearing).
		WithAttributes(originalAttrs).
		Build()

	// Modify original map
	originalAttrs["key"] = "modified"
	originalAttrs["new_key"] = "new_value"

	// Verify account attributes are unchanged
	attrs := account.Attributes()
	assert.Equal(t, "value", attrs["key"], "builder should deep copy attributes")
	assert.NotContains(t, attrs, "new_key", "builder should deep copy attributes")
}

func TestVersionIncrement(t *testing.T) {
	account := createTestAccount(t, AccountTypeClearing)
	assert.Equal(t, int64(1), account.Version())

	// First transition
	suspended, err := account.Suspend("Test")
	require.NoError(t, err)
	assert.Equal(t, int64(2), suspended.Version())

	// Second transition
	reactivated, err := suspended.Activate()
	require.NoError(t, err)
	assert.Equal(t, int64(3), reactivated.Version())

	// Third transition
	closed, err := reactivated.Close("Done")
	require.NoError(t, err)
	assert.Equal(t, int64(4), closed.Version())

	// Verify original still has version 1
	assert.Equal(t, int64(1), account.Version())
}

func TestVersionIncrement_UpdateCorrespondent(t *testing.T) {
	nostro := createTestAccount(t, AccountTypeNostro)
	assert.Equal(t, int64(1), nostro.Version())

	correspondent, err := NewCorrespondentDetails(
		"BANK001",
		"Test Bank",
		"REF-001",
	)
	require.NoError(t, err)

	// Update correspondent should increment version
	updated, err := nostro.UpdateCorrespondent(correspondent)
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version())

	// Original should still be version 1
	assert.Equal(t, int64(1), nostro.Version())
}

// createTestAccount is a helper function to create a test account with the given type.
func createTestAccount(t *testing.T, accountType AccountType) InternalBankAccount {
	t.Helper()

	accountID := "IBA-TEST"
	accountCode := "TEST_" + string(accountType)
	name := "Test " + string(accountType) + " Account"

	account, err := NewInternalBankAccount(
		accountID,
		accountCode,
		name,
		accountType,
		"USD",
		"CURRENCY",
	)
	require.NoError(t, err)

	return account
}
