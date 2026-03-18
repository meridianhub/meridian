package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewInternalAccount_Success(t *testing.T) {
	tests := []struct {
		name            string
		accountID       string
		accountCode     string
		accountName     string
		accountType     AccountType
		clearingPurpose ClearingPurpose
		instrumentCode  string
		dimension       string
	}{
		{
			name:            "clearing account with general purpose",
			accountID:       "IBA-001",
			accountCode:     "GBP_CLEARING",
			accountName:     "GBP Clearing Account",
			accountType:     AccountTypeClearing,
			clearingPurpose: ClearingPurposeGeneral,
			instrumentCode:  "GBP",
			dimension:       "CURRENCY",
		},
		{
			name:            "nostro account",
			accountID:       "IBA-002",
			accountCode:     "USD_NOSTRO_CHASE",
			accountName:     "USD Nostro at Chase",
			accountType:     AccountTypeNostro,
			clearingPurpose: ClearingPurposeUnspecified,
			instrumentCode:  "USD",
			dimension:       "CURRENCY",
		},
		{
			name:            "vostro account",
			accountID:       "IBA-003",
			accountCode:     "EUR_VOSTRO_DB",
			accountName:     "EUR Vostro for Deutsche Bank",
			accountType:     AccountTypeVostro,
			clearingPurpose: ClearingPurposeUnspecified,
			instrumentCode:  "EUR",
			dimension:       "CURRENCY",
		},
		{
			name:            "suspense account",
			accountID:       "IBA-004",
			accountCode:     "SUSPENSE_001",
			accountName:     "General Suspense",
			accountType:     AccountTypeSuspense,
			clearingPurpose: ClearingPurposeUnspecified,
			instrumentCode:  "USD",
			dimension:       "CURRENCY",
		},
		{
			name:            "energy holding account",
			accountID:       "IBA-005",
			accountCode:     "KWH_HOLDING",
			accountName:     "Energy Holding Account",
			accountType:     AccountTypeHolding,
			clearingPurpose: ClearingPurposeUnspecified,
			instrumentCode:  "KWH",
			dimension:       "ENERGY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beforeCreate := time.Now()

			account, err := NewInternalAccount(
				tt.accountID,
				tt.accountCode,
				tt.accountName,
				tt.accountType,
				tt.clearingPurpose,
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
			assert.Equal(t, tt.clearingPurpose, account.ClearingPurpose())
			assert.Equal(t, tt.instrumentCode, account.InstrumentCode())
			assert.Equal(t, tt.dimension, account.Dimension())

			// Verify initial state
			assert.Equal(t, AccountStatusActive, account.Status(), "initial status should be ACTIVE")
			assert.Equal(t, int64(1), account.Version(), "initial version should be 1")
			assert.Nil(t, account.Counterparty(), "counterparty should be nil initially")
			assert.Nil(t, account.Attributes(), "attributes should be nil initially")

			// Verify timestamps
			assert.False(t, account.CreatedAt().Before(beforeCreate), "createdAt should be >= beforeCreate")
			assert.False(t, account.UpdatedAt().Before(beforeCreate), "updatedAt should be >= beforeCreate")
			assert.Equal(t, account.CreatedAt(), account.UpdatedAt(), "createdAt and updatedAt should match initially")
		})
	}
}

