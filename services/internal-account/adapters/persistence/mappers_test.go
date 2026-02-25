package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ptr returns a pointer to the given string value.
func ptr(s string) *string {
	return &s
}

// createTestContextForMappers creates a context with audit information for mapper tests.
func createTestContextForMappers() context.Context {
	ctx := context.Background()
	return context.WithValue(ctx, auth.UserIDContextKey, "test-mapper-user")
}

// TestToEntity_BasicFields tests that toEntity correctly maps all basic fields.
func TestToEntity_BasicFields(t *testing.T) {
	ctx := createTestContextForMappers()

	// Create domain account
	account, err := domain.NewInternalAccount(
		"IBA-MAP-001",
		"GBP_CLEARING",
		"GBP Clearing Account",
		domain.AccountTypeClearing,
		domain.ClearingPurposeDeposit,
		"GBP",
		"CURRENCY",
	)
	require.NoError(t, err)

	// Convert to entity
	entity := toEntity(ctx, account)

	// Verify basic fields
	assert.Equal(t, account.ID(), entity.ID)
	assert.Equal(t, account.AccountID(), entity.AccountID)
	assert.Equal(t, account.AccountCode(), entity.AccountCode)
	assert.Equal(t, account.Name(), entity.Name)
	assert.Equal(t, string(account.AccountType()), entity.AccountType)
	require.NotNil(t, entity.ClearingPurpose)
	assert.Equal(t, string(account.ClearingPurpose()), *entity.ClearingPurpose)
	assert.Equal(t, account.InstrumentCode(), entity.InstrumentCode)
	assert.Equal(t, account.Dimension(), entity.Dimension)
	assert.Equal(t, string(account.Status()), entity.Status)
	assert.Equal(t, account.Version(), entity.Version)

	// Verify audit fields
	assert.Equal(t, "test-mapper-user", entity.CreatedBy)
	assert.Equal(t, "test-mapper-user", entity.UpdatedBy)
	assert.False(t, entity.CreatedAt.IsZero())
	assert.False(t, entity.UpdatedAt.IsZero())

	// Verify nullable fields are nil for non-correspondent accounts
	assert.Nil(t, entity.CorrespondentBankID)
	assert.Nil(t, entity.CorrespondentBankName)
	assert.Nil(t, entity.CorrespondentExternalRef)
}

// TestToEntity_WithCorrespondent tests mapping accounts with correspondent details.
func TestToEntity_WithCorrespondent(t *testing.T) {
	ctx := createTestContextForMappers()

	// Create NOSTRO account with correspondent details
	account, err := domain.NewInternalAccount(
		"IBA-MAP-002",
		"USD_NOSTRO_CITI",
		"USD NOSTRO at Citibank",
		domain.AccountTypeNostro,
		domain.ClearingPurposeUnspecified,
		"USD",
		"CURRENCY",
	)
	require.NoError(t, err)

	correspondent, err := domain.NewCorrespondentDetails("CITI001", "Citibank NA", "12345678")
	require.NoError(t, err)
	account, err = account.UpdateCorrespondent(correspondent)
	require.NoError(t, err)

	// Convert to entity
	entity := toEntity(ctx, account)

	// Verify correspondent fields
	require.NotNil(t, entity.CorrespondentBankID)
	require.NotNil(t, entity.CorrespondentBankName)
	require.NotNil(t, entity.CorrespondentExternalRef)

	assert.Equal(t, "CITI001", *entity.CorrespondentBankID)
	assert.Equal(t, "Citibank NA", *entity.CorrespondentBankName)
	assert.Equal(t, "12345678", *entity.CorrespondentExternalRef)
}

// TestToEntity_WithAttributes tests mapping accounts with JSONB attributes.
func TestToEntity_WithAttributes(t *testing.T) {
	ctx := createTestContextForMappers()

	// Create account with attributes using builder
	account := domain.NewInternalAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-MAP-003").
		WithAccountCode("GBP_SPECIAL").
		WithName("GBP Special Account").
		WithAccountType(domain.AccountTypeClearing).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithStatus(domain.AccountStatusActive).
		WithAttributes(map[string]string{
			"cost_center": "CC001",
			"department":  "Treasury",
		}).
		WithVersion(1).
		Build()

	// Convert to entity
	entity := toEntity(ctx, account)

	// Verify attributes
	require.NotNil(t, entity.Attributes)
	assert.Len(t, entity.Attributes, 2)
	assert.Equal(t, "CC001", entity.Attributes["cost_center"])
	assert.Equal(t, "Treasury", entity.Attributes["department"])
}

