package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCounterpartyDetails_Success(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		counterpartyID   string
		counterpartyName string
		externalRef      string
	}{
		{
			name:             "minimum valid inputs",
			counterpartyID:   "B",
			counterpartyName: "ABC",
			externalRef:      "X",
		},
		{
			name:             "typical values",
			counterpartyID:   "BARCLAYS_UK",
			counterpartyName: "Barclays Bank UK PLC",
			externalRef:      "GB82WEST12345698765432",
		},
		{
			name:             "longer values",
			counterpartyID:   "COUNTERPARTY_12345",
			counterpartyName: "First National Bank of Testing International",
			externalRef:      "NOSTRO-ACC-2024-001-GBP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			details, err := NewCounterpartyDetails(tt.counterpartyID, tt.counterpartyName, tt.externalRef)

			require.NoError(t, err)
			require.NotNil(t, details)
			assert.Equal(t, tt.counterpartyID, details.CounterpartyID())
			assert.Equal(t, tt.counterpartyName, details.CounterpartyName())
			assert.Equal(t, tt.externalRef, details.ExternalRef())
			assert.Nil(t, details.Attributes(), "Attributes should be nil when not provided")
		})
	}
}

func TestNewCounterpartyDetailsWithOptions_Success(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		counterpartyID   string
		counterpartyName string
		externalRef      string
		attributes       map[string]string
	}{
		{
			name:             "with attributes only",
			counterpartyID:   "HSBC_UK",
			counterpartyName: "HSBC Holdings",
			externalRef:      "ACC456",
			attributes:       map[string]string{"region": "EMEA", "tier": "primary"},
		},
		{
			name:             "with product-specific attributes",
			counterpartyID:   "LLOYDS_UK",
			counterpartyName: "Lloyds Banking Group",
			externalRef:      "ACC789",
			attributes:       map[string]string{"swift_code": "LOYDGB2LXXX", "category": "clearing"},
		},
		{
			name:             "with nil attributes",
			counterpartyID:   "BARCLAYS_UK",
			counterpartyName: "Barclays Bank",
			externalRef:      "ACC123",
			attributes:       nil,
		},
		{
			name:             "with empty attributes map",
			counterpartyID:   "NATWEST_UK",
			counterpartyName: "NatWest Group",
			externalRef:      "ACC000",
			attributes:       map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			details, err := NewCounterpartyDetailsWithOptions(
				tt.counterpartyID, tt.counterpartyName, tt.externalRef,
				tt.attributes,
			)

			require.NoError(t, err)
			require.NotNil(t, details)
			assert.Equal(t, tt.counterpartyID, details.CounterpartyID())
			assert.Equal(t, tt.counterpartyName, details.CounterpartyName())
			assert.Equal(t, tt.externalRef, details.ExternalRef())

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

func TestNewCounterpartyDetails_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		counterpartyID   string
		counterpartyName string
		externalRef      string
		expectedErr      error
		errDescription   string
	}{
		{
			name:             "empty counterparty ID",
			counterpartyID:   "",
			counterpartyName: "Valid Counterparty Name",
			externalRef:      "ACC123",
			expectedErr:      errCounterpartyIDRequired,
			errDescription:   "counterparty ID is required",
		},
		{
			name:             "empty external ref",
			counterpartyID:   "CPTY123",
			counterpartyName: "Valid Counterparty Name",
			externalRef:      "",
			expectedErr:      errCounterpartyExternalRefRequired,
			errDescription:   "counterparty external reference is required",
		},
		{
			name:             "counterparty name too short - empty",
			counterpartyID:   "CPTY123",
			counterpartyName: "",
			externalRef:      "ACC123",
			expectedErr:      errCounterpartyNameTooShort,
			errDescription:   "counterparty name must be at least 3 characters",
		},
		{
			name:             "counterparty name too short - one char",
			counterpartyID:   "CPTY123",
			counterpartyName: "A",
			externalRef:      "ACC123",
			expectedErr:      errCounterpartyNameTooShort,
			errDescription:   "counterparty name must be at least 3 characters",
		},
		{
			name:             "counterparty name too short - two chars",
			counterpartyID:   "CPTY123",
			counterpartyName: "AB",
			externalRef:      "ACC123",
			expectedErr:      errCounterpartyNameTooShort,
			errDescription:   "counterparty name must be at least 3 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			details, err := NewCounterpartyDetails(tt.counterpartyID, tt.counterpartyName, tt.externalRef)

			assert.Nil(t, details, "details should be nil on validation error")
			require.Error(t, err)
			assert.ErrorIs(t, err, tt.expectedErr, "expected error: %s", tt.errDescription)
		})
	}
}