func TestNewInternalAccount_ValidationErrors(t *testing.T) {
	tests := []struct {
		name            string
		accountID       string
		accountCode     string
		accountName     string
		accountType     AccountType
		clearingPurpose ClearingPurpose
		instrumentCode  string
		dimension       string
		expectedErr     error
	}{
		{
			name:            "empty account ID",
			accountID:       "",
			accountCode:     "GBP_CLEARING",
			accountName:     "GBP Clearing Account",
			accountType:     AccountTypeClearing,
			clearingPurpose: ClearingPurposeGeneral,
			instrumentCode:  "GBP",
			dimension:       "CURRENCY",
			expectedErr:     ErrAccountIDRequired,
		},
		{
			name:            "empty account code",
			accountID:       "IBA-001",
			accountCode:     "",
			accountName:     "GBP Clearing Account",
			accountType:     AccountTypeClearing,
			clearingPurpose: ClearingPurposeGeneral,
			instrumentCode:  "GBP",
			dimension:       "CURRENCY",
			expectedErr:     ErrAccountCodeRequired,
		},
		{
			name:            "empty name",
			accountID:       "IBA-001",
			accountCode:     "GBP_CLEARING",
			accountName:     "",
			accountType:     AccountTypeClearing,
			clearingPurpose: ClearingPurposeGeneral,
			instrumentCode:  "GBP",
			dimension:       "CURRENCY",
			expectedErr:     ErrNameRequired,
		},
		{
			name:            "invalid account type",
			accountID:       "IBA-001",
			accountCode:     "GBP_CLEARING",
			accountName:     "GBP Clearing Account",
			accountType:     AccountType("INVALID"),
			clearingPurpose: ClearingPurposeGeneral,
			instrumentCode:  "GBP",
			dimension:       "CURRENCY",
			expectedErr:     ErrInvalidAccountType,
		},
		{
			name:            "empty account type",
			accountID:       "IBA-001",
			accountCode:     "GBP_CLEARING",
			accountName:     "GBP Clearing Account",
			accountType:     AccountType(""),
			clearingPurpose: ClearingPurposeGeneral,
			instrumentCode:  "GBP",
			dimension:       "CURRENCY",
			expectedErr:     ErrInvalidAccountType,
		},
		{
			name:            "invalid clearing purpose",
			accountID:       "IBA-001",
			accountCode:     "GBP_CLEARING",
			accountName:     "GBP Clearing Account",
			accountType:     AccountTypeClearing,
			clearingPurpose: ClearingPurpose("INVALID"),
			instrumentCode:  "GBP",
			dimension:       "CURRENCY",
			expectedErr:     ErrInvalidClearingPurpose,
		},
		{
			name:            "clearing account with unspecified purpose",
			accountID:       "IBA-001",
			accountCode:     "GBP_CLEARING",
			accountName:     "GBP Clearing Account",
			accountType:     AccountTypeClearing,
			clearingPurpose: ClearingPurposeUnspecified,
			instrumentCode:  "GBP",
			dimension:       "CURRENCY",
			expectedErr:     ErrClearingPurposeRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := NewInternalAccount(
				tt.accountID,
				tt.accountCode,
				tt.accountName,
				tt.accountType,
				tt.clearingPurpose,
				tt.instrumentCode,
				tt.dimension,
			)

			require.Error(t, err)
			assert.ErrorIs(t, err, tt.expectedErr)
			assert.Equal(t, InternalAccount{}, account, "should return zero value on error")
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
	time.Sleep(1 * time.Millisecond) //nolint:forbidigo // ensures distinct timestamps

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
	account := NewInternalAccountBuilder().
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

func TestUpdateCounterparty_Success(t *testing.T) {
	// Create a NOSTRO account
	nostroAccount := createTestAccount(t, AccountTypeNostro)

	// Create counterparty details
	counterparty, err := NewCounterpartyDetails(
		"CHASE001",
		"JPMorgan Chase Bank",
		"ACC-12345",
	)
	require.NoError(t, err)

	// Update counterparty
	updated, err := nostroAccount.UpdateCounterparty(counterparty)
	require.NoError(t, err)

	assert.NotNil(t, updated.Counterparty())
	assert.Equal(t, "CHASE001", updated.Counterparty().CounterpartyID())
	assert.Equal(t, int64(2), updated.Version())

	// Verify original is unchanged
	assert.Nil(t, nostroAccount.Counterparty())
	assert.Equal(t, int64(1), nostroAccount.Version())
}

func TestUpdateCounterparty_VostroAccount(t *testing.T) {
	// Create a VOSTRO account
	vostroAccount := createTestAccount(t, AccountTypeVostro)

	// Create counterparty details
	counterparty, err := NewCounterpartyDetails(
		"DB001",
		"Deutsche Bank",
		"VOSTRO-REF-001",
	)
	require.NoError(t, err)

	// Update counterparty
	updated, err := vostroAccount.UpdateCounterparty(counterparty)
	require.NoError(t, err)

	assert.NotNil(t, updated.Counterparty())
	assert.Equal(t, "DB001", updated.Counterparty().CounterpartyID())
}

func TestUpdateCounterparty_ValidationForType(t *testing.T) {
	t.Run("NOSTRO requires counterparty", func(t *testing.T) {
		nostro := createTestAccount(t, AccountTypeNostro)

		// Try to set nil counterparty on NOSTRO
		_, err := nostro.UpdateCounterparty(nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCounterpartyRequired)
	})

	t.Run("VOSTRO requires counterparty", func(t *testing.T) {
		vostro := createTestAccount(t, AccountTypeVostro)

		// Try to set nil counterparty on VOSTRO
		_, err := vostro.UpdateCounterparty(nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCounterpartyRequired)
	})

	t.Run("CLEARING rejects counterparty", func(t *testing.T) {
		clearing := createTestAccount(t, AccountTypeClearing)

		counterparty, err := NewCounterpartyDetails(
			"BANK001",
			"Some Bank",
			"REF-001",
		)
		require.NoError(t, err)

		// Try to set counterparty on CLEARING account
		_, err = clearing.UpdateCounterparty(counterparty)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCounterpartyNotAllowed)
	})

	t.Run("HOLDING rejects counterparty", func(t *testing.T) {
		holding := createTestAccount(t, AccountTypeHolding)

		counterparty, err := NewCounterpartyDetails(
			"BANK001",
			"Some Bank",
			"REF-001",
		)
		require.NoError(t, err)

		// Try to set counterparty on HOLDING account
		_, err = holding.UpdateCounterparty(counterparty)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCounterpartyNotAllowed)
	})

	t.Run("SUSPENSE rejects counterparty", func(t *testing.T) {
		suspense := createTestAccount(t, AccountTypeSuspense)

		counterparty, err := NewCounterpartyDetails(
			"BANK001",
			"Some Bank",
			"REF-001",
		)
		require.NoError(t, err)

		_, err = suspense.UpdateCounterparty(counterparty)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCounterpartyNotAllowed)
	})
}

