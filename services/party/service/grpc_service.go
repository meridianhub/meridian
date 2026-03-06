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
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/services/party/verification"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// fromJSONB extracts a string from JSONB storage.
// If the stored value is a JSON string, it's unmarshaled.
// Otherwise, the raw value is returned.
func fromJSONB(s string) string {
	var result string
	if err := json.Unmarshal([]byte(s), &result); err == nil {
		return result
	}
	return s
}

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
	// SaveInTx persists a party within an existing database transaction.
	// Use this when the caller manages the transaction boundary (e.g., to include
	// an outbox event write in the same transaction for atomicity).
	SaveInTx(ctx context.Context, party *domain.Party, tx *gorm.DB) error
	FindByID(ctx context.Context, partyID uuid.UUID) (*domain.Party, error)
	FindByIDForUpdate(ctx context.Context, partyID uuid.UUID) (*domain.Party, error)
	FindByExternalReference(ctx context.Context, ref, refType string) (*domain.Party, error)

	// Business Qualifier operations
	SaveAssociation(ctx context.Context, partyID, relatedPartyID uuid.UUID, relationshipType string) (uuid.UUID, error)
	FindAssociations(ctx context.Context, partyID uuid.UUID) ([]persistence.PartyAssociationEntity, error)
	UpdateAssociation(ctx context.Context, associationID uuid.UUID, relationshipType string) (*persistence.PartyAssociationEntity, error)
	CheckCircularAssociation(ctx context.Context, partyID, relatedPartyID uuid.UUID) (bool, error)

	SaveDemographic(ctx context.Context, partyID uuid.UUID, socioEconomicData, employmentHistory string) error
	FindDemographic(ctx context.Context, partyID uuid.UUID) (*persistence.PartyDemographicEntity, error)

	SaveReference(ctx context.Context, partyID uuid.UUID, refType, refValue, issuingAuthority, expiryDate string) error
	SaveReferences(ctx context.Context, partyID uuid.UUID, refs []persistence.ReferenceInput) error
	FindReferences(ctx context.Context, partyID uuid.UUID) ([]persistence.PartyReferenceEntity, error)

	SaveBankRelation(ctx context.Context, partyID uuid.UUID, accountOfficerID, relationshipManagerID, assignedBranch string) error
	FindBankRelation(ctx context.Context, partyID uuid.UUID) (*persistence.PartyBankRelationEntity, error)

	// Syndicate participant operations
	SaveAssociationWithInput(ctx context.Context, partyID, relatedPartyID uuid.UUID, relationshipType string, input *persistence.AssociationInput) (uuid.UUID, error)
	ListParticipants(ctx context.Context, orgPartyID uuid.UUID, relationshipType string) ([]persistence.PartyAssociationEntity, error)
	GetStructuringData(ctx context.Context, partyID, orgPartyID uuid.UUID, relationshipType string) (map[string]interface{}, error)

	// List operations
	ListParties(ctx context.Context, params persistence.ListPartiesParams) (*persistence.ListPartiesResult, error)
}

