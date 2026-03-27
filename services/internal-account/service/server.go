// Package service implements gRPC services for the internal account domain.
package service

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// Operation status constants for metrics and logging.
const (
	operationStatusSuccess = "success"
	operationStatusFailed  = "failed"

	opStatusAccountNotFound         = "account_not_found"
	opStatusInvalidAccountType      = "invalid_account_type"
	opStatusInvalidStatusTransition = "invalid_status_transition"
	opStatusVersionConflict         = "version_conflict"
	opStatusDuplicateCode           = "duplicate_code"
	opStatusInstrumentNotFound      = "instrument_not_found"
	opStatusInstrumentNotActive     = "instrument_not_active"
	opStatusInstrumentValidationErr = "instrument_validation_error"
	opStatusPositionKeepingError    = "position_keeping_error"
)

// Repository extends the domain repository port with infrastructure-layer methods
// required by the service (e.g. transactional save for atomic outbox publishing).
// Keeping SaveInTx here rather than in domain.Repository preserves the hexagonal
// boundary: the domain package stays free of GORM imports.
type Repository interface {
	domain.Repository

	// SaveInTx persists a new or updated account within the provided transaction.
	// Used when the caller manages the transaction boundary (e.g., to atomically
	// persist the account and publish an outbox event in the same transaction).
	SaveInTx(ctx context.Context, account domain.InternalAccount, tx *gorm.DB) error
}

// Service implements the InternalAccountService gRPC service.
type Service struct {
	pb.UnimplementedInternalAccountServiceServer
	repo                  Repository
	valuationFeatureRepo  *persistence.ValuationFeatureRepository
	lienRepo              *persistence.LienRepository
	positionKeepingClient PositionKeepingClient
	referenceDataClient   ReferenceDataClient
	valuationEngine       ValuationEngine              // Optional: executes valuation method logic
	idempotencyService    idempotency.Service          // Optional: nil = no Redis idempotency guard
	accountTypeCache      *cache.LocalAccountTypeCache // Optional: resolves product_type_code
	outboxPublisher       *events.OutboxPublisher      // Optional: nil = no event publishing
	db                    *gorm.DB                     // Required when outboxPublisher is set
	logger                *slog.Logger
	tracer                *observability.Tracer
}

// NewService creates a new internal account service with minimal dependencies.
// This is primarily used for testing. For production use, prefer NewServiceWithClients.
// Returns an error if repository is nil.
func NewService(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	return &Service{
		repo:   repo,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}, nil
}

// NewServiceWithClients creates a new service with external client dependencies.
// Use this constructor for production deployments where Position Keeping and Reference Data
// service integrations are required.
func NewServiceWithClients(
	repo Repository,
	posKeepingClient PositionKeepingClient,
	refDataClient ReferenceDataClient,
	logger *slog.Logger,
	tracer *observability.Tracer,
) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}

	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	return &Service{
		repo:                  repo,
		positionKeepingClient: posKeepingClient,
		referenceDataClient:   refDataClient,
		logger:                logger,
		tracer:                tracer,
	}, nil
}

// NewServiceWithValuationFeatures creates a new service with valuation feature support.
// This constructor is used when the service needs to manage valuation features
// for internal accounts.
func NewServiceWithValuationFeatures(repo Repository, valuationFeatureRepo *persistence.ValuationFeatureRepository) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	return &Service{
		repo:                 repo,
		valuationFeatureRepo: valuationFeatureRepo,
		logger:               slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}, nil
}

// Option configures optional Service dependencies.
type Option func(*Service)

// WithLienRepo sets the lien repository.
func WithLienRepo(lienRepo *persistence.LienRepository) Option {
	return func(s *Service) {
		s.lienRepo = lienRepo
	}
}

// WithValuationEngine sets the valuation engine.
func WithValuationEngine(engine ValuationEngine) Option {
	return func(s *Service) {
		s.valuationEngine = engine
	}
}

// WithValuationFeatureRepo sets the valuation feature repository.
func WithValuationFeatureRepo(repo *persistence.ValuationFeatureRepository) Option {
	return func(s *Service) {
		s.valuationFeatureRepo = repo
	}
}

// WithIdempotencyService sets the idempotency service for Redis-backed exactly-once guards.
// When nil (default), mutating lien operations proceed without Redis idempotency protection.
func WithIdempotencyService(svc idempotency.Service) Option {
	return func(s *Service) {
		s.idempotencyService = svc
	}
}

