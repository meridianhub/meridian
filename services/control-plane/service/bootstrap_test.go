package service

import (
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestValidateManifest_Valid(t *testing.T) {
	mf := &controlplanev1.Manifest{}
	err := protojson.Unmarshal([]byte(`{
		"version": "1.0",
		"metadata": {"name": "test", "industry": "platform"},
		"instruments": [{
			"code": "GBP",
			"name": "British Pound",
			"type": "INSTRUMENT_TYPE_FIAT",
			"dimensions": {"unit": "GBP", "precision": 2}
		}],
		"accountTypes": [{
			"code": "CLEARING",
			"name": "Clearing",
			"normalBalance": "NORMAL_BALANCE_DEBIT",
			"allowedInstruments": ["GBP"]
		}]
	}`), mf)
	require.NoError(t, err)

	result, err := ValidateManifest(mf, nil)
	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Errors)
}

func TestValidateManifest_Invalid(t *testing.T) {
	// Empty manifest should have validation errors
	mf := &controlplanev1.Manifest{}

	result, err := ValidateManifest(mf, nil)
	require.NoError(t, err)
	assert.False(t, result.Valid)
	assert.NotEmpty(t, result.Errors)
}