// TestToEntity_NilAttributes tests that nil attributes result in empty map.
func TestToEntity_NilAttributes(t *testing.T) {
	ctx := createTestContextForMappers()

	account, err := domain.NewInternalAccount(
		"IBA-MAP-004",
		"GBP_BASIC",
		"GBP Basic Account",
		domain.AccountTypeClearing,
		domain.ClearingPurposeGeneral, // CLEARING accounts require a specific purpose
		"GBP",
		"CURRENCY",
	)
	require.NoError(t, err)

	entity := toEntity(ctx, account)

	// Attributes should be empty map, not nil (for JSONB compatibility)
	require.NotNil(t, entity.Attributes)
	assert.Len(t, entity.Attributes, 0)
}

// TestToDomain_BasicFields tests that toDomain correctly maps all basic fields.
func TestToDomain_BasicFields(t *testing.T) {
	now := time.Now()
	entityID := uuid.New()

	entity := &InternalAccountEntity{
		ID:              entityID,
		AccountID:       "IBA-MAP-010",
		AccountCode:     "EUR_CLEARING",
		Name:            "EUR Clearing Account",
		AccountType:     "CLEARING",
		ClearingPurpose: ptr("CLEARING_PURPOSE_SETTLEMENT"),
		InstrumentCode:  "EUR",
		Dimension:       "CURRENCY",
		Status:          "ACTIVE",
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
		CreatedBy:       "system",
		UpdatedBy:       "system",
		Attributes:      make(AttributesJSON),
	}

	// Convert to domain
	account := toDomain(entity)

	// Verify basic fields
	assert.Equal(t, entityID, account.ID())
	assert.Equal(t, "IBA-MAP-010", account.AccountID())
	assert.Equal(t, "EUR_CLEARING", account.AccountCode())
	assert.Equal(t, "EUR Clearing Account", account.Name())
	assert.Equal(t, domain.AccountTypeClearing, account.AccountType())
	assert.Equal(t, domain.ClearingPurposeSettlement, account.ClearingPurpose())
	assert.Equal(t, "EUR", account.InstrumentCode())
	assert.Equal(t, "CURRENCY", account.Dimension())
	assert.Equal(t, domain.AccountStatusActive, account.Status())
	assert.Equal(t, int64(1), account.Version())

	// Verify correspondent is nil for non-NOSTRO/VOSTRO accounts
	assert.Nil(t, account.Correspondent())
}

// TestToDomain_WithCorrespondent tests reconstructing correspondent details from entity.
func TestToDomain_WithCorrespondent(t *testing.T) {
	now := time.Now()
	bankID := "CITI001"
	bankName := "Citibank NA"
	externalRef := "12345678"

	entity := &InternalAccountEntity{
		ID:                       uuid.New(),
		AccountID:                "IBA-MAP-011",
		AccountCode:              "USD_NOSTRO_CITI",
		Name:                     "USD NOSTRO at Citibank",
		AccountType:              "NOSTRO",
		ClearingPurpose:          nil, // NOSTRO accounts don't have clearing purpose
		InstrumentCode:           "USD",
		Dimension:                "CURRENCY",
		Status:                   "ACTIVE",
		CorrespondentBankID:      &bankID,
		CorrespondentBankName:    &bankName,
		CorrespondentExternalRef: &externalRef,
		Version:                  1,
		CreatedAt:                now,
		UpdatedAt:                now,
		CreatedBy:                "system",
		UpdatedBy:                "system",
		Attributes:               make(AttributesJSON),
	}

	// Convert to domain
	account := toDomain(entity)

	// Verify correspondent details reconstructed
	require.NotNil(t, account.Correspondent())
	assert.Equal(t, "CITI001", account.Correspondent().BankID())
	assert.Equal(t, "Citibank NA", account.Correspondent().BankName())
	assert.Equal(t, "12345678", account.Correspondent().ExternalAccountRef())
}

