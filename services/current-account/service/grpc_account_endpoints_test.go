package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// stripEnumPrefix
// =============================================================================

func TestStripEnumPrefix_Matching(t *testing.T) {
	result := stripEnumPrefix("PARTY_TYPE_PERSON", "PARTY_TYPE_")
	assert.Equal(t, "PERSON", result)
}

func TestStripEnumPrefix_NoMatch(t *testing.T) {
	// Prefix not present - returns original value
	result := stripEnumPrefix("PERSON", "PARTY_TYPE_")
	assert.Equal(t, "PERSON", result)
}

func TestStripEnumPrefix_EmptyValue(t *testing.T) {
	result := stripEnumPrefix("", "PARTY_TYPE_")
	assert.Equal(t, "", result)
}

func TestStripEnumPrefix_EmptyPrefix(t *testing.T) {
	result := stripEnumPrefix("PARTY_TYPE_PERSON", "")
	assert.Equal(t, "PARTY_TYPE_PERSON", result)
}

func TestStripEnumPrefix_PartyStatus(t *testing.T) {
	result := stripEnumPrefix("PARTY_STATUS_ACTIVE", "PARTY_STATUS_")
	assert.Equal(t, "ACTIVE", result)
}

func TestStripEnumPrefix_ExternalRefType(t *testing.T) {
	result := stripEnumPrefix("EXTERNAL_REFERENCE_TYPE_PASSPORT", "EXTERNAL_REFERENCE_TYPE_")
	assert.Equal(t, "PASSPORT", result)
}

// =============================================================================
// validateAttributes
// =============================================================================

// mockValidatingSchema is a minimal implementation of the interface required by validateAttributes.
type mockValidatingSchema struct {
	validateErr error
}

func (m *mockValidatingSchema) Validate(_ interface{}) error {
	return m.validateErr
}

func TestValidateAttributes_Success(t *testing.T) {
	schema := &mockValidatingSchema{validateErr: nil}
	attrs := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}

	err := validateAttributes(schema, attrs)
	require.NoError(t, err)
}

func TestValidateAttributes_ValidationFailure(t *testing.T) {
	expectedErr := errors.New("schema validation failed")
	schema := &mockValidatingSchema{validateErr: expectedErr}
	attrs := map[string]string{"key": "value"}

	err := validateAttributes(schema, attrs)
	require.Error(t, err)
	assert.ErrorIs(t, err, expectedErr)
}

func TestValidateAttributes_EmptyAttributes(t *testing.T) {
	schema := &mockValidatingSchema{validateErr: nil}
	attrs := map[string]string{}

	err := validateAttributes(schema, attrs)
	require.NoError(t, err)
}

func TestValidateAttributes_NilAttributes(t *testing.T) {
	schema := &mockValidatingSchema{validateErr: nil}

	// nil map is valid - should not panic, treated as empty
	err := validateAttributes(schema, nil)
	require.NoError(t, err)
}

func TestValidateAttributes_MultipleAttributes(t *testing.T) {
	schema := &mockValidatingSchema{validateErr: nil}
	attrs := map[string]string{
		"batch_id":    "BATCH-001",
		"origin":      "warehouse-a",
		"grade":       "A",
		"temperature": "4",
	}

	err := validateAttributes(schema, attrs)
	require.NoError(t, err)
}
