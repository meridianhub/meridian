// Package rbac defines RBAC permission maps for the control-plane service.
package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all control-plane gRPC services.
//
// RoleService is intentionally absent from AllowedRoles across all services.
// Service-to-service calls are internal gRPC calls that do not pass through the
// MethodRBACInterceptor. Internal calls are authenticated via a separate mechanism
// (e.g., mTLS or internal auth middleware) and bypass per-method RBAC entirely.
//
// Services covered:
// ApplyManifestService, ManifestHistoryService, SagaExecutionService,
// EconomyGeneratorService, AuthService, CausationVisualizerService, BalanceSheetService,
// SagaAdminService, SagaRegistryService, MappingService, and HealthService.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// ApplyManifestService - admin only (deploys configuration)
		"/meridian.control_plane.v1.ApplyManifestService/ApplyManifest": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.control_plane.v1.ApplyManifestService/ApplyResource": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},

		// ManifestHistoryService - read: all; write (reconcile): admin
		"/meridian.control_plane.v1.ManifestHistoryService/GetCurrentManifest": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.control_plane.v1.ManifestHistoryService/GetManifestVersion": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.control_plane.v1.ManifestHistoryService/ListManifestVersions": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.control_plane.v1.ManifestHistoryService/DiffManifestVersions": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.control_plane.v1.ManifestHistoryService/ExportManifest": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.control_plane.v1.ManifestHistoryService/ReconcileManifest": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},

		// SagaExecutionService - admin, operator
		"/meridian.control_plane.v1.SagaExecutionService/ExecuteSaga": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},

		// EconomyGeneratorService - admin, operator
		"/meridian.control_plane.v1.EconomyGeneratorService/GenerateManifest": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.control_plane.v1.EconomyGeneratorService/GetGenerationContext": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// AuthService - admin only
		"/meridian.control_plane.v1.AuthService/ValidateAPIKey": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// CausationVisualizerService - read: all
		"/meridian.control_plane.v1.CausationVisualizerService/GetCausationTreeForPosition": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.control_plane.v1.CausationVisualizerService/GetCausationTreeForTransaction": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.control_plane.v1.CausationVisualizerService/GetCausationTreeForEvent": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// BalanceSheetService - read: all
		"/meridian.control_plane.v1.BalanceSheetService/GetBalanceSheet": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.control_plane.v1.BalanceSheetService/GetPositionDetails": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.control_plane.v1.BalanceSheetService/ExportBalanceSheetCSV": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// SagaAdminService - read: all
		"/meridian.saga.v1.SagaAdminService/GetCausationTree": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// SagaRegistryService - Write: admin, operator; Read: all
		"/meridian.saga.v1.SagaRegistryService/CreateSagaDraft": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.saga.v1.SagaRegistryService/UpdateSagaDefinition": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.saga.v1.SagaRegistryService/ActivateSaga": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.saga.v1.SagaRegistryService/DeprecateSaga": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.saga.v1.SagaRegistryService/ValidateSagaDraft": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.saga.v1.SagaRegistryService/ValidateSaga": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.saga.v1.SagaRegistryService/AnalyzeDeprecationImpact": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.saga.v1.SagaRegistryService/GetSaga": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.saga.v1.SagaRegistryService/GetActiveSaga": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.saga.v1.SagaRegistryService/ListSagas": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.saga.v1.SagaRegistryService/DescribeHandlers": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// MappingService - Write: admin, operator; Read: all
		"/meridian.mapping.v1.MappingService/CreateMapping": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.mapping.v1.MappingService/UpdateMapping": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.mapping.v1.MappingService/DeleteMapping": {
			AllowedRoles: []auth.Role{auth.RoleAdmin},
		},
		"/meridian.mapping.v1.MappingService/DryRunMapping": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
		"/meridian.mapping.v1.MappingService/GetMapping": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
		"/meridian.mapping.v1.MappingService/ListMappings": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// AuditService - read: all (auditors need this most)
		"/meridian.audit.v1.AuditService/ListAuditEntries": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},

		// HealthService - all roles (infrastructure check)
		"/meridian.common.v1.HealthService/Check": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator, auth.RoleAuditor},
		},
	},
}

// ExpectedMethods lists all gRPC methods expected to be registered for this service.
var ExpectedMethods = []string{
	// ApplyManifestService
	"/meridian.control_plane.v1.ApplyManifestService/ApplyManifest",
	"/meridian.control_plane.v1.ApplyManifestService/ApplyResource",
	// ManifestHistoryService
	"/meridian.control_plane.v1.ManifestHistoryService/GetCurrentManifest",
	"/meridian.control_plane.v1.ManifestHistoryService/GetManifestVersion",
	"/meridian.control_plane.v1.ManifestHistoryService/ListManifestVersions",
	"/meridian.control_plane.v1.ManifestHistoryService/DiffManifestVersions",
	"/meridian.control_plane.v1.ManifestHistoryService/ExportManifest",
	"/meridian.control_plane.v1.ManifestHistoryService/ReconcileManifest",
	// SagaExecutionService
	"/meridian.control_plane.v1.SagaExecutionService/ExecuteSaga",
	// EconomyGeneratorService
	"/meridian.control_plane.v1.EconomyGeneratorService/GenerateManifest",
	"/meridian.control_plane.v1.EconomyGeneratorService/GetGenerationContext",
	// AuthService
	"/meridian.control_plane.v1.AuthService/ValidateAPIKey",
	// CausationVisualizerService
	"/meridian.control_plane.v1.CausationVisualizerService/GetCausationTreeForPosition",
	"/meridian.control_plane.v1.CausationVisualizerService/GetCausationTreeForTransaction",
	"/meridian.control_plane.v1.CausationVisualizerService/GetCausationTreeForEvent",
	// BalanceSheetService
	"/meridian.control_plane.v1.BalanceSheetService/GetBalanceSheet",
	"/meridian.control_plane.v1.BalanceSheetService/GetPositionDetails",
	"/meridian.control_plane.v1.BalanceSheetService/ExportBalanceSheetCSV",
	// SagaAdminService
	"/meridian.saga.v1.SagaAdminService/GetCausationTree",
	// SagaRegistryService
	"/meridian.saga.v1.SagaRegistryService/CreateSagaDraft",
	"/meridian.saga.v1.SagaRegistryService/UpdateSagaDefinition",
	"/meridian.saga.v1.SagaRegistryService/ActivateSaga",
	"/meridian.saga.v1.SagaRegistryService/DeprecateSaga",
	"/meridian.saga.v1.SagaRegistryService/GetSaga",
	"/meridian.saga.v1.SagaRegistryService/GetActiveSaga",
	"/meridian.saga.v1.SagaRegistryService/ListSagas",
	"/meridian.saga.v1.SagaRegistryService/ValidateSagaDraft",
	"/meridian.saga.v1.SagaRegistryService/ValidateSaga",
	"/meridian.saga.v1.SagaRegistryService/AnalyzeDeprecationImpact",
	"/meridian.saga.v1.SagaRegistryService/DescribeHandlers",
	// MappingService
	"/meridian.mapping.v1.MappingService/CreateMapping",
	"/meridian.mapping.v1.MappingService/GetMapping",
	"/meridian.mapping.v1.MappingService/ListMappings",
	"/meridian.mapping.v1.MappingService/UpdateMapping",
	"/meridian.mapping.v1.MappingService/DeleteMapping",
	"/meridian.mapping.v1.MappingService/DryRunMapping",
	// AuditService
	"/meridian.audit.v1.AuditService/ListAuditEntries",
	// HealthService
	"/meridian.common.v1.HealthService/Check",
}