func TestUpdateCounterparty_ClosedAccount(t *testing.T) {
	nostro := createTestAccount(t, AccountTypeNostro)

	// Close the account
	closed, err := nostro.Close("Decommissioned")
	require.NoError(t, err)

	// Try to update counterparty on closed account
	counterparty, err := NewCounterpartyDetails(
		"BANK001",
		"Some Bank",
		"REF-001",
	)
	require.NoError(t, err)

	_, err = closed.UpdateCounterparty(counterparty)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountClosed)
}

func TestRename_Success(t *testing.T) {
	account, err := NewInternalAccount(
		"IBA-001",
		"CLR-001",
		"Original Name",
		AccountTypeClearing,
		ClearingPurposeGeneral,
		"USD",
		"CURRENCY",
	)
	require.NoError(t, err)
	assert.Equal(t, "Original Name", account.Name())
	assert.Equal(t, int64(1), account.Version())

	// Rename the account
	renamed, err := account.Rename("Updated Name")
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", renamed.Name())
	assert.Equal(t, int64(2), renamed.Version())

	// Original should be unchanged (immutability)
	assert.Equal(t, "Original Name", account.Name())
	assert.Equal(t, int64(1), account.Version())
}

func TestRename_EmptyName(t *testing.T) {
	account, err := NewInternalAccount(
		"IBA-001",
		"CLR-001",
		"Original Name",
		AccountTypeClearing,
		ClearingPurposeGeneral,
		"USD",
		"CURRENCY",
	)
	require.NoError(t, err)

	_, err = account.Rename("")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNameRequired)
}

func TestRename_ClosedAccount(t *testing.T) {
	account, err := NewInternalAccount(
		"IBA-001",
		"CLR-001",
		"Original Name",
		AccountTypeClearing,
		ClearingPurposeGeneral,
		"USD",
		"CURRENCY",
	)
	require.NoError(t, err)

	// Close the account
	closed, err := account.Close("Decommissioned")
	require.NoError(t, err)

	// Try to rename closed account
	_, err = closed.Rename("New Name")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAccountClosed)
}

func TestBuilder_Reconstruction(t *testing.T) {
	// Simulate values from persistence
	id := uuid.New()
	createdAt := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC)
	counterparty, err := NewCounterpartyDetails(
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
	account := NewInternalAccountBuilder().
		WithID(id).
		WithAccountID("IBA-001").
		WithAccountCode("USD_NOSTRO_CHASE").
		WithName("USD Nostro at Chase").
		WithAccountType(AccountTypeNostro).
		WithInstrumentCode("USD").
		WithDimension("CURRENCY").
		WithStatus(AccountStatusSuspended).
		WithCounterparty(counterparty).
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
	assert.NotNil(t, account.Counterparty())
	assert.Equal(t, "CHASE001", account.Counterparty().CounterpartyID())
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

	account := NewInternalAccountBuilder().
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

func TestVersionIncrement_UpdateCounterparty(t *testing.T) {
	nostro := createTestAccount(t, AccountTypeNostro)
	assert.Equal(t, int64(1), nostro.Version())

	counterparty, err := NewCounterpartyDetails(
		"BANK001",
		"Test Bank",
		"REF-001",
	)
	require.NoError(t, err)

	// Update counterparty should increment version
	updated, err := nostro.UpdateCounterparty(counterparty)
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version())

	// Original should still be version 1
	assert.Equal(t, int64(1), nostro.Version())
}