// TestToDomain_WithAttributes tests reconstructing attributes from entity.
func TestToDomain_WithAttributes(t *testing.T) {
	now := time.Now()

	entity := &InternalAccountEntity{
		ID:              uuid.New(),
		AccountID:       "IBA-MAP-012",
		AccountCode:     "GBP_SPECIAL",
		Name:            "GBP Special Account",
		AccountType:     "CLEARING",
		ClearingPurpose: ptr("CLEARING_PURPOSE_GENERAL"),
		InstrumentCode:  "GBP",
		Dimension:       "CURRENCY",
		Status:          "ACTIVE",
		Attributes: AttributesJSON{
			"cost_center": "CC001",
			"department":  "Treasury",
			"region":      "EMEA",
		},
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: "system",
		UpdatedBy: "system",
	}

	// Convert to domain
	account := toDomain(entity)

	// Verify attributes
	attrs := account.Attributes()
	require.NotNil(t, attrs)
	assert.Len(t, attrs, 3)
	assert.Equal(t, "CC001", attrs["cost_center"])
	assert.Equal(t, "Treasury", attrs["department"])
	assert.Equal(t, "EMEA", attrs["region"])
}

// TestToDomain_EmptyAttributes tests that empty attributes result in nil map in domain.
func TestToDomain_EmptyAttributes(t *testing.T) {
	now := time.Now()

	entity := &InternalAccountEntity{
		ID:              uuid.New(),
		AccountID:       "IBA-MAP-013",
		AccountCode:     "GBP_BASIC",
		Name:            "GBP Basic Account",
		AccountType:     "CLEARING",
		ClearingPurpose: nil, // nil means unspecified
		InstrumentCode:  "GBP",
		Dimension:       "CURRENCY",
		Status:          "ACTIVE",
		Attributes:      make(AttributesJSON),
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
		CreatedBy:       "system",
		UpdatedBy:       "system",
	}

	// Convert to domain
	account := toDomain(entity)

	// Verify attributes are nil for empty map
	assert.Nil(t, account.Attributes())
}

// TestToDomain_AllAccountTypes tests mapping for all account types.
func TestToDomain_AllAccountTypes(t *testing.T) {
	testCases := []struct {
		entityType string
		domainType domain.AccountType
	}{
		{"CLEARING", domain.AccountTypeClearing},
		{"NOSTRO", domain.AccountTypeNostro},
		{"VOSTRO", domain.AccountTypeVostro},
		{"HOLDING", domain.AccountTypeHolding},
		{"SUSPENSE", domain.AccountTypeSuspense},
		{"REVENUE", domain.AccountTypeRevenue},
		{"EXPENSE", domain.AccountTypeExpense},
	}

	for _, tc := range testCases {
		t.Run(tc.entityType, func(t *testing.T) {
			entity := &InternalAccountEntity{
				ID:              uuid.New(),
				AccountID:       "IBA-TYPE-" + tc.entityType,
				AccountCode:     "CODE_" + tc.entityType,
				Name:            tc.entityType + " Account",
				AccountType:     tc.entityType,
				ClearingPurpose: nil, // nil means unspecified
				InstrumentCode:  "GBP",
				Dimension:       "CURRENCY",
				Status:          "ACTIVE",
				Attributes:      make(AttributesJSON),
				Version:         1,
				CreatedAt:       time.Now(),
				UpdatedAt:       time.Now(),
				CreatedBy:       "system",
				UpdatedBy:       "system",
			}

			account := toDomain(entity)
			assert.Equal(t, tc.domainType, account.AccountType())
		})
	}
}

// TestToDomain_AllStatuses tests mapping for all account statuses.
func TestToDomain_AllStatuses(t *testing.T) {
	testCases := []struct {
		entityStatus string
		domainStatus domain.AccountStatus
	}{
		{"ACTIVE", domain.AccountStatusActive},
		{"SUSPENDED", domain.AccountStatusSuspended},
		{"CLOSED", domain.AccountStatusClosed},
	}

	for _, tc := range testCases {
		t.Run(tc.entityStatus, func(t *testing.T) {
			entity := &InternalAccountEntity{
				ID:              uuid.New(),
				AccountID:       "IBA-STATUS-" + tc.entityStatus,
				AccountCode:     "CODE_" + tc.entityStatus,
				Name:            tc.entityStatus + " Account",
				AccountType:     "CLEARING",
				ClearingPurpose: nil, // nil means unspecified
				InstrumentCode:  "GBP",
				Dimension:       "CURRENCY",
				Status:          tc.entityStatus,
				Attributes:      make(AttributesJSON),
				Version:         1,
				CreatedAt:       time.Now(),
				UpdatedAt:       time.Now(),
				CreatedBy:       "system",
				UpdatedBy:       "system",
			}

			account := toDomain(entity)
			assert.Equal(t, tc.domainStatus, account.Status())
		})
	}
}

