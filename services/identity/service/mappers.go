package service

import (
	pb "github.com/meridianhub/meridian/api/proto/meridian/identity/v1"
	"github.com/meridianhub/meridian/services/identity/domain"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// identityToProto converts a domain Identity to its proto representation.
func identityToProto(identity *domain.Identity) *pb.Identity {
	if identity == nil {
		return nil
	}

	pbIdentity := &pb.Identity{
		Id:             identity.ID().String(),
		Email:          identity.Email(),
		Status:         domainStatusToProto(identity.Status()),
		FailedAttempts: int32(identity.FailedAttempts()),
		CreatedAt:      timestamppb.New(identity.CreatedAt()),
		UpdatedAt:      timestamppb.New(identity.UpdatedAt()),
		Version:        int32(identity.Version()),
	}

	if identity.ExternalIDP() != "" {
		pbIdentity.ExternalIdp = identity.ExternalIDP()
		pbIdentity.ExternalIdpSub = identity.ExternalSub()
	}

	return pbIdentity
}

// roleAssignmentToProto converts a domain RoleAssignment to its proto representation.
func roleAssignmentToProto(ra *domain.RoleAssignment) *pb.RoleAssignment {
	if ra == nil {
		return nil
	}

	pbRA := &pb.RoleAssignment{
		Id:         ra.ID().String(),
		IdentityId: ra.IdentityID().String(),
		Role:       domainRoleToProto(ra.Role()),
		GrantedBy:  ra.GrantedBy().String(),
		GrantedAt:  timestamppb.New(ra.CreatedAt()),
	}

	if ra.ExpiresAt() != nil {
		pbRA.ExpiresAt = timestamppb.New(*ra.ExpiresAt())
	}

	if ra.RevokedAt() != nil {
		pbRA.Revoked = true
		pbRA.RevokedAt = timestamppb.New(*ra.RevokedAt())
		if ra.RevokedBy() != nil {
			pbRA.RevokedBy = ra.RevokedBy().String()
		}
	}

	return pbRA
}

// invitationToProto converts a domain Invitation to its proto representation.
func invitationToProto(inv *domain.Invitation) *pb.Invitation {
	if inv == nil {
		return nil
	}

	pbInv := &pb.Invitation{
		Id:         inv.ID().String(),
		IdentityId: inv.IdentityID().String(),
		InvitedBy:  inv.InvitedBy().String(),
		ExpiresAt:  timestamppb.New(inv.ExpiresAt()),
		CreatedAt:  timestamppb.New(inv.CreatedAt()),
	}

	if inv.Status() == domain.InvitationStatusAccepted {
		pbInv.AcceptedAt = timestamppb.New(inv.UpdatedAt())
	}

	return pbInv
}

// domainStatusToProto maps domain IdentityStatus to proto IdentityStatus.
func domainStatusToProto(s domain.IdentityStatus) pb.IdentityStatus {
	switch s {
	case domain.IdentityStatusPendingInvite:
		return pb.IdentityStatus_IDENTITY_STATUS_PENDING_INVITE
	case domain.IdentityStatusActive:
		return pb.IdentityStatus_IDENTITY_STATUS_ACTIVE
	case domain.IdentityStatusSuspended:
		return pb.IdentityStatus_IDENTITY_STATUS_SUSPENDED
	case domain.IdentityStatusLocked:
		return pb.IdentityStatus_IDENTITY_STATUS_LOCKED
	case domain.IdentityStatusPendingVerification:
		return pb.IdentityStatus_IDENTITY_STATUS_PENDING_VERIFICATION
	default:
		return pb.IdentityStatus_IDENTITY_STATUS_UNSPECIFIED
	}
}

// domainRoleToProto maps a domain Role to its proto enum value.
// Note: The domain model defines 5 privilege levels (VIEWER through PLATFORM).
// The proto API exposes SUPER_ADMIN as an alias for PLATFORM_ADMIN; both map to
// the domain's highest privilege level (RolePlatform). On output, RolePlatform
// always maps to ROLE_PLATFORM_ADMIN. See protoRoleToDomain for input handling.
func domainRoleToProto(r domain.Role) pb.Role {
	switch r {
	case domain.RoleViewer:
		return pb.Role_ROLE_AUDITOR // VIEWER maps to AUDITOR (read-only)
	case domain.RoleOperator:
		return pb.Role_ROLE_OPERATOR
	case domain.RoleAdmin:
		return pb.Role_ROLE_ADMIN
	case domain.RoleTenantOwner:
		return pb.Role_ROLE_TENANT_OWNER
	case domain.RolePlatform:
		return pb.Role_ROLE_PLATFORM_ADMIN
	default:
		return pb.Role_ROLE_UNSPECIFIED
	}
}

// protoRoleToDomain maps a proto Role enum to a domain role string.
// SUPER_ADMIN is an API-level alias that intentionally maps to the domain's
// highest privilege level (RolePlatform), the same as PLATFORM_ADMIN. This is
// by design: the domain model treats them as equivalent. On roundtrip, a
// SUPER_ADMIN grant is stored as PLATFORM and returned as PLATFORM_ADMIN.
func protoRoleToDomain(r pb.Role) string {
	switch r {
	case pb.Role_ROLE_UNSPECIFIED:
		return ""
	case pb.Role_ROLE_ADMIN:
		return string(domain.RoleAdmin)
	case pb.Role_ROLE_OPERATOR:
		return string(domain.RoleOperator)
	case pb.Role_ROLE_AUDITOR:
		return string(domain.RoleViewer) // AUDITOR maps to VIEWER
	case pb.Role_ROLE_TENANT_OWNER:
		return string(domain.RoleTenantOwner)
	case pb.Role_ROLE_PLATFORM_ADMIN:
		return string(domain.RolePlatform)
	case pb.Role_ROLE_SUPER_ADMIN:
		return string(domain.RolePlatform) // Alias for PLATFORM_ADMIN (see func doc)
	default:
		return ""
	}
}
