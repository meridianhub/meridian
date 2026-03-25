package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testRATenantID = tenant.MustNewTenantID("test_tenant")

func TestNewRoleAssignment_EmptyTenantID_ReturnsError(t *testing.T) {
	_, err := NewRoleAssignment("", uuid.New(), uuid.New(), string(RolePlatform), string(RoleAdmin))
	assert.ErrorIs(t, err, ErrTenantIDRequired)
}

func TestNewRoleAssignment_ValidRole(t *testing.T) {
	tests := []struct {
		granter string
		target  string
	}{
		{string(RolePlatform), string(RoleTenantOwner)},
		{string(RolePlatform), string(RoleAdmin)},
		{string(RolePlatform), string(RoleOperator)},
		{string(RolePlatform), string(RoleViewer)},
		{string(RoleTenantOwner), string(RoleAdmin)},
		{string(RoleAdmin), string(RoleViewer)},
	}
	identityID := uuid.New()
	grantedBy := uuid.New()

	for _, tt := range tests {
		t.Run(tt.granter+"->"+tt.target, func(t *testing.T) {
			ra, err := NewRoleAssignment(testRATenantID, identityID, grantedBy, tt.granter, tt.target)
			require.NoError(t, err)
			assert.NotEqual(t, uuid.Nil, ra.ID())
			assert.Equal(t, identityID, ra.IdentityID())
			assert.Equal(t, grantedBy, ra.GrantedBy())
			assert.Equal(t, Role(tt.target), ra.Role())
			assert.True(t, ra.IsActive())
			assert.Nil(t, ra.RevokedAt())
			assert.Nil(t, ra.RevokedBy())
			assert.Nil(t, ra.ExpiresAt())
		})
	}
}

func TestNewRoleAssignment_InvalidRole(t *testing.T) {
	_, err := NewRoleAssignment(testRATenantID, uuid.New(), uuid.New(), string(RolePlatform), "SUPERUSER")
	assert.ErrorIs(t, err, ErrInvalidRole)
}

func TestNewRoleAssignment_InsufficientPermissions(t *testing.T) {
	tests := []struct {
		granter string
		target  string
	}{
		// Operator cannot grant anyone
		{string(RoleOperator), string(RoleViewer)},
		// Viewer cannot grant anyone
		{string(RoleViewer), string(RoleViewer)},
		// Admin cannot grant itself or above
		{string(RoleAdmin), string(RoleAdmin)},
		{string(RoleAdmin), string(RoleTenantOwner)},
		// TenantOwner cannot grant Platform
		{string(RoleTenantOwner), string(RolePlatform)},
	}
	for _, tt := range tests {
		t.Run(tt.granter+"->"+tt.target, func(t *testing.T) {
			_, err := NewRoleAssignment(testRATenantID, uuid.New(), uuid.New(), tt.granter, tt.target)
			assert.ErrorIs(t, err, ErrInsufficientRolePermissions)
		})
	}
}

func TestRoleAssignment_Revoke(t *testing.T) {
	ra, _ := NewRoleAssignment(testRATenantID, uuid.New(), uuid.New(), string(RoleAdmin), string(RoleViewer))
	assert.True(t, ra.IsActive())

	revokedBy := uuid.New()
	err := ra.Revoke(revokedBy)
	require.NoError(t, err)

	assert.False(t, ra.IsActive())
	assert.NotNil(t, ra.RevokedAt())
	assert.Equal(t, &revokedBy, ra.RevokedBy())
}

func TestRoleAssignment_RevokeAlreadyRevoked(t *testing.T) {
	ra, _ := NewRoleAssignment(testRATenantID, uuid.New(), uuid.New(), string(RolePlatform), string(RoleAdmin))
	_ = ra.Revoke(uuid.New())

	err := ra.Revoke(uuid.New())
	assert.ErrorIs(t, err, ErrRoleAlreadyRevoked)
}

func TestRoleAssignment_IsActive_Expired(t *testing.T) {
	ra, _ := NewRoleAssignment(testRATenantID, uuid.New(), uuid.New(), string(RolePlatform), string(RoleOperator))
	// Set expiry in the past via reconstruction
	past := time.Now().Add(-time.Hour)
	ra2 := ReconstructRoleAssignment(
		ra.ID(), testRATenantID, ra.IdentityID(), ra.GrantedBy(), ra.Role(),
		&past, nil, nil, ra.CreatedAt(), ra.UpdatedAt(),
	)
	assert.False(t, ra2.IsActive())
}

func TestRoleAssignment_IsActive_NotExpired(t *testing.T) {
	ra, _ := NewRoleAssignment(testRATenantID, uuid.New(), uuid.New(), string(RolePlatform), string(RoleOperator))
	future := time.Now().Add(time.Hour)
	ra2 := ReconstructRoleAssignment(
		ra.ID(), testRATenantID, ra.IdentityID(), ra.GrantedBy(), ra.Role(),
		&future, nil, nil, ra.CreatedAt(), ra.UpdatedAt(),
	)
	assert.True(t, ra2.IsActive())
}

func TestCanGrant_HierarchyEnforcement(t *testing.T) {
	tests := []struct {
		granter  string
		target   string
		canGrant bool
	}{
		// Platform can grant all lower roles
		{string(RolePlatform), string(RoleTenantOwner), true},
		{string(RolePlatform), string(RoleAdmin), true},
		{string(RolePlatform), string(RoleOperator), true},
		{string(RolePlatform), string(RoleViewer), true},
		// TenantOwner can grant admin and below
		{string(RoleTenantOwner), string(RoleAdmin), true},
		{string(RoleTenantOwner), string(RoleOperator), true},
		{string(RoleTenantOwner), string(RoleViewer), true},
		// TenantOwner cannot grant Platform
		{string(RoleTenantOwner), string(RolePlatform), false},
		// Admin can grant operator and below
		{string(RoleAdmin), string(RoleOperator), true},
		{string(RoleAdmin), string(RoleViewer), true},
		// Admin cannot grant itself or above
		{string(RoleAdmin), string(RoleAdmin), false},
		{string(RoleAdmin), string(RoleTenantOwner), false},
		// Operator cannot grant anyone
		{string(RoleOperator), string(RoleViewer), false},
		// Viewer cannot grant anyone
		{string(RoleViewer), string(RoleViewer), false},
		// Invalid roles
		{"INVALID", string(RoleViewer), false},
		{string(RoleAdmin), "INVALID", false},
	}

	for _, tt := range tests {
		t.Run(tt.granter+"->"+tt.target, func(t *testing.T) {
			result := CanGrant(tt.granter, tt.target)
			assert.Equal(t, tt.canGrant, result)
		})
	}
}

func TestIsValidRole(t *testing.T) {
	assert.True(t, IsValidRole(string(RoleViewer)))
	assert.True(t, IsValidRole(string(RoleOperator)))
	assert.True(t, IsValidRole(string(RoleAdmin)))
	assert.True(t, IsValidRole(string(RoleTenantOwner)))
	assert.True(t, IsValidRole(string(RolePlatform)))
	assert.False(t, IsValidRole("SUPERUSER"))
	assert.False(t, IsValidRole(""))
}