// TestToDomain_AllClearingPurposes tests mapping for all clearing purpose values.
func TestToDomain_AllClearingPurposes(t *testing.T) {
	testCases := []struct {
		name          string
		entityPurpose *string
		domainPurpose domain.ClearingPurpose
	}{
		{"nil (unspecified)", nil, domain.ClearingPurposeUnspecified},
		{"DEPOSIT", ptr("CLEARING_PURPOSE_DEPOSIT"), domain.ClearingPurposeDeposit},
		{"WITHDRAWAL", ptr("CLEARING_PURPOSE_WITHDRAWAL"), domain.ClearingPurposeWithdrawal},
		{"SETTLEMENT", ptr("CLEARING_PURPOSE_SETTLEMENT"), domain.ClearingPurposeSettlement},
		{"GENERAL", ptr("CLEARING_PURPOSE_GENERAL"), domain.ClearingPurposeGeneral},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			entity := &InternalAccountEntity{
				ID:              uuid.New(),
				AccountID:       "IBA-PURPOSE-" + tc.name,
				AccountCode:     "CODE_" + tc.name,
				Name:            tc.name + " Purpose Account",
				AccountType:     "CLEARING",
				ClearingPurpose: tc.entityPurpose,
				InstrumentCode:  "GBP",
				Dimension:       "CURRENCY",
				Status:          "ACTIVE",
				Attributes:      make(AttributesJSON),
				Version:         1,
				CreatedAt:       time.Now(),
				UpdatedAt:       time.Now(),
				CreatedBy:       "system",
				UpdatedBy:       "system",
			}

			account := toDomain(entity)
			assert.Equal(t, tc.domainPurpose, account.ClearingPurpose())
		})
	}
}

// TestToEntity_ClearingPurpose tests that toEntity correctly maps clearing purpose field.
func TestToEntity_ClearingPurpose(t *testing.T) {
	ctx := createTestContextForMappers()

	// Test specific clearing purposes for CLEARING accounts
	testCases := []struct {
		name            string
		clearingPurpose domain.ClearingPurpose
	}{
		{"Deposit", domain.ClearingPurposeDeposit},
		{"Withdrawal", domain.ClearingPurposeWithdrawal},
		{"Settlement", domain.ClearingPurposeSettlement},
		{"General", domain.ClearingPurposeGeneral},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			account, err := domain.NewInternalAccount(
				"IBA-CP-"+tc.name,
				"CODE_"+tc.name,
				tc.name+" Clearing Account",
				domain.AccountTypeClearing,
				tc.clearingPurpose,
				"GBP",
				"CURRENCY",
			)
			require.NoError(t, err)

			entity := toEntity(ctx, account)

			require.NotNil(t, entity.ClearingPurpose)
			assert.Equal(t, string(tc.clearingPurpose), *entity.ClearingPurpose)
		})
	}

	// Test that non-CLEARING accounts with UNSPECIFIED purpose have nil ClearingPurpose
	t.Run("Unspecified_NonClearing", func(t *testing.T) {
		account, err := domain.NewInternalAccount(
			"IBA-CP-Unspecified",
			"CODE_Unspecified",
			"Holding Account with Unspecified Purpose",
			domain.AccountTypeHolding, // Non-CLEARING account type
			domain.ClearingPurposeUnspecified,
			"GBP",
			"CURRENCY",
		)
		require.NoError(t, err)

		entity := toEntity(ctx, account)

		assert.Nil(t, entity.ClearingPurpose)
	})
}

