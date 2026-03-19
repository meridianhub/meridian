package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// RegisterAssociations creates a party association
func (s *Service) RegisterAssociations(ctx context.Context, req *pb.RegisterAssociationsRequest) (*pb.RegisterAssociationsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	relatedPartyID, err := uuid.Parse(req.RelatedPartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid related party ID format: %v", err)
	}

	// Verify both parties exist with FOR UPDATE locks to prevent race condition
	// where a party could be deleted between verification and association creation
	if _, err := s.repo.FindByIDForUpdate(ctx, partyID); err != nil {
		s.logger.Error("failed to find party for association", "party_id", req.PartyId, "error", err)
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		return nil, status.Errorf(codes.Internal, "failed to verify party: %v", err)
	}
	if _, err := s.repo.FindByIDForUpdate(ctx, relatedPartyID); err != nil {
		s.logger.Error("failed to find related party for association", "related_party_id", req.RelatedPartyId, "error", err)
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return nil, status.Errorf(codes.NotFound, "related party not found: %s", req.RelatedPartyId)
		}
		return nil, status.Errorf(codes.Internal, "failed to verify related party: %v", err)
	}

	// Check for circular association
	isCircular, err := s.repo.CheckCircularAssociation(ctx, partyID, relatedPartyID)
	if err != nil {
		s.logger.Error("failed to check circular association", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to check circular association: %v", err)
	}
	if isCircular {
		return nil, status.Errorf(codes.InvalidArgument, "circular association detected")
	}

	// Build association input with optional fields
	relationshipType, err := protoToRelationshipType(req.RelationshipType)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid relationship type: %v", err)
	}
	var input *persistence.AssociationInput
	if req.Metadata != nil || req.Status != pb.AssociationStatus_ASSOCIATION_STATUS_UNSPECIFIED || req.EffectiveFrom != nil || req.EffectiveTo != nil {
		input = &persistence.AssociationInput{}
		if req.Metadata != nil {
			metadataBytes, marshalErr := json.Marshal(req.Metadata.AsMap())
			if marshalErr != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid metadata: %v", marshalErr)
			}
			metadataStr := string(metadataBytes)
			input.Metadata = &metadataStr
		}
		if req.Status != pb.AssociationStatus_ASSOCIATION_STATUS_UNSPECIFIED {
			assocStatus, statusErr := protoAssociationStatusToString(req.Status)
			if statusErr != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid association status: %v", statusErr)
			}
			input.Status = assocStatus
		}
		if req.EffectiveFrom != nil {
			ef := req.EffectiveFrom.AsTime()
			input.EffectiveFrom = &ef
		}
		if req.EffectiveTo != nil {
			et := req.EffectiveTo.AsTime()
			input.EffectiveTo = &et
		}
	}

	// Save association
	associationID, err := s.repo.SaveAssociationWithInput(ctx, partyID, relatedPartyID, relationshipType, input)
	if err != nil {
		s.logger.Error("failed to save association", "party_id", req.PartyId, "error", err)
		if errors.Is(err, persistence.ErrAssociationExists) {
			return nil, status.Errorf(codes.AlreadyExists, "association already exists between parties")
		}
		return nil, status.Errorf(codes.Internal, "failed to save association: %v", err)
	}

	s.logger.Info("party association registered", "party_id", req.PartyId, "related_party_id", req.RelatedPartyId)

	resp := &pb.RegisterAssociationsResponse{
		AssociationId:    associationID.String(),
		PartyId:          req.PartyId,
		RelatedPartyId:   req.RelatedPartyId,
		RelationshipType: req.RelationshipType,
		CreatedAt:        timestamppb.Now(),
		Metadata:         req.Metadata,
		Status:           req.Status,
		EffectiveFrom:    req.EffectiveFrom,
		EffectiveTo:      req.EffectiveTo,
	}
	if resp.Status == pb.AssociationStatus_ASSOCIATION_STATUS_UNSPECIFIED {
		resp.Status = pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE
	}

	return resp, nil
}

