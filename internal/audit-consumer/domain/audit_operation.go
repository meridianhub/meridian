// Package domain provides domain-level types and utilities for audit event processing.
package domain

import (
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
)

// ProtoToOperation converts a protobuf AuditOperation to a string.
// Returns an empty string for AUDIT_OPERATION_UNSPECIFIED or unknown operations.
func ProtoToOperation(op auditv1.AuditOperation) string {
	switch op {
	case auditv1.AuditOperation_AUDIT_OPERATION_INSERT:
		return "INSERT"
	case auditv1.AuditOperation_AUDIT_OPERATION_UPDATE:
		return "UPDATE"
	case auditv1.AuditOperation_AUDIT_OPERATION_DELETE:
		return "DELETE"
	case auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED:
		return ""
	}
	return ""
}
