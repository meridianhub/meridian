// Package rbac defines RBAC permission maps for the market-information service.
package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all MarketInformationService gRPC methods.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// Write operations: admin, operator
		"/meridian.market_information.v1.MarketInformationService/RegisterDataSet": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.market_information.v1.MarketInformationService/UpdateDataSet": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.market_information.v1.MarketInformationService/ActivateDataSet": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.market_information.v1.MarketInformationService/DeprecateDataSet": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.market_information.v1.MarketInformationService/RegisterDataSource": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.market_information.v1.MarketInformationService/UpdateDataSource": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.market_information.v1.MarketInformationService/DeactivateDataSource": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.market_information.v1.MarketInformationService/RecordObservation": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.market_information.v1.MarketInformationService/RecordObservationBatch": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// Read operations: admin, operator, auditor
		"/meridian.market_information.v1.MarketInformationService/RetrieveDataSet": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.market_information.v1.MarketInformationService/ListDataSets": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.market_information.v1.MarketInformationService/ListDataSources": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.market_information.v1.MarketInformationService/RetrieveObservation": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.market_information.v1.MarketInformationService/ListObservations": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

// ExpectedMethods lists all gRPC methods expected to be registered for this service.
var ExpectedMethods = []string{
	"/meridian.market_information.v1.MarketInformationService/RegisterDataSet",
	"/meridian.market_information.v1.MarketInformationService/UpdateDataSet",
	"/meridian.market_information.v1.MarketInformationService/ActivateDataSet",
	"/meridian.market_information.v1.MarketInformationService/DeprecateDataSet",
	"/meridian.market_information.v1.MarketInformationService/RetrieveDataSet",
	"/meridian.market_information.v1.MarketInformationService/ListDataSets",
	"/meridian.market_information.v1.MarketInformationService/RegisterDataSource",
	"/meridian.market_information.v1.MarketInformationService/UpdateDataSource",
	"/meridian.market_information.v1.MarketInformationService/DeactivateDataSource",
	"/meridian.market_information.v1.MarketInformationService/ListDataSources",
	"/meridian.market_information.v1.MarketInformationService/RecordObservation",
	"/meridian.market_information.v1.MarketInformationService/RecordObservationBatch",
	"/meridian.market_information.v1.MarketInformationService/RetrieveObservation",
	"/meridian.market_information.v1.MarketInformationService/ListObservations",
}
