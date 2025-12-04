package tenancy

import "regexp"

// tenantIDPattern matches alphanumeric characters and underscores, 1-50 chars.
var tenantIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_]{1,50}$`)

// TenantID is a strongly-typed identifier for a tenant.
// It is validated on construction to ensure it follows naming conventions.
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
	tid, err := NewTenantID(id)
	if err != nil {
		panic("invalid tenant ID: " + id)
	}
	return tid
}

// String returns the string representation of the tenant ID.
func (t TenantID) String() string {
	return string(t)
}

// SchemaName returns the PostgreSQL schema name for this tenant.
// Uses the convention "tenant_" + tenant ID.
func (t TenantID) SchemaName() string {
	return "tenant_" + string(t)
}

// IsEmpty returns true if the tenant ID is empty.
func (t TenantID) IsEmpty() bool {
	return t == ""
}