func TestVersionIncrement_Rename(t *testing.T) {
	account := createTestAccount(t, AccountTypeClearing)
	assert.Equal(t, int64(1), account.Version())

	// Rename should increment version
	renamed, err := account.Rename("New Name")
	require.NoError(t, err)
	assert.Equal(t, int64(2), renamed.Version())

	// Original should still be version 1
	assert.Equal(t, int64(1), account.Version())
}

func TestImmutability_StatusChangePreservesAttributes(t *testing.T) {
	// Test that status changes on an account WITH attributes properly deep copies them
	// This tests the copyWithUpdatedTime method's attribute deep copy branch
	attrs := map[string]string{"key": "value", "another": "attr"}
	account := NewInternalAccountBuilder().
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

	// Suspend the account - this triggers copyWithUpdatedTime which should deep copy attributes
	suspended, err := account.Suspend("Testing attribute preservation")
	require.NoError(t, err)

	// Verify attributes are preserved in the new account
	suspendedAttrs := suspended.Attributes()
	require.NotNil(t, suspendedAttrs)
	assert.Equal(t, "value", suspendedAttrs["key"])
	assert.Equal(t, "attr", suspendedAttrs["another"])

	// Modify the suspended account's attributes
	suspendedAttrs["key"] = "modified"
	suspendedAttrs["new_key"] = "new_value"

	// Verify original suspended account is not affected
	freshSuspendedAttrs := suspended.Attributes()
	assert.Equal(t, "value", freshSuspendedAttrs["key"], "modifying returned attributes should not affect internal state")
	assert.NotContains(t, freshSuspendedAttrs, "new_key")

	// Verify original account attributes are also unchanged
	originalAttrs := account.Attributes()
	assert.Equal(t, "value", originalAttrs["key"])
	assert.NotContains(t, originalAttrs, "new_key")
}

func TestImmutability_RenamePreservesAttributes(t *testing.T) {
	// Test that rename on an account WITH attributes properly deep copies them
	attrs := map[string]string{"department": "treasury"}
	account := NewInternalAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-001").
		WithAccountCode("TEST_CLEARING").
		WithName("Original Name").
		WithAccountType(AccountTypeClearing).
		WithInstrumentCode("USD").
		WithDimension("CURRENCY").
		WithStatus(AccountStatusActive).
		WithAttributes(attrs).
		WithVersion(1).
		WithCreatedAt(time.Now()).
		WithUpdatedAt(time.Now()).
		Build()

	// Rename the account - this triggers copyWithUpdatedTime
	renamed, err := account.Rename("New Name")
	require.NoError(t, err)

	// Verify attributes are preserved
	renamedAttrs := renamed.Attributes()
	require.NotNil(t, renamedAttrs)
	assert.Equal(t, "treasury", renamedAttrs["department"])
}

func TestUpdateCounterparty_PreservesAttributes(t *testing.T) {
	// Test that UpdateCounterparty on an account WITH attributes properly deep copies them
	attrs := map[string]string{"category": "international"}
	account := NewInternalAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-001").
		WithAccountCode("TEST_NOSTRO").
		WithName("Test Nostro").
		WithAccountType(AccountTypeNostro).
		WithInstrumentCode("USD").
		WithDimension("CURRENCY").
		WithStatus(AccountStatusActive).
		WithAttributes(attrs).
		WithVersion(1).
		WithCreatedAt(time.Now()).
		WithUpdatedAt(time.Now()).
		Build()

	counterparty, err := NewCounterpartyDetails(
		"CHASE001",
		"JPMorgan Chase Bank",
		"ACC-12345",
	)
	require.NoError(t, err)

	// Update counterparty - this triggers copyWithUpdatedTime
	updated, err := account.UpdateCounterparty(counterparty)
	require.NoError(t, err)

	// Verify attributes are preserved
	updatedAttrs := updated.Attributes()
	require.NotNil(t, updatedAttrs)
	assert.Equal(t, "international", updatedAttrs["category"])
}

