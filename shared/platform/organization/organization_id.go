package organization

import (
	"regexp"
	"strings"
)

// organizationIDPattern matches alphanumeric characters and underscores, 1-50 chars.
var organizationIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_]{1,50}$`)

// OrganizationID is a strongly-typed identifier for an organization.
// It is validated on construction to ensure it follows naming conventions.
//
//nolint:revive // OrganizationID is intentionally explicit for clarity at call sites (organization.OrganizationID)
type OrganizationID string

// NewOrganizationID creates a new OrganizationID after validating the format.
// Valid organization IDs contain only alphanumeric characters and underscores,
// and must be 1-50 characters long.
func NewOrganizationID(id string) (OrganizationID, error) {
	if !organizationIDPattern.MatchString(id) {
		return "", ErrInvalidOrganizationID
	}
	return OrganizationID(id), nil
}

// MustNewOrganizationID creates a new OrganizationID, panicking if validation fails.
// Use only with compile-time constants or well-validated input.
func MustNewOrganizationID(id string) OrganizationID {
	orgID, err := NewOrganizationID(id)
	if err != nil {
		panic("invalid organization ID: " + id)
	}
	return orgID
}

// String returns the string representation of the organization ID.
func (o OrganizationID) String() string {
	return string(o)
}

// SchemaName returns the PostgreSQL schema name for this organization.
// Uses the convention "org_" + lowercase(organization ID).
// Normalized to lowercase to match PostgreSQL's identifier case folding behavior.
func (o OrganizationID) SchemaName() string {
	return "org_" + strings.ToLower(string(o))
}

// IsEmpty returns true if the organization ID is empty.
func (o OrganizationID) IsEmpty() bool {
	return o == ""
}
