package rbac_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/current-account/rbac"
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

func TestMethodPermissions_AllEntriesHaveRoles(t *testing.T) {
	for method, perm := range rbac.MethodPermissions.Permissions {
		if len(perm.AllowedRoles) == 0 && perm.ResourceType == "" {
			t.Errorf("method %s has no AllowedRoles and no ResourceType (would always deny)", method)
		}
	}
}

func TestMethodPermissions_FailClosed(t *testing.T) {
	if rbac.MethodPermissions.AllowUnmapped {
		t.Error("AllowUnmapped must be false for fail-closed guarantee")
	}
}