func TestUpdateCounterparty_RevenueRejectsCounterparty(t *testing.T) {
	revenue := createTestAccount(t, AccountTypeRevenue)

	counterparty, err := NewCounterpartyDetails(
		"BANK001",
		"Some Bank",
		"REF-001",
	)
	require.NoError(t, err)

	// Try to set counterparty on REVENUE account
	_, err = revenue.UpdateCounterparty(counterparty)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCounterpartyNotAllowed)
}

func TestUpdateCounterparty_ExpenseRejectsCounterparty(t *testing.T) {
	expense := createTestAccount(t, AccountTypeExpense)

	counterparty, err := NewCounterpartyDetails(
		"BANK001",
		"Some Bank",
		"REF-001",
	)
	require.NoError(t, err)

	// Try to set counterparty on EXPENSE account
	_, err = expense.UpdateCounterparty(counterparty)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCounterpartyNotAllowed)
}

func TestBuilder_WithNilAttributes(t *testing.T) {
	// Test builder with nil attributes
	account := NewInternalAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-001").
		WithAccountCode("TEST").
		WithName("Test").
		WithAccountType(AccountTypeClearing).
		WithInstrumentCode("USD").
		WithDimension("CURRENCY").
		WithStatus(AccountStatusActive).
		WithAttributes(nil).
		WithVersion(1).
		WithCreatedAt(time.Now()).
		WithUpdatedAt(time.Now()).
		Build()

	// Verify attributes are nil
	assert.Nil(t, account.Attributes())
}

func TestBuilder_MinimalFields(t *testing.T) {
	// Test builder with only essential fields set
	id := uuid.New()
	account := NewInternalAccountBuilder().
		WithID(id).
		WithAccountID("IBA-001").
		WithAccountCode("TEST").
		WithName("Test Account").
		WithAccountType(AccountTypeClearing).
		Build()

	// Verify essential fields are set
	assert.Equal(t, id, account.ID())
	assert.Equal(t, "IBA-001", account.AccountID())
	assert.Equal(t, "TEST", account.AccountCode())
	assert.Equal(t, "Test Account", account.Name())
	assert.Equal(t, AccountTypeClearing, account.AccountType())

	// Verify optional fields have zero values
	assert.Empty(t, account.InstrumentCode())
	assert.Empty(t, account.Dimension())
	assert.Equal(t, AccountStatus(""), account.Status())
	assert.Nil(t, account.Counterparty())
	assert.Nil(t, account.Attributes())
	assert.Equal(t, int64(0), account.Version())
	assert.True(t, account.CreatedAt().IsZero())
	assert.True(t, account.UpdatedAt().IsZero())
}

func TestNewInternalAccount_AllAccountTypes(t *testing.T) {
	// Test creating accounts with all valid account types
	tests := []struct {
		name            string
		accountType     AccountType
		clearingPurpose ClearingPurpose
	}{
		{"CLEARING", AccountTypeClearing, ClearingPurposeGeneral},
		{"NOSTRO", AccountTypeNostro, ClearingPurposeUnspecified},
		{"VOSTRO", AccountTypeVostro, ClearingPurposeUnspecified},
		{"HOLDING", AccountTypeHolding, ClearingPurposeUnspecified},
		{"SUSPENSE", AccountTypeSuspense, ClearingPurposeUnspecified},
		{"REVENUE", AccountTypeRevenue, ClearingPurposeUnspecified},
		{"EXPENSE", AccountTypeExpense, ClearingPurposeUnspecified},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := NewInternalAccount(
				"IBA-001",
				"CODE_"+tt.name,
				"Test "+tt.name+" Account",
				tt.accountType,
				tt.clearingPurpose,
				"USD",
				"CURRENCY",
			)

			require.NoError(t, err)
			assert.Equal(t, tt.accountType, account.AccountType())
			assert.Equal(t, AccountStatusActive, account.Status())
		})
	}
}

func TestLifecycleTransitions_RenameFromSuspended(t *testing.T) {
	account := createTestAccount(t, AccountTypeClearing)

	// Suspend the account
	suspended, err := account.Suspend("Maintenance")
	require.NoError(t, err)

	// Rename should work on suspended accounts
	renamed, err := suspended.Rename("Renamed While Suspended")
	require.NoError(t, err)
	assert.Equal(t, "Renamed While Suspended", renamed.Name())
	assert.Equal(t, AccountStatusSuspended, renamed.Status())
}

