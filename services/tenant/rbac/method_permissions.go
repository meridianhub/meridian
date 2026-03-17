package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all TenantService gRPC methods.
// Tenant management is admin-only except for read operations.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// Admin-only operations
		"/meridian.tenant.v1.TenantService/InitiateTenant": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.tenant.v1.TenantService/UpdateTenantStatus": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.tenant.v1.TenantService/ReconcileMigrations": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},

		// Read operations: admin, operator, auditor
		"/meridian.tenant.v1.TenantService/RetrieveTenant": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.tenant.v1.TenantService/ListTenants": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.tenant.v1.TenantService/GetTenantProvisioningStatus": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

var ExpectedMethods = []string{
	"/meridian.tenant.v1.TenantService/InitiateTenant",
	"/meridian.tenant.v1.TenantService/RetrieveTenant",
	"/meridian.tenant.v1.TenantService/UpdateTenantStatus",
	"/meridian.tenant.v1.TenantService/ListTenants",
	"/meridian.tenant.v1.TenantService/ReconcileMigrations",
	"/meridian.tenant.v1.TenantService/GetTenantProvisioningStatus",
}