// UpdateAssociations updates a party association
func (s *Service) UpdateAssociations(ctx context.Context, req *pb.UpdateAssociationsRequest) (*pb.UpdateAssociationsResponse, error) {
	associationID, err := uuid.Parse(req.AssociationId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid association ID format: %v", err)
	}

	relationshipType, err := protoToRelationshipType(req.RelationshipType)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid relationship type: %v", err)
	}
	entity, err := s.repo.UpdateAssociation(ctx, associationID, relationshipType)
	if err != nil {
		s.logger.Error("failed to update association", "association_id", req.AssociationId, "error", err)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "association not found: %s", req.AssociationId)
		}
		return nil, status.Errorf(codes.Internal, "failed to update association: %v", err)
	}

	s.logger.Info("party association updated", "association_id", req.AssociationId)

	return &pb.UpdateAssociationsResponse{
		AssociationId:    req.AssociationId,
		PartyId:          entity.PartyID.String(),
		RelatedPartyId:   entity.RelatedPartyID.String(),
		RelationshipType: req.RelationshipType,
		UpdatedAt:        timestamppb.Now(),
	}, nil
}

// RetrieveAssociations retrieves party associations
func (s *Service) RetrieveAssociations(ctx context.Context, req *pb.RetrieveAssociationsRequest) (*pb.RetrieveAssociationsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	associations, err := s.repo.FindAssociations(ctx, partyID)
	if err != nil {
		s.logger.Error("failed to retrieve associations", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve associations: %v", err)
	}

	pbAssociations := make([]*pb.Association, len(associations))
	for i, assoc := range associations {
		pbAssociations[i] = associationEntityToProto(&assoc)
	}

	return &pb.RetrieveAssociationsResponse{
		PartyId:      req.PartyId,
		Associations: pbAssociations,
	}, nil
}

// ListParticipants returns all active participants for a syndicate (org party).
func (s *Service) ListParticipants(ctx context.Context, req *pb.ListParticipantsRequest) (*pb.ListParticipantsResponse, error) {
	orgPartyID, err := uuid.Parse(req.OrgPartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid org_party_id format: %v", err)
	}

	relationshipType, err := protoToRelationshipType(req.RelationshipType)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid relationship type: %v", err)
	}

	associations, err := s.repo.ListParticipants(ctx, orgPartyID, relationshipType)
	if err != nil {
		s.logger.Error("failed to list participants",
			"org_party_id", req.OrgPartyId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to list participants: %v", err)
	}

	participants := make([]*pb.Association, len(associations))
	for i, assoc := range associations {
		participants[i] = associationEntityToProto(&assoc)
	}

	return &pb.ListParticipantsResponse{
		Participants: participants,
	}, nil
}

// GetStructuringData returns the structuring metadata for a specific participant in a syndicate.
func (s *Service) GetStructuringData(ctx context.Context, req *pb.GetStructuringDataRequest) (*pb.GetStructuringDataResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party_id format: %v", err)
	}

	orgPartyID, err := uuid.Parse(req.OrgPartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid org_party_id format: %v", err)
	}

	relationshipType, err := protoToRelationshipType(req.RelationshipType)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid relationship type: %v", err)
	}

	metadata, err := s.repo.GetStructuringData(ctx, partyID, orgPartyID, relationshipType)
	if err != nil {
		s.logger.Error("failed to get structuring data",
			"party_id", req.PartyId,
			"org_party_id", req.OrgPartyId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to get structuring data: %v", err)
	}

	pbStruct, err := structpb.NewStruct(metadata)
	if err != nil {
		s.logger.Error("failed to convert metadata to proto struct",
			"party_id", req.PartyId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to convert metadata: %v", err)
	}

	return &pb.GetStructuringDataResponse{
		Metadata: pbStruct,
	}, nil
}
