package tenant

import (
	"regexp"
	"strings"
)

// tenantIDPattern matches alphanumeric characters and underscores, 1-50 chars.
var tenantIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_]{1,50}$`)

// TenantID is a strongly-typed identifier for a tenant.
// It is validated on construction to ensure it follows naming conventions.
//
//nolint:revive // TenantID is intentionally explicit for clarity at call sites (tenant.TenantID)
type TenantID string

// NewTenantID creates a new TenantID after validating the format.
// Valid tenant IDs contain only alphanumeric characters and underscores,
// and must be 1-50 characters long.
func NewTenantID(id string) (TenantID, error) {
	if !tenantIDPattern.MatchString(id) {
		return "", ErrInvalidTenantID
	}
	return TenantID(id), nil
}

// MustNewTenantID creates a new TenantID, panicking if validation fails.
// Use only with compile-time constants or well-validated input.
func MustNewTenantID(id string) TenantID {
	tenantID, err := NewTenantID(id)
	if err != nil {
		panic("invalid tenant ID: " + id)
	}
	return tenantID
}

// String returns the string representation of the tenant ID.
func (t TenantID) String() string {
	return string(t)
}

// SchemaName returns the PostgreSQL schema name for this tenant.
// Uses the convention "org_" + lowercase(tenant ID).
// Note: The "org_" prefix is retained for backward compatibility with existing schemas.
// Normalized to lowercase to match PostgreSQL's identifier case folding behavior.
func (t TenantID) SchemaName() string {
	return "org_" + strings.ToLower(string(t))
}

// IsEmpty returns true if the tenant ID is empty.
func (t TenantID) IsEmpty() bool {
	return t == ""
}
