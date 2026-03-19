package persistence

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONBMap_Value_Nil(t *testing.T) {
	var j JSONBMap
	val, err := j.Value()
	require.NoError(t, err)
	assert.Nil(t, val, "nil JSONBMap should return nil driver.Value")
}

func TestJSONBMap_Value_NonNil(t *testing.T) {
	j := JSONBMap(`{"key":"value"}`)
	val, err := j.Value()
	require.NoError(t, err)
	assert.Equal(t, []byte(`{"key":"value"}`), val)
}

func TestJSONBMap_Scan_Nil(t *testing.T) {
	var j JSONBMap
	err := j.Scan(nil)
	require.NoError(t, err)
	assert.Nil(t, j, "scanning nil should set JSONBMap to nil")
}

func TestJSONBMap_Scan_Bytes(t *testing.T) {
	var j JSONBMap
	err := j.Scan([]byte(`{"amount":100}`))
	require.NoError(t, err)
	assert.Equal(t, JSONBMap(`{"amount":100}`), j)
}

func TestJSONBMap_Scan_String(t *testing.T) {
	var j JSONBMap
	err := j.Scan(`{"amount":200}`)
	require.NoError(t, err)
	assert.Equal(t, JSONBMap(`{"amount":200}`), j)
}

func TestJSONBMap_Scan_UnsupportedType(t *testing.T) {
	var j JSONBMap
	err := j.Scan(12345)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedJSONBType)
}

func TestLienEntity_TableName(t *testing.T) {
	entity := LienEntity{}
	assert.Equal(t, "lien", entity.TableName())
}

// =============================================================================
// StatusHistoryJSON Scan tests (account_entity.go)
// =============================================================================

func TestStatusHistoryJSON_Value_Nil(t *testing.T) {
	var s StatusHistoryJSON
	val, err := s.Value()
	require.NoError(t, err)
	assert.Equal(t, "[]", val)
}

func TestStatusHistoryJSON_Value_NonNil(t *testing.T) {
	s := StatusHistoryJSON{
		{FromStatus: "ACTIVE", ToStatus: "FROZEN", Reason: "test"},
	}
	val, err := s.Value()
	require.NoError(t, err)
	assert.NotNil(t, val)
}

func TestStatusHistoryJSON_Scan_Nil(t *testing.T) {
	var s StatusHistoryJSON
	err := s.Scan(nil)
	require.NoError(t, err)
	assert.Empty(t, s)
}

func TestStatusHistoryJSON_Scan_Bytes(t *testing.T) {
	var s StatusHistoryJSON
	err := s.Scan([]byte(`[{"from_status":"ACTIVE","to_status":"FROZEN","reason":"test","timestamp":"2026-01-01T00:00:00Z","changed_by":"user"}]`))
	require.NoError(t, err)
	require.Len(t, s, 1)
	assert.Equal(t, "ACTIVE", s[0].FromStatus)
	assert.Equal(t, "FROZEN", s[0].ToStatus)
}

func TestStatusHistoryJSON_Scan_String(t *testing.T) {
	var s StatusHistoryJSON
	err := s.Scan(`[{"from_status":"FROZEN","to_status":"ACTIVE","reason":"unfrozen","timestamp":"2026-01-02T00:00:00Z","changed_by":"admin"}]`)
	require.NoError(t, err)
	require.Len(t, s, 1)
	assert.Equal(t, "FROZEN", s[0].FromStatus)
	assert.Equal(t, "ACTIVE", s[0].ToStatus)
}

func TestStatusHistoryJSON_Scan_UnsupportedType(t *testing.T) {
	var s StatusHistoryJSON
	err := s.Scan(12345)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusHistoryScan)
}

func TestCurrentAccountEntity_TableName(t *testing.T) {
	entity := CurrentAccountEntity{}
	assert.Equal(t, "account", entity.TableName())
}
