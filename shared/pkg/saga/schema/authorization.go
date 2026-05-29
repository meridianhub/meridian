package schema

import (
	"fmt"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// authorizeHandlerInvocation checks RBAC authorization before handler invocation.
//
// Fail-safe rules:
//   - Handlers without RBAC metadata (both empty): allow (backward compatibility)
//   - Partial RBAC metadata (one set, other empty): deny (fail-closed)
//   - Sagas without Claims (system-initiated): allow
//   - Claims present + both RBAC fields set: check that Claims has the required scope
//     formatted as "resource_type:permission" (e.g., "payment_order:write")
func authorizeHandlerInvocation(ctx *saga.StarlarkContext, handlerDef *HandlerDef, fullName string) error {
	// No RBAC metadata declared: backward compatibility, allow
	if handlerDef.ResourceType == "" && handlerDef.RequiredPermission == "" {
		return nil
	}

	// Partial RBAC metadata: fail closed
	if handlerDef.ResourceType == "" || handlerDef.RequiredPermission == "" {
		return fmt.Errorf(
			"%w: handler %s must declare both resource_type and required_permission",
			ErrHandlerAuthorizationDenied,
			fullName,
		)
	}

	// No Claims on context: system-initiated saga, allow
	if ctx.Claims == nil {
		return nil
	}

	// Build the required scope string: "resource_type:permission"
	requiredScope := handlerDef.ResourceType + ":" + handlerDef.RequiredPermission

	// Check if the user has the required scope
	if ctx.Claims.HasScope(requiredScope) {
		return nil
	}

	// Also check role-based access: "resource_type:permission" as a role
	if ctx.Claims.HasRole(requiredScope) {
		return nil
	}

	return fmt.Errorf("%w: handler %s requires permission %q via scope or role", ErrHandlerAuthorizationDenied, fullName, requiredScope)
}