func TestUpdateCounterparty_ReplaceExisting(t *testing.T) {
	// Create a NOSTRO account with counterparty already set via builder
	originalCounterparty, err := NewCounterpartyDetails(
		"OLD_BANK",
		"Old Bank Name",
		"OLD-REF-001",
	)
	require.NoError(t, err)

	account := NewInternalAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-001").
		WithAccountCode("TEST_NOSTRO").
		WithName("Test Nostro").
		WithAccountType(AccountTypeNostro).
		WithInstrumentCode("USD").
		WithDimension("CURRENCY").
		WithStatus(AccountStatusActive).
		WithCounterparty(originalCounterparty).
		WithVersion(1).
		WithCreatedAt(time.Now()).
		WithUpdatedAt(time.Now()).
		Build()

	// Verify original counterparty is set
	assert.Equal(t, "OLD_BANK", account.Counterparty().CounterpartyID())

	// Replace with new counterparty
	newCounterparty, err := NewCounterpartyDetails(
		"NEW_BANK",
		"New Bank Name",
		"NEW-REF-001",
	)
	require.NoError(t, err)

	updated, err := account.UpdateCounterparty(newCounterparty)
	require.NoError(t, err)

	// Verify new counterparty is set
	assert.Equal(t, "NEW_BANK", updated.Counterparty().CounterpartyID())
	assert.Equal(t, int64(2), updated.Version())

	// Verify original account still has old counterparty
	assert.Equal(t, "OLD_BANK", account.Counterparty().CounterpartyID())
	assert.Equal(t, int64(1), account.Version())
}

func TestAttributes_NilReturnedForNilInternal(t *testing.T) {
	// Test that Attributes() returns nil when internal attributes are nil
	account := createTestAccount(t, AccountTypeClearing)

	// Account created via NewInternalAccount has nil attributes
	assert.Nil(t, account.Attributes())
}

func TestTimestamps_UpdatedAtChangesOnModification(t *testing.T) {
	account := createTestAccount(t, AccountTypeClearing)
	originalUpdatedAt := account.UpdatedAt()

	// Wait a tiny bit to ensure timestamp difference
	time.Sleep(1 * time.Millisecond) //nolint:forbidigo // ensures distinct timestamps

	// Suspend should update the timestamp
	suspended, err := account.Suspend("Test")
	require.NoError(t, err)

	assert.True(t, suspended.UpdatedAt().After(originalUpdatedAt) || suspended.UpdatedAt().Equal(originalUpdatedAt),
		"updatedAt should be equal or after original")
}

func TestCreatedAt_NeverChanges(t *testing.T) {
	account := createTestAccount(t, AccountTypeClearing)
	originalCreatedAt := account.CreatedAt()

	// Multiple modifications should not change createdAt
	suspended, err := account.Suspend("Test")
	require.NoError(t, err)
	assert.Equal(t, originalCreatedAt, suspended.CreatedAt())

	reactivated, err := suspended.Activate()
	require.NoError(t, err)
	assert.Equal(t, originalCreatedAt, reactivated.CreatedAt())

	closed, err := reactivated.Close("Done")
	require.NoError(t, err)
	assert.Equal(t, originalCreatedAt, closed.CreatedAt())
}

// createTestAccount is a helper function to create a test account with the given type.
func createTestAccount(t *testing.T, accountType AccountType) InternalAccount {
	t.Helper()

	accountID := "IBA-TEST"
	accountCode := "TEST_" + string(accountType)
	name := "Test " + string(accountType) + " Account"

	// CLEARING accounts require a specific purpose, other types must use UNSPECIFIED
	clearingPurpose := ClearingPurposeUnspecified
	if accountType == AccountTypeClearing {
		clearingPurpose = ClearingPurposeGeneral
	}

	account, err := NewInternalAccount(
		accountID,
		accountCode,
		name,
		accountType,
		clearingPurpose,
		"USD",
		"CURRENCY",
	)
	require.NoError(t, err)

	return account
}

