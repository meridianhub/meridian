package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDataCategoryUtilization_IsValid verifies the UTILIZATION constant is valid.
// The dataset_test.go covers PRICING and CONTEXTUAL but not UTILIZATION.
func TestDataCategoryUtilization_IsValid(t *testing.T) {
	assert.True(t, DataCategoryUtilization.IsValid())
}

func TestDataCategoryUtilization_String(t *testing.T) {
	assert.Equal(t, "UTILIZATION", DataCategoryUtilization.String())
}

// TestDataCategory_AllThreeConstantsValid verifies all three category constants are valid.
func TestDataCategory_AllThreeConstantsValid(t *testing.T) {
	categories := []DataCategory{
		DataCategoryPricing,
		DataCategoryContextual,
		DataCategoryUtilization,
	}

	for _, c := range categories {
		assert.True(t, c.IsValid(), "expected %q to be valid", c)
	}
}

// TestDataCategory_UnknownValuesInvalid verifies several invalid inputs.
func TestDataCategory_UnknownValuesInvalid(t *testing.T) {
	invalid := []DataCategory{
		DataCategory("UNKNOWN"),
		DataCategory(""),
		DataCategory("pricing"),    // lowercase
		DataCategory("PRICE"),      // partial match
		DataCategory("contextual"), // lowercase
	}

	for _, c := range invalid {
		assert.False(t, c.IsValid(), "expected %q to be invalid", c)
	}
}

// TestDataCategory_StringPreservesArbitraryValue verifies String() returns
// the underlying string even for non-constant values.
func TestDataCategory_StringPreservesArbitraryValue(t *testing.T) {
	custom := DataCategory("MY_CUSTOM_CATEGORY")
	assert.Equal(t, "MY_CUSTOM_CATEGORY", custom.String())
}

// TestDataCategory_UsedAsMapKey verifies DataCategory can be used as a map key.
func TestDataCategory_UsedAsMapKey(t *testing.T) {
	labels := map[DataCategory]string{
		DataCategoryPricing:     "market pricing",
		DataCategoryContextual:  "reference data",
		DataCategoryUtilization: "resource usage",
	}

	assert.Equal(t, "market pricing", labels[DataCategoryPricing])
	assert.Equal(t, "reference data", labels[DataCategoryContextual])
	assert.Equal(t, "resource usage", labels[DataCategoryUtilization])

	_, exists := labels[DataCategory("NONEXISTENT")]
	assert.False(t, exists)
}

// TestDataCategory_ConstantStringValues verifies underlying string values of constants.
func TestDataCategory_ConstantStringValues(t *testing.T) {
	assert.Equal(t, DataCategory("PRICING"), DataCategoryPricing)
	assert.Equal(t, DataCategory("CONTEXTUAL"), DataCategoryContextual)
	assert.Equal(t, DataCategory("UTILIZATION"), DataCategoryUtilization)
}
