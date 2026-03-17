package rbac_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/identity/rbac"
	"github.com/meridianhub/meridian/shared/platform/auth"
)

func TestMethodPermissions_CompleteCoverage(t *testing.T) {
	missing, extra := auth.VerifyMethodCoverage(rbac.MethodPermissions, rbac.ExpectedMethods)

	for _, m := range missing {
		t.Errorf("proto method not configured in RBAC map (fail-closed violation): %s", m)
	}
	for _, m := range extra {
		t.Errorf("RBAC map contains method not in proto definition: %s", m)
	}
}

func TestMethodPermissions_AllEntriesHaveRolesOrPublic(t *testing.T) {
	for method, perm := range rbac.MethodPermissions.Permissions {
		if perm.Public {
			// Public methods intentionally have no roles (pre-authentication flows).
			if len(perm.AllowedRoles) > 0 {
				t.Errorf("public method %s should not have AllowedRoles", method)
			}
			continue
		}
		if len(perm.AllowedRoles) == 0 && perm.ResourceType == "" {
			t.Errorf("method %s has no AllowedRoles, no ResourceType, and is not Public (would always deny)", method)
		}
	}
}

func TestMethodPermissions_PublicMethods(t *testing.T) {
	publicMethods := []string{
		"/meridian.identity.v1.IdentityService/Authenticate",
		"/meridian.identity.v1.IdentityService/RequestPasswordReset",
		"/meridian.identity.v1.IdentityService/CompletePasswordReset",
		"/meridian.identity.v1.IdentityService/AcceptInvitation",
	}
	for _, method := range publicMethods {
		perm, ok := rbac.MethodPermissions.Permissions[method]
		if !ok {
			t.Errorf("expected public method %s in RBAC map", method)
			continue
		}
		if !perm.Public {
			t.Errorf("method %s should be marked Public", method)
		}
	}
}

func TestMethodPermissions_FailClosed(t *testing.T) {
	if rbac.MethodPermissions.AllowUnmapped {
		t.Error("AllowUnmapped must be false for fail-closed guarantee")
	}
}