func TestNewInternalAccount_ClearingTypeWithPurpose(t *testing.T) {
	// CLEARING accounts must have a specific clearing purpose (not UNSPECIFIED)
	tests := []struct {
		name            string
		clearingPurpose ClearingPurpose
	}{
		{"DEPOSIT purpose", ClearingPurposeDeposit},
		{"WITHDRAWAL purpose", ClearingPurposeWithdrawal},
		{"SETTLEMENT purpose", ClearingPurposeSettlement},
		{"GENERAL purpose", ClearingPurposeGeneral},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, err := NewInternalAccount(
				"IBA-001",
				"GBP_CLEARING",
				"GBP Clearing Account",
				AccountTypeClearing,
				tt.clearingPurpose,
				"GBP",
				"CURRENCY",
			)

			require.NoError(t, err)
			assert.Equal(t, AccountTypeClearing, account.AccountType())
			assert.Equal(t, tt.clearingPurpose, account.ClearingPurpose())
		})
	}
}

func TestNewInternalAccount_NonClearingTypeWithPurpose(t *testing.T) {
	// Non-CLEARING accounts must not have a specific clearing purpose (only UNSPECIFIED is allowed)
	nonClearingTypes := []AccountType{
		AccountTypeNostro,
		AccountTypeVostro,
		AccountTypeHolding,
		AccountTypeSuspense,
		AccountTypeRevenue,
		AccountTypeExpense,
	}

	specificPurposes := []ClearingPurpose{
		ClearingPurposeDeposit,
		ClearingPurposeWithdrawal,
		ClearingPurposeSettlement,
		ClearingPurposeGeneral,
	}

	for _, accountType := range nonClearingTypes {
		for _, purpose := range specificPurposes {
			testName := string(accountType) + " with " + string(purpose)
			t.Run(testName, func(t *testing.T) {
				account, err := NewInternalAccount(
					"IBA-001",
					"TEST_ACCOUNT",
					"Test Account",
					accountType,
					purpose,
					"USD",
					"CURRENCY",
				)

				require.Error(t, err)
				assert.ErrorIs(t, err, ErrClearingPurposeNotAllowed)
				assert.Equal(t, InternalAccount{}, account, "should return zero value on error")
			})
		}
	}
}

func TestNewInternalAccount_ClearingTypeWithUnspecified(t *testing.T) {
	// CLEARING accounts with UNSPECIFIED purpose should be rejected
	account, err := NewInternalAccount(
		"IBA-001",
		"GBP_CLEARING",
		"GBP Clearing Account",
		AccountTypeClearing,
		ClearingPurposeUnspecified,
		"GBP",
		"CURRENCY",
	)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrClearingPurposeRequired)
	assert.Equal(t, InternalAccount{}, account, "should return zero value on error")
}

func TestBuilder_WithClearingPurpose(t *testing.T) {
	// Test builder method for setting clearing purpose
	id := uuid.New()
	account := NewInternalAccountBuilder().
		WithID(id).
		WithAccountID("IBA-001").
		WithAccountCode("GBP_CLEARING_DEPOSIT").
		WithName("GBP Deposit Clearing").
		WithAccountType(AccountTypeClearing).
		WithClearingPurpose(ClearingPurposeDeposit).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithStatus(AccountStatusActive).
		WithVersion(1).
		WithCreatedAt(time.Now()).
		WithUpdatedAt(time.Now()).
		Build()

	assert.Equal(t, id, account.ID())
	assert.Equal(t, AccountTypeClearing, account.AccountType())
	assert.Equal(t, ClearingPurposeDeposit, account.ClearingPurpose())
}

func TestBuilder_WithClearingPurpose_DefaultValue(t *testing.T) {
	// Test that builder without WithClearingPurpose returns zero value (empty string)
	account := NewInternalAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-001").
		WithAccountCode("TEST").
		WithName("Test").
		WithAccountType(AccountTypeClearing).
		Build()

	// Without calling WithClearingPurpose, the value should be the zero value
	assert.Equal(t, ClearingPurpose(""), account.ClearingPurpose())
}

func TestNewInternalAccount_OrgScoped_NonClearingSuccess(t *testing.T) {
	orgID := uuid.New()

	// Non-CLEARING account types should succeed with org scoping
	nonClearingTypes := []AccountType{
		AccountTypeNostro,
		AccountTypeVostro,
		AccountTypeHolding,
		AccountTypeSuspense,
		AccountTypeRevenue,
		AccountTypeExpense,
	}

	for _, accountType := range nonClearingTypes {
		t.Run(string(accountType), func(t *testing.T) {
			account, err := NewInternalAccount(
				"IBA-ORG-001",
				"ORG_"+string(accountType),
				"Org Scoped "+string(accountType),
				accountType,
				ClearingPurposeUnspecified,
				"USD",
				"CURRENCY",
				WithOrgPartyID(orgID),
			)

			require.NoError(t, err)
			require.NotNil(t, account.OrgPartyID())
			assert.Equal(t, orgID, *account.OrgPartyID())
			assert.Equal(t, accountType, account.AccountType())
		})
	}
}