func TestCounterpartyDetails_Equality(t *testing.T) {
	t.Parallel()

	t.Run("identical values are equal", func(t *testing.T) {
		t.Parallel()

		details1, err := NewCounterpartyDetails("CPTY1", "First Counterparty", "ACC001")
		require.NoError(t, err)

		details2, err := NewCounterpartyDetails("CPTY1", "First Counterparty", "ACC001")
		require.NoError(t, err)

		assert.True(t, details1.Equals(details2), "identical details should be equal")
		assert.True(t, details2.Equals(details1), "equality should be symmetric")
	})

	t.Run("different counterparty ID not equal", func(t *testing.T) {
		t.Parallel()

		details1, err := NewCounterpartyDetails("CPTY1", "First Counterparty", "ACC001")
		require.NoError(t, err)

		details2, err := NewCounterpartyDetails("CPTY2", "First Counterparty", "ACC001")
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different counterparty IDs should not be equal")
	})

	t.Run("different counterparty name not equal", func(t *testing.T) {
		t.Parallel()

		details1, err := NewCounterpartyDetails("CPTY1", "First Counterparty", "ACC001")
		require.NoError(t, err)

		details2, err := NewCounterpartyDetails("CPTY1", "Second Counterparty", "ACC001")
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different counterparty names should not be equal")
	})

	t.Run("different external ref not equal", func(t *testing.T) {
		t.Parallel()

		details1, err := NewCounterpartyDetails("CPTY1", "First Counterparty", "ACC001")
		require.NoError(t, err)

		details2, err := NewCounterpartyDetails("CPTY1", "First Counterparty", "ACC002")
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different external refs should not be equal")
	})

	t.Run("different attributes not equal", func(t *testing.T) {
		t.Parallel()

		attrs1 := map[string]string{"key": "value1"}
		attrs2 := map[string]string{"key": "value2"}

		details1, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", attrs1)
		require.NoError(t, err)

		details2, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", attrs2)
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different attributes should not be equal")
	})

	t.Run("different attribute keys not equal", func(t *testing.T) {
		t.Parallel()

		attrs1 := map[string]string{"key1": "value"}
		attrs2 := map[string]string{"key2": "value"}

		details1, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", attrs1)
		require.NoError(t, err)

		details2, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", attrs2)
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different attribute keys should not be equal")
	})

	t.Run("nil attributes vs empty attributes", func(t *testing.T) {
		t.Parallel()

		details1, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", nil)
		require.NoError(t, err)

		details2, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", map[string]string{})
		require.NoError(t, err)

		// Both have len 0, so they should be equal
		assert.True(t, details1.Equals(details2), "nil and empty attributes should be equal")
	})

	t.Run("identical with all optional fields", func(t *testing.T) {
		t.Parallel()

		attrs := map[string]string{"region": "EMEA", "tier": "primary"}

		details1, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", attrs)
		require.NoError(t, err)

		details2, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", attrs)
		require.NoError(t, err)

		assert.True(t, details1.Equals(details2), "identical details with all fields should be equal")
	})
}

func TestCounterpartyDetails_NilHandling(t *testing.T) {
	t.Parallel()

	t.Run("nil equals nil", func(t *testing.T) {
		t.Parallel()

		var details1 *CounterpartyDetails
		var details2 *CounterpartyDetails

		assert.True(t, details1.Equals(details2), "two nil CounterpartyDetails should be equal")
	})

	t.Run("nil not equal to non-nil", func(t *testing.T) {
		t.Parallel()

		var nilDetails *CounterpartyDetails
		nonNilDetails, err := NewCounterpartyDetails("CPTY1", "First Counterparty", "ACC001")
		require.NoError(t, err)

		assert.False(t, nilDetails.Equals(nonNilDetails), "nil should not equal non-nil")
		assert.False(t, nonNilDetails.Equals(nilDetails), "non-nil should not equal nil")
	})

	t.Run("nil receiver returns empty values", func(t *testing.T) {
		t.Parallel()

		var details *CounterpartyDetails

		assert.Empty(t, details.CounterpartyID(), "nil receiver CounterpartyID() should return empty string")
		assert.Empty(t, details.CounterpartyName(), "nil receiver CounterpartyName() should return empty string")
		assert.Empty(t, details.ExternalRef(), "nil receiver ExternalRef() should return empty string")
		assert.Nil(t, details.Attributes(), "nil receiver Attributes() should return nil")
	})
}

