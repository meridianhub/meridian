package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all PartyService gRPC methods.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// Write operations: admin, operator
		"/meridian.party.v1.PartyService/RegisterParty": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.party.v1.PartyService/UpdateParty": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.party.v1.PartyService/ControlParty": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.party.v1.PartyService/UpdateReference": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.party.v1.PartyService/RegisterAssociations": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.party.v1.PartyService/UpdateAssociations": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.party.v1.PartyService/ExchangeDemographics": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.party.v1.PartyService/UpdateDemographics": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.party.v1.PartyService/UpdateBankRelations": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.party.v1.PartyService/AddPaymentMethod": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.party.v1.PartyService/RemovePaymentMethod": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.party.v1.PartyService/SetDefaultPaymentMethod": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.party.v1.PartyService/RegisterPartyType": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.party.v1.PartyService/UpdatePartyType": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},

		// Read operations: admin, operator, auditor
		"/meridian.party.v1.PartyService/ListParties": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.party.v1.PartyService/RetrieveParty": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.party.v1.PartyService/RetrieveReference": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.party.v1.PartyService/RetrieveAssociations": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.party.v1.PartyService/RetrieveDemographics": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.party.v1.PartyService/RetrieveBankRelations": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.party.v1.PartyService/ListParticipants": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.party.v1.PartyService/GetStructuringData": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.party.v1.PartyService/ListPaymentMethods": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.party.v1.PartyService/GetDefaultPaymentMethod": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.party.v1.PartyService/GetPartyType": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.party.v1.PartyService/ListPartyTypes": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

var ExpectedMethods = []string{
	"/meridian.party.v1.PartyService/RegisterParty",
	"/meridian.party.v1.PartyService/ListParties",
	"/meridian.party.v1.PartyService/RetrieveParty",
	"/meridian.party.v1.PartyService/UpdateParty",
	"/meridian.party.v1.PartyService/ControlParty",
	"/meridian.party.v1.PartyService/UpdateReference",
	"/meridian.party.v1.PartyService/RetrieveReference",
	"/meridian.party.v1.PartyService/RegisterAssociations",
	"/meridian.party.v1.PartyService/UpdateAssociations",
	"/meridian.party.v1.PartyService/RetrieveAssociations",
	"/meridian.party.v1.PartyService/ExchangeDemographics",
	"/meridian.party.v1.PartyService/UpdateDemographics",
	"/meridian.party.v1.PartyService/RetrieveDemographics",
	"/meridian.party.v1.PartyService/UpdateBankRelations",
	"/meridian.party.v1.PartyService/RetrieveBankRelations",
	"/meridian.party.v1.PartyService/ListParticipants",
	"/meridian.party.v1.PartyService/GetStructuringData",
	"/meridian.party.v1.PartyService/AddPaymentMethod",
	"/meridian.party.v1.PartyService/RemovePaymentMethod",
	"/meridian.party.v1.PartyService/SetDefaultPaymentMethod",
	"/meridian.party.v1.PartyService/ListPaymentMethods",
	"/meridian.party.v1.PartyService/GetDefaultPaymentMethod",
	"/meridian.party.v1.PartyService/RegisterPartyType",
	"/meridian.party.v1.PartyService/GetPartyType",
	"/meridian.party.v1.PartyService/ListPartyTypes",
	"/meridian.party.v1.PartyService/UpdatePartyType",
}