// WithAccountTypeCache sets the account type cache for product type resolution.
func WithAccountTypeCache(c *cache.LocalAccountTypeCache) Option {
	return func(s *Service) {
		s.accountTypeCache = c
	}
}

// WithOutboxPublisher sets the outbox publisher and database connection for event publishing.
// When set, InitiateInternalAccount publishes a FacilityCreatedEvent atomically with the save.
func WithOutboxPublisher(publisher *events.OutboxPublisher, db *gorm.DB) Option {
	return func(s *Service) {
		s.outboxPublisher = publisher
		s.db = db
	}
}

// SetOutboxPublisher wires an outbox publisher into an already-constructed service.
// This is the preferred approach when using constructors other than NewServiceFull.
func (s *Service) SetOutboxPublisher(publisher *events.OutboxPublisher, db *gorm.DB) {
	s.outboxPublisher = publisher
	s.db = db
}

// NewServiceFull creates a service with all dependencies using functional options.
func NewServiceFull(
	repo Repository,
	posKeepingClient PositionKeepingClient,
	refDataClient ReferenceDataClient,
	logger *slog.Logger,
	tracer *observability.Tracer,
	opts ...Option,
) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	svc := &Service{
		repo:                  repo,
		positionKeepingClient: posKeepingClient,
		referenceDataClient:   refDataClient,
		logger:                logger,
		tracer:                tracer,
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc, nil
}

// internalBehaviorClasses defines BehaviorClass values that are valid for InternalAccount.
// CUSTOMER is explicitly excluded - only CurrentAccount may use CUSTOMER behavior class.
var internalBehaviorClasses = map[accounttype.BehaviorClass]bool{
	accounttype.BehaviorClassClearing:  true,
	accounttype.BehaviorClassNostro:    true,
	accounttype.BehaviorClassVostro:    true,
	accounttype.BehaviorClassHolding:   true,
	accounttype.BehaviorClassSuspense:  true,
	accounttype.BehaviorClassRevenue:   true,
	accounttype.BehaviorClassExpense:   true,
	accounttype.BehaviorClassInventory: true,
}

// behaviorClassToAccountType maps a BehaviorClass to the corresponding domain AccountType.
// INVENTORY maps to HOLDING because the IBA domain has no separate inventory type.
var behaviorClassToAccountType = map[accounttype.BehaviorClass]domain.AccountType{
	accounttype.BehaviorClassClearing:  domain.AccountTypeClearing,
	accounttype.BehaviorClassNostro:    domain.AccountTypeNostro,
	accounttype.BehaviorClassVostro:    domain.AccountTypeVostro,
	accounttype.BehaviorClassHolding:   domain.AccountTypeHolding,
	accounttype.BehaviorClassSuspense:  domain.AccountTypeSuspense,
	accounttype.BehaviorClassRevenue:   domain.AccountTypeRevenue,
	accounttype.BehaviorClassExpense:   domain.AccountTypeExpense,
	accounttype.BehaviorClassInventory: domain.AccountTypeHolding,
}

// findAccountByID finds an account by its ID (UUID or business ID).
func (s *Service) findAccountByID(ctx context.Context, accountID string) (domain.InternalAccount, error) {
	// First try to parse as UUID
	if id, err := uuid.Parse(accountID); err == nil {
		account, err := s.repo.FindByID(ctx, id)
		if err != nil {
			if errors.Is(err, domain.ErrAccountNotFound) || errors.Is(err, persistence.ErrAccountNotFound) {
				return domain.InternalAccount{}, status.Errorf(codes.NotFound, "account not found: %s", accountID)
			}
			return domain.InternalAccount{}, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
		}
		return account, nil
	}

	// Try to find by business account ID (e.g. IBA-xxx)
	account, err := s.repo.FindByAccountID(ctx, accountID)
	if err == nil {
		return account, nil
	}
	if !errors.Is(err, domain.ErrAccountNotFound) && !errors.Is(err, persistence.ErrAccountNotFound) {
		return domain.InternalAccount{}, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Fall back to account code
	account, err = s.repo.FindByCode(ctx, accountID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) || errors.Is(err, persistence.ErrAccountNotFound) {
			return domain.InternalAccount{}, status.Errorf(codes.NotFound, "account not found: %s", accountID)
		}
		return domain.InternalAccount{}, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}
	return account, nil
}
