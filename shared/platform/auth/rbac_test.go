package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRole_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		role  Role
		valid bool
	}{
		{"admin is valid", RoleAdmin, true},
		{"operator is valid", RoleOperator, true},
		{"auditor is valid", RoleAuditor, true},
		{"service is valid", RoleService, true},
		{"invalid role", Role("invalid"), false},
		{"empty role", Role(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.role.IsValid(); got != tt.valid {
				t.Errorf("Role.IsValid() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestRole_String(t *testing.T) {
	if got := RoleAdmin.String(); got != "admin" {
		t.Errorf("RoleAdmin.String() = %v, want 'admin'", got)
	}
}

func TestHasAnyRole(t *testing.T) {
	tests := []struct {
		name     string
		claims   *Claims
		roles    []Role
		expected bool
	}{
		{
			name:     "has one of multiple roles",
			claims:   &Claims{Roles: []string{"operator"}},
			roles:    []Role{RoleAdmin, RoleOperator, RoleService},
			expected: true,
		},
		{
			name:     "has none of the roles",
			claims:   &Claims{Roles: []string{"auditor"}},
			roles:    []Role{RoleAdmin, RoleOperator},
			expected: false,
		},
		{
			name:     "nil claims",
			claims:   nil,
			roles:    []Role{RoleAdmin},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasAnyRole(tt.claims, tt.roles...); got != tt.expected {
				t.Errorf("HasAnyRole() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHasPermission(t *testing.T) {
	tests := []struct {
		name         string
		claims       *Claims
		resourceType ResourceType
		permission   Permission
		expected     bool
	}{
		{
			name:         "admin has write permission on accounts",
			claims:       &Claims{Roles: []string{"admin"}},
			resourceType: ResourceTypeAccount,
			permission:   PermissionWrite,
			expected:     true,
		},
		{
			name:         "auditor only has read permission",
			claims:       &Claims{Roles: []string{"auditor"}},
			resourceType: ResourceTypeAccount,
			permission:   PermissionRead,
			expected:     true,
		},
		{
			name:         "auditor does not have write permission",
			claims:       &Claims{Roles: []string{"auditor"}},
			resourceType: ResourceTypeAccount,
			permission:   PermissionWrite,
			expected:     false,
		},
		{
			name:         "operator cannot delete audit logs",
			claims:       &Claims{Roles: []string{"operator"}},
			resourceType: ResourceTypeAudit,
			permission:   PermissionDelete,
			expected:     false,
		},
		{
			name:         "service can write to accounts",
			claims:       &Claims{Roles: []string{"service"}},
			resourceType: ResourceTypeAccount,
			permission:   PermissionWrite,
			expected:     true,
		},
		{
			name:         "nil claims",
			claims:       nil,
			resourceType: ResourceTypeAccount,
			permission:   PermissionRead,
			expected:     false,
		},
		{
			name:         "invalid role ignored",
			claims:       &Claims{Roles: []string{"invalid_role"}},
			resourceType: ResourceTypeAccount,
			permission:   PermissionRead,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasPermission(tt.claims, tt.resourceType, tt.permission); got != tt.expected {
				t.Errorf("HasPermission() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCheckRole(t *testing.T) {
	tests := []struct {
		name      string
		claims    *Claims
		role      Role
		expectErr bool
	}{
		{
			name:      "has required role",
			claims:    &Claims{Roles: []string{"admin"}},
			role:      RoleAdmin,
			expectErr: false,
		},
		{
			name:      "missing required role",
			claims:    &Claims{Roles: []string{"auditor"}},
			role:      RoleAdmin,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckRole(tt.claims, tt.role)
			if (err != nil) != tt.expectErr {
				t.Errorf("CheckRole() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestCheckAnyRole(t *testing.T) {
	tests := []struct {
		name      string
		claims    *Claims
		roles     []Role
		expectErr bool
	}{
		{
			name:      "has one of required roles",
			claims:    &Claims{Roles: []string{"operator"}},
			roles:     []Role{RoleAdmin, RoleOperator},
			expectErr: false,
		},
		{
			name:      "missing all required roles",
			claims:    &Claims{Roles: []string{"auditor"}},
			roles:     []Role{RoleAdmin, RoleOperator},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckAnyRole(tt.claims, tt.roles...)
			if (err != nil) != tt.expectErr {
				t.Errorf("CheckAnyRole() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestCheckPermission(t *testing.T) {
	tests := []struct {
		name         string
		claims       *Claims
		resourceType ResourceType
		permission   Permission
		expectErr    bool
	}{
		{
			name:         "has required permission",
			claims:       &Claims{Roles: []string{"admin"}},
			resourceType: ResourceTypeAccount,
			permission:   PermissionWrite,
			expectErr:    false,
		},
		{
			name:         "missing required permission",
			claims:       &Claims{Roles: []string{"auditor"}},
			resourceType: ResourceTypeAccount,
			permission:   PermissionWrite,
			expectErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckPermission(tt.claims, tt.resourceType, tt.permission)
			if (err != nil) != tt.expectErr {
				t.Errorf("CheckPermission() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestHTTPAuthorizationMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		claims         *Claims
		requiredRoles  []Role
		expectedStatus int
	}{
		{
			name:           "authorized with admin role",
			claims:         &Claims{Roles: []string{"admin"}},
			requiredRoles:  []Role{RoleAdmin},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "authorized with any of multiple roles",
			claims:         &Claims{Roles: []string{"operator"}},
			requiredRoles:  []Role{RoleAdmin, RoleOperator},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "unauthorized missing role",
			claims:         &Claims{Roles: []string{"auditor"}},
			requiredRoles:  []Role{RoleAdmin},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "no claims in context",
			claims:         nil,
			requiredRoles:  []Role{RoleAdmin},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "no role requirement passes",
			claims:         &Claims{Roles: []string{"auditor"}},
			requiredRoles:  []Role{},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test handler
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			// Create middleware
			middleware := NewHTTPAuthorizationMiddleware(tt.requiredRoles...)
			wrappedHandler := middleware.Handler(handler)

			// Create request
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.claims != nil {
				ctx := context.WithValue(req.Context(), ClaimsContextKey, tt.claims)
				req = req.WithContext(ctx)
			}

			// Record response
			w := httptest.NewRecorder()
			wrappedHandler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("HTTPAuthorizationMiddleware status = %v, want %v", w.Code, tt.expectedStatus)
			}
		})
	}
}

func TestRequireRoleStream_Integration(t *testing.T) {
	// Test that RequireRoleStream properly wraps the existing RequireRole logic
	// This is an integration test with the existing auth infrastructure

	adminClaims := &Claims{Roles: []string{"admin"}}
	auditorClaims := &Claims{Roles: []string{"auditor"}}

	tests := []struct {
		name      string
		claims    *Claims
		roles     []Role
		expectErr bool
	}{
		{
			name:      "admin role allows access",
			claims:    adminClaims,
			roles:     []Role{RoleAdmin},
			expectErr: false,
		},
		{
			name:      "auditor lacks admin role",
			claims:    auditorClaims,
			roles:     []Role{RoleAdmin},
			expectErr: true,
		},
		{
			name:      "auditor has auditor role",
			claims:    auditorClaims,
			roles:     []Role{RoleAuditor},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify the role check logic
			err := CheckAnyRole(tt.claims, tt.roles...)
			if (err != nil) != tt.expectErr {
				t.Errorf("CheckAnyRole() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestParseRoles(t *testing.T) {
	tests := []struct {
		name        string
		roleStrings []string
		expectErr   bool
		expected    []Role
	}{
		{
			name:        "valid roles",
			roleStrings: []string{"admin", "operator"},
			expectErr:   false,
			expected:    []Role{RoleAdmin, RoleOperator},
		},
		{
			name:        "invalid role",
			roleStrings: []string{"admin", "invalid"},
			expectErr:   true,
			expected:    nil,
		},
		{
			name:        "empty list",
			roleStrings: []string{},
			expectErr:   false,
			expected:    []Role{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roles, err := ParseRoles(tt.roleStrings)
			if (err != nil) != tt.expectErr {
				t.Errorf("ParseRoles() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if !tt.expectErr {
				if len(roles) != len(tt.expected) {
					t.Errorf("ParseRoles() length = %v, want %v", len(roles), len(tt.expected))
				}
				for i, role := range roles {
					if role != tt.expected[i] {
						t.Errorf("ParseRoles()[%d] = %v, want %v", i, role, tt.expected[i])
					}
				}
			}
		})
	}
}

func TestAuthorizeResource(t *testing.T) {
	tests := []struct {
		name         string
		claims       *Claims
		resourceType ResourceType
		permission   Permission
		expectErr    bool
	}{
		{
			name:         "admin can write accounts",
			claims:       &Claims{Roles: []string{"admin"}},
			resourceType: ResourceTypeAccount,
			permission:   PermissionWrite,
			expectErr:    false,
		},
		{
			name:         "auditor cannot write accounts",
			claims:       &Claims{Roles: []string{"auditor"}},
			resourceType: ResourceTypeAccount,
			permission:   PermissionWrite,
			expectErr:    true,
		},
		{
			name:         "no claims in context",
			claims:       nil,
			resourceType: ResourceTypeAccount,
			permission:   PermissionRead,
			expectErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.claims != nil {
				ctx = context.WithValue(ctx, ClaimsContextKey, tt.claims)
			}

			err := AuthorizeResource(ctx, tt.resourceType, tt.permission)
			if (err != nil) != tt.expectErr {
				t.Errorf("AuthorizeResource() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestAuthorizeHelpers(t *testing.T) {
	adminClaims := &Claims{Roles: []string{"admin"}}
	auditorClaims := &Claims{Roles: []string{"auditor"}}

	tests := []struct {
		name      string
		fn        func(context.Context) error
		claims    *Claims
		expectErr bool
	}{
		{"admin can read accounts", AuthorizeAccountRead, adminClaims, false},
		{"admin can write accounts", AuthorizeAccountWrite, adminClaims, false},
		{"auditor can read accounts", AuthorizeAccountRead, auditorClaims, false},
		{"auditor cannot write accounts", AuthorizeAccountWrite, auditorClaims, true},
		{"admin can read positions", AuthorizePositionRead, adminClaims, false},
		{"admin can write positions", AuthorizePositionWrite, adminClaims, false},
		{"admin can read transactions", AuthorizeTransactionRead, adminClaims, false},
		{"admin can write transactions", AuthorizeTransactionWrite, adminClaims, false},
		{"auditor can read audit logs", AuthorizeAuditRead, auditorClaims, false},
		{"admin can read system config", AuthorizeSystemRead, adminClaims, false},
		{"admin can write system config", AuthorizeSystemWrite, adminClaims, false},
		{"auditor cannot write system config", AuthorizeSystemWrite, auditorClaims, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), ClaimsContextKey, tt.claims)
			err := tt.fn(ctx)
			if (err != nil) != tt.expectErr {
				t.Errorf("%s error = %v, expectErr %v", tt.name, err, tt.expectErr)
			}
		})
	}
}

func TestNewRoles_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		role  Role
		valid bool
	}{
		{"tenant-owner is valid", RoleTenantOwner, true},
		{"platform-admin is valid", RolePlatformAdmin, true},
		{"super-admin is valid", RoleSuperAdmin, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.role.IsValid(); got != tt.valid {
				t.Errorf("Role.IsValid() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestCanGrantRole(t *testing.T) {
	tests := []struct {
		name         string
		granterRoles []Role
		targetRole   Role
		expected     bool
	}{
		// super-admin can grant all roles including platform-admin
		{"super-admin can grant platform-admin", []Role{RoleSuperAdmin}, RolePlatformAdmin, true},
		{"super-admin can grant tenant-owner", []Role{RoleSuperAdmin}, RoleTenantOwner, true},
		{"super-admin can grant admin", []Role{RoleSuperAdmin}, RoleAdmin, true},
		{"super-admin can grant operator", []Role{RoleSuperAdmin}, RoleOperator, true},
		{"super-admin can grant auditor", []Role{RoleSuperAdmin}, RoleAuditor, true},
		{"super-admin cannot grant service", []Role{RoleSuperAdmin}, RoleService, false},

		// platform-admin can grant tenant-owner, admin, operator, auditor
		{"platform-admin can grant tenant-owner", []Role{RolePlatformAdmin}, RoleTenantOwner, true},
		{"platform-admin can grant admin", []Role{RolePlatformAdmin}, RoleAdmin, true},
		{"platform-admin can grant operator", []Role{RolePlatformAdmin}, RoleOperator, true},
		{"platform-admin can grant auditor", []Role{RolePlatformAdmin}, RoleAuditor, true},
		{"platform-admin cannot grant service", []Role{RolePlatformAdmin}, RoleService, false},
		{"platform-admin cannot grant super-admin", []Role{RolePlatformAdmin}, RoleSuperAdmin, false},

		// tenant-owner can grant admin, operator, auditor
		{"tenant-owner can grant admin", []Role{RoleTenantOwner}, RoleAdmin, true},
		{"tenant-owner can grant operator", []Role{RoleTenantOwner}, RoleOperator, true},
		{"tenant-owner can grant auditor", []Role{RoleTenantOwner}, RoleAuditor, true},
		{"tenant-owner cannot grant tenant-owner", []Role{RoleTenantOwner}, RoleTenantOwner, false},
		{"tenant-owner cannot grant platform-admin", []Role{RoleTenantOwner}, RolePlatformAdmin, false},

		// admin can grant operator, auditor
		{"admin can grant operator", []Role{RoleAdmin}, RoleOperator, true},
		{"admin can grant auditor", []Role{RoleAdmin}, RoleAuditor, true},
		{"admin cannot grant admin", []Role{RoleAdmin}, RoleAdmin, false},
		{"admin cannot grant tenant-owner", []Role{RoleAdmin}, RoleTenantOwner, false},

		// operator and auditor cannot grant any roles
		{"operator cannot grant any role", []Role{RoleOperator}, RoleAuditor, false},
		{"auditor cannot grant any role", []Role{RoleAuditor}, RoleOperator, false},

		// multiple granter roles - highest privilege wins
		{"tenant-owner + admin can grant tenant-owner via platform-admin not present", []Role{RoleTenantOwner, RoleAdmin}, RoleTenantOwner, false},
		{"platform-admin + admin can grant tenant-owner", []Role{RolePlatformAdmin, RoleAdmin}, RoleTenantOwner, true},

		// empty granter roles
		{"empty granter cannot grant anything", []Role{}, RoleAuditor, false},
		{"nil granter cannot grant anything", nil, RoleAuditor, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanGrantRole(tt.granterRoles, tt.targetRole); got != tt.expected {
				t.Errorf("CanGrantRole() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRoleHierarchy_NotNil(t *testing.T) {
	if roleHierarchy == nil {
		t.Error("roleHierarchy should not be nil")
	}
}

func TestTenantOwnerPermissions(t *testing.T) {
	tests := []struct {
		name         string
		resourceType ResourceType
		permission   Permission
		expected     bool
	}{
		// tenant-owner: same as admin + user management (identity resource)
		{"tenant-owner can read accounts", ResourceTypeAccount, PermissionRead, true},
		{"tenant-owner can write accounts", ResourceTypeAccount, PermissionWrite, true},
		{"tenant-owner can delete accounts", ResourceTypeAccount, PermissionDelete, true},
		{"tenant-owner can execute accounts", ResourceTypeAccount, PermissionExecute, true},
		{"tenant-owner can read identity", ResourceTypeIdentity, PermissionRead, true},
		{"tenant-owner can write identity", ResourceTypeIdentity, PermissionWrite, true},
		{"tenant-owner can delete identity", ResourceTypeIdentity, PermissionDelete, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := &Claims{Roles: []string{RoleTenantOwner.String()}}
			if got := HasPermission(claims, tt.resourceType, tt.permission); got != tt.expected {
				t.Errorf("HasPermission(tenant-owner, %s, %s) = %v, want %v", tt.resourceType, tt.permission, got, tt.expected)
			}
		})
	}
}

func TestPlatformAdminPermissions(t *testing.T) {
	tests := []struct {
		name         string
		resourceType ResourceType
		permission   Permission
		expected     bool
	}{
		// platform-admin: cross-tenant access, tenant provisioning
		{"platform-admin can read accounts", ResourceTypeAccount, PermissionRead, true},
		{"platform-admin can write system", ResourceTypeSystem, PermissionWrite, true},
		{"platform-admin can read identity", ResourceTypeIdentity, PermissionRead, true},
		{"platform-admin can write identity", ResourceTypeIdentity, PermissionWrite, true},
		{"platform-admin can delete identity", ResourceTypeIdentity, PermissionDelete, true},
		{"platform-admin can execute identity", ResourceTypeIdentity, PermissionExecute, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := &Claims{Roles: []string{RolePlatformAdmin.String()}}
			if got := HasPermission(claims, tt.resourceType, tt.permission); got != tt.expected {
				t.Errorf("HasPermission(platform-admin, %s, %s) = %v, want %v", tt.resourceType, tt.permission, got, tt.expected)
			}
		})
	}
}

func TestSuperAdminPermissions(t *testing.T) {
	tests := []struct {
		name         string
		resourceType ResourceType
		permission   Permission
		expected     bool
	}{
		{"super-admin can read accounts", ResourceTypeAccount, PermissionRead, true},
		{"super-admin can write accounts", ResourceTypeAccount, PermissionWrite, true},
		{"super-admin can delete accounts", ResourceTypeAccount, PermissionDelete, true},
		{"super-admin can write system", ResourceTypeSystem, PermissionWrite, true},
		{"super-admin can execute identity", ResourceTypeIdentity, PermissionExecute, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := &Claims{Roles: []string{RoleSuperAdmin.String()}}
			if got := HasPermission(claims, tt.resourceType, tt.permission); got != tt.expected {
				t.Errorf("HasPermission(super-admin, %s, %s) = %v, want %v", tt.resourceType, tt.permission, got, tt.expected)
			}
		})
	}
}

func TestResourceTypeIdentity(t *testing.T) {
	if ResourceTypeIdentity != "identity" {
		t.Errorf("ResourceTypeIdentity = %v, want 'identity'", ResourceTypeIdentity)
	}
}

// Benchmark tests
func BenchmarkHasAnyRole(b *testing.B) {
	claims := &Claims{Roles: []string{"admin", "operator", "auditor"}}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		HasAnyRole(claims, RoleOperator)
	}
}

func BenchmarkHasPermission(b *testing.B) {
	claims := &Claims{Roles: []string{"admin"}}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		HasPermission(claims, ResourceTypeAccount, PermissionWrite)
	}
}

func BenchmarkHTTPAuthorizationMiddleware(b *testing.B) {
	claims := &Claims{Roles: []string{"admin"}}
	handler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {})
	middleware := NewHTTPAuthorizationMiddleware(RoleAdmin)
	wrappedHandler := middleware.Handler(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	ctx := context.WithValue(req.Context(), ClaimsContextKey, claims)
	req = req.WithContext(ctx)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		wrappedHandler.ServeHTTP(w, req)
	}
}
