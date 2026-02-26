package bootstrap

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadPlatformManifest(t *testing.T) {
	mf, err := LoadPlatformManifest()
	require.NoError(t, err)
	require.NotNil(t, mf)

	// Verify instruments (4 fiat + 1 commodity)
	assert.Len(t, mf.Instruments, 5)
	codes := make([]string, len(mf.Instruments))
	for i, inst := range mf.Instruments {
		codes[i] = inst.Code
	}
	assert.Contains(t, codes, "GBP")
	assert.Contains(t, codes, "EUR")
	assert.Contains(t, codes, "USD")
	assert.Contains(t, codes, "NZD")
	assert.Contains(t, codes, "ACTIVE_PARTY")

	// Verify account types (3 standard + 3 platform billing)
	assert.Len(t, mf.AccountTypes, 6)
	acctCodes := make([]string, len(mf.AccountTypes))
	for i, at := range mf.AccountTypes {
		acctCodes[i] = at.Code
	}
	assert.Contains(t, acctCodes, "CLEARING")
	assert.Contains(t, acctCodes, "SETTLEMENT")
	assert.Contains(t, acctCodes, "NOSTRO")
	assert.Contains(t, acctCodes, "USAGE_METERING")
	assert.Contains(t, acctCodes, "PLATFORM_RECEIVABLE")
	assert.Contains(t, acctCodes, "PLATFORM_REVENUE")

	// Verify valuation rules (3 FX + 1 usage pricing)
	assert.Len(t, mf.ValuationRules, 4)
}

func TestLoadPlatformManifest_ValidJSON(t *testing.T) {
	// Verify the embedded JSON is valid by loading it
	mf, err := LoadPlatformManifest()
	require.NoError(t, err)

	// Verify metadata
	assert.Equal(t, "1.0", mf.Version)
	assert.NotNil(t, mf.Metadata)
	assert.Equal(t, "Meridian Platform Economy", mf.Metadata.Name)
	assert.Equal(t, "platform", mf.Metadata.Industry)
}

func TestValidatePlatformManifest(t *testing.T) {
	// The embedded manifest should pass validation
	err := validatePlatformManifest(slog.Default())
	require.NoError(t, err)
}

func TestMasterTenantID(t *testing.T) {
	assert.Equal(t, "meridian_master", MasterTenantID)
}