// Service implements the PartyService gRPC service
type Service struct {
	pb.UnimplementedPartyServiceServer
	repo                 Repository
	pmRepo               PaymentMethodRepository
	verificationProvider verification.Provider
	partyTypeService     *PartyTypeDefinitionService
	attributeValidator   *AttributeValidator
	outboxPublisher      *events.OutboxPublisher
	db                   *gorm.DB // raw connection for wrapping transactions with outbox writes
	logger               *slog.Logger
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

// WithPaymentMethodRepository sets the payment method repository on the service.
// When set, payment method gRPC operations are enabled.
func (s *Service) WithPaymentMethodRepository(pmRepo PaymentMethodRepository) *Service {
	s.pmRepo = pmRepo
	return s
}

// WithVerificationProvider sets the KYC/AML verification provider on the service.
// When set, ExchangeDemographics delegates to the real provider instead of returning a stub.
func (s *Service) WithVerificationProvider(provider verification.Provider) *Service {
	s.verificationProvider = provider
	return s
}

// WithAttributeValidator sets the attribute validator on the service.
// When set, RegisterParty and UpdateParty validate attributes against the tenant's
// PartyTypeDefinition schema and CEL rules.
func (s *Service) WithAttributeValidator(v *AttributeValidator) *Service {
	s.attributeValidator = v
	return s
}

// WithOutboxPublisher enables transactional event publishing via the outbox pattern.
// When set, RegisterParty and UpdateParty publish domain events atomically with the party save.
// db must be the same *gorm.DB instance used by the repository.
func (s *Service) WithOutboxPublisher(publisher *events.OutboxPublisher, db *gorm.DB) *Service {
	s.outboxPublisher = publisher
	s.db = db
	return s
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

	// Set optional attributes if provided at registration time.
	if len(req.Attributes) > 0 {
		party.SetAttributes(protoAttributesToDomain(req.Attributes))
	}

	// === Attribute validation ===

	// Validate attributes against the tenant's PartyTypeDefinition schema and CEL rules.
	// Validation is skipped when no validator is configured or no tenant context is present.
	if s.attributeValidator != nil {
		if tid, ok := tenant.FromContext(ctx); ok {
			if err := s.attributeValidator.ValidateAttributes(ctx, tid.String(), string(partyType), party); err != nil {
				s.logger.Warn("attribute validation failed during registration",
					"party_type", partyType,
					"tenant_id", tid.String(),
					"error", err)
				return nil, status.Errorf(codes.InvalidArgument, "attribute validation failed: %v", err)
			}
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

	// Save party and publish event atomically
	if err := s.savePartyWithEvent(ctx, party, func(tx *gorm.DB) error {
		event := &eventsv1.PartyCreatedEvent{
			EventId:   uuid.New().String(),
			PartyId:   party.ID().String(),
			PartyType: string(party.PartyType()),
			LegalName: party.LegalName(),
			Status:    string(party.Status()),
			Timestamp: timestamppb.New(time.Now().UTC()),
		}
		return s.outboxPublisher.Publish(ctx, tx, event, events.PublishConfig{
			EventType:     "party.created.v1",
			AggregateID:   party.ID().String(),
			AggregateType: "Party",
			Topic:         topics.PartyCreatedV1,
		})
	}); err != nil {
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

// ListParties returns a paginated list of parties with optional filtering.
func (s *Service) ListParties(ctx context.Context, req *pb.ListPartiesRequest) (*pb.ListPartiesResponse, error) {
	// Apply defaults and validate page size
	pageSize := int(req.PageSize)
	if pageSize <= 0 {
		pageSize = 25
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// Decode cursor
	cursor, err := persistence.DecodePartyCursor(req.PageToken)
	if err != nil {
		s.logger.Warn("invalid page_token", "page_token", req.PageToken, "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
	}

	// Build filter params
	params := persistence.ListPartiesParams{
		Limit:       pageSize,
		Cursor:      cursor,
		SearchQuery: req.SearchQuery,
	}

	if req.PartyType != pb.PartyType_PARTY_TYPE_UNSPECIFIED {
		partyType, err := protoToPartyType(req.PartyType)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid party_type: %v", err)
		}
		params.PartyType = string(partyType)
	}

	if req.Status != pb.PartyStatus_PARTY_STATUS_UNSPECIFIED {
		params.Status = protoToPartyStatus(req.Status)
	}

	result, err := s.repo.ListParties(ctx, params)
	if err != nil {
		s.logger.Error("failed to list parties", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to list parties: %v", err)
	}

	pbParties := make([]*pb.Party, len(result.Parties))
	for i, p := range result.Parties {
		pbParties[i] = domainToProto(p)
	}

	return &pb.ListPartiesResponse{
		Parties:       pbParties,
		NextPageToken: result.NextCursor,
		TotalCount:    result.TotalCount,
	}, nil
}

// protoToPartyStatus converts a proto PartyStatus to domain string
func protoToPartyStatus(s pb.PartyStatus) string {
	switch s {
	case pb.PartyStatus_PARTY_STATUS_ACTIVE:
		return string(domain.PartyStatusActive)
	case pb.PartyStatus_PARTY_STATUS_RESTRICTED:
		return string(domain.PartyStatusRestricted)
	case pb.PartyStatus_PARTY_STATUS_SUSPENDED:
		return string(domain.PartyStatusSuspended)
	case pb.PartyStatus_PARTY_STATUS_TERMINATED:
		return string(domain.PartyStatusTerminated)
	case pb.PartyStatus_PARTY_STATUS_UNSPECIFIED:
		return ""
	}
	return ""
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
		Attributes:            domainAttributesToProto(party.Attributes()),
		CreatedAt:             timestamppb.New(party.CreatedAt()),
		UpdatedAt:             timestamppb.New(party.UpdatedAt()),
		// #nosec G115 - Version is bounded by database constraints
		Version: int32(party.Version()),
	}
}

// domainAttributesToProto converts domain AttributeEntry slice to proto AttributeEntry slice.
func domainAttributesToProto(attrs []domain.AttributeEntry) []*quantityv1.AttributeEntry {
	result := make([]*quantityv1.AttributeEntry, len(attrs))
	for i, a := range attrs {
		result[i] = &quantityv1.AttributeEntry{Key: a.Key, Value: a.Value}
	}
	return result
}

// protoAttributesToDomain converts proto AttributeEntry slice to domain AttributeEntry slice.
// Nil proto entries are skipped.
func protoAttributesToDomain(attrs []*quantityv1.AttributeEntry) []domain.AttributeEntry {
	result := make([]domain.AttributeEntry, 0, len(attrs))
	for _, a := range attrs {
		if a != nil {
			result = append(result, domain.AttributeEntry{Key: a.Key, Value: a.Value})
		}
	}
	return result
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
	attributesUpdated := false
	if req.UpdateMask != nil && len(req.UpdateMask.Paths) > 0 {
		for _, path := range req.UpdateMask.Paths {
			if path == "attributes" {
				attributesUpdated = true
			}
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

	// Validate attributes if they were updated and a validator is configured.
	// Validation is skipped when no validator is configured or no tenant context is present.
	if attributesUpdated && s.attributeValidator != nil {
		if tid, ok := tenant.FromContext(ctx); ok {
			partyType := string(party.PartyType())
			if err := s.attributeValidator.ValidateAttributes(ctx, tid.String(), partyType, party); err != nil {
				s.logger.Warn("attribute validation failed during update",
					"party_id", req.PartyId,
					"party_type", partyType,
					"tenant_id", tid.String(),
					"error", err)
				return nil, status.Errorf(codes.InvalidArgument, "attribute validation failed: %v", err)
			}
		}
	}

	// Persist updated party and publish event atomically
	if err := s.savePartyWithEvent(ctx, party, func(tx *gorm.DB) error {
		event := &eventsv1.PartyUpdatedEvent{
			EventId:   uuid.New().String(),
			PartyId:   party.ID().String(),
			PartyType: string(party.PartyType()),
			Status:    string(party.Status()),
			Timestamp: timestamppb.New(time.Now().UTC()),
		}
		return s.outboxPublisher.Publish(ctx, tx, event, events.PublishConfig{
			EventType:     "party.updated.v1",
			AggregateID:   party.ID().String(),
			AggregateType: "Party",
			Topic:         topics.PartyUpdatedV1,
		})
	}); err != nil {
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
	case "attributes":
		party.SetAttributes(protoAttributesToDomain(req.Attributes))
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

	// Set actor_id in context for audit trail if provided
	saveCtx := ctx
	if req.ActorId != "" {
		saveCtx = context.WithValue(ctx, auth.UserIDContextKey, req.ActorId)
	}

	// Persist updated party and publish control event atomically
	actionTime := time.Now()
	if err := s.savePartyWithEvent(saveCtx, party, func(tx *gorm.DB) error {
		event := &eventsv1.PartyControlledEvent{
			EventId:       uuid.New().String(),
			PartyId:       party.ID().String(),
			ControlAction: string(action),
			NewStatus:     string(party.Status()),
			Reason:        req.Reason,
			ActorId:       req.ActorId,
			Timestamp:     timestamppb.New(actionTime),
		}
		return s.outboxPublisher.Publish(saveCtx, tx, event, events.PublishConfig{
			EventType:     "party.controlled.v1",
			AggregateID:   party.ID().String(),
			AggregateType: "Party",
			Topic:         topics.PartyControlledV1,
		})
	}); err != nil {
		s.logger.Error("failed to save party after control action", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to save party: %v", err)
	}

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

// UpdateReference adds party reference data.
// NOTE: This method creates new reference records rather than updating existing ones.
// Multiple references of the same type (e.g., multiple passports) are allowed.
// To implement true update-or-insert behavior, a unique constraint on
// (party_id, reference_type) would be needed.
func (s *Service) UpdateReference(ctx context.Context, req *pb.UpdateReferenceRequest) (*pb.UpdateReferenceResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		s.logger.Error("invalid party ID format", "party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Verify party exists with FOR UPDATE lock to prevent deletion during update.
	// The FK constraint on party_reference provides additional safety.
	if _, err := s.repo.FindByIDForUpdate(ctx, partyID); err != nil {
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
	relationshipType := protoToRelationshipType(req.RelationshipType)
	var input *persistence.AssociationInput
	if req.Metadata != nil || req.Status != pb.AssociationStatus_ASSOCIATION_STATUS_UNSPECIFIED || req.EffectiveFrom != nil || req.EffectiveTo != nil {
		input = &persistence.AssociationInput{}
		if req.Metadata != nil {
			metadataBytes, marshalErr := json.Marshal(req.Metadata.AsMap())
			if marshalErr == nil {
				metadataStr := string(metadataBytes)
				input.Metadata = &metadataStr
			}
		}
		if req.Status != pb.AssociationStatus_ASSOCIATION_STATUS_UNSPECIFIED {
			input.Status = protoAssociationStatusToString(req.Status)
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

// Association status string constants
const (
	associationStatusActive     = "ACTIVE"
	associationStatusSuspended  = "SUSPENDED"
	associationStatusTerminated = "TERMINATED"
)

// protoAssociationStatusToString converts proto AssociationStatus to string
func protoAssociationStatusToString(s pb.AssociationStatus) string {
	switch s {
	case pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE:
		return associationStatusActive
	case pb.AssociationStatus_ASSOCIATION_STATUS_SUSPENDED:
		return associationStatusSuspended
	case pb.AssociationStatus_ASSOCIATION_STATUS_TERMINATED:
		return associationStatusTerminated
	case pb.AssociationStatus_ASSOCIATION_STATUS_UNSPECIFIED:
		return associationStatusActive
	default:
		return associationStatusActive
	}
}

// UpdateAssociations updates a party association
func (s *Service) UpdateAssociations(ctx context.Context, req *pb.UpdateAssociationsRequest) (*pb.UpdateAssociationsResponse, error) {
	associationID, err := uuid.Parse(req.AssociationId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid association ID format: %v", err)
	}

	relationshipType := protoToRelationshipType(req.RelationshipType)
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

// ExchangeDemographics verifies party demographics data
func (s *Service) ExchangeDemographics(ctx context.Context, req *pb.ExchangeDemographicsRequest) (*pb.ExchangeDemographicsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Retrieve party for verification
	party, err := s.repo.FindByID(ctx, partyID)
	if err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return nil, status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve party: %v", err)
	}

	// If no verification provider is configured, fall back to stub behavior
	if s.verificationProvider == nil {
		return s.exchangeDemographicsStub(req.PartyId)
	}

	// Delegate to the real verification provider
	result, err := s.verificationProvider.VerifyIdentity(ctx, party)
	if err != nil {
		s.logger.Error("verification provider error",
			"party_id", req.PartyId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "verification failed: %v", err)
	}

	verificationStatus := string(result.Status)

	s.logger.Info("identity verification completed",
		"party_id", req.PartyId,
		"verification_id", result.VerificationID,
		"status", verificationStatus,
		"risk_score", result.RiskScore)

	// Sanctions screening - errors warn but do not fail the request
	sanctionsResult, err := s.verificationProvider.CheckSanctions(ctx, party)
	if err != nil {
		s.logger.Warn("sanctions screening failed, proceeding with identity result",
			"party_id", req.PartyId,
			"error", err)
	} else if sanctionsResult.Status == verification.SanctionsStatusMatch {
		verificationStatus = string(verification.StatusManualReview)
		s.logger.Warn("sanctions match found, overriding status to MANUAL_REVIEW",
			"party_id", req.PartyId,
			"screening_id", sanctionsResult.ScreeningID,
			"match_count", len(sanctionsResult.Matches))
	}

	return &pb.ExchangeDemographicsResponse{
		PartyId:               req.PartyId,
		VerificationStatus:    verificationStatus,
		VerificationTimestamp: timestamppb.Now(),
	}, nil
}

// exchangeDemographicsStub returns a stub response when no verification provider is configured.
// In production, this returns Unimplemented to prevent operating without a real provider.
// In development/test environments, it returns a stub VERIFIED response.
func (s *Service) exchangeDemographicsStub(partyID string) (*pb.ExchangeDemographicsResponse, error) {
	if os.Getenv("ENVIRONMENT") == "production" {
		return nil, status.Error(codes.Unimplemented,
			"KYC/AML verification not implemented - no verification provider configured")
	}

	s.logger.Warn("using stub KYC verification - no provider configured",
		"party_id", partyID,
		"environment", os.Getenv("ENVIRONMENT"))

	return &pb.ExchangeDemographicsResponse{
		PartyId:               partyID,
		VerificationStatus:    "VERIFIED",
		VerificationTimestamp: timestamppb.Now(),
	}, nil
}

// UpdateDemographics updates party demographics data
func (s *Service) UpdateDemographics(ctx context.Context, req *pb.UpdateDemographicsRequest) (*pb.UpdateDemographicsResponse, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	// Verify party exists with FOR UPDATE lock to prevent deletion during update.
	// The FK constraint on party_demographic provides additional safety.
	if _, err := s.repo.FindByIDForUpdate(ctx, partyID); err != nil {
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
			// Extract string from JSONB - handles both JSON strings and raw values
			resp.SocioEconomicData = fromJSONB(*demo.SocioEconomicData)
		}
		if demo.EmploymentHistory != nil {
			// Extract string from JSONB - handles both JSON strings and raw values
			resp.EmploymentHistory = fromJSONB(*demo.EmploymentHistory)
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

	// Verify party exists with FOR UPDATE lock to prevent deletion during update.
	// The FK constraint on party_bank_relation provides additional safety.
	if _, err := s.repo.FindByIDForUpdate(ctx, partyID); err != nil {
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

// ListParticipants returns all active participants for a syndicate (org party).
func (s *Service) ListParticipants(ctx context.Context, req *pb.ListParticipantsRequest) (*pb.ListParticipantsResponse, error) {
	orgPartyID, err := uuid.Parse(req.OrgPartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid org_party_id format: %v", err)
	}

	relationshipType := protoToRelationshipType(req.RelationshipType)

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

	relationshipType := protoToRelationshipType(req.RelationshipType)

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

// associationEntityToProto converts a persistence entity to a proto Association message.
func associationEntityToProto(entity *persistence.PartyAssociationEntity) *pb.Association {
	assoc := &pb.Association{
		AssociationId:    entity.ID.String(),
		PartyId:          entity.PartyID.String(),
		RelatedPartyId:   entity.RelatedPartyID.String(),
		RelationshipType: relationshipTypeToProto(entity.RelationshipType),
		CreatedAt:        timestamppb.New(entity.CreatedAt),
		UpdatedAt:        timestamppb.New(entity.UpdatedAt),
		Status:           associationStatusToProto(entity.Status),
		EffectiveFrom:    timestamppb.New(entity.EffectiveFrom),
	}

	if entity.EffectiveTo != nil {
		assoc.EffectiveTo = timestamppb.New(*entity.EffectiveTo)
	}

	if entity.Metadata != nil && *entity.Metadata != "" && *entity.Metadata != "{}" {
		var metadataMap map[string]interface{}
		if err := json.Unmarshal([]byte(*entity.Metadata), &metadataMap); err == nil {
			if pbStruct, err := structpb.NewStruct(metadataMap); err == nil {
				assoc.Metadata = pbStruct
			}
		}
	}

	return assoc
}

// associationStatusToProto converts a status string to proto AssociationStatus
func associationStatusToProto(status string) pb.AssociationStatus {
	switch status {
	case associationStatusActive:
		return pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE
	case associationStatusSuspended:
		return pb.AssociationStatus_ASSOCIATION_STATUS_SUSPENDED
	case associationStatusTerminated:
		return pb.AssociationStatus_ASSOCIATION_STATUS_TERMINATED
	default:
		return pb.AssociationStatus_ASSOCIATION_STATUS_UNSPECIFIED
	}
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
	case pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT:
		return string(domain.RelationshipTypeSyndicateParticipant)
	case pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_HOST:
		return string(domain.RelationshipTypeSyndicateHost)
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
	case string(domain.RelationshipTypeSyndicateParticipant):
		return pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT
	case string(domain.RelationshipTypeSyndicateHost):
		return pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_HOST
	default:
		return pb.RelationshipType_RELATIONSHIP_TYPE_UNSPECIFIED
	}
}

// savePartyWithEvent persists a party and, if an outbox publisher is configured, atomically
// writes an event to the outbox within the same database transaction.
//
// publishFn is called with the active transaction and should call outboxPublisher.Publish.
// If no outbox publisher is configured, the party is saved without event publishing.
func (s *Service) savePartyWithEvent(ctx context.Context, party *domain.Party, publishFn func(tx *gorm.DB) error) error {
	if s.outboxPublisher == nil || s.db == nil {
		// No outbox configured — fall back to plain save (no event)
		return s.repo.Save(ctx, party)
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.repo.SaveInTx(ctx, party, tx); err != nil {
			return err
		}
		return publishFn(tx)
	})
}
