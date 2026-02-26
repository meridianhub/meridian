package persistence

// Tests for multi-asset (non-CURRENCY) account, lien, and withdrawal persistence.
// These tests validate that precision is correctly persisted and retrieved
// for ENERGY, CARBON, and other non-CURRENCY dimensions.

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSaveAndFindAccount_GBP validates a standard CURRENCY account round-trip
// with precision correctly persisted (precision=2 for GBP).
func TestSaveAndFindAccount_GBP(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765432"

	account, err := domain.NewCurrentAccount(accountID, iban, partyID, "GBP")
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	assert.Equal(t, "GBP", retrieved.InstrumentCode())
	assert.Equal(t, "CURRENCY", retrieved.Dimension())
	assert.Equal(t, 2, retrieved.Balance().Precision(), "GBP should have precision 2")
}

// TestSaveAndFindAccount_KWH validates an ENERGY account round-trip with precision=3.
func TestSaveAndFindAccount_KWH(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765433"

	account, err := domain.NewCurrentAccountWithDimension(accountID, iban, partyID, "KWH", "ENERGY", 3)
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	assert.Equal(t, "KWH", retrieved.InstrumentCode())
	assert.Equal(t, "ENERGY", retrieved.Dimension())
	assert.Equal(t, 3, retrieved.Balance().Precision(), "KWH should have precision 3")
}

// TestSaveAndFindAccount_CarbonCredit validates a CARBON account round-trip with precision=4.
func TestSaveAndFindAccount_CarbonCredit(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	partyID := uuid.New().String()
	accountID := "ACC-" + uuid.New().String()[:8]
	iban := "GB82WEST12345698765434"

	account, err := domain.NewCurrentAccountWithDimension(accountID, iban, partyID, "CARBON_CREDIT", "CARBON", 4)
	require.NoError(t, err)

	err = repo.Save(ctx, account)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, accountID)
	require.NoError(t, err)

	assert.Equal(t, "CARBON_CREDIT", retrieved.InstrumentCode())
	assert.Equal(t, "CARBON", retrieved.Dimension())
	assert.Equal(t, 4, retrieved.Balance().Precision(), "CARBON_CREDIT should have precision 4")
}

// TestPrecisionPersistedAndRetrieved validates that precision is stored and
// reconstructed correctly for non-CURRENCY accounts.
func TestPrecisionPersistedAndRetrieved(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	cases := []struct {
		name           string
		instrumentCode string
		dimension      string
		precision      int
		iban           string
		accountID      string
	}{
		{
			name:           "GBP currency precision 2",
			instrumentCode: "GBP",
			dimension:      "CURRENCY",
			precision:      2,
			iban:           "GB82WEST12345698765435",
			accountID:      "ACC-" + uuid.New().String()[:8],
		},
		{
			name:           "KWH energy precision 3",
			instrumentCode: "KWH",
			dimension:      "ENERGY",
			precision:      3,
			iban:           "GB82WEST12345698765436",
			accountID:      "ACC-" + uuid.New().String()[:8],
		},
		{
			name:           "CARBON_CREDIT carbon precision 4",
			instrumentCode: "CARBON_CREDIT",
			dimension:      "CARBON",
			precision:      4,
			iban:           "GB82WEST12345698765437",
			accountID:      "ACC-" + uuid.New().String()[:8],
		},
		{
			name:           "GPU_HOUR compute precision 6",
			instrumentCode: "GPU_HOUR",
			dimension:      "COMPUTE",
			precision:      6,
			iban:           "GB82WEST12345698765438",
			accountID:      "ACC-" + uuid.New().String()[:8],
		},
	}

	partyID := uuid.New().String()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var account domain.CurrentAccount
			var err error
			if tc.dimension == "CURRENCY" {
				account, err = domain.NewCurrentAccount(tc.accountID, tc.iban, partyID, tc.instrumentCode)
			} else {
				account, err = domain.NewCurrentAccountWithDimension(tc.accountID, tc.iban, partyID, tc.instrumentCode, tc.dimension, tc.precision)
			}
			require.NoError(t, err)

			err = repo.Save(ctx, account)
			require.NoError(t, err, "Save should succeed for %s", tc.name)

			retrieved, err := repo.FindByID(ctx, tc.accountID)
			require.NoError(t, err, "FindByID should succeed for %s", tc.name)

			assert.Equal(t, tc.instrumentCode, retrieved.InstrumentCode(), "instrument_code should match for %s", tc.name)
			assert.Equal(t, tc.dimension, retrieved.Dimension(), "dimension should match for %s", tc.name)
			assert.Equal(t, tc.precision, retrieved.Balance().Precision(), "precision should match for %s", tc.name)
		})
	}
}

