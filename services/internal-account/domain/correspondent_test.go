package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCorrespondentDetails_Success(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		bankID             string
		bankName           string
		externalAccountRef string
	}{
		{
			name:               "minimum valid inputs",
			bankID:             "B",
			bankName:           "ABC",
			externalAccountRef: "X",
		},
		{
			name:               "typical values",
			bankID:             "BARCLAYS_UK",
			bankName:           "Barclays Bank UK PLC",
			externalAccountRef: "GB82WEST12345698765432",
		},
		{
			name:               "longer values",
			bankID:             "CORRESPONDENT_BANK_12345",
			bankName:           "First National Bank of Testing International",
			externalAccountRef: "NOSTRO-ACC-2024-001-GBP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			details, err := NewCorrespondentDetails(tt.bankID, tt.bankName, tt.externalAccountRef)

			require.NoError(t, err)
			require.NotNil(t, details)
			assert.Equal(t, tt.bankID, details.BankID())
			assert.Equal(t, tt.bankName, details.BankName())
			assert.Equal(t, tt.externalAccountRef, details.ExternalAccountRef())
			assert.Empty(t, details.SwiftCode(), "SwiftCode should be empty when not provided")
			assert.Nil(t, details.Attributes(), "Attributes should be nil when not provided")
		})
	}
}

func TestNewCorrespondentDetailsWithOptions_Success(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		bankID             string
		bankName           string
		externalAccountRef string
		swiftCode          string
		attributes         map[string]string
	}{
		{
			name:               "with swift code only",
			bankID:             "BARCLAYS_UK",
			bankName:           "Barclays Bank",
			externalAccountRef: "ACC123",
			swiftCode:          "BABORKLXXX",
			attributes:         nil,
		},
		{
			name:               "with attributes only",
			bankID:             "HSBC_UK",
			bankName:           "HSBC Holdings",
			externalAccountRef: "ACC456",
			swiftCode:          "",
			attributes:         map[string]string{"region": "EMEA", "tier": "primary"},
		},
		{
			name:               "with all optional fields",
			bankID:             "LLOYDS_UK",
			bankName:           "Lloyds Banking Group",
			externalAccountRef: "ACC789",
			swiftCode:          "LOYDGB2LXXX",
			attributes:         map[string]string{"category": "clearing", "priority": "high"},
		},
		{
			name:               "with empty attributes map",
			bankID:             "NATWEST_UK",
			bankName:           "NatWest Group",
			externalAccountRef: "ACC000",
			swiftCode:          "NWBKGB2L",
			attributes:         map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			details, err := NewCorrespondentDetailsWithOptions(
				tt.bankID, tt.bankName, tt.externalAccountRef,
				tt.swiftCode, tt.attributes,
			)

			require.NoError(t, err)
			require.NotNil(t, details)
			assert.Equal(t, tt.bankID, details.BankID())
			assert.Equal(t, tt.bankName, details.BankName())
			assert.Equal(t, tt.externalAccountRef, details.ExternalAccountRef())
			assert.Equal(t, tt.swiftCode, details.SwiftCode())

			if tt.attributes == nil {
				assert.Nil(t, details.Attributes())
			} else {
				// Verify attributes match (empty map returns empty map, not nil)
				attrs := details.Attributes()
				if len(tt.attributes) == 0 {
					assert.Empty(t, attrs)
				} else {
					assert.Equal(t, tt.attributes, attrs)
				}
			}
		})
	}
}

