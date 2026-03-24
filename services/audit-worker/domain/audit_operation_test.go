package domain

import (
	"testing"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/stretchr/testify/assert"
)

func TestProtoToOperation(t *testing.T) {
	tests := []struct {
		name     string
		op       auditv1.AuditOperation
		expected string
	}{
		{
			name:     "insert maps to INSERT",
			op:       auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			expected: "INSERT",
		},
		{
			name:     "update maps to UPDATE",
			op:       auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
			expected: "UPDATE",
		},
		{
			name:     "delete maps to DELETE",
			op:       auditv1.AuditOperation_AUDIT_OPERATION_DELETE,
			expected: "DELETE",
		},
		{
			name:     "initial import maps to INITIAL_IMPORT",
			op:       auditv1.AuditOperation_AUDIT_OPERATION_INITIAL_IMPORT,
			expected: "INITIAL_IMPORT",
		},
		{
			name:     "unspecified maps to empty string",
			op:       auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED,
			expected: "",
		},
		{
			name:     "unknown value maps to empty string",
			op:       auditv1.AuditOperation(9999),
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProtoToOperation(tt.op)
			assert.Equal(t, tt.expected, result)
		})
	}
}
