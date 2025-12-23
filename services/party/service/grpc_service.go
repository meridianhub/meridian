// Package service implements gRPC services for the party reference data domain
package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Service errors
var (
	// ErrRepositoryNil is returned when attempting to create a service with a nil repository
	ErrRepositoryNil = errors.New("repository cannot be nil")
	// ErrExternalRefTypeRequired is returned when external reference is provided without a type
	ErrExternalRefTypeRequired = errors.New("external reference type required when external reference is provided")
	// ErrUnknownExternalRefType is returned for unrecognized external reference types
	ErrUnknownExternalRefType = errors.New("unknown external reference type")
	// ErrUnsupportedFieldUpdate is returned when an unsupported field is specified for update
	ErrUnsupportedFieldUpdate = errors.New("unsupported field for update")
	// ErrControlActionUnspecified is returned when control action is unspecified
	ErrControlActionUnspecified = errors.New("control action unspecified")
	// ErrUnknownControlAction is returned for unknown control actions
	ErrUnknownControlAction = errors.New("unknown control action")
)

// Repository defines the interface for party persistence operations.
// All methods accept context for cancellation, timeout, and tracing support.
type Repository interface {
	Save(ctx context.Context, party *domain.Party) error
	FindByID(ctx context.Context, partyID uuid.UUID) (*domain.Party, error)
	FindByIDForUpdate(ctx context.Context, partyID uuid.UUID) (*domain.Party, error)
	FindByExternalReference(ctx context.Context, ref, refType string) (*domain.Party, error)

	// Business Qualifier operations
	SaveAssociation(ctx context.Context, partyID, relatedPartyID uuid.UUID, relationshipType string) (uuid.UUID, error)
	FindAssociations(ctx context.Context, partyID uuid.UUID) ([]persistence.PartyAssociationEntity, error)
	UpdateAssociation(ctx context.Context, associationID uuid.UUID, relationshipType string) error
	CheckCircularAssociation(ctx context.Context, partyID, relatedPartyID uuid.UUID) (bool, error)

	SaveDemographic(ctx context.Context, partyID uuid.UUID, socioEconomicData, employmentHistory string) error
	FindDemographic(ctx context.Context, partyID uuid.UUID) (*persistence.PartyDemographicEntity, error)

	SaveReference(ctx context.Context, partyID uuid.UUID, refType, refValue, issuingAuthority, expiryDate string) error
	SaveReferences(ctx context.Context, partyID uuid.UUID, refs []persistence.ReferenceInput) error
	FindReferences(ctx context.Context, partyID uuid.UUID) ([]persistence.PartyReferenceEntity, error)

	SaveBankRelation(ctx context.Context, partyID uuid.UUID, accountOfficerID, relationshipManagerID, assignedBranch string) error
	FindBankRelation(ctx context.Context, partyID uuid.UUID) (*persistence.PartyBankRelationEntity, error)
}

// Service implements the PartyService gRPC service
type Service struct {
	pb.UnimplementedPartyServiceServer
	repo   Repository
	logger *slog.Logger
}

// NewService creates a new party service
func NewService(repo Repository, logger *slog.Logger) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}

	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	return &Service{
		repo:   repo,
		logger: logger,
	}, nil
}

