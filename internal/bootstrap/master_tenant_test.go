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

	// Verify instruments
	assert.Len(t, mf.Instruments, 4)
	codes := make([]string, len(mf.Instruments))
	for i, inst := range mf.Instruments {
		codes[i] = inst.Code
	}
	assert.Contains(t, codes, "GBP")
	assert.Contains(t, codes, "EUR")
	assert.Contains(t, codes, "USD")
	assert.Contains(t, codes, "NZD")

	// Verify account types
	assert.Len(t, mf.AccountTypes, 3)
	acctCodes := make([]string, len(mf.AccountTypes))
	for i, at := range mf.AccountTypes {
		acctCodes[i] = at.Code
	}
	assert.Contains(t, acctCodes, "CLEARING")
	assert.Contains(t, acctCodes, "SETTLEMENT")
	assert.Contains(t, acctCodes, "NOSTRO")

	// Verify valuation rules
	assert.Len(t, mf.ValuationRules, 3)
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