// TestRoundTrip_BasicAccount tests domain -> entity -> domain preserves equality.
func TestRoundTrip_BasicAccount(t *testing.T) {
	ctx := createTestContextForMappers()

	// Create original domain account
	original, err := domain.NewInternalAccount(
		"IBA-RT-001",
		"GBP_CLEARING",
		"GBP Clearing Account",
		domain.AccountTypeClearing,
		domain.ClearingPurposeGeneral,
		"GBP",
		"CURRENCY",
	)
	require.NoError(t, err)

	// Convert domain -> entity -> domain
	entity := toEntity(ctx, original)
	reconstructed := toDomain(entity)

	// Verify equality of key fields
	assert.Equal(t, original.ID(), reconstructed.ID())
	assert.Equal(t, original.AccountID(), reconstructed.AccountID())
	assert.Equal(t, original.AccountCode(), reconstructed.AccountCode())
	assert.Equal(t, original.Name(), reconstructed.Name())
	assert.Equal(t, original.AccountType(), reconstructed.AccountType())
	assert.Equal(t, original.ClearingPurpose(), reconstructed.ClearingPurpose())
	assert.Equal(t, original.InstrumentCode(), reconstructed.InstrumentCode())
	assert.Equal(t, original.Dimension(), reconstructed.Dimension())
	assert.Equal(t, original.Status(), reconstructed.Status())
	assert.Equal(t, original.Version(), reconstructed.Version())
}

// TestRoundTrip_WithCorrespondent tests roundtrip with correspondent details.
func TestRoundTrip_WithCorrespondent(t *testing.T) {
	ctx := createTestContextForMappers()

	// Create NOSTRO account with correspondent
	original, err := domain.NewInternalAccount(
		"IBA-RT-002",
		"USD_NOSTRO_CITI",
		"USD NOSTRO at Citibank",
		domain.AccountTypeNostro,
		domain.ClearingPurposeUnspecified,
		"USD",
		"CURRENCY",
	)
	require.NoError(t, err)

	correspondent, err := domain.NewCorrespondentDetails("CITI001", "Citibank NA", "12345678")
	require.NoError(t, err)
	original, err = original.UpdateCorrespondent(correspondent)
	require.NoError(t, err)

	// Convert domain -> entity -> domain
	entity := toEntity(ctx, original)
	reconstructed := toDomain(entity)

	// Verify correspondent details preserved
	require.NotNil(t, reconstructed.Correspondent())
	assert.Equal(t, original.Correspondent().BankID(), reconstructed.Correspondent().BankID())
	assert.Equal(t, original.Correspondent().BankName(), reconstructed.Correspondent().BankName())
	assert.Equal(t, original.Correspondent().ExternalAccountRef(), reconstructed.Correspondent().ExternalAccountRef())
}

// TestRoundTrip_WithAttributes tests roundtrip with JSONB attributes.
func TestRoundTrip_WithAttributes(t *testing.T) {
	ctx := createTestContextForMappers()

	// Create account with attributes
	original := domain.NewInternalAccountBuilder().
		WithID(uuid.New()).
		WithAccountID("IBA-RT-003").
		WithAccountCode("GBP_SPECIAL").
		WithName("GBP Special Account").
		WithAccountType(domain.AccountTypeClearing).
		WithInstrumentCode("GBP").
		WithDimension("CURRENCY").
		WithStatus(domain.AccountStatusActive).
		WithAttributes(map[string]string{
			"cost_center": "CC001",
			"department":  "Treasury",
			"region":      "EMEA",
		}).
		WithVersion(1).
		Build()

	// Convert domain -> entity -> domain
	entity := toEntity(ctx, original)
	reconstructed := toDomain(entity)

	// Verify attributes preserved
	originalAttrs := original.Attributes()
	reconstructedAttrs := reconstructed.Attributes()

	require.NotNil(t, reconstructedAttrs)
	assert.Equal(t, len(originalAttrs), len(reconstructedAttrs))

	for key, value := range originalAttrs {
		assert.Equal(t, value, reconstructedAttrs[key], "Attribute %s should match", key)
	}
}

