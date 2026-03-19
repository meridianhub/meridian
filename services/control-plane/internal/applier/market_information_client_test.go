package applier

import (
	"testing"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDataCategory_Empty(t *testing.T) {
	cat, err := parseDataCategory("")
	require.NoError(t, err)
	assert.Equal(t, marketinformationv1.DataCategory_DATA_CATEGORY_UNSPECIFIED, cat)
}

func TestParseDataCategory_Prefixed(t *testing.T) {
	cat, err := parseDataCategory("DATA_CATEGORY_FX_RATE")
	require.NoError(t, err)
	assert.Equal(t, marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE, cat)
}

func TestParseDataCategory_Stripped(t *testing.T) {
	cat, err := parseDataCategory("FX_RATE")
	require.NoError(t, err)
	assert.Equal(t, marketinformationv1.DataCategory_DATA_CATEGORY_FX_RATE, cat)
}

func TestParseDataCategory_Unknown(t *testing.T) {
	_, err := parseDataCategory("NONSENSE")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownDataCategory)
	assert.Contains(t, err.Error(), "NONSENSE")
}

func TestNewMarketInformationClient(t *testing.T) {
	c := NewMarketInformationClient(nil)
	assert.NotNil(t, c)
}
