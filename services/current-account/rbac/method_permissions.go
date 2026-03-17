package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all CurrentAccountService gRPC methods.
// Fail-closed: any method not listed here is denied by default.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// Write operations: admin, operator
		"/meridian.current_account.v1.CurrentAccountService/InitiateCurrentAccount": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.current_account.v1.CurrentAccountService/ExecuteDeposit": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.current_account.v1.CurrentAccountService/InitiateWithdrawal": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.current_account.v1.CurrentAccountService/ExecuteWithdrawal": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.current_account.v1.CurrentAccountService/UpdateWithdrawal": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.current_account.v1.CurrentAccountService/UpdateCurrentAccount": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.current_account.v1.CurrentAccountService/ControlCurrentAccount": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.current_account.v1.CurrentAccountService/InitiateLien": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.current_account.v1.CurrentAccountService/ExecuteLien": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.current_account.v1.CurrentAccountService/TerminateLien": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.current_account.v1.CurrentAccountService/CreateValuationFeature": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.current_account.v1.CurrentAccountService/UpdateValuationFeature": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.current_account.v1.CurrentAccountService/EvaluateAssetValuation": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// Read operations: admin, operator, auditor
		"/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.current_account.v1.CurrentAccountService/RetrieveWithdrawal": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.current_account.v1.CurrentAccountService/RetrieveLien": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.current_account.v1.CurrentAccountService/GetActiveAmountBlocks": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.current_account.v1.CurrentAccountService/GetValuationFeature": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.current_account.v1.CurrentAccountService/ListValuationFeatures": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

// ExpectedMethods lists all gRPC methods defined in the CurrentAccountService proto.
// Used by tests to verify complete coverage (fail-closed guarantee).
var ExpectedMethods = []string{
	"/meridian.current_account.v1.CurrentAccountService/InitiateCurrentAccount",
	"/meridian.current_account.v1.CurrentAccountService/ListCurrentAccounts",
	"/meridian.current_account.v1.CurrentAccountService/ExecuteDeposit",
	"/meridian.current_account.v1.CurrentAccountService/InitiateWithdrawal",
	"/meridian.current_account.v1.CurrentAccountService/ExecuteWithdrawal",
	"/meridian.current_account.v1.CurrentAccountService/UpdateWithdrawal",
	"/meridian.current_account.v1.CurrentAccountService/RetrieveWithdrawal",
	"/meridian.current_account.v1.CurrentAccountService/RetrieveCurrentAccount",
	"/meridian.current_account.v1.CurrentAccountService/InitiateLien",
	"/meridian.current_account.v1.CurrentAccountService/ExecuteLien",
	"/meridian.current_account.v1.CurrentAccountService/TerminateLien",
	"/meridian.current_account.v1.CurrentAccountService/RetrieveLien",
	"/meridian.current_account.v1.CurrentAccountService/GetActiveAmountBlocks",
	"/meridian.current_account.v1.CurrentAccountService/UpdateCurrentAccount",
	"/meridian.current_account.v1.CurrentAccountService/ControlCurrentAccount",
	"/meridian.current_account.v1.CurrentAccountService/CreateValuationFeature",
	"/meridian.current_account.v1.CurrentAccountService/UpdateValuationFeature",
	"/meridian.current_account.v1.CurrentAccountService/GetValuationFeature",
	"/meridian.current_account.v1.CurrentAccountService/ListValuationFeatures",
	"/meridian.current_account.v1.CurrentAccountService/EvaluateAssetValuation",
}
