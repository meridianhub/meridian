// Package rbac defines RBAC permission maps for the position-keeping service.
package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all PositionKeepingService gRPC methods.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// Write/execute operations: admin, operator
		"/meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLog": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLogBatch": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/InitiateWithOpeningBalance": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/UpdateFinancialPositionLog": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/BulkImportTransactions": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/RecordMeasurement": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/UpdatePosition": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/MergePositions": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/RecordReservation": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/ReleaseReservation": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// Read operations: admin, operator, auditor
		"/meridian.position_keeping.v1.PositionKeepingService/RetrieveFinancialPositionLog": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/ListFinancialPositionLogs": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/GetAccountBalance": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/GetAccountBalances": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.position_keeping.v1.PositionKeepingService/GetProjectedBalance": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

// ExpectedMethods lists all gRPC methods expected to be registered for this service.
var ExpectedMethods = []string{
	"/meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLog",
	"/meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLogBatch",
	"/meridian.position_keeping.v1.PositionKeepingService/InitiateWithOpeningBalance",
	"/meridian.position_keeping.v1.PositionKeepingService/UpdateFinancialPositionLog",
	"/meridian.position_keeping.v1.PositionKeepingService/RetrieveFinancialPositionLog",
	"/meridian.position_keeping.v1.PositionKeepingService/BulkImportTransactions",
	"/meridian.position_keeping.v1.PositionKeepingService/ListFinancialPositionLogs",
	"/meridian.position_keeping.v1.PositionKeepingService/RecordMeasurement",
	"/meridian.position_keeping.v1.PositionKeepingService/GetAccountBalance",
	"/meridian.position_keeping.v1.PositionKeepingService/GetAccountBalances",
	"/meridian.position_keeping.v1.PositionKeepingService/UpdatePosition",
	"/meridian.position_keeping.v1.PositionKeepingService/MergePositions",
	"/meridian.position_keeping.v1.PositionKeepingService/RecordReservation",
	"/meridian.position_keeping.v1.PositionKeepingService/ReleaseReservation",
	"/meridian.position_keeping.v1.PositionKeepingService/GetProjectedBalance",
}