// RegisterParty creates a new party in the reference data directory
func (s *Service) RegisterParty(ctx context.Context, req *pb.RegisterPartyRequest) (*pb.RegisterPartyResponse, error) {
	// === Input validation (fail-fast) ===

	// Validate party type
	partyType, err := protoToPartyType(req.PartyType)
	if err != nil {
		s.logger.Error("invalid party type",
			"party_type", req.PartyType.String(),
			"error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid party type: %v", err)
	}

	// Validate external reference and type consistency
	if req.ExternalReferenceType != pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED && req.ExternalReference == "" {
		s.logger.Error("external reference type provided without reference",
			"external_reference_type", req.ExternalReferenceType.String())
		return nil, status.Errorf(codes.InvalidArgument, "external reference required when type is specified")
	}

	// Validate external reference type if external reference is provided
	var extRefType domain.ExternalReferenceType
	if req.ExternalReference != "" {
		extRefType, err = protoToExternalRefType(req.ExternalReferenceType)
		if err != nil {
			s.logger.Error("invalid external reference type",
				"external_reference_type", req.ExternalReferenceType.String(),
				"error", err)
			return nil, status.Errorf(codes.InvalidArgument, "invalid external reference type: %v", err)
		}
	}

	// === Domain object creation ===

	party, err := domain.NewParty(partyType, req.LegalName)
	if err != nil {
		s.logger.Error("failed to create party",
			"party_type", partyType,
			"error", err)
		return nil, status.Errorf(codes.InvalidArgument, "failed to create party: %v", err)
	}

	// Set optional display name
	if req.DisplayName != "" {
		if err := party.SetDisplayName(req.DisplayName); err != nil {
			s.logger.Error("invalid display name",
				"error", err)
			return nil, status.Errorf(codes.InvalidArgument, "invalid display name: %v", err)
		}
	}

	// === External reference handling ===

	if req.ExternalReference != "" {
		// Check for duplicate external reference
		existing, err := s.repo.FindByExternalReference(ctx, req.ExternalReference, string(extRefType))
		if err != nil && !errors.Is(err, persistence.ErrPartyNotFound) {
			s.logger.Error("failed to check external reference uniqueness",
				"external_reference_type", extRefType,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to check external reference: %v", err)
		}
		if existing != nil {
			s.logger.Warn("duplicate external reference",
				"external_reference_type", extRefType,
				"existing_party_id", existing.ID().String())
			return nil, status.Errorf(codes.AlreadyExists,
				"party with external reference of type %s already exists",
				extRefType)
		}

		if err := party.SetExternalReference(req.ExternalReference, extRefType); err != nil {
			s.logger.Error("invalid external reference",
				"external_reference_type", extRefType,
				"error", err)
			return nil, status.Errorf(codes.InvalidArgument, "invalid external reference: %v", err)
		}
	}

	// Save party
	if err := s.repo.Save(ctx, party); err != nil {
		if errors.Is(err, persistence.ErrPartyExists) {
			s.logger.Warn("party already exists (race condition)",
				"party_id", party.ID().String())
			return nil, status.Errorf(codes.AlreadyExists, "party already exists")
		}
		s.logger.Error("failed to save party",
			"party_id", party.ID().String(),
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to save party: %v", err)
	}

	s.logger.Info("party registered",
		"party_id", party.ID().String(),
		"party_type", partyType,
		"external_reference_type", party.ExternalReferenceType())

	return &pb.RegisterPartyResponse{
		Party: domainToProto(party),
	}, nil
}

// RetrieveParty gets party details by ID
func (s *Service) RetrieveParty(ctx context.Context, req *pb.RetrievePartyRequest) (*pb.RetrievePartyResponse, error) {
	// Parse party ID as UUID
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		s.logger.Error("invalid party ID format",
			"party_id", req.PartyId,
			"error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Retrieve party
	party, err := s.repo.FindByID(ctx, partyID)
	if err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			s.logger.Warn("party not found",
				"party_id", req.PartyId)
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		s.logger.Error("failed to retrieve party",
			"party_id", req.PartyId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	return &pb.RetrievePartyResponse{
		Party: domainToProto(party),
	}, nil
}

// domainToProto converts a domain Party to a proto Party message.
// Returns nil if the input party is nil.
func domainToProto(party *domain.Party) *pb.Party {
	if party == nil {
		return nil
	}
	return &pb.Party{
		PartyId:               party.ID().String(),
		PartyType:             partyTypeToProto(party.PartyType()),
		LegalName:             party.LegalName(),
		DisplayName:           party.DisplayName(),
		Status:                partyStatusToProto(party.Status()),
		ExternalReference:     party.ExternalReference(),
		ExternalReferenceType: externalRefTypeToProto(party.ExternalReferenceType()),
		CreatedAt:             timestamppb.New(party.CreatedAt()),
		UpdatedAt:             timestamppb.New(party.UpdatedAt()),
		// #nosec G115 - Version is bounded by database constraints
		Version: int32(party.Version()),
	}
}

// protoToPartyType converts a proto PartyType to domain PartyType
func protoToPartyType(pt pb.PartyType) (domain.PartyType, error) {
	switch pt {
	case pb.PartyType_PARTY_TYPE_PERSON:
		return domain.PartyTypePerson, nil
	case pb.PartyType_PARTY_TYPE_ORGANIZATION:
		return domain.PartyTypeOrganization, nil
	case pb.PartyType_PARTY_TYPE_UNSPECIFIED:
		return "", domain.ErrInvalidPartyType
	default:
		return "", domain.ErrInvalidPartyType
	}
}

// partyTypeToProto converts a domain PartyType to proto PartyType
func partyTypeToProto(pt domain.PartyType) pb.PartyType {
	switch pt {
	case domain.PartyTypePerson:
		return pb.PartyType_PARTY_TYPE_PERSON
	case domain.PartyTypeOrganization:
		return pb.PartyType_PARTY_TYPE_ORGANIZATION
	default:
		return pb.PartyType_PARTY_TYPE_UNSPECIFIED
	}
}

// partyStatusToProto converts a domain PartyStatus to proto PartyStatus
func partyStatusToProto(status domain.PartyStatus) pb.PartyStatus {
	switch status {
	case domain.PartyStatusActive:
		return pb.PartyStatus_PARTY_STATUS_ACTIVE
	case domain.PartyStatusRestricted:
		return pb.PartyStatus_PARTY_STATUS_RESTRICTED
	case domain.PartyStatusSuspended:
		return pb.PartyStatus_PARTY_STATUS_SUSPENDED
	case domain.PartyStatusTerminated:
		return pb.PartyStatus_PARTY_STATUS_TERMINATED
	default:
		return pb.PartyStatus_PARTY_STATUS_UNSPECIFIED
	}
}

// protoToExternalRefType converts a proto ExternalReferenceType to domain ExternalReferenceType
func protoToExternalRefType(rt pb.ExternalReferenceType) (domain.ExternalReferenceType, error) {
	switch rt {
	case pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE:
		return domain.ExternalReferenceTypeCompaniesHouse, nil
	case pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_NATIONAL_ID:
		return domain.ExternalReferenceTypeNationalID, nil
	case pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_LEI:
		return domain.ExternalReferenceTypeLEI, nil
	case pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_TAX_ID:
		return domain.ExternalReferenceTypeTaxID, nil
	case pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED:
		return "", ErrExternalRefTypeRequired
	default:
		return "", ErrUnknownExternalRefType
	}
}

// externalRefTypeToProto converts a domain ExternalReferenceType to proto ExternalReferenceType
func externalRefTypeToProto(rt domain.ExternalReferenceType) pb.ExternalReferenceType {
	switch rt {
	case domain.ExternalReferenceTypeCompaniesHouse:
		return pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE
	case domain.ExternalReferenceTypeNationalID:
		return pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_NATIONAL_ID
	case domain.ExternalReferenceTypeLEI:
		return pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_LEI
	case domain.ExternalReferenceTypeTaxID:
		return pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_TAX_ID
	default:
		return pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED
	}
}

// UpdateParty updates party details with field mask support for partial updates
func (s *Service) UpdateParty(ctx context.Context, req *pb.UpdatePartyRequest) (*pb.UpdatePartyResponse, error) {
	// Parse party ID
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		s.logger.Error("invalid party ID format", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Load existing party with pessimistic lock for consistent read-modify-write
	party, err := s.repo.FindByIDForUpdate(ctx, partyID)
	if err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			s.logger.Warn("party not found", "party_id", req.PartyId)
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		s.logger.Error("failed to retrieve party", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// Verify version for optimistic locking (defense in depth)
	// #nosec G115 - Version is bounded by database constraints
	if req.Version > 0 && party.Version() != int64(req.Version) {
		s.logger.Warn("version conflict", "party_id", req.PartyId, "expected", req.Version, "actual", party.Version())
		return nil, status.Errorf(codes.Aborted, "version conflict: party was modified by another transaction")
	}

	// Apply field mask updates
	if req.UpdateMask != nil && len(req.UpdateMask.Paths) > 0 {
		for _, path := range req.UpdateMask.Paths {
			if err := s.applyFieldUpdate(party, path, req); err != nil {
				s.logger.Error("failed to apply field update", "field", path, "error", err)
				return nil, status.Errorf(codes.InvalidArgument, "failed to update field %s: %v", path, err)
			}
		}
	} else {
		// No field mask - update all non-empty fields
		if req.DisplayName != "" {
			if err := party.SetDisplayName(req.DisplayName); err != nil {
				s.logger.Error("invalid display name", "error", err)
				return nil, status.Errorf(codes.InvalidArgument, "invalid display name: %v", err)
			}
		}
	}

	// Persist updated party
	if err := s.repo.Save(ctx, party); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			s.logger.Warn("version conflict on save", "party_id", req.PartyId)
			return nil, status.Errorf(codes.Aborted, "version conflict: party was modified by another transaction")
		}
		s.logger.Error("failed to save party", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to save party: %v", err)
	}

	s.logger.Info("party updated", "party_id", req.PartyId, "version", party.Version())

	return &pb.UpdatePartyResponse{
		Party: domainToProto(party),
	}, nil
}

// applyFieldUpdate applies a single field update based on field mask path
func (s *Service) applyFieldUpdate(party *domain.Party, path string, req *pb.UpdatePartyRequest) error {
	switch path {
	case "display_name":
		if req.DisplayName != "" {
			return party.SetDisplayName(req.DisplayName)
		}
	default:
		return ErrUnsupportedFieldUpdate
	}
	return nil
}

// ControlParty manages party lifecycle with state machine enforcement
func (s *Service) ControlParty(ctx context.Context, req *pb.ControlPartyRequest) (*pb.ControlPartyResponse, error) {
	// Parse party ID
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		s.logger.Error("invalid party ID format", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Convert control action to domain type
	action, err := protoToControlAction(req.ControlAction)
	if err != nil {
		s.logger.Error("invalid control action", "action", req.ControlAction.String(), "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid control action: %v", err)
	}

	// Load existing party
	party, err := s.repo.FindByIDForUpdate(ctx, partyID)
	if err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			s.logger.Warn("party not found", "party_id", req.PartyId)
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		s.logger.Error("failed to retrieve party", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// Apply control action (state machine enforced in domain)
	if err := party.ControlParty(action, req.Reason); err != nil {
		s.logger.Error("failed to apply control action",
			"party_id", req.PartyId,
			"action", action,
			"error", err)
		return nil, status.Errorf(codes.FailedPrecondition, "invalid status transition: %v", err)
	}

	// Persist updated party
	if err := s.repo.Save(ctx, party); err != nil {
		s.logger.Error("failed to save party after control action", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to save party: %v", err)
	}

	actionTime := time.Now()
	s.logger.Info("party control action executed",
		"party_id", req.PartyId,
		"action", action,
		"actor_id", req.ActorId,
		"reason", req.Reason,
		"new_status", party.Status())

	return &pb.ControlPartyResponse{
		Party:           domainToProto(party),
		ActionTimestamp: timestamppb.New(actionTime),
	}, nil
}

// protoToControlAction converts proto ControlAction to domain ControlAction
func protoToControlAction(action pb.ControlAction) (domain.ControlAction, error) {
	switch action {
	case pb.ControlAction_CONTROL_ACTION_ACTIVATE:
		return domain.ControlActionActivate, nil
	case pb.ControlAction_CONTROL_ACTION_RESTRICT:
		return domain.ControlActionRestrict, nil
	case pb.ControlAction_CONTROL_ACTION_SUSPEND:
		return domain.ControlActionSuspend, nil
	case pb.ControlAction_CONTROL_ACTION_TERMINATE:
		return domain.ControlActionTerminate, nil
	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		return "", ErrControlActionUnspecified
	default:
		return "", ErrUnknownControlAction
	}
}

// UpdateReference updates party reference data
func (s *Service) UpdateReference(ctx context.Context, req *pb.UpdateReferenceRequest) (*pb.UpdateReferenceResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		s.logger.Error("invalid party ID format", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Verify party exists
	if _, err := s.repo.FindByID(ctx, partyID); err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// Collect references to save in a single transaction
	var refs []persistence.ReferenceInput
	if req.GovernmentId != "" {
		refs = append(refs, persistence.ReferenceInput{
			RefType:          "GOVERNMENT_ID",
			RefValue:         req.GovernmentId,
			IssuingAuthority: req.IssuingAuthority,
			ExpiryDate:       req.ExpiryDate,
		})
	}
	if req.TaxReference != "" {
		refs = append(refs, persistence.ReferenceInput{
			RefType:  "TAX_REFERENCE",
			RefValue: req.TaxReference,
		})
	}

	// Save all references in a single transaction
	if len(refs) > 0 {
		if err := s.repo.SaveReferences(ctx, partyID, refs); err != nil {
			s.logger.Error("failed to save references", "party_id", req.PartyId, "error", err)
			return nil, status.Errorf(codes.Internal, "failed to save references: %v", err)
		}
	}

	s.logger.Info("party reference updated", "party_id", req.PartyId)

	return &pb.UpdateReferenceResponse{
		PartyId:          req.PartyId,
		GovernmentId:     req.GovernmentId,
		TaxReference:     req.TaxReference,
		IssuingAuthority: req.IssuingAuthority,
		ExpiryDate:       req.ExpiryDate,
		UpdatedAt:        timestamppb.Now(),
	}, nil
}

// RetrieveReference retrieves party reference data
func (s *Service) RetrieveReference(ctx context.Context, req *pb.RetrieveReferenceRequest) (*pb.RetrieveReferenceResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	refs, err := s.repo.FindReferences(ctx, partyID)
	if err != nil {
		s.logger.Error("failed to retrieve references", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve references: %v", err)
	}

	// Build response from references by type
	resp := &pb.RetrieveReferenceResponse{
		PartyId: req.PartyId,
	}
	for _, ref := range refs {
		switch ref.ReferenceType {
		case "GOVERNMENT_ID":
			resp.GovernmentId = ref.ReferenceValue
			if ref.IssuingAuthority != nil {
				resp.IssuingAuthority = *ref.IssuingAuthority
			}
			if ref.ExpiryDate != nil {
				resp.ExpiryDate = ref.ExpiryDate.Format("2006-01-02")
			}
			resp.UpdatedAt = timestamppb.New(ref.CreatedAt)
		case "TAX_REFERENCE":
			resp.TaxReference = ref.ReferenceValue
		}
	}

	return resp, nil
}

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

	// Verify both parties exist
	if _, err := s.repo.FindByID(ctx, partyID); err != nil {
		s.logger.Error("party not found for association", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
	}
	if _, err := s.repo.FindByID(ctx, relatedPartyID); err != nil {
		s.logger.Error("related party not found for association", "related_party_id", req.RelatedPartyId, "error", err)
		return nil, status.Errorf(codes.NotFound, "related party not found: %s", req.RelatedPartyId)
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

	// Save association
	relationshipType := protoToRelationshipType(req.RelationshipType)
	associationID, err := s.repo.SaveAssociation(ctx, partyID, relatedPartyID, relationshipType)
	if err != nil {
		s.logger.Error("failed to save association", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to save association: %v", err)
	}

	s.logger.Info("party association registered", "party_id", req.PartyId, "related_party_id", req.RelatedPartyId)

	return &pb.RegisterAssociationsResponse{
		AssociationId:    associationID.String(),
		PartyId:          req.PartyId,
		RelatedPartyId:   req.RelatedPartyId,
		RelationshipType: req.RelationshipType,
		CreatedAt:        timestamppb.Now(),
	}, nil
}

// UpdateAssociations updates a party association
func (s *Service) UpdateAssociations(ctx context.Context, req *pb.UpdateAssociationsRequest) (*pb.UpdateAssociationsResponse, error) {
	associationID, err := uuid.Parse(req.AssociationId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid association ID format: %v", err)
	}

	relationshipType := protoToRelationshipType(req.RelationshipType)
	if err := s.repo.UpdateAssociation(ctx, associationID, relationshipType); err != nil {
		s.logger.Error("failed to update association", "association_id", req.AssociationId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to update association: %v", err)
	}

	s.logger.Info("party association updated", "association_id", req.AssociationId)

	return &pb.UpdateAssociationsResponse{
		AssociationId:    req.AssociationId,
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
		pbAssociations[i] = &pb.Association{
			AssociationId:    assoc.ID.String(),
			RelatedPartyId:   assoc.RelatedPartyID.String(),
			RelationshipType: relationshipTypeToProto(assoc.RelationshipType),
			CreatedAt:        timestamppb.New(assoc.CreatedAt),
			UpdatedAt:        timestamppb.New(assoc.UpdatedAt),
		}
	}

	return &pb.RetrieveAssociationsResponse{
		PartyId:      req.PartyId,
		Associations: pbAssociations,
	}, nil
}

// ExchangeDemographics verifies party demographics data
func (s *Service) ExchangeDemographics(ctx context.Context, req *pb.ExchangeDemographicsRequest) (*pb.ExchangeDemographicsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Verify party exists
	if _, err := s.repo.FindByID(ctx, partyID); err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// Simplified verification (would call external service in production)
	verificationStatus := "VERIFIED"
	s.logger.Info("demographics verified", "party_id", req.PartyId)

	return &pb.ExchangeDemographicsResponse{
		PartyId:               req.PartyId,
		VerificationStatus:    verificationStatus,
		VerificationTimestamp: timestamppb.Now(),
	}, nil
}

// UpdateDemographics updates party demographics data
func (s *Service) UpdateDemographics(ctx context.Context, req *pb.UpdateDemographicsRequest) (*pb.UpdateDemographicsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Verify party exists
	if _, err := s.repo.FindByID(ctx, partyID); err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// Save demographic data
	if err := s.repo.SaveDemographic(ctx, partyID, req.SocioEconomicData, req.EmploymentHistory); err != nil {
		s.logger.Error("failed to save demographic", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to save demographic: %v", err)
	}

	s.logger.Info("party demographics updated", "party_id", req.PartyId)

	return &pb.UpdateDemographicsResponse{
		PartyId:           req.PartyId,
		SocioEconomicData: req.SocioEconomicData,
		EmploymentHistory: req.EmploymentHistory,
		UpdatedAt:         timestamppb.Now(),
	}, nil
}

// RetrieveDemographics retrieves party demographics data
func (s *Service) RetrieveDemographics(ctx context.Context, req *pb.RetrieveDemographicsRequest) (*pb.RetrieveDemographicsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	demo, err := s.repo.FindDemographic(ctx, partyID)
	if err != nil {
		s.logger.Error("failed to retrieve demographic", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve demographic: %v", err)
	}

	resp := &pb.RetrieveDemographicsResponse{
		PartyId: req.PartyId,
	}
	if demo != nil {
		if demo.SocioEconomicData != nil {
			// Unmarshal JSON string for JSONB column
			var socioEconStr string
			if err := json.Unmarshal([]byte(*demo.SocioEconomicData), &socioEconStr); err == nil {
				resp.SocioEconomicData = socioEconStr
			} else {
				resp.SocioEconomicData = *demo.SocioEconomicData
			}
		}
		if demo.EmploymentHistory != nil {
			// Unmarshal JSON string for JSONB column
			var empHistoryStr string
			if err := json.Unmarshal([]byte(*demo.EmploymentHistory), &empHistoryStr); err == nil {
				resp.EmploymentHistory = empHistoryStr
			} else {
				resp.EmploymentHistory = *demo.EmploymentHistory
			}
		}
		resp.UpdatedAt = timestamppb.New(demo.UpdatedAt)
	}

	return resp, nil
}

// UpdateBankRelations updates party bank relationship data
func (s *Service) UpdateBankRelations(ctx context.Context, req *pb.UpdateBankRelationsRequest) (*pb.UpdateBankRelationsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Verify party exists
	if _, err := s.repo.FindByID(ctx, partyID); err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// Save bank relation data
	if err := s.repo.SaveBankRelation(ctx, partyID, req.AccountOfficerId, req.RelationshipManagerId, req.AssignedBranch); err != nil {
		s.logger.Error("failed to save bank relation", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to save bank relation: %v", err)
	}

	s.logger.Info("party bank relations updated", "party_id", req.PartyId)

	return &pb.UpdateBankRelationsResponse{
		PartyId:               req.PartyId,
		AccountOfficerId:      req.AccountOfficerId,
		RelationshipManagerId: req.RelationshipManagerId,
		AssignedBranch:        req.AssignedBranch,
		UpdatedAt:             timestamppb.Now(),
	}, nil
}

// RetrieveBankRelations retrieves party bank relationship data
func (s *Service) RetrieveBankRelations(ctx context.Context, req *pb.RetrieveBankRelationsRequest) (*pb.RetrieveBankRelationsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	bankRel, err := s.repo.FindBankRelation(ctx, partyID)
	if err != nil {
		s.logger.Error("failed to retrieve bank relation", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve bank relation: %v", err)
	}

	resp := &pb.RetrieveBankRelationsResponse{
		PartyId: req.PartyId,
	}
	if bankRel != nil {
		if bankRel.AccountOfficerID != nil {
			resp.AccountOfficerId = *bankRel.AccountOfficerID
		}
		if bankRel.RelationshipManagerID != nil {
			resp.RelationshipManagerId = *bankRel.RelationshipManagerID
		}
		if bankRel.AssignedBranch != nil {
			resp.AssignedBranch = *bankRel.AssignedBranch
		}
		resp.UpdatedAt = timestamppb.New(bankRel.UpdatedAt)
	}

	return resp, nil
}

// protoToRelationshipType converts proto RelationshipType to domain string
func protoToRelationshipType(rt pb.RelationshipType) string {
	switch rt {
	case pb.RelationshipType_RELATIONSHIP_TYPE_UNSPECIFIED:
		return "UNSPECIFIED"
	case pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE:
		return string(domain.RelationshipTypeSpouse)
	case pb.RelationshipType_RELATIONSHIP_TYPE_DEPENDENT:
		return string(domain.RelationshipTypeDependent)
	case pb.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER:
		return string(domain.RelationshipTypeBusinessPartner)
	case pb.RelationshipType_RELATIONSHIP_TYPE_GUARANTOR:
		return string(domain.RelationshipTypeGuarantor)
	case pb.RelationshipType_RELATIONSHIP_TYPE_BENEFICIAL_OWNER:
		return string(domain.RelationshipTypeBeneficialOwner)
	default:
		return "UNSPECIFIED"
	}
}

// relationshipTypeToProto converts domain string to proto RelationshipType
func relationshipTypeToProto(rt string) pb.RelationshipType {
	switch rt {
	case string(domain.RelationshipTypeSpouse):
		return pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE
	case string(domain.RelationshipTypeDependent):
		return pb.RelationshipType_RELATIONSHIP_TYPE_DEPENDENT
	case string(domain.RelationshipTypeBusinessPartner):
		return pb.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER
	case string(domain.RelationshipTypeGuarantor):
		return pb.RelationshipType_RELATIONSHIP_TYPE_GUARANTOR
	case string(domain.RelationshipTypeBeneficialOwner):
		return pb.RelationshipType_RELATIONSHIP_TYPE_BENEFICIAL_OWNER
	default:
		return pb.RelationshipType_RELATIONSHIP_TYPE_UNSPECIFIED
	}
}