func TestNewCorrespondentDetails_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		bankID             string
		bankName           string
		externalAccountRef string
		expectedErr        error
		errDescription     string
	}{
		{
			name:               "empty bank ID",
			bankID:             "",
			bankName:           "Valid Bank Name",
			externalAccountRef: "ACC123",
			expectedErr:        errBankIDRequired,
			errDescription:     "bank ID is required",
		},
		{
			name:               "empty external account ref",
			bankID:             "BANK123",
			bankName:           "Valid Bank Name",
			externalAccountRef: "",
			expectedErr:        errExternalAccountRefRequired,
			errDescription:     "external account reference is required",
		},
		{
			name:               "bank name too short - empty",
			bankID:             "BANK123",
			bankName:           "",
			externalAccountRef: "ACC123",
			expectedErr:        errBankNameTooShort,
			errDescription:     "bank name must be at least 3 characters",
		},
		{
			name:               "bank name too short - one char",
			bankID:             "BANK123",
			bankName:           "A",
			externalAccountRef: "ACC123",
			expectedErr:        errBankNameTooShort,
			errDescription:     "bank name must be at least 3 characters",
		},
		{
			name:               "bank name too short - two chars",
			bankID:             "BANK123",
			bankName:           "AB",
			externalAccountRef: "ACC123",
			expectedErr:        errBankNameTooShort,
			errDescription:     "bank name must be at least 3 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			details, err := NewCorrespondentDetails(tt.bankID, tt.bankName, tt.externalAccountRef)

			assert.Nil(t, details, "details should be nil on validation error")
			require.Error(t, err)
			assert.ErrorIs(t, err, tt.expectedErr, "expected error: %s", tt.errDescription)
		})
	}
}

func TestCorrespondentDetails_Equality(t *testing.T) {
	t.Parallel()

	t.Run("identical values are equal", func(t *testing.T) {
		t.Parallel()

		details1, err := NewCorrespondentDetails("BANK1", "First Bank", "ACC001")
		require.NoError(t, err)

		details2, err := NewCorrespondentDetails("BANK1", "First Bank", "ACC001")
		require.NoError(t, err)

		assert.True(t, details1.Equals(details2), "identical details should be equal")
		assert.True(t, details2.Equals(details1), "equality should be symmetric")
	})

	t.Run("different bank ID not equal", func(t *testing.T) {
		t.Parallel()

		details1, err := NewCorrespondentDetails("BANK1", "First Bank", "ACC001")
		require.NoError(t, err)

		details2, err := NewCorrespondentDetails("BANK2", "First Bank", "ACC001")
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different bank IDs should not be equal")
	})

	t.Run("different bank name not equal", func(t *testing.T) {
		t.Parallel()

		details1, err := NewCorrespondentDetails("BANK1", "First Bank", "ACC001")
		require.NoError(t, err)

		details2, err := NewCorrespondentDetails("BANK1", "Second Bank", "ACC001")
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different bank names should not be equal")
	})

	t.Run("different external account ref not equal", func(t *testing.T) {
		t.Parallel()

		details1, err := NewCorrespondentDetails("BANK1", "First Bank", "ACC001")
		require.NoError(t, err)

		details2, err := NewCorrespondentDetails("BANK1", "First Bank", "ACC002")
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different external refs should not be equal")
	})

	t.Run("different swift code not equal", func(t *testing.T) {
		t.Parallel()

		details1, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "SWIFT1", nil)
		require.NoError(t, err)

		details2, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "SWIFT2", nil)
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different swift codes should not be equal")
	})

	t.Run("different attributes not equal", func(t *testing.T) {
		t.Parallel()

		attrs1 := map[string]string{"key": "value1"}
		attrs2 := map[string]string{"key": "value2"}

		details1, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "", attrs1)
		require.NoError(t, err)

		details2, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "", attrs2)
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different attributes should not be equal")
	})

	t.Run("different attribute keys not equal", func(t *testing.T) {
		t.Parallel()

		attrs1 := map[string]string{"key1": "value"}
		attrs2 := map[string]string{"key2": "value"}

		details1, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "", attrs1)
		require.NoError(t, err)

		details2, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "", attrs2)
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different attribute keys should not be equal")
	})

	t.Run("nil attributes vs empty attributes", func(t *testing.T) {
		t.Parallel()

		details1, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "", nil)
		require.NoError(t, err)

		details2, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "", map[string]string{})
		require.NoError(t, err)

		// Both have len 0, so they should be equal
		assert.True(t, details1.Equals(details2), "nil and empty attributes should be equal")
	})

	t.Run("identical with all optional fields", func(t *testing.T) {
		t.Parallel()

		attrs := map[string]string{"region": "EMEA", "tier": "primary"}

		details1, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "SWIFT123", attrs)
		require.NoError(t, err)

		details2, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "SWIFT123", attrs)
		require.NoError(t, err)

		assert.True(t, details1.Equals(details2), "identical details with all fields should be equal")
	})
}

