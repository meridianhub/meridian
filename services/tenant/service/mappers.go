package service

import (
	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// toProto converts a domain tenant to protobuf.
func (s *Service) toProto(tenant *domain.Tenant) *pb.Tenant {
	proto := &pb.Tenant{
		TenantId:        tenant.ID.String(),
		Slug:            tenant.Slug,
		DisplayName:     tenant.DisplayName,
		SettlementAsset: tenant.SettlementAsset,
		Subdomain:       tenant.Subdomain,
		Status:          s.toProtoStatus(tenant.Status),
		CreatedAt:       timestamppb.New(tenant.CreatedAt),
		Version:         int32(tenant.Version),
		PartyId:         tenant.PartyID,
		ErrorMessage:    tenant.ErrorMessage,
	}

	if tenant.DeprovisionedAt != nil {
		proto.DeprovisionedAt = timestamppb.New(*tenant.DeprovisionedAt)
	}

	if tenant.Metadata != nil {
		if metadata, err := structpb.NewStruct(tenant.Metadata); err == nil {
			proto.Metadata = metadata
		}
	}

	return proto
}

// toProtoStatus converts domain status to protobuf status.
func (s *Service) toProtoStatus(status domain.Status) pb.TenantStatus {
	switch status {
	case domain.StatusProvisioningPending:
		return pb.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING
	case domain.StatusProvisioning:
		return pb.TenantStatus_TENANT_STATUS_PROVISIONING
	case domain.StatusProvisioningFailed:
		return pb.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED
	case domain.StatusActive:
		return pb.TenantStatus_TENANT_STATUS_ACTIVE
	case domain.StatusSuspended:
		return pb.TenantStatus_TENANT_STATUS_SUSPENDED
	case domain.StatusDeprovisioned:
		return pb.TenantStatus_TENANT_STATUS_DEPROVISIONED
	default:
		return pb.TenantStatus_TENANT_STATUS_UNSPECIFIED
	}
}

// toDomainStatus converts protobuf status to domain status.
func (s *Service) toDomainStatus(status pb.TenantStatus) (domain.Status, error) {
	switch status {
	case pb.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING:
		return domain.StatusProvisioningPending, nil
	case pb.TenantStatus_TENANT_STATUS_PROVISIONING:
		return domain.StatusProvisioning, nil
	case pb.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED:
		return domain.StatusProvisioningFailed, nil
	case pb.TenantStatus_TENANT_STATUS_ACTIVE:
		return domain.StatusActive, nil
	case pb.TenantStatus_TENANT_STATUS_SUSPENDED:
		return domain.StatusSuspended, nil
	case pb.TenantStatus_TENANT_STATUS_DEPROVISIONED:
		return domain.StatusDeprovisioned, nil
	case pb.TenantStatus_TENANT_STATUS_UNSPECIFIED:
		return "", ErrUnknownStatus
	default:
		return "", ErrUnknownStatus
	}
}

// toProtoServiceStatus converts domain service provisioning status to protobuf status.
func (s *Service) toProtoServiceStatus(status domain.ServiceProvisioningStatus) pb.ServiceProvisioningStatus_Status {
	switch status {
	case domain.ServiceStatusPending:
		return pb.ServiceProvisioningStatus_STATUS_PENDING
	case domain.ServiceStatusInProgress:
		return pb.ServiceProvisioningStatus_STATUS_IN_PROGRESS
	case domain.ServiceStatusCompleted:
		return pb.ServiceProvisioningStatus_STATUS_COMPLETED
	case domain.ServiceStatusFailed:
		return pb.ServiceProvisioningStatus_STATUS_FAILED
	default:
		return pb.ServiceProvisioningStatus_STATUS_UNSPECIFIED
	}
}
