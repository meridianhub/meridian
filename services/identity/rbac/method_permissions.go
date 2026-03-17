package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all IdentityService gRPC methods.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// Admin-only operations (user management, role assignment)
		"/meridian.identity.v1.IdentityService/CreateIdentity": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.identity.v1.IdentityService/GrantRole": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.identity.v1.IdentityService/RevokeRole": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.identity.v1.IdentityService/InviteUser": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.identity.v1.IdentityService/SuspendIdentity": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.identity.v1.IdentityService/ReactivateIdentity": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.identity.v1.IdentityService/SetPassword": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},

		// Write operations: admin, operator
		"/meridian.identity.v1.IdentityService/UpdateIdentity": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.identity.v1.IdentityService/ChangePassword": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// Public operations (authentication flow, no role required - handled by auth middleware)
		"/meridian.identity.v1.IdentityService/Authenticate": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.identity.v1.IdentityService/RequestPasswordReset": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.identity.v1.IdentityService/CompletePasswordReset": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.identity.v1.IdentityService/AcceptInvitation": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// Read operations: admin, operator, auditor
		"/meridian.identity.v1.IdentityService/RetrieveIdentity": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.identity.v1.IdentityService/ListIdentities": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.identity.v1.IdentityService/ListRoleAssignments": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

var ExpectedMethods = []string{
	"/meridian.identity.v1.IdentityService/CreateIdentity",
	"/meridian.identity.v1.IdentityService/RetrieveIdentity",
	"/meridian.identity.v1.IdentityService/UpdateIdentity",
	"/meridian.identity.v1.IdentityService/ListIdentities",
	"/meridian.identity.v1.IdentityService/Authenticate",
	"/meridian.identity.v1.IdentityService/SetPassword",
	"/meridian.identity.v1.IdentityService/ChangePassword",
	"/meridian.identity.v1.IdentityService/RequestPasswordReset",
	"/meridian.identity.v1.IdentityService/CompletePasswordReset",
	"/meridian.identity.v1.IdentityService/GrantRole",
	"/meridian.identity.v1.IdentityService/RevokeRole",
	"/meridian.identity.v1.IdentityService/ListRoleAssignments",
	"/meridian.identity.v1.IdentityService/InviteUser",
	"/meridian.identity.v1.IdentityService/AcceptInvitation",
	"/meridian.identity.v1.IdentityService/SuspendIdentity",
	"/meridian.identity.v1.IdentityService/ReactivateIdentity",
}
