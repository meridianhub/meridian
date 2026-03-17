// Package rbac defines RBAC permission maps for the reference-data service.
package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all reference-data gRPC services:
// ReferenceDataService, AccountTypeRegistryService, and NodeService.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// ReferenceDataService - Write operations: admin, operator
		"/meridian.reference_data.v1.ReferenceDataService/RegisterInstrument": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.reference_data.v1.ReferenceDataService/UpdateInstrument": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.reference_data.v1.ReferenceDataService/ActivateInstrument": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.reference_data.v1.ReferenceDataService/DeprecateInstrument": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.reference_data.v1.ReferenceDataService/EvaluateInstrument": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// ReferenceDataService - Read operations: admin, operator, auditor
		"/meridian.reference_data.v1.ReferenceDataService/RetrieveInstrument": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reference_data.v1.ReferenceDataService/ListInstruments": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reference_data.v1.ReferenceDataService/GetAttributeSchema": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// AccountTypeRegistryService - Write operations: admin, operator
		"/meridian.reference_data.v1.AccountTypeRegistryService/CreateDraft": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.reference_data.v1.AccountTypeRegistryService/UpdateDefinition": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.reference_data.v1.AccountTypeRegistryService/ActivateAccountType": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.reference_data.v1.AccountTypeRegistryService/DeprecateAccountType": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.reference_data.v1.AccountTypeRegistryService/ValidateProductDefinition": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// AccountTypeRegistryService - Read operations: admin, operator, auditor
		"/meridian.reference_data.v1.AccountTypeRegistryService/GetDefinition": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reference_data.v1.AccountTypeRegistryService/GetActiveDefinition": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reference_data.v1.AccountTypeRegistryService/ListActive": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// NodeService - Write operations: admin, operator
		"/meridian.reference_data.v1.NodeService/CreateNode": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.reference_data.v1.NodeService/UpdateNode": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.reference_data.v1.NodeService/ImportNodes": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// NodeService - Read operations: admin, operator, auditor
		"/meridian.reference_data.v1.NodeService/GetNode": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reference_data.v1.NodeService/GetChildren": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reference_data.v1.NodeService/GetAncestors": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reference_data.v1.NodeService/GetSubtree": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reference_data.v1.NodeService/GetNodeAsAt": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.reference_data.v1.NodeService/GetNodeHistory": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

// ExpectedMethods lists all gRPC methods expected to be registered for this service.
var ExpectedMethods = []string{
	// ReferenceDataService
	"/meridian.reference_data.v1.ReferenceDataService/RegisterInstrument",
	"/meridian.reference_data.v1.ReferenceDataService/UpdateInstrument",
	"/meridian.reference_data.v1.ReferenceDataService/RetrieveInstrument",
	"/meridian.reference_data.v1.ReferenceDataService/ListInstruments",
	"/meridian.reference_data.v1.ReferenceDataService/ActivateInstrument",
	"/meridian.reference_data.v1.ReferenceDataService/DeprecateInstrument",
	"/meridian.reference_data.v1.ReferenceDataService/EvaluateInstrument",
	"/meridian.reference_data.v1.ReferenceDataService/GetAttributeSchema",
	// AccountTypeRegistryService
	"/meridian.reference_data.v1.AccountTypeRegistryService/GetDefinition",
	"/meridian.reference_data.v1.AccountTypeRegistryService/GetActiveDefinition",
	"/meridian.reference_data.v1.AccountTypeRegistryService/ListActive",
	"/meridian.reference_data.v1.AccountTypeRegistryService/CreateDraft",
	"/meridian.reference_data.v1.AccountTypeRegistryService/UpdateDefinition",
	"/meridian.reference_data.v1.AccountTypeRegistryService/ActivateAccountType",
	"/meridian.reference_data.v1.AccountTypeRegistryService/DeprecateAccountType",
	"/meridian.reference_data.v1.AccountTypeRegistryService/ValidateProductDefinition",
	// NodeService
	"/meridian.reference_data.v1.NodeService/CreateNode",
	"/meridian.reference_data.v1.NodeService/UpdateNode",
	"/meridian.reference_data.v1.NodeService/GetNode",
	"/meridian.reference_data.v1.NodeService/GetChildren",
	"/meridian.reference_data.v1.NodeService/GetAncestors",
	"/meridian.reference_data.v1.NodeService/GetSubtree",
	"/meridian.reference_data.v1.NodeService/GetNodeAsAt",
	"/meridian.reference_data.v1.NodeService/GetNodeHistory",
	"/meridian.reference_data.v1.NodeService/ImportNodes",
}
