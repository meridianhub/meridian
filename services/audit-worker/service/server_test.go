package service

import (
	"testing"
	"time"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRowToProto_NilOptionalFields verifies that nil optional fields are handled correctly.
func TestRowToProto_NilOptionalFields(t *testing.T) {
	row := auditLogRow{
		ID:        "entry-1",
		TableName: "accounts",
		Operation: "INSERT",
		RecordID:  "rec-1",
		CreatedAt: time.Now(),
		// ChangedBy, OldValues, NewValues all nil
	}

	entry, err := rowToProto(row)
	require.NoError(t, err)

	assert.Equal(t, "entry-1", entry.EntryId)
	assert.Equal(t, "accounts", entry.TableName)
	assert.Equal(t, auditv1.AuditOperation_AUDIT_OPERATION_INSERT, entry.Operation)
	assert.Equal(t, "rec-1", entry.RecordId)
	assert.Empty(t, entry.ChangedBy)
	assert.Nil(t, entry.OldValues)
	assert.Nil(t, entry.NewValues)
}

// TestRowToProto_WithAllFields verifies that all optional fields are correctly mapped.
func TestRowToProto_WithAllFields(t *testing.T) {
	changedBy := "alice"
	oldVals := `{"balance": "100.00"}`
	newVals := `{"balance": "200.00"}`

	row := auditLogRow{
		ID:        "entry-2",
		TableName: "accounts",
		Operation: "UPDATE",
		RecordID:  "rec-2",
		CreatedAt: time.Now(),
		ChangedBy: &changedBy,
		OldValues: &oldVals,
		NewValues: &newVals,
	}

	entry, err := rowToProto(row)
	require.NoError(t, err)

	assert.Equal(t, "alice", entry.ChangedBy)
	require.NotNil(t, entry.OldValues)
	assert.Equal(t, "100.00", entry.OldValues.Fields["balance"].GetStringValue())
	require.NotNil(t, entry.NewValues)
	assert.Equal(t, "200.00", entry.NewValues.Fields["balance"].GetStringValue())
}

// TestRowToProto_EmptyStringValues verifies that empty string values (not nil) are skipped.
func TestRowToProto_EmptyStringValues(t *testing.T) {
	empty := ""
	row := auditLogRow{
		ID:        "entry-3",
		TableName: "parties",
		Operation: "DELETE",
		RecordID:  "rec-3",
		CreatedAt: time.Now(),
		OldValues: &empty, // Non-nil but empty - should not produce OldValues
		NewValues: nil,
	}

	entry, err := rowToProto(row)
	require.NoError(t, err)

	assert.Nil(t, entry.OldValues)
	assert.Nil(t, entry.NewValues)
}

// TestRowToProto_InvalidOldValuesJSON verifies that malformed JSON returns an error.
func TestRowToProto_InvalidOldValuesJSON(t *testing.T) {
	badJSON := "not valid json"
	row := auditLogRow{
		ID:        "entry-4",
		TableName: "accounts",
		Operation: "UPDATE",
		RecordID:  "rec-4",
		CreatedAt: time.Now(),
		OldValues: &badJSON,
	}

	_, err := rowToProto(row)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "old_values")
}

// TestRowToProto_InvalidNewValuesJSON verifies that malformed new_values JSON returns an error.
func TestRowToProto_InvalidNewValuesJSON(t *testing.T) {
	valid := `{"key": "val"}`
	invalid := "not json"
	row := auditLogRow{
		ID:        "entry-5",
		TableName: "accounts",
		Operation: "UPDATE",
		RecordID:  "rec-5",
		CreatedAt: time.Now(),
		OldValues: &valid,
		NewValues: &invalid,
	}

	_, err := rowToProto(row)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new_values")
}

// TestRowToProto_AllOperations verifies each operation string maps to the correct proto enum.
func TestRowToProto_AllOperations(t *testing.T) {
	tests := []struct {
		operation string
		expected  auditv1.AuditOperation
	}{
		{"INSERT", auditv1.AuditOperation_AUDIT_OPERATION_INSERT},
		{"UPDATE", auditv1.AuditOperation_AUDIT_OPERATION_UPDATE},
		{"DELETE", auditv1.AuditOperation_AUDIT_OPERATION_DELETE},
		{"INITIAL_IMPORT", auditv1.AuditOperation_AUDIT_OPERATION_INITIAL_IMPORT},
		{"UNKNOWN", auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.operation, func(t *testing.T) {
			row := auditLogRow{
				ID:        "entry-op",
				TableName: "t",
				Operation: tt.operation,
				RecordID:  "r",
				CreatedAt: time.Now(),
			}
			entry, err := rowToProto(row)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, entry.Operation)
		})
	}
}

// TestJsonToStruct_ValidJSON verifies correct JSON-to-proto-struct conversion.
func TestJsonToStruct_ValidJSON(t *testing.T) {
	s, err := jsonToStruct(`{"name": "test", "amount": 42}`)
	require.NoError(t, err)
	require.NotNil(t, s)
	assert.Equal(t, "test", s.Fields["name"].GetStringValue())
}

// TestJsonToStruct_InvalidJSON verifies that malformed JSON returns an error.
func TestJsonToStruct_InvalidJSON(t *testing.T) {
	_, err := jsonToStruct("not json {")
	require.Error(t, err)
}

// TestJsonToStruct_EmptyObject verifies that an empty JSON object succeeds.
func TestJsonToStruct_EmptyObject(t *testing.T) {
	s, err := jsonToStruct("{}")
	require.NoError(t, err)
	assert.NotNil(t, s)
	assert.Empty(t, s.Fields)
}