func TestNewInternalAccount_OrgScoped_ClearingRejected(t *testing.T) {
	orgID := uuid.New()

	account, err := NewInternalAccount(
		"IBA-ORG-002",
		"ORG_CLEARING",
		"Org Scoped Clearing",
		AccountTypeClearing,
		ClearingPurposeGeneral,
		"USD",
		"CURRENCY",
		WithOrgPartyID(orgID),
	)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOrgScopedClearingNotAllowed)
	assert.Equal(t, InternalAccount{}, account, "should return zero value on error")
}

func TestNewInternalAccount_GlobalAccount_NilOrgPartyID(t *testing.T) {
	// Default (no WithOrgPartyID option) should result in nil OrgPartyID
	account, err := NewInternalAccount(
		"IBA-GLOBAL",
		"GLOBAL_CLEARING",
		"Global Clearing",
		AccountTypeClearing,
		ClearingPurposeGeneral,
		"USD",
		"CURRENCY",
	)

	require.NoError(t, err)
	assert.Nil(t, account.OrgPartyID(), "global account should have nil OrgPartyID")
}

func TestBuilder_WithOrgPartyID(t *testing.T) {
	orgID := uuid.New()

	account := NewInternalAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-001").
		WithAccountCode("ORG_NOSTRO").
		WithName("Org Nostro").
		WithAccountType(AccountTypeNostro).
		WithOrgPartyID(&orgID).
		WithInstrumentCode("USD").
		WithDimension("CURRENCY").
		WithStatus(AccountStatusActive).
		WithVersion(1).
		WithCreatedAt(time.Now()).
		WithUpdatedAt(time.Now()).
		Build()

	require.NotNil(t, account.OrgPartyID())
	assert.Equal(t, orgID, *account.OrgPartyID())
}

func TestIsScopedToOrganization(t *testing.T) {
	t.Run("global account returns false", func(t *testing.T) {
		account, err := NewInternalAccount(
			"IBA-GLOBAL",
			"GLOBAL_HOLDING",
			"Global Holding",
			AccountTypeHolding,
			ClearingPurposeUnspecified,
			"USD",
			"CURRENCY",
		)
		require.NoError(t, err)
		assert.False(t, account.IsScopedToOrganization())
	})

	t.Run("org-scoped account returns true", func(t *testing.T) {
		orgID := uuid.New()
		account, err := NewInternalAccount(
			"IBA-ORG",
			"ORG_HOLDING",
			"Org Holding",
			AccountTypeHolding,
			ClearingPurposeUnspecified,
			"USD",
			"CURRENCY",
			WithOrgPartyID(orgID),
		)
		require.NoError(t, err)
		assert.True(t, account.IsScopedToOrganization())
	})

	t.Run("builder with nil OrgPartyID returns false", func(t *testing.T) {
		account := NewInternalAccountBuilder().
			WithID(uuid.New()).
			WithAccountID("IBA-001").
			WithAccountCode("TEST").
			WithName("Test").
			WithAccountType(AccountTypeHolding).
			WithOrgPartyID(nil).
			Build()
		assert.False(t, account.IsScopedToOrganization())
	})

	t.Run("builder with OrgPartyID returns true", func(t *testing.T) {
		orgID := uuid.New()
		account := NewInternalAccountBuilder().
			WithID(uuid.New()).
			WithAccountID("IBA-001").
			WithAccountCode("TEST").
			WithName("Test").
			WithAccountType(AccountTypeHolding).
			WithOrgPartyID(&orgID).
			Build()
		assert.True(t, account.IsScopedToOrganization())
	})
}

func TestBuilder_WithOrgPartyID_Nil(t *testing.T) {
	account := NewInternalAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-001").
		WithAccountCode("GLOBAL_CLEARING").
		WithName("Global Clearing").
		WithAccountType(AccountTypeClearing).
		WithOrgPartyID(nil).
		WithInstrumentCode("USD").
		WithDimension("CURRENCY").
		WithStatus(AccountStatusActive).
		WithVersion(1).
		WithCreatedAt(time.Now()).
		WithUpdatedAt(time.Now()).
		Build()

	assert.Nil(t, account.OrgPartyID())
}
