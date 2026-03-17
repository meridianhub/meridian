package auth

import "testing"

func TestVerifyMethodCoverage_AllPresent(t *testing.T) {
	cfg := MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/svc/A": {AllowedRoles: []Role{RoleAdmin}},
			"/svc/B": {AllowedRoles: []Role{RoleOperator}},
		},
	}
	expected := []string{"/svc/A", "/svc/B"}

	missing, extra := VerifyMethodCoverage(cfg, expected)
	if len(missing) != 0 {
		t.Errorf("expected no missing, got %v", missing)
	}
	if len(extra) != 0 {
		t.Errorf("expected no extra, got %v", extra)
	}
}

func TestVerifyMethodCoverage_Missing(t *testing.T) {
	cfg := MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/svc/A": {AllowedRoles: []Role{RoleAdmin}},
		},
	}
	expected := []string{"/svc/A", "/svc/B"}

	missing, extra := VerifyMethodCoverage(cfg, expected)
	if len(missing) != 1 || missing[0] != "/svc/B" {
		t.Errorf("expected missing [/svc/B], got %v", missing)
	}
	if len(extra) != 0 {
		t.Errorf("expected no extra, got %v", extra)
	}
}

func TestVerifyMethodCoverage_Extra(t *testing.T) {
	cfg := MethodRBACConfig{
		Permissions: map[string]MethodPermission{
			"/svc/A": {AllowedRoles: []Role{RoleAdmin}},
			"/svc/C": {AllowedRoles: []Role{RoleAdmin}},
		},
	}
	expected := []string{"/svc/A"}

	missing, extra := VerifyMethodCoverage(cfg, expected)
	if len(missing) != 0 {
		t.Errorf("expected no missing, got %v", missing)
	}
	if len(extra) != 1 || extra[0] != "/svc/C" {
		t.Errorf("expected extra [/svc/C], got %v", extra)
	}
}
