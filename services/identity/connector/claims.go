package connector

import (
	"strings"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// BuildClaims constructs the JWT custom claims map for an authenticated identity.
// The map is consumed by Dex's ID-token builder when enriching tokens with
// connector-provided attributes.
//
// Claim keys follow the shared platform conventions:
//   - "sub"        — stable user identifier (UUID)
//   - "email"      — verified email address
//   - "name"       — display name (falls back to email when not set)
//   - "x-tenant-id" — tenant identifier injected into every token for downstream routing
//   - "roles"      — active role names; downstream services use this for RBAC
//   - "groups"     — mirrors roles; included for compatibility with Dex's group claim handling
func BuildClaims(identity Identity, tenantID tenant.TenantID) map[string]interface{} {
	name := identity.Username
	if name == "" {
		name = identity.Email
	}

	rawRoles := identity.Groups
	if rawRoles == nil {
		rawRoles = []string{}
	}
	roles := make([]string, len(rawRoles))
	for i, r := range rawRoles {
		roles[i] = strings.ToLower(r)
	}

	return map[string]interface{}{
		"sub":              identity.UserID,
		"email":            identity.Email,
		"name":             name,
		tenant.TenantIDKey: tenantID.String(),
		"roles":            roles,
		"groups":           roles,
	}
}
