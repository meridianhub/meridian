package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all AccountReconciliationService gRPC methods.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// Write/execute operations: admin, operator
		"/meridian.reconciliation.v1.AccountReconciliationService/InitiateAccountReconciliation": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.reconciliation.v1.AccountReconciliationService/ExecuteAccountReconciliation": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.reconciliation.v1.AccountReconciliationService/ControlAccountReconciliation": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.reconciliation.v1.AccountReconciliationService/AssertBalance": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.reconciliation.v1.AccountReconciliationService/InitiateDispute": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.reconciliation.v1.AccountReconciliationService/ControlDispute": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.reconciliation.v1.AccountReconciliationService/UpdateDispute": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// Read operations: admin, operator, auditor
		"/meridian.reconciliation.v1.AccountReconciliationService/ListAccountReconciliations": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reconciliation.v1.AccountReconciliationService/RetrieveAccountReconciliation": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reconciliation.v1.AccountReconciliationService/ListReconciliationResults": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reconciliation.v1.AccountReconciliationService/RetrieveDispute": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reconciliation.v1.AccountReconciliationService/ListDisputes": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reconciliation.v1.AccountReconciliationService/ListBalanceAssertions": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

var ExpectedMethods = []string{
	"/meridian.reconciliation.v1.AccountReconciliationService/InitiateAccountReconciliation",
	"/meridian.reconciliation.v1.AccountReconciliationService/ExecuteAccountReconciliation",
	"/meridian.reconciliation.v1.AccountReconciliationService/ListAccountReconciliations",
	"/meridian.reconciliation.v1.AccountReconciliationService/RetrieveAccountReconciliation",
	"/meridian.reconciliation.v1.AccountReconciliationService/ControlAccountReconciliation",
	"/meridian.reconciliation.v1.AccountReconciliationService/ListReconciliationResults",
	"/meridian.reconciliation.v1.AccountReconciliationService/AssertBalance",
	"/meridian.reconciliation.v1.AccountReconciliationService/InitiateDispute",
	"/meridian.reconciliation.v1.AccountReconciliationService/ControlDispute",
	"/meridian.reconciliation.v1.AccountReconciliationService/RetrieveDispute",
	"/meridian.reconciliation.v1.AccountReconciliationService/ListDisputes",
	"/meridian.reconciliation.v1.AccountReconciliationService/UpdateDispute",
	"/meridian.reconciliation.v1.AccountReconciliationService/ListBalanceAssertions",
}
