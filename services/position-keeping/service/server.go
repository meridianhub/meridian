package service

import (
	"errors"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/adapters/messaging"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
)

// Service initialization errors
var (
	// ErrRepositoryNil is returned when attempting to create a service with a nil repository
	ErrRepositoryNil = errors.New("position keeping service: repository cannot be nil")
	// ErrEventPublisherNil is returned when attempting to create a service with a nil event publisher
	ErrEventPublisherNil = errors.New("position keeping service: event publisher cannot be nil")
	// ErrIdempotencyServiceNil is returned when attempting to create a service with a nil idempotency service
	ErrIdempotencyServiceNil = errors.New("position keeping service: idempotency service cannot be nil")
	// ErrMeasurementRepoNil is returned when attempting to create a service with a nil measurement repository
	ErrMeasurementRepoNil = errors.New("position keeping service: measurement repository cannot be nil")
	// ErrOutboxPublisherNil is returned when attempting to create a service with a nil outbox publisher
	ErrOutboxPublisherNil = errors.New("position keeping service: outbox publisher cannot be nil")
)

// MaxBucketsPerAccountInstrument is the maximum number of distinct buckets allowed
// per account/instrument combination. This is a safety valve to prevent "Infinite Buckets"
// DOS attacks where a malicious or misconfigured instrument could create unbounded
// numbers of buckets. Most legitimate accounts will have far fewer buckets.
const MaxBucketsPerAccountInstrument = 10000

// PositionKeepingService implements the gRPC service for Position Keeping operations.
type PositionKeepingService struct {
	positionkeepingv1.UnimplementedPositionKeepingServiceServer
	repository      domain.FinancialPositionLogRepository
	measurementRepo domain.MeasurementRepository
	eventPublisher  domain.EventPublisher
	outboxPublisher *messaging.OutboxEventPublisher
	idempotency     idempotency.Service
	// instrumentCache is OPTIONAL - if nil, CEL validation is skipped.
	// This allows backwards compatibility with existing deployments.
	instrumentCache InstrumentCache
	// bucketCounter is OPTIONAL - if nil, cardinality checking is skipped.
	// Used to enforce MaxBucketsPerAccountInstrument limit.
	bucketCounter BucketCounter
	// currentAccountClient is OPTIONAL - if nil, Reserve/Available/Free balance
	// computations will fail with a FailedPrecondition error.
	// Used to query active liens (amount blocks) from Current Account service.
	currentAccountClient domain.CurrentAccountClient
	// accountValidator is OPTIONAL - if nil, account validation is skipped.
	// When set along with accountValidationEnabled, validates that accounts exist
	// in Current Account service before creating position logs.
	accountValidator AccountValidator
	// accountValidationEnabled controls whether account validation is performed.
	// Both this flag and accountValidator must be set for validation to occur.
	// Defaults to false for backwards compatibility.
	accountValidationEnabled bool
	// reservationRepo is OPTIONAL - if nil, reservation RPCs return FailedPrecondition.
	reservationRepo domain.ReservationRepository
	// positionRepo is OPTIONAL - if nil, projected balance RPCs return FailedPrecondition.
	positionRepo domain.PositionRepository
	// instrumentResolver is OPTIONAL - if nil, asset RPCs requiring instrument resolution will error.
	// When set, resolves instrument dimension and precision from Reference Data.
	instrumentResolver refdata.InstrumentResolver
}

// Option configures optional dependencies for PositionKeepingService.
type Option func(*PositionKeepingService)

// WithInstrumentCache sets an optional instrument cache for CEL validation.
// If not set or set to nil, CEL validation is skipped for backwards compatibility.
func WithInstrumentCache(cache InstrumentCache) Option {
	return func(s *PositionKeepingService) {
		s.instrumentCache = cache
	}
}

// WithBucketCounter sets an optional bucket counter for cardinality enforcement.
// If not set or set to nil, cardinality checking is skipped.
// When set, the service will reject measurements that would exceed
// MaxBucketsPerAccountInstrument buckets for any account/instrument combination.
func WithBucketCounter(counter BucketCounter) Option {
	return func(s *PositionKeepingService) {
		s.bucketCounter = counter
	}
}