func TestCorrespondentDetails_NilHandling(t *testing.T) {
	t.Parallel()

	t.Run("nil equals nil", func(t *testing.T) {
		t.Parallel()

		var details1 *CorrespondentDetails
		var details2 *CorrespondentDetails

		assert.True(t, details1.Equals(details2), "two nil CorrespondentDetails should be equal")
	})

	t.Run("nil not equal to non-nil", func(t *testing.T) {
		t.Parallel()

		var nilDetails *CorrespondentDetails
		nonNilDetails, err := NewCorrespondentDetails("BANK1", "First Bank", "ACC001")
		require.NoError(t, err)

		assert.False(t, nilDetails.Equals(nonNilDetails), "nil should not equal non-nil")
		assert.False(t, nonNilDetails.Equals(nilDetails), "non-nil should not equal nil")
	})

	t.Run("nil receiver returns empty values", func(t *testing.T) {
		t.Parallel()

		var details *CorrespondentDetails

		assert.Empty(t, details.BankID(), "nil receiver BankID() should return empty string")
		assert.Empty(t, details.BankName(), "nil receiver BankName() should return empty string")
		assert.Empty(t, details.ExternalAccountRef(), "nil receiver ExternalAccountRef() should return empty string")
		assert.Empty(t, details.SwiftCode(), "nil receiver SwiftCode() should return empty string")
		assert.Nil(t, details.Attributes(), "nil receiver Attributes() should return nil")
	})
}

func TestCorrespondentDetails_Immutability(t *testing.T) {
	t.Parallel()

	t.Run("attributes are copied on construction", func(t *testing.T) {
		t.Parallel()

		originalAttrs := map[string]string{"key": "original"}
		details, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "", originalAttrs)
		require.NoError(t, err)

		// Modify the original map
		originalAttrs["key"] = "modified"
		originalAttrs["new_key"] = "new_value"

		// The details should not be affected
		attrs := details.Attributes()
		assert.Equal(t, "original", attrs["key"], "internal attributes should not be modified")
		assert.NotContains(t, attrs, "new_key", "internal attributes should not have new keys")
	})

	t.Run("returned attributes are a copy", func(t *testing.T) {
		t.Parallel()

		attrs := map[string]string{"key": "original"}
		details, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "", attrs)
		require.NoError(t, err)

		// Get attributes and modify the returned map
		returnedAttrs := details.Attributes()
		returnedAttrs["key"] = "modified"
		returnedAttrs["new_key"] = "new_value"

		// Get attributes again - should be unchanged
		freshAttrs := details.Attributes()
		assert.Equal(t, "original", freshAttrs["key"], "original attributes should not be modified")
		assert.NotContains(t, freshAttrs, "new_key", "original attributes should not have new keys")
	})
}

func TestCorrespondentDetails_Equality_AttributeCountDifference(t *testing.T) {
	t.Parallel()

	// Test where one has more attributes than another (different length)
	t.Run("different number of attributes", func(t *testing.T) {
		t.Parallel()

		attrs1 := map[string]string{"key1": "value1"}
		attrs2 := map[string]string{"key1": "value1", "key2": "value2"}

		details1, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "", attrs1)
		require.NoError(t, err)

		details2, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "", attrs2)
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different attribute counts should not be equal")
		assert.False(t, details2.Equals(details1), "equality should be symmetric")
	})
}

func TestCorrespondentDetails_Equality_AttributeKeyNotPresent(t *testing.T) {
	t.Parallel()

	// Test where both have same number of attributes but different keys
	// This tests the "key not found" branch in the Equals method
	t.Run("same attribute count but different keys", func(t *testing.T) {
		t.Parallel()

		attrs1 := map[string]string{"keyA": "value", "keyB": "value"}
		attrs2 := map[string]string{"keyA": "value", "keyC": "value"}

		details1, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "", attrs1)
		require.NoError(t, err)

		details2, err := NewCorrespondentDetailsWithOptions("BANK1", "First Bank", "ACC001", "", attrs2)
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different attribute keys should not be equal")
	})
}