// TestLienCreateAndRetrieve_NonCurrency validates lien creation and retrieval
// for non-CURRENCY instruments.
func TestLienCreateAndRetrieve_NonCurrency(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Create a KWH lien (ENERGY, precision=3)
	amount, err := domain.NewAmountFromInstrument("KWH", "ENERGY", 3, 1500) // 1.500 KWH
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "energy-bucket", "PO-ENERGY-001", nil)
	require.NoError(t, err)

	err = repo.Create(ctx, lien)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, lien.ID)
	require.NoError(t, err)

	assert.Equal(t, "KWH", retrieved.Amount.InstrumentCode())
	assert.Equal(t, "ENERGY", retrieved.Amount.Dimension())
	assert.Equal(t, 3, retrieved.Amount.Precision())
	assert.Equal(t, int64(1500), retrieved.Amount.ToMinorUnitsUnchecked(), "minor units should be preserved")
}

// TestLienCreateAndRetrieve_Carbon validates lien creation and retrieval for CARBON dimension.
func TestLienCreateAndRetrieve_Carbon(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Create a CARBON_CREDIT lien (CARBON, precision=4)
	amount, err := domain.NewAmountFromInstrument("CARBON_CREDIT", "CARBON", 4, 25000) // 2.5000 credits
	require.NoError(t, err)

	lien, err := domain.NewLien(accountID, amount, "carbon-bucket", "PO-CARBON-001", nil)
	require.NoError(t, err)

	err = repo.Create(ctx, lien)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, lien.ID)
	require.NoError(t, err)

	assert.Equal(t, "CARBON_CREDIT", retrieved.Amount.InstrumentCode())
	assert.Equal(t, "CARBON", retrieved.Amount.Dimension())
	assert.Equal(t, 4, retrieved.Amount.Precision())
	assert.Equal(t, int64(25000), retrieved.Amount.ToMinorUnitsUnchecked(), "minor units should be preserved")
}

// TestWithdrawalCreateAndRetrieve_NonCurrency validates withdrawal creation and
// retrieval for non-CURRENCY instruments.
func TestWithdrawalCreateAndRetrieve_NonCurrency(t *testing.T) {
	db, ctx, cleanup := setupWithdrawalTestDB(t)
	defer cleanup()

	repo := NewWithdrawalRepository(db)
	accountID := uuid.New()

	// Create a KWH withdrawal (ENERGY, precision=3)
	amount, err := domain.NewAmountFromInstrument("KWH", "ENERGY", 3, 5000) // 5.000 KWH
	require.NoError(t, err)

	withdrawal, err := domain.NewWithdrawal(accountID, amount, "WITHDRAWAL-ENERGY-001")
	require.NoError(t, err)

	err = repo.Create(ctx, withdrawal)
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, withdrawal.ID)
	require.NoError(t, err)

	assert.Equal(t, "KWH", retrieved.Amount.InstrumentCode())
	assert.Equal(t, "ENERGY", retrieved.Amount.Dimension())
	assert.Equal(t, 3, retrieved.Amount.Precision())
	assert.Equal(t, int64(5000), retrieved.Amount.ToMinorUnitsUnchecked(), "minor units should be preserved")
}

// TestBackwardCompat_LegacyCurrencyColumn validates that lien/withdrawal rows
// with empty instrument_code fall back to using the legacy currency column.
func TestBackwardCompat_LegacyCurrencyColumn(t *testing.T) {
	db, ctx, cleanup := setupLienTestDB(t)
	defer cleanup()

	repo := NewLienRepository(db)
	accountID := uuid.New()

	// Insert a lien with empty instrument_code (simulating pre-migration row)
	lienID := uuid.New()
	err := db.Exec(`INSERT INTO lien (id, account_id, amount_cents, currency, instrument_code, dimension, precision, bucket_id, status, payment_order_reference, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW(), 1)`,
		lienID, accountID, 5000, "GBP", "", "CURRENCY", 2, "", "ACTIVE", "PO-LEGACY-001",
	).Error
	require.NoError(t, err)

	retrieved, err := repo.FindByID(ctx, lienID)
	require.NoError(t, err)

	// Should fall back to currency column ("GBP") when instrument_code is empty
	assert.Equal(t, "GBP", retrieved.Amount.InstrumentCode())
	assert.Equal(t, "CURRENCY", retrieved.Amount.Dimension())
	assert.Equal(t, int64(5000), retrieved.Amount.ToMinorUnitsUnchecked())
}