// WithCurrentAccountClient sets an optional client for querying liens from Current Account.
// If not set or set to nil, Reserve/Available/Free balance computations will return
// a FailedPrecondition error, as these balances require lien information.
func WithCurrentAccountClient(client domain.CurrentAccountClient) Option {
	return func(s *PositionKeepingService) {
		s.currentAccountClient = client
	}
}

// WithAccountValidator sets an optional account validator for validating account existence.
// When enabled via WithAccountValidationEnabled, the service will validate that accounts
// exist in the Current Account service before creating position logs.
// If not set or set to nil, account validation is skipped for backwards compatibility.
func WithAccountValidator(validator AccountValidator) Option {
	return func(s *PositionKeepingService) {
		s.accountValidator = validator
	}
}

// WithAccountValidationEnabled enables or disables account validation.
// When set to true and an AccountValidator is configured, the service will validate
// that accounts exist before creating position logs.
// Defaults to false for backwards compatibility.
func WithAccountValidationEnabled(enabled bool) Option {
	return func(s *PositionKeepingService) {
		s.accountValidationEnabled = enabled
	}
}

// WithReservationRepository sets the reservation repository for reservation RPCs.
// If not set, RecordReservation, ReleaseReservation, and GetProjectedBalance return FailedPrecondition.
func WithReservationRepository(repo domain.ReservationRepository) Option {
	return func(s *PositionKeepingService) {
		s.reservationRepo = repo
	}
}

// WithPositionRepository sets the position repository for projected balance queries.
// If not set, GetProjectedBalance returns FailedPrecondition.
func WithPositionRepository(repo domain.PositionRepository) Option {
	return func(s *PositionKeepingService) {
		s.positionRepo = repo
	}
}

// WithInstrumentResolver sets the InstrumentResolver for resolving instrument properties
// (dimension, precision) from Reference Data. If not set, asset instrument resolution
// uses hardcoded defaults for backwards compatibility.
func WithInstrumentResolver(resolver refdata.InstrumentResolver) Option {
	return func(s *PositionKeepingService) {
		s.instrumentResolver = resolver
	}
}

// NewPositionKeepingService creates a new PositionKeepingService with dependency injection.
//
// Dependencies:
//   - repository: Persistence layer for financial position logs (must not be nil)
//   - measurementRepo: Persistence layer for measurements (must not be nil)
//   - eventPublisher: Publishes domain events (must not be nil)
//   - idempotencySvc: Ensures exactly-once processing of idempotent operations (must not be nil)
//   - outboxPublisher: Publishes events via transactional outbox (must not be nil)
//
// Optional dependencies can be provided via Option functions:
//   - WithInstrumentCache: Enables CEL validation of measurements against instrument definitions
//
// Returns an error if any required dependency is nil.
func NewPositionKeepingService(
	repository domain.FinancialPositionLogRepository,
	measurementRepo domain.MeasurementRepository,
	eventPublisher domain.EventPublisher,
	idempotencySvc idempotency.Service,
	outboxPublisher *messaging.OutboxEventPublisher,
	opts ...Option,
) (*PositionKeepingService, error) {
	if repository == nil {
		return nil, ErrRepositoryNil
	}
	if measurementRepo == nil {
		return nil, ErrMeasurementRepoNil
	}
	if eventPublisher == nil {
		return nil, ErrEventPublisherNil
	}
	if idempotencySvc == nil {
		return nil, ErrIdempotencyServiceNil
	}
	if outboxPublisher == nil {
		return nil, ErrOutboxPublisherNil
	}

	svc := &PositionKeepingService{
		repository:      repository,
		measurementRepo: measurementRepo,
		eventPublisher:  eventPublisher,
		outboxPublisher: outboxPublisher,
		idempotency:     idempotencySvc,
	}

	// Apply optional configurations
	for _, opt := range opts {
		opt(svc)
	}

	return svc, nil
}