// TestRoundTrip_VostroAccount tests roundtrip for VOSTRO account type.
func TestRoundTrip_VostroAccount(t *testing.T) {
	ctx := createTestContextForMappers()

	// Create VOSTRO account with correspondent
	original, err := domain.NewInternalAccount(
		"IBA-RT-004",
		"EUR_VOSTRO_DB",
		"EUR VOSTRO from Deutsche Bank",
		domain.AccountTypeVostro,
		domain.ClearingPurposeUnspecified,
		"EUR",
		"CURRENCY",
	)
	require.NoError(t, err)

	correspondent, err := domain.NewCorrespondentDetails("DB001", "Deutsche Bank AG", "DE89370400440532013000")
	require.NoError(t, err)
	original, err = original.UpdateCorrespondent(correspondent)
	require.NoError(t, err)

	// Convert domain -> entity -> domain
	entity := toEntity(ctx, original)
	reconstructed := toDomain(entity)

	// Verify all fields
	assert.Equal(t, original.ID(), reconstructed.ID())
	assert.Equal(t, original.AccountID(), reconstructed.AccountID())
	assert.Equal(t, original.AccountCode(), reconstructed.AccountCode())
	assert.Equal(t, original.Name(), reconstructed.Name())
	assert.Equal(t, original.AccountType(), reconstructed.AccountType())
	assert.Equal(t, original.InstrumentCode(), reconstructed.InstrumentCode())
	assert.Equal(t, original.Dimension(), reconstructed.Dimension())
	assert.Equal(t, original.Status(), reconstructed.Status())
	assert.Equal(t, original.Version(), reconstructed.Version())

	require.NotNil(t, reconstructed.Correspondent())
	assert.Equal(t, "DB001", reconstructed.Correspondent().BankID())
	assert.Equal(t, "Deutsche Bank AG", reconstructed.Correspondent().BankName())
	assert.Equal(t, "DE89370400440532013000", reconstructed.Correspondent().ExternalAccountRef())
}

// TestRoundTrip_SuspendedStatus tests roundtrip for suspended account.
func TestRoundTrip_SuspendedStatus(t *testing.T) {
	ctx := createTestContextForMappers()

	// Create and suspend account
	original, err := domain.NewInternalAccount(
		"IBA-RT-005",
		"GBP_SUSPENDED",
		"GBP Suspended Account",
		domain.AccountTypeClearing,
		domain.ClearingPurposeGeneral, // CLEARING accounts require a specific purpose
		"GBP",
		"CURRENCY",
	)
	require.NoError(t, err)

	original, err = original.Suspend("Test suspension")
	require.NoError(t, err)

	// Convert domain -> entity -> domain
	entity := toEntity(ctx, original)
	reconstructed := toDomain(entity)

	// Verify status preserved
	assert.Equal(t, domain.AccountStatusSuspended, reconstructed.Status())
	assert.Equal(t, int64(2), reconstructed.Version()) // Version incremented on suspend
}

// TestAttributesJSON_Value tests AttributesJSON Value() for database writes.
func TestAttributesJSON_Value(t *testing.T) {
	t.Run("non-nil map", func(t *testing.T) {
		attrs := AttributesJSON{
			"key1": "value1",
			"key2": "value2",
		}

		value, err := attrs.Value()
		require.NoError(t, err)

		// Should be JSON bytes
		bytes, ok := value.([]byte)
		require.True(t, ok)
		assert.Contains(t, string(bytes), "key1")
		assert.Contains(t, string(bytes), "value1")
	})

	t.Run("nil map", func(t *testing.T) {
		var attrs AttributesJSON

		value, err := attrs.Value()
		require.NoError(t, err)
		assert.Equal(t, "{}", value)
	})
}

// TestAttributesJSON_Scan tests AttributesJSON Scan() for database reads.
func TestAttributesJSON_Scan(t *testing.T) {
	t.Run("scan bytes", func(t *testing.T) {
		var attrs AttributesJSON
		input := []byte(`{"key1":"value1","key2":"value2"}`)

		err := attrs.Scan(input)
		require.NoError(t, err)

		assert.Equal(t, "value1", attrs["key1"])
		assert.Equal(t, "value2", attrs["key2"])
	})

	t.Run("scan string", func(t *testing.T) {
		var attrs AttributesJSON
		input := `{"key3":"value3"}`

		err := attrs.Scan(input)
		require.NoError(t, err)

		assert.Equal(t, "value3", attrs["key3"])
	})

	t.Run("scan nil", func(t *testing.T) {
		var attrs AttributesJSON

		err := attrs.Scan(nil)
		require.NoError(t, err)

		require.NotNil(t, attrs)
		assert.Len(t, attrs, 0)
	})

	t.Run("scan invalid type", func(t *testing.T) {
		var attrs AttributesJSON

		err := attrs.Scan(12345)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidAttributesScan)
	})
}

