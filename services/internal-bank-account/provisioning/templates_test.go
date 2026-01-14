package provisioning

import (
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_bank_account/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltInTemplateSets_Exist(t *testing.T) {
	// Verify expected template sets exist
	expectedSets := []string{"default", "energy", "compute", "minimal"}

	for _, name := range expectedSets {
		ts := GetTemplateSet(name)
		require.NotNil(t, ts, "template set %s should exist", name)
		assert.Equal(t, name, ts.Name)
		assert.NotEmpty(t, ts.Description)
		assert.NotEmpty(t, ts.Templates)
	}
}

func TestGetTemplateSet_Unknown(t *testing.T) {
	ts := GetTemplateSet("nonexistent")
	assert.Nil(t, ts)
}

func TestListTemplateSets(t *testing.T) {
	names := ListTemplateSets()
	assert.GreaterOrEqual(t, len(names), 4)
	assert.Contains(t, names, "default")
	assert.Contains(t, names, "energy")
	assert.Contains(t, names, "compute")
	assert.Contains(t, names, "minimal")
}

func TestEnergyAccounts_HasRequiredTypes(t *testing.T) {
	typeCount := make(map[pb.InternalAccountType]int)
	for _, template := range EnergyAccounts {
		typeCount[template.Type]++
	}

	// Energy should have:
	// - Clearing accounts (currency + energy)
	// - At least one inventory account
	// - Revenue accounts
	// - Expense accounts
	// - Suspense account
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING], 0, "should have CLEARING accounts")
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_INVENTORY], 0, "should have INVENTORY accounts")
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE], 0, "should have REVENUE accounts")
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_EXPENSE], 0, "should have EXPENSE accounts")
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE], 0, "should have SUSPENSE accounts")
}

func TestEnergyAccounts_HasEnergyInstruments(t *testing.T) {
	var hasKWH bool
	for _, template := range EnergyAccounts {
		if template.InstrumentCode == "KWH" {
			hasKWH = true
			assert.Equal(t, DimensionEnergy, template.Dimension, "KWH should have ENERGY dimension")
		}
	}
	assert.True(t, hasKWH, "energy accounts should include KWH instruments")
}

func TestComputeAccounts_HasRequiredTypes(t *testing.T) {
	typeCount := make(map[pb.InternalAccountType]int)
	for _, template := range ComputeAccounts {
		typeCount[template.Type]++
	}

	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING], 0, "should have CLEARING accounts")
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_INVENTORY], 0, "should have INVENTORY accounts")
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE], 0, "should have REVENUE accounts")
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_EXPENSE], 0, "should have EXPENSE accounts")
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE], 0, "should have SUSPENSE accounts")
}

func TestComputeAccounts_HasComputeInstruments(t *testing.T) {
	var hasGPU, hasCPU, hasData bool
	for _, template := range ComputeAccounts {
		switch template.InstrumentCode {
		case "GPU-HOUR":
			hasGPU = true
			assert.Equal(t, DimensionCompute, template.Dimension, "GPU-HOUR should have COMPUTE dimension")
		case "CPU-HOUR":
			hasCPU = true
			assert.Equal(t, DimensionCompute, template.Dimension, "CPU-HOUR should have COMPUTE dimension")
		case "GB-DATA":
			hasData = true
			assert.Equal(t, DimensionData, template.Dimension, "GB-DATA should have DATA dimension")
		}
	}
	assert.True(t, hasGPU, "compute accounts should include GPU-HOUR instruments")
	assert.True(t, hasCPU, "compute accounts should include CPU-HOUR instruments")
	assert.True(t, hasData, "compute accounts should include GB-DATA instruments")
}

func TestMinimalAccounts_HasOnlySuspense(t *testing.T) {
	assert.Equal(t, 1, len(MinimalAccounts), "minimal should have only 1 account")
	assert.Equal(t, pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE, MinimalAccounts[0].Type)
}

func TestTemplateSet_UniqueCodes(t *testing.T) {
	for name, ts := range BuiltInTemplateSets {
		t.Run(name, func(t *testing.T) {
			codes := make(map[string]bool)
			for _, template := range ts.Templates {
				if codes[template.Code] {
					t.Errorf("duplicate account code in %s: %s", name, template.Code)
				}
				codes[template.Code] = true
			}
		})
	}
}

func TestTemplateSet_ValidDimensions(t *testing.T) {
	validDimensions := map[string]bool{
		DimensionCurrency: true,
		DimensionEnergy:   true,
		DimensionMass:     true,
		DimensionVolume:   true,
		DimensionTime:     true,
		DimensionCompute:  true,
		DimensionCarbon:   true,
		DimensionData:     true,
		DimensionCount:    true,
	}

	for name, ts := range BuiltInTemplateSets {
		t.Run(name, func(t *testing.T) {
			for _, template := range ts.Templates {
				assert.True(t, validDimensions[template.Dimension],
					"template %s in set %s has invalid dimension: %s",
					template.Code, name, template.Dimension)
			}
		})
	}
}

func TestTemplateSet_ValidAccountTypes(t *testing.T) {
	for name, ts := range BuiltInTemplateSets {
		t.Run(name, func(t *testing.T) {
			for _, template := range ts.Templates {
				assert.NotEqual(t, pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_UNSPECIFIED, template.Type,
					"template %s in set %s has unspecified account type",
					template.Code, name)
			}
		})
	}
}
