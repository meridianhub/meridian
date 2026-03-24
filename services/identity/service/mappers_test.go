package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/identity/v1"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- identityToProto: cases not covered by grpc_service_test.go ---

func TestIdentityToProto_WithoutExternalIDP(t *testing.T) {
	// Verifies that when ExternalIDP is empty, the external_idp/external_idp_sub fields
	// are not populated (the mapper guard: if identity.ExternalIDP() != "")
	identity, err := domain.NewIdentity("local@example.com")
	require.NoError(t, err)

	proto := identityToProto(identity)

	require.NotNil(t, proto)
	assert.Empty(t, proto.ExternalIdp)
	assert.Empty(t, proto.ExternalIdpSub)
}

func TestIdentityToProto_PendingInviteStatus(t *testing.T) {
	identity, err := domain.NewIdentity("new@example.com")
	require.NoError(t, err)

	proto := identityToProto(identity)

	require.NotNil(t, proto)
	assert.Equal(t, pb.IdentityStatus_IDENTITY_STATUS_PENDING_INVITE, proto.Status)
}

func TestIdentityToProto_TimestampsPopulated(t *testing.T) {
	before := time.Now().Truncate(time.Second)
	identity, err := domain.NewIdentity("ts@example.com")
	require.NoError(t, err)

	proto := identityToProto(identity)

	require.NotNil(t, proto)
	require.NotNil(t, proto.CreatedAt)
	require.NotNil(t, proto.UpdatedAt)
	assert.False(t, proto.CreatedAt.AsTime().Before(before))
	assert.False(t, proto.UpdatedAt.AsTime().Before(before))
}

// --- roleAssignmentToProto: GrantedAt is populated ---

func TestRoleAssignmentToProto_GrantedAtPopulated(t *testing.T) {
	identityID := uuid.New()
	granterID := uuid.New()

	ra, err := domain.NewRoleAssignment(identityID, granterID, string(domain.RolePlatform), string(domain.RoleAdmin))
	require.NoError(t, err)

	proto := roleAssignmentToProto(ra)

	require.NotNil(t, proto)
	require.NotNil(t, proto.GrantedAt)
}

// --- roleAssignmentToProto: revocation without revokedBy ---

func TestRoleAssignmentToProto_RevocationWithoutRevokedBy(t *testing.T) {
	revokedAt := time.Now()
	ra := domain.ReconstructRoleAssignment(
		uuid.New(), uuid.New(), uuid.New(),
		domain.RoleAdmin, nil, &revokedAt, nil, // revokedBy is nil
		time.Now(), time.Now(),
	)

	proto := roleAssignmentToProto(ra)

	require.NotNil(t, proto)
	assert.True(t, proto.Revoked)
	assert.NotNil(t, proto.RevokedAt)
	assert.Empty(t, proto.RevokedBy)
}

// --- protoRoleToDomain: unknown proto value ---

func TestProtoRoleToDomain_UnrecognizedValue(t *testing.T) {
	result := protoRoleToDomain(pb.Role(9999))
	assert.Equal(t, "", result)
}

// --- Round-trip: domain role -> proto -> domain ---

func TestRoleRoundTrip(t *testing.T) {
	// Confirm roles that survive domain -> proto -> domain without loss
	cases := []struct {
		role  domain.Role
		proto pb.Role
	}{
		{domain.RoleOperator, pb.Role_ROLE_OPERATOR},
		{domain.RoleAdmin, pb.Role_ROLE_ADMIN},
		{domain.RoleTenantOwner, pb.Role_ROLE_TENANT_OWNER},
		{domain.RolePlatform, pb.Role_ROLE_PLATFORM_ADMIN},
	}

	for _, tc := range cases {
		t.Run(string(tc.role), func(t *testing.T) {
			got := domainRoleToProto(tc.role)
			assert.Equal(t, tc.proto, got)
			assert.Equal(t, string(tc.role), protoRoleToDomain(got))
		})
	}
}

// --- SUPER_ADMIN alias mapping ---

func TestProtoRoleToDomain_SuperAdminAlias(t *testing.T) {
	// SUPER_ADMIN is an API alias for PLATFORM_ADMIN; both map to domain.RolePlatform.
	// On roundtrip, SUPER_ADMIN is stored as PLATFORM and returned as PLATFORM_ADMIN.
	result := protoRoleToDomain(pb.Role_ROLE_SUPER_ADMIN)
	assert.Equal(t, string(domain.RolePlatform), result)

	// And PLATFORM_ADMIN also maps back to PLATFORM
	resultPA := protoRoleToDomain(pb.Role_ROLE_PLATFORM_ADMIN)
	assert.Equal(t, result, resultPA)
}