// TestReconstructCorrespondent tests the helper function for correspondent reconstruction.
func TestReconstructCorrespondent(t *testing.T) {
	t.Run("valid correspondent", func(t *testing.T) {
		result := reconstructCorrespondent("BANK001", "Test Bank", "REF123")

		require.NotNil(t, result)
		assert.Equal(t, "BANK001", result.BankID())
		assert.Equal(t, "Test Bank", result.BankName())
		assert.Equal(t, "REF123", result.ExternalAccountRef())
	})

	t.Run("invalid bank name too short", func(t *testing.T) {
		// Bank name must be at least 3 characters
		result := reconstructCorrespondent("BANK001", "XY", "REF123")

		// Should return nil for invalid data (logged as warning)
		assert.Nil(t, result)
	})

	t.Run("empty bank ID", func(t *testing.T) {
		result := reconstructCorrespondent("", "Test Bank", "REF123")

		// Should return nil for invalid data
		assert.Nil(t, result)
	})

	t.Run("empty external ref", func(t *testing.T) {
		result := reconstructCorrespondent("BANK001", "Test Bank", "")

		// Should return nil for invalid data
		assert.Nil(t, result)
	})
}

// TestToEntity_WithOrgPartyID tests that toEntity correctly maps the org_party_id field.
func TestToEntity_WithOrgPartyID(t *testing.T) {
	ctx := createTestContextForMappers()

	t.Run("org-scoped account", func(t *testing.T) {
		orgID := uuid.New()
		account, err := domain.NewInternalAccount(
			"IBA-ORG-001",
			"ORG_HOLDING",
			"Org Holding Account",
			domain.AccountTypeHolding,
			domain.ClearingPurposeUnspecified,
			"GBP",
			"CURRENCY",
			domain.WithOrgPartyID(orgID),
		)
		require.NoError(t, err)

		entity := toEntity(ctx, account)

		require.NotNil(t, entity.OrgPartyID)
		assert.Equal(t, orgID, *entity.OrgPartyID)
	})

	t.Run("global account", func(t *testing.T) {
		account, err := domain.NewInternalAccount(
			"IBA-GLOBAL-001",
			"GLOBAL_HOLDING",
			"Global Holding Account",
			domain.AccountTypeHolding,
			domain.ClearingPurposeUnspecified,
			"GBP",
			"CURRENCY",
		)
		require.NoError(t, err)

		entity := toEntity(ctx, account)

		assert.Nil(t, entity.OrgPartyID)
	})
}

// TestToDomain_WithOrgPartyID tests reconstructing org_party_id from entity.
func TestToDomain_WithOrgPartyID(t *testing.T) {
	now := time.Now()

	t.Run("org-scoped entity", func(t *testing.T) {
		orgID := uuid.New()
		entity := &InternalAccountEntity{
			ID:             uuid.New(),
			AccountID:      "IBA-ORG-010",
			AccountCode:    "ORG_HOLDING",
			Name:           "Org Holding",
			AccountType:    "HOLDING",
			InstrumentCode: "GBP",
			Dimension:      "CURRENCY",
			Status:         "ACTIVE",
			OrgPartyID:     &orgID,
			Attributes:     make(AttributesJSON),
			Version:        1,
			CreatedAt:      now,
			UpdatedAt:      now,
			CreatedBy:      "system",
			UpdatedBy:      "system",
		}

		account := toDomain(entity)

		require.NotNil(t, account.OrgPartyID())
		assert.Equal(t, orgID, *account.OrgPartyID())
		assert.True(t, account.IsScopedToOrganization())
	})

	t.Run("global entity", func(t *testing.T) {
		entity := &InternalAccountEntity{
			ID:             uuid.New(),
			AccountID:      "IBA-GLOBAL-010",
			AccountCode:    "GLOBAL_HOLDING",
			Name:           "Global Holding",
			AccountType:    "HOLDING",
			InstrumentCode: "GBP",
			Dimension:      "CURRENCY",
			Status:         "ACTIVE",
			OrgPartyID:     nil,
			Attributes:     make(AttributesJSON),
			Version:        1,
			CreatedAt:      now,
			UpdatedAt:      now,
			CreatedBy:      "system",
			UpdatedBy:      "system",
		}

		account := toDomain(entity)

		assert.Nil(t, account.OrgPartyID())
		assert.False(t, account.IsScopedToOrganization())
	})
}

