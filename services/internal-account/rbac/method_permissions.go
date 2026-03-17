// Package rbac defines RBAC permission maps for the internal-account service.
package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all InternalAccountService gRPC methods.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// Write operations: admin, operator
		"/meridian.internal_account.v1.InternalAccountService/InitiateInternalAccount": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.internal_account.v1.InternalAccountService/UpdateInternalAccount": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.internal_account.v1.InternalAccountService/ControlInternalAccount": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.internal_account.v1.InternalAccountService/CreateValuationFeature": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.internal_account.v1.InternalAccountService/UpdateValuationFeature": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.internal_account.v1.InternalAccountService/EvaluateAssetValuation": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.internal_account.v1.InternalAccountService/InitiateLien": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.internal_account.v1.InternalAccountService/ExecuteLien": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.internal_account.v1.InternalAccountService/TerminateLien": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// Read operations: admin, operator, auditor
		"/meridian.internal_account.v1.InternalAccountService/RetrieveInternalAccount": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.internal_account.v1.InternalAccountService/ListInternalAccounts": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.internal_account.v1.InternalAccountService/GetBalance": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.internal_account.v1.InternalAccountService/GetValuationFeature": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.internal_account.v1.InternalAccountService/ListValuationFeatures": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.internal_account.v1.InternalAccountService/RetrieveLien": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

// ExpectedMethods lists all gRPC methods expected to be registered for this service.
var ExpectedMethods = []string{
	"/meridian.internal_account.v1.InternalAccountService/InitiateInternalAccount",
	"/meridian.internal_account.v1.InternalAccountService/UpdateInternalAccount",
	"/meridian.internal_account.v1.InternalAccountService/ControlInternalAccount",
	"/meridian.internal_account.v1.InternalAccountService/RetrieveInternalAccount",
	"/meridian.internal_account.v1.InternalAccountService/ListInternalAccounts",
	"/meridian.internal_account.v1.InternalAccountService/GetBalance",
	"/meridian.internal_account.v1.InternalAccountService/CreateValuationFeature",
	"/meridian.internal_account.v1.InternalAccountService/UpdateValuationFeature",
	"/meridian.internal_account.v1.InternalAccountService/GetValuationFeature",
	"/meridian.internal_account.v1.InternalAccountService/EvaluateAssetValuation",
	"/meridian.internal_account.v1.InternalAccountService/ListValuationFeatures",
	"/meridian.internal_account.v1.InternalAccountService/InitiateLien",
	"/meridian.internal_account.v1.InternalAccountService/ExecuteLien",
	"/meridian.internal_account.v1.InternalAccountService/TerminateLien",
	"/meridian.internal_account.v1.InternalAccountService/RetrieveLien",
}
