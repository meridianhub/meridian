// Package rbac defines RBAC permission maps for the identity service.
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

		// Pre-authentication flows: callers do not have a JWT yet, so these must
		// bypass the claims check entirely. Public: true causes the interceptor to
		// skip authentication and allow the request through.
		"/meridian.identity.v1.IdentityService/Authenticate": {
			Public: true,
		},
		"/meridian.identity.v1.IdentityService/RequestPasswordReset": {
			Public: true,
		},
		"/meridian.identity.v1.IdentityService/CompletePasswordReset": {
			Public: true,
		},
		"/meridian.identity.v1.IdentityService/AcceptInvitation": {
			Public: true,
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

// ExpectedMethods lists all gRPC methods expected to be registered for this service.
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
