package service

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

func claimsContext(roles ...string) context.Context {
	claims := &auth.Claims{
		UserID: "test-user",
		Roles:  roles,
	}
	return context.WithValue(context.Background(), auth.ClaimsContextKey, claims)
}

func metadataContext(role string) context.Context {
	md := metadata.New(map[string]string{"x-meridian-role": role})
	return metadata.NewIncomingContext(context.Background(), md)
}

func TestExtractCallerRole_JWTServiceRole(t *testing.T) {
	ctx := claimsContext("service")
	assert.Equal(t, CallerRoleSystem, extractCallerRole(ctx))
}

func TestExtractCallerRole_JWTAdminRole(t *testing.T) {
	ctx := claimsContext("admin")
	assert.Equal(t, CallerRoleSystem, extractCallerRole(ctx))
}

func TestExtractCallerRole_JWTAuditorRole(t *testing.T) {
	ctx := claimsContext("auditor")
	assert.Equal(t, CallerRoleAuditor, extractCallerRole(ctx))
}

func TestExtractCallerRole_JWTOperatorRole(t *testing.T) {
	ctx := claimsContext("operator")
	assert.Equal(t, CallerRoleTenantAdmin, extractCallerRole(ctx))
}

func TestExtractCallerRole_JWTUnknownRole(t *testing.T) {
	ctx := claimsContext("viewer")
	assert.Equal(t, CallerRoleTenantAdmin, extractCallerRole(ctx))
}

func TestExtractCallerRole_JWTNoRoles(t *testing.T) {
	ctx := claimsContext()
	assert.Equal(t, CallerRoleTenantAdmin, extractCallerRole(ctx))
}

func TestExtractCallerRole_NoClaims_DefaultsTenantAdmin(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, CallerRoleTenantAdmin, extractCallerRole(ctx))
}

func TestExtractCallerRole_NoClaims_MetadataSystem(t *testing.T) {
	ctx := metadataContext("SYSTEM")
	assert.Equal(t, CallerRoleSystem, extractCallerRole(ctx))
}

func TestExtractCallerRole_NoClaims_MetadataAuditor(t *testing.T) {
	ctx := metadataContext("AUDITOR")
	assert.Equal(t, CallerRoleAuditor, extractCallerRole(ctx))
}

func TestExtractCallerRole_NoClaims_MetadataUnknown(t *testing.T) {
	ctx := metadataContext("UNKNOWN")
	assert.Equal(t, CallerRoleTenantAdmin, extractCallerRole(ctx))
}

func TestExtractCallerRole_NoClaims_EmptyMetadata(t *testing.T) {
	md := metadata.New(map[string]string{})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	assert.Equal(t, CallerRoleTenantAdmin, extractCallerRole(ctx))
}

// TestExtractCallerRole_JWTWinsOverMetadata is a security test.
// When valid JWT claims exist (TENANT_ADMIN role), malicious metadata
// claiming SYSTEM role must be ignored. The JWT claim takes precedence.
func TestExtractCallerRole_JWTWinsOverMetadata(t *testing.T) {
	// Create context with JWT claims (operator role = TenantAdmin)
	claims := &auth.Claims{
		UserID: "test-user",
		Roles:  []string{"operator"},
	}
	ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, claims)

	// Add malicious metadata claiming SYSTEM role
	md := metadata.New(map[string]string{"x-meridian-role": "SYSTEM"})
	ctx = metadata.NewIncomingContext(ctx, md)

	// JWT claims must win - operator maps to TenantAdmin, not System
	assert.Equal(t, CallerRoleTenantAdmin, extractCallerRole(ctx))
}

func TestExtractCallerRole_JWTMultipleRoles(t *testing.T) {
	// Service role should take precedence when multiple roles present
	ctx := claimsContext("operator", "service")
	assert.Equal(t, CallerRoleSystem, extractCallerRole(ctx))
}

func TestExtractCallerRole_JWTAuditorAndOperator(t *testing.T) {
	ctx := claimsContext("operator", "auditor")
	assert.Equal(t, CallerRoleAuditor, extractCallerRole(ctx))
}