func TestCorrespondentDetails_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("minimum valid bank name (3 chars)", func(t *testing.T) {
		t.Parallel()

		details, err := NewCorrespondentDetails("B1", "ABC", "REF")
		require.NoError(t, err)
		assert.Equal(t, "ABC", details.BankName())
	})

	t.Run("very long values", func(t *testing.T) {
		t.Parallel()

		longString := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		details, err := NewCorrespondentDetails(longString, longString, longString)
		require.NoError(t, err)
		assert.Equal(t, longString, details.BankID())
		assert.Equal(t, longString, details.BankName())
		assert.Equal(t, longString, details.ExternalAccountRef())
	})

	t.Run("special characters in values", func(t *testing.T) {
		t.Parallel()

		details, err := NewCorrespondentDetails(
			"BANK-123/456",
			"Test Bank (UK) Ltd.",
			"ACC#001@REF",
		)
		require.NoError(t, err)
		assert.Equal(t, "BANK-123/456", details.BankID())
		assert.Equal(t, "Test Bank (UK) Ltd.", details.BankName())
		assert.Equal(t, "ACC#001@REF", details.ExternalAccountRef())
	})

	t.Run("unicode in values", func(t *testing.T) {
		t.Parallel()

		details, err := NewCorrespondentDetails(
			"银行001",
			"東京三菱UFJ銀行",
			"REF-日本-001",
		)
		require.NoError(t, err)
		assert.Equal(t, "银行001", details.BankID())
		assert.Equal(t, "東京三菱UFJ銀行", details.BankName())
		assert.Equal(t, "REF-日本-001", details.ExternalAccountRef())
	})
}

func TestCorrespondentDetails_SwiftCodeVariations(t *testing.T) {
	t.Parallel()

	t.Run("8-character SWIFT code", func(t *testing.T) {
		t.Parallel()

		details, err := NewCorrespondentDetailsWithOptions(
			"BANK1", "First Bank", "ACC001",
			"BOFAUS3N", nil,
		)
		require.NoError(t, err)
		assert.Equal(t, "BOFAUS3N", details.SwiftCode())
	})

	t.Run("11-character SWIFT code", func(t *testing.T) {
		t.Parallel()

		details, err := NewCorrespondentDetailsWithOptions(
			"BANK1", "First Bank", "ACC001",
			"BOFAUS3NXXX", nil,
		)
		require.NoError(t, err)
		assert.Equal(t, "BOFAUS3NXXX", details.SwiftCode())
	})
}

func TestCorrespondentDetails_MultipleAttributes(t *testing.T) {
	t.Parallel()

	t.Run("many attributes", func(t *testing.T) {
		t.Parallel()

		attrs := map[string]string{
			"region":     "EMEA",
			"tier":       "primary",
			"priority":   "high",
			"category":   "clearing",
			"department": "treasury",
			"costCenter": "CC001",
			"createdBy":  "admin@example.com",
			"approvedBy": "manager@example.com",
		}

		details, err := NewCorrespondentDetailsWithOptions(
			"BANK1", "First Bank", "ACC001", "SWIFT123", attrs,
		)
		require.NoError(t, err)

		retrievedAttrs := details.Attributes()
		assert.Len(t, retrievedAttrs, 8)
		assert.Equal(t, "EMEA", retrievedAttrs["region"])
		assert.Equal(t, "primary", retrievedAttrs["tier"])
	})
}

func TestCorrespondentDetails_EqualityReflexive(t *testing.T) {
	t.Parallel()

	// Test that x.Equals(x) is always true
	details, err := NewCorrespondentDetailsWithOptions(
		"BANK1", "First Bank", "ACC001", "SWIFT123",
		map[string]string{"key": "value"},
	)
	require.NoError(t, err)

	assert.True(t, details.Equals(details), "a value should equal itself")
}

func TestCorrespondentDetails_ValidationPriority(t *testing.T) {
	t.Parallel()

	// Test that validation errors are returned in the expected order
	// bankID is checked first, then bankName, then externalAccountRef
	t.Run("empty bankID checked before other validations", func(t *testing.T) {
		t.Parallel()

		_, err := NewCorrespondentDetails("", "AB", "")
		require.Error(t, err)
		assert.ErrorIs(t, err, errBankIDRequired)
	})

	t.Run("bankName checked before externalAccountRef", func(t *testing.T) {
		t.Parallel()

		_, err := NewCorrespondentDetails("BANK1", "AB", "")
		require.Error(t, err)
		assert.ErrorIs(t, err, errBankNameTooShort)
	})
}
