// Package rbac defines RBAC permission maps for the operational-gateway service.
package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all operational-gateway gRPC services:
// OperationalGatewayService, ProviderConnectionService, and InstructionRouteService.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// OperationalGatewayService - Write: admin, operator
		"/meridian.operational_gateway.v1.OperationalGatewayService/DispatchInstruction": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.operational_gateway.v1.OperationalGatewayService/CancelInstruction": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.operational_gateway.v1.OperationalGatewayService/ProcessCallback": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.operational_gateway.v1.OperationalGatewayService/UpsertConnection": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.operational_gateway.v1.OperationalGatewayService/TestConnection": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.operational_gateway.v1.OperationalGatewayService/UpsertRoute": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// OperationalGatewayService - Read: admin, operator, auditor
		"/meridian.operational_gateway.v1.OperationalGatewayService/GetInstruction": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.operational_gateway.v1.OperationalGatewayService/ListInstructions": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.operational_gateway.v1.OperationalGatewayService/GetConnection": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.operational_gateway.v1.OperationalGatewayService/ListConnections": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.operational_gateway.v1.OperationalGatewayService/GetRoute": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.operational_gateway.v1.OperationalGatewayService/ListRoutes": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// ProviderConnectionService - Write: admin, operator
		"/meridian.operational_gateway.v1.ProviderConnectionService/UpsertConnection": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.operational_gateway.v1.ProviderConnectionService/TestConnection": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// ProviderConnectionService - Read: admin, operator, auditor
		"/meridian.operational_gateway.v1.ProviderConnectionService/GetConnection": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.operational_gateway.v1.ProviderConnectionService/ListConnections": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// InstructionRouteService - Write: admin, operator
		"/meridian.operational_gateway.v1.InstructionRouteService/UpsertRoute": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// InstructionRouteService - Read: admin, operator, auditor
		"/meridian.operational_gateway.v1.InstructionRouteService/GetRoute": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.operational_gateway.v1.InstructionRouteService/ListRoutes": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

// ExpectedMethods lists all gRPC methods expected to be registered for this service.
var ExpectedMethods = []string{
	// OperationalGatewayService
	"/meridian.operational_gateway.v1.OperationalGatewayService/DispatchInstruction",
	"/meridian.operational_gateway.v1.OperationalGatewayService/CancelInstruction",
	"/meridian.operational_gateway.v1.OperationalGatewayService/GetInstruction",
	"/meridian.operational_gateway.v1.OperationalGatewayService/ListInstructions",
	"/meridian.operational_gateway.v1.OperationalGatewayService/ProcessCallback",
	"/meridian.operational_gateway.v1.OperationalGatewayService/UpsertConnection",
	"/meridian.operational_gateway.v1.OperationalGatewayService/GetConnection",
	"/meridian.operational_gateway.v1.OperationalGatewayService/ListConnections",
	"/meridian.operational_gateway.v1.OperationalGatewayService/TestConnection",
	"/meridian.operational_gateway.v1.OperationalGatewayService/UpsertRoute",
	"/meridian.operational_gateway.v1.OperationalGatewayService/GetRoute",
	"/meridian.operational_gateway.v1.OperationalGatewayService/ListRoutes",
	// ProviderConnectionService
	"/meridian.operational_gateway.v1.ProviderConnectionService/UpsertConnection",
	"/meridian.operational_gateway.v1.ProviderConnectionService/GetConnection",
	"/meridian.operational_gateway.v1.ProviderConnectionService/ListConnections",
	"/meridian.operational_gateway.v1.ProviderConnectionService/TestConnection",
	// InstructionRouteService
	"/meridian.operational_gateway.v1.InstructionRouteService/UpsertRoute",
	"/meridian.operational_gateway.v1.InstructionRouteService/GetRoute",
	"/meridian.operational_gateway.v1.InstructionRouteService/ListRoutes",
}