// TestRoundTrip_OrgScopedAccount tests domain -> entity -> domain preserves OrgPartyID.
func TestRoundTrip_OrgScopedAccount(t *testing.T) {
	ctx := createTestContextForMappers()
	orgID := uuid.New()

	original, err := domain.NewInternalAccount(
		"IBA-RT-ORG-001",
		"ORG_NOSTRO",
		"Org Scoped Nostro",
		domain.AccountTypeNostro,
		domain.ClearingPurposeUnspecified,
		"USD",
		"CURRENCY",
		domain.WithOrgPartyID(orgID),
	)
	require.NoError(t, err)

	entity := toEntity(ctx, original)
	reconstructed := toDomain(entity)

	require.NotNil(t, reconstructed.OrgPartyID())
	assert.Equal(t, orgID, *reconstructed.OrgPartyID())
	assert.True(t, reconstructed.IsScopedToOrganization())
	assert.Equal(t, original.AccountID(), reconstructed.AccountID())
}

// TestRoundTrip_GlobalAccount tests that global account round-trips with nil OrgPartyID.
func TestRoundTrip_GlobalAccount(t *testing.T) {
	ctx := createTestContextForMappers()

	original, err := domain.NewInternalAccount(
		"IBA-RT-GLOBAL-001",
		"GLOBAL_NOSTRO",
		"Global Nostro",
		domain.AccountTypeNostro,
		domain.ClearingPurposeUnspecified,
		"USD",
		"CURRENCY",
	)
	require.NoError(t, err)

	entity := toEntity(ctx, original)
	reconstructed := toDomain(entity)

	assert.Nil(t, reconstructed.OrgPartyID())
	assert.False(t, reconstructed.IsScopedToOrganization())
}

// TestToDomain_PartialCorrespondent tests handling when only some correspondent fields are set.
func TestToDomain_PartialCorrespondent(t *testing.T) {
	now := time.Now()

	t.Run("only bank ID set", func(t *testing.T) {
		bankID := "CITI001"
		entity := &InternalAccountEntity{
			ID:                  uuid.New(),
			AccountID:           "IBA-PARTIAL-001",
			AccountCode:         "TEST",
			Name:                "Test Account",
			AccountType:         "CLEARING",
			ClearingPurpose:     nil, // nil means unspecified
			InstrumentCode:      "GBP",
			Dimension:           "CURRENCY",
			Status:              "ACTIVE",
			CorrespondentBankID: &bankID,
			// Other correspondent fields are nil
			Attributes: make(AttributesJSON),
			Version:    1,
			CreatedAt:  now,
			UpdatedAt:  now,
			CreatedBy:  "system",
			UpdatedBy:  "system",
		}

		account := toDomain(entity)

		// Correspondent should be nil when not all fields are present
		assert.Nil(t, account.Correspondent())
	})

	t.Run("two of three fields set", func(t *testing.T) {
		bankID := "CITI001"
		bankName := "Citibank NA"
		entity := &InternalAccountEntity{
			ID:                    uuid.New(),
			AccountID:             "IBA-PARTIAL-002",
			AccountCode:           "TEST",
			Name:                  "Test Account",
			AccountType:           "CLEARING",
			ClearingPurpose:       nil, // nil means unspecified
			InstrumentCode:        "GBP",
			Dimension:             "CURRENCY",
			Status:                "ACTIVE",
			CorrespondentBankID:   &bankID,
			CorrespondentBankName: &bankName,
			// ExternalRef is nil
			Attributes: make(AttributesJSON),
			Version:    1,
			CreatedAt:  now,
			UpdatedAt:  now,
			CreatedBy:  "system",
			UpdatedBy:  "system",
		}

		account := toDomain(entity)

		// Correspondent should be nil when not all fields are present
		assert.Nil(t, account.Correspondent())
	})
}
