package applier

import (
	"testing"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseExternalReferenceType_Empty(t *testing.T) {
	refType, err := parseExternalReferenceType("")
	require.NoError(t, err)
	assert.Equal(t, partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED, refType)
}

func TestParseExternalReferenceType_Known(t *testing.T) {
	refType, err := parseExternalReferenceType("EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE")
	require.NoError(t, err)
	assert.Equal(t, partyv1.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE, refType)
}

func TestParseExternalReferenceType_Unknown(t *testing.T) {
	_, err := parseExternalReferenceType("BOGUS")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownExternalReferenceType)
}

func TestNewPartyClient(t *testing.T) {
	c := NewPartyClient(nil)
	assert.NotNil(t, c)
}
