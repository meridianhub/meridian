// Package domain contains the domain models for the Market Information service.
package domain

// DataAccessLevel defines the visibility and access control level for a dataset.
// This determines who can access the dataset and whether hierarchical lookup is enabled.
type DataAccessLevel string

const (
	// AccessLevelPublic indicates the dataset is publicly available to all tenants.
	// No entitlement checks are performed. Shared datasets with PUBLIC access
	// enable hierarchical lookup (tenant-first, then master fallback).
	AccessLevelPublic DataAccessLevel = "PUBLIC"

	// AccessLevelPrivate indicates the dataset is private to the owning tenant.
	// No sharing or hierarchical lookup is performed. Only the tenant that created
	// the dataset can access it.
	AccessLevelPrivate DataAccessLevel = "PRIVATE"

	// AccessLevelRestricted indicates the dataset is shared but requires explicit entitlements.
	// Hierarchical lookup is enabled for tenants with active entitlements.
	// Access control checks are performed before allowing fallback to master data.
	AccessLevelRestricted DataAccessLevel = "RESTRICTED"
)

// IsValid checks if the access level is a recognized value.
func (a DataAccessLevel) IsValid() bool {
	switch a {
	case AccessLevelPublic, AccessLevelPrivate, AccessLevelRestricted:
		return true
	default:
		return false
	}
}

// String returns the string representation of the access level.
func (a DataAccessLevel) String() string {
	return string(a)
}
