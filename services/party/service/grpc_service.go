// Package service implements gRPC services for the party reference data domain
package service

import (
	"context"
	"errors"
	"log/slog"
	"os"

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
)

// Repository defines the interface for party persistence operations.
// All methods accept context for cancellation, timeout, and tracing support.
type Repository interface {
	Save(ctx context.Context, party *domain.Party) error
	FindByID(ctx context.Context, partyID uuid.UUID) (*domain.Party, error)
	FindByIDForUpdate(ctx context.Context, partyID uuid.UUID) (*domain.Party, error)
	FindByExternalReference(ctx context.Context, ref, refType string) (*domain.Party, error)
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