func TestCounterpartyDetails_Immutability(t *testing.T) {
	t.Parallel()

	t.Run("attributes are copied on construction", func(t *testing.T) {
		t.Parallel()

		originalAttrs := map[string]string{"key": "original"}
		details, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", originalAttrs)
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
		details, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", attrs)
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

func TestCounterpartyDetails_Equality_AttributeCountDifference(t *testing.T) {
	t.Parallel()

	t.Run("different number of attributes", func(t *testing.T) {
		t.Parallel()

		attrs1 := map[string]string{"key1": "value1"}
		attrs2 := map[string]string{"key1": "value1", "key2": "value2"}

		details1, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", attrs1)
		require.NoError(t, err)

		details2, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", attrs2)
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different attribute counts should not be equal")
		assert.False(t, details2.Equals(details1), "equality should be symmetric")
	})
}

func TestCounterpartyDetails_Equality_AttributeKeyNotPresent(t *testing.T) {
	t.Parallel()

	t.Run("same attribute count but different keys", func(t *testing.T) {
		t.Parallel()

		attrs1 := map[string]string{"keyA": "value", "keyB": "value"}
		attrs2 := map[string]string{"keyA": "value", "keyC": "value"}

		details1, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", attrs1)
		require.NoError(t, err)

		details2, err := NewCounterpartyDetailsWithOptions("CPTY1", "First Counterparty", "ACC001", attrs2)
		require.NoError(t, err)

		assert.False(t, details1.Equals(details2), "different attribute keys should not be equal")
	})
}

func TestCounterpartyDetails_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("minimum valid counterparty name (3 chars)", func(t *testing.T) {
		t.Parallel()

		details, err := NewCounterpartyDetails("C1", "ABC", "REF")
		require.NoError(t, err)
		assert.Equal(t, "ABC", details.CounterpartyName())
	})

	t.Run("very long values", func(t *testing.T) {
		t.Parallel()

		longString := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		details, err := NewCounterpartyDetails(longString, longString, longString)
		require.NoError(t, err)
		assert.Equal(t, longString, details.CounterpartyID())
		assert.Equal(t, longString, details.CounterpartyName())
		assert.Equal(t, longString, details.ExternalRef())
	})

	t.Run("special characters in values", func(t *testing.T) {
		t.Parallel()

		details, err := NewCounterpartyDetails(
			"CPTY-123/456",
			"Test Counterparty (UK) Ltd.",
			"ACC#001@REF",
		)
		require.NoError(t, err)
		assert.Equal(t, "CPTY-123/456", details.CounterpartyID())
		assert.Equal(t, "Test Counterparty (UK) Ltd.", details.CounterpartyName())
		assert.Equal(t, "ACC#001@REF", details.ExternalRef())
	})

	t.Run("unicode in values", func(t *testing.T) {
		t.Parallel()

		details, err := NewCounterpartyDetails(
			"银行001",
			"東京三菱UFJ銀行",
			"REF-日本-001",
		)
		require.NoError(t, err)
		assert.Equal(t, "银行001", details.CounterpartyID())
		assert.Equal(t, "東京三菱UFJ銀行", details.CounterpartyName())
		assert.Equal(t, "REF-日本-001", details.ExternalRef())
	})
}

func TestCounterpartyDetails_MultipleAttributes(t *testing.T) {
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

		details, err := NewCounterpartyDetailsWithOptions(
			"CPTY1", "First Counterparty", "ACC001", attrs,
		)
		require.NoError(t, err)

		retrievedAttrs := details.Attributes()
		assert.Len(t, retrievedAttrs, 8)
		assert.Equal(t, "EMEA", retrievedAttrs["region"])
		assert.Equal(t, "primary", retrievedAttrs["tier"])
	})
}

func TestCounterpartyDetails_EqualityReflexive(t *testing.T) {
	t.Parallel()

	details, err := NewCounterpartyDetailsWithOptions(
		"CPTY1", "First Counterparty", "ACC001",
		map[string]string{"key": "value"},
	)
	require.NoError(t, err)

	assert.True(t, details.Equals(details), "a value should equal itself")
}

func TestCounterpartyDetails_ValidationPriority(t *testing.T) {
	t.Parallel()

	t.Run("empty counterpartyID checked before other validations", func(t *testing.T) {
		t.Parallel()

		_, err := NewCounterpartyDetails("", "AB", "")
		require.Error(t, err)
		assert.ErrorIs(t, err, errCounterpartyIDRequired)
	})

	t.Run("counterpartyName checked before externalRef", func(t *testing.T) {
		t.Parallel()

		_, err := NewCounterpartyDetails("CPTY1", "AB", "")
		require.Error(t, err)
		assert.ErrorIs(t, err, errCounterpartyNameTooShort)
	})
}
