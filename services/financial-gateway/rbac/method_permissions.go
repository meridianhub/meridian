// Package rbac defines RBAC permission maps for the financial-gateway service.
package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all FinancialGatewayService gRPC methods.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// Write/execute operations: admin, operator
		"/meridian.financial_gateway.v1.FinancialGatewayService/DispatchPayment": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.financial_gateway.v1.FinancialGatewayService/DispatchRefund": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.financial_gateway.v1.FinancialGatewayService/CancelPayment": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// Read operations: admin, operator, auditor
		"/meridian.financial_gateway.v1.FinancialGatewayService/GetProviderHealth": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

// ExpectedMethods lists all gRPC methods expected to be registered for this service.
var ExpectedMethods = []string{
	"/meridian.financial_gateway.v1.FinancialGatewayService/DispatchPayment",
	"/meridian.financial_gateway.v1.FinancialGatewayService/DispatchRefund",
	"/meridian.financial_gateway.v1.FinancialGatewayService/CancelPayment",
	"/meridian.financial_gateway.v1.FinancialGatewayService/GetProviderHealth",
}
