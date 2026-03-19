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
	"github.com/meridianhub/meridian/services/party/verification"
	"github.com/meridianhub/meridian/shared/platform/events"
	"gorm.io/gorm"
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
	// ErrUnknownPartyStatus is returned for unrecognized party status enum values
	ErrUnknownPartyStatus = errors.New("unknown party status")
	// ErrUnknownAssociationStatus is returned for unrecognized association status enum values
	ErrUnknownAssociationStatus = errors.New("unknown association status")
	// ErrUnknownRelationshipType is returned for unrecognized relationship type enum values
	ErrUnknownRelationshipType = errors.New("unknown relationship type")
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
