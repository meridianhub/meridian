package rbac

import "github.com/meridianhub/meridian/shared/platform/auth"

// MethodPermissions defines RBAC permissions for all ForecastingService gRPC methods.
var MethodPermissions = auth.MethodRBACConfig{
	Permissions: map[string]auth.MethodPermission{
		// Execute operations: admin, operator
		"/meridian.forecasting.v1.ForecastingService/ComputeForwardCurve": {
			AllowedRoles: []auth.Role{auth.RoleAdmin, auth.RoleOperator},
		},
	},
}

var ExpectedMethods = []string{
	"/meridian.forecasting.v1.ForecastingService/ComputeForwardCurve",
}
