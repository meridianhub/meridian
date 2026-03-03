package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTenantAccountMapping_ValidJSON(t *testing.T) {
	tenantID1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	accountID1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	tenantID2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	accountID2 := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	json := `{
		"00000000-0000-0000-0000-000000000001": "11111111-1111-1111-1111-111111111111",
		"00000000-0000-0000-0000-000000000002": "22222222-2222-2222-2222-222222222222"
	}`

	result, err := ParseTenantAccountMapping(json)

	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, accountID1, result[tenantID1])
	assert.Equal(t, accountID2, result[tenantID2])
}

func TestParseTenantAccountMapping_SingleMapping(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	accountID := uuid.MustParse("12345678-1234-1234-1234-123456789012")

	json := `{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee": "12345678-1234-1234-1234-123456789012"}`

	result, err := ParseTenantAccountMapping(json)

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, accountID, result[tenantID])
}

func TestParseTenantAccountMapping_EmptyString(t *testing.T) {
	result, err := ParseTenantAccountMapping("")

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestParseTenantAccountMapping_EmptyJSON(t *testing.T) {
	result, err := ParseTenantAccountMapping("{}")

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestParseTenantAccountMapping_InvalidJSON(t *testing.T) {
	_, err := ParseTenantAccountMapping(`{invalid json}`)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse tenant account mapping JSON")
}

func TestParseTenantAccountMapping_InvalidTenantUUID(t *testing.T) {
	json := `{"not-a-uuid": "11111111-1111-1111-1111-111111111111"}`

	_, err := ParseTenantAccountMapping(json)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid tenant_id")
}

func TestParseTenantAccountMapping_InvalidAccountUUID(t *testing.T) {
	json := `{"00000000-0000-0000-0000-000000000001": "not-a-uuid"}`

	_, err := ParseTenantAccountMapping(json)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid account_id")
}

func TestParseTenantAccountMapping_WhitespacePreserved(t *testing.T) {
	// Ensure whitespace in JSON is handled correctly
	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	accountID := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	json := `
	{
		"00000000-0000-0000-0000-000000000001" : "11111111-1111-1111-1111-111111111111"
	}
	`

	result, err := ParseTenantAccountMapping(json)

	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, accountID, result[tenantID])
}
