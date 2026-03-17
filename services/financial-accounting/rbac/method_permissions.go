package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all FinancialAccountingService gRPC methods.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// Write operations: admin, operator
		"/meridian.financial_accounting.v1.FinancialAccountingService/InitiateFinancialBookingLog": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.financial_accounting.v1.FinancialAccountingService/UpdateFinancialBookingLog": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.financial_accounting.v1.FinancialAccountingService/CaptureLedgerPosting": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.financial_accounting.v1.FinancialAccountingService/UpdateLedgerPosting": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.financial_accounting.v1.FinancialAccountingService/ControlFinancialBookingLog": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},

		// Read operations: admin, operator, auditor
		"/meridian.financial_accounting.v1.FinancialAccountingService/RetrieveFinancialBookingLog": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.financial_accounting.v1.FinancialAccountingService/ListFinancialBookingLogs": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.financial_accounting.v1.FinancialAccountingService/RetrieveLedgerPosting": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.financial_accounting.v1.FinancialAccountingService/ListLedgerPostings": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

var ExpectedMethods = []string{
	"/meridian.financial_accounting.v1.FinancialAccountingService/InitiateFinancialBookingLog",
	"/meridian.financial_accounting.v1.FinancialAccountingService/UpdateFinancialBookingLog",
	"/meridian.financial_accounting.v1.FinancialAccountingService/RetrieveFinancialBookingLog",
	"/meridian.financial_accounting.v1.FinancialAccountingService/ListFinancialBookingLogs",
	"/meridian.financial_accounting.v1.FinancialAccountingService/CaptureLedgerPosting",
	"/meridian.financial_accounting.v1.FinancialAccountingService/UpdateLedgerPosting",
	"/meridian.financial_accounting.v1.FinancialAccountingService/RetrieveLedgerPosting",
	"/meridian.financial_accounting.v1.FinancialAccountingService/ListLedgerPostings",
	"/meridian.financial_accounting.v1.FinancialAccountingService/ControlFinancialBookingLog",
}
