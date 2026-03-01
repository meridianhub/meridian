// Package service implements gRPC services for the internal account domain.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	ibaobservability "github.com/meridianhub/meridian/services/internal-account/observability"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	vf "github.com/meridianhub/meridian/shared/pkg/valuationfeature"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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

// Service implements the InternalAccountService gRPC service.
type Service struct {
	pb.UnimplementedInternalAccountServiceServer
	repo                  domain.Repository
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
func NewService(repo domain.Repository) (*Service, error) {
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
	repo domain.Repository,
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
func NewServiceWithValuationFeatures(repo domain.Repository, valuationFeatureRepo *persistence.ValuationFeatureRepository) (*Service, error) {
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
	repo domain.Repository,
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
// CUSTOMER is explicitly excluded — only CurrentAccount may use CUSTOMER behavior class.
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

// InitiateInternalAccount creates a new internal account.
func (s *Service) InitiateInternalAccount(ctx context.Context, req *pb.InitiateInternalAccountRequest) (*pb.InitiateInternalAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("initiate_internal_account", operationStatus, time.Since(start))
	}()

	// 1. Determine product_type_code (required for new accounts).
	productTypeCode := req.ProductTypeCode

	var accountType domain.AccountType
	var clearingPurpose domain.ClearingPurpose
	var dimension string
	var productTypeVersion int
	var productTypeDef *accounttype.Definition

	// 2. Product type resolution via cache (if cache is configured and code is available)
	if s.accountTypeCache != nil && productTypeCode != "" {
		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			operationStatus = operationStatusFailed
			return nil, status.Error(codes.InvalidArgument, "tenant context required")
		}

		cached, err := s.accountTypeCache.GetOrLoad(ctx, tenantID, productTypeCode)
		if err != nil {
			operationStatus = operationStatusFailed
			s.logger.Warn("product type resolution failed",
				"product_type_code", productTypeCode,
				"error", err)
			return nil, status.Errorf(codes.InvalidArgument, "product type not found: %s", productTypeCode)
		}

		if cached.Definition == nil {
			operationStatus = operationStatusFailed
			return nil, status.Errorf(codes.Internal, "product type %s has no definition", productTypeCode)
		}

		def := cached.Definition

		// 3. BehaviorClass gating: must NOT be CUSTOMER
		if !internalBehaviorClasses[def.BehaviorClass] {
			operationStatus = operationStatusFailed
			return nil, status.Errorf(codes.InvalidArgument,
				"product type %s has behavior class %s which is not an internal account type",
				productTypeCode, def.BehaviorClass)
		}

		// 4. EligibilityCEL evaluation (skip if empty or "true")
		if cached.EligibilityProgram != nil && def.EligibilityCEL != "" && def.EligibilityCEL != "true" {
			// For internal accounts, eligibility checks evaluate without a party.
			// The CEL environment receives account-level context only.
			activation := map[string]interface{}{
				"instrument_code": req.InstrumentCode,
				"account_code":    req.AccountCode,
			}
			out, _, evalErr := cached.EligibilityProgram.Eval(activation)
			if evalErr != nil {
				operationStatus = operationStatusFailed
				return nil, status.Errorf(codes.Internal, "eligibility evaluation failed: %v", evalErr)
			}
			eligible, isBool := out.Value().(bool)
			if !isBool || !eligible {
				operationStatus = operationStatusFailed
				return nil, status.Errorf(codes.FailedPrecondition,
					"account not eligible per product type %s eligibility rules", productTypeCode)
			}
		}

		// 5. Attribute validation against AttributeSchema (if defined)
		if cached.CompiledSchema != nil {
			attrsMap := map[string]interface{}{}
			if req.Attributes != nil {
				attrsMap = req.Attributes.AsMap()
			}
			attrsJSON, err := json.Marshal(attrsMap)
			if err != nil {
				operationStatus = operationStatusFailed
				return nil, status.Errorf(codes.InvalidArgument, "invalid attributes: %v", err)
			}
			var attrs interface{}
			if err := json.Unmarshal(attrsJSON, &attrs); err != nil {
				operationStatus = operationStatusFailed
				return nil, status.Errorf(codes.InvalidArgument, "invalid attributes JSON: %v", err)
			}
			if err := cached.CompiledSchema.Validate(attrs); err != nil {
				operationStatus = operationStatusFailed
				return nil, status.Errorf(codes.InvalidArgument, "attributes validation failed: %v", err)
			}
		}

		// Derive account type from BehaviorClass via mapping
		accountType = behaviorClassToAccountType[def.BehaviorClass]
		productTypeVersion = def.Version
		productTypeDef = def

		// Derive dimension from instrument via Reference Data (if available)
	} else if s.accountTypeCache == nil && req.ProductTypeCode != "" {
		// product_type_code was explicitly provided but cache is not configured
		operationStatus = operationStatusFailed
		return nil, status.Error(codes.FailedPrecondition,
			"product type resolution not available; configure account type cache")
	} else if productTypeCode == "" {
		// product_type_code is required - the deprecated account_type enum has been removed
		operationStatus = opStatusInvalidAccountType
		return nil, status.Error(codes.InvalidArgument,
			"product_type_code is required; the deprecated account_type enum has been removed")
	}

	// Convert clearing purpose from proto to domain
	var err error
	clearingPurpose, err = protoToClearingPurpose(req.ClearingPurpose)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "invalid clearing purpose: %v", err)
	}

	// Validate instrument exists and is ACTIVE via Reference Data service (if client is configured)
	if s.referenceDataClient != nil {
		validationStart := time.Now()
		refDataCtx, refDataCancel := context.WithTimeout(ctx, 5*time.Second)
		defer refDataCancel()

		refDataResp, err := s.referenceDataClient.RetrieveInstrument(refDataCtx, &referencedatav1.RetrieveInstrumentRequest{
			Code: req.InstrumentCode,
		})
		if err != nil {
			validationDuration := time.Since(validationStart)
			errCode := status.Code(err)
			s.logger.Warn("instrument validation failed",
				"instrument_code", req.InstrumentCode,
				"error", err)

			if errCode == codes.NotFound {
				operationStatus = opStatusInstrumentNotFound
				ibaobservability.RecordInstrumentValidation("not_found", validationDuration)
				return nil, status.Errorf(codes.InvalidArgument, "instrument not found: %s", req.InstrumentCode)
			}
			if errCode == codes.DeadlineExceeded || errCode == codes.Canceled {
				operationStatus = opStatusInstrumentValidationErr
				ibaobservability.RecordInstrumentValidation("timeout", validationDuration)
				return nil, status.Errorf(codes.DeadlineExceeded, "instrument validation timed out for: %s", req.InstrumentCode)
			}
			operationStatus = opStatusInstrumentValidationErr
			ibaobservability.RecordInstrumentValidation("error", validationDuration)
			return nil, status.Errorf(codes.Internal, "failed to validate instrument: %v", err)
		}

		// Guard against nil instrument in response (defensive programming)
		if refDataResp.Instrument == nil {
			validationDuration := time.Since(validationStart)
			operationStatus = opStatusInstrumentValidationErr
			s.logger.Error("reference data service returned nil instrument",
				"instrument_code", req.InstrumentCode)
			ibaobservability.RecordInstrumentValidation("error", validationDuration)
			return nil, status.Errorf(codes.Internal, "reference data service returned invalid response for instrument: %s", req.InstrumentCode)
		}

		// Validate instrument status is ACTIVE
		if refDataResp.Instrument.Status != referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE {
			validationDuration := time.Since(validationStart)
			operationStatus = opStatusInstrumentNotActive
			s.logger.Warn("instrument not active",
				"instrument_code", req.InstrumentCode,
				"status", refDataResp.Instrument.Status.String())
			ibaobservability.RecordInstrumentValidation("not_active", validationDuration)
			return nil, status.Errorf(codes.InvalidArgument, "instrument %s is not active (status: %s)",
				req.InstrumentCode, refDataResp.Instrument.Status.String())
		}

		// Extract dimension from validated instrument (strip DIMENSION_ prefix for domain consistency)
		dimension = strings.TrimPrefix(refDataResp.Instrument.Dimension.String(), "DIMENSION_")
		ibaobservability.RecordInstrumentValidation("success", time.Since(validationStart))
	}

	// Generate account ID using full UUID for uniqueness
	accountID := fmt.Sprintf("IBA-%s", uuid.New().String())

	// Create domain entity with dimension from Reference Data
	account, err := domain.NewInternalAccount(
		accountID,
		req.AccountCode,
		req.Name,
		accountType,
		clearingPurpose,
		req.InstrumentCode,
		dimension,
	)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, mapDomainErrorToGRPC(err)
	}

	// Set product type fields on the account via builder rebuild
	if productTypeCode != "" {
		account = domain.NewInternalAccountBuilder().
			WithID(account.ID()).
			WithAccountID(account.AccountID()).
			WithAccountCode(account.AccountCode()).
			WithName(account.Name()).
			WithAccountType(account.AccountType()).
			WithClearingPurpose(account.ClearingPurpose()).
			WithInstrumentCode(account.InstrumentCode()).
			WithDimension(account.Dimension()).
			WithStatus(account.Status()).
			WithOrgPartyID(account.OrgPartyID()).
			WithCounterparty(account.Counterparty()).
			WithAttributes(account.Attributes()).
			WithProductTypeCode(productTypeCode).
			WithProductTypeVersion(productTypeVersion).
			WithVersion(account.Version()).
			WithCreatedAt(account.CreatedAt()).
			WithUpdatedAt(account.UpdatedAt()).
			Build()
	}

	// Handle counterparty details for NOSTRO/VOSTRO accounts
	if req.CounterpartyDetails != nil {
		counterparty, err := domain.NewCounterpartyDetailsWithOptions(
			req.CounterpartyDetails.CounterpartyId,
			req.CounterpartyDetails.CounterpartyName,
			req.CounterpartyDetails.CounterpartyExternalRef,
			req.CounterpartyDetails.Attributes,
		)
		if err != nil {
			operationStatus = operationStatusFailed
			return nil, status.Errorf(codes.InvalidArgument, "invalid counterparty details: %v", err)
		}
		account, err = account.UpdateCounterparty(counterparty)
		if err != nil {
			operationStatus = operationStatusFailed
			return nil, mapDomainErrorToGRPC(err)
		}
	} else if accountType.RequiresCorrespondent() {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "counterparty details required for %s accounts", accountType)
	}

	// Persist via repository (atomically with outbox event when publisher is configured)
	if err := s.saveAccountWithEvent(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrDuplicateCode) {
			operationStatus = opStatusDuplicateCode
			return nil, status.Errorf(codes.AlreadyExists, "account code already exists: %s", req.AccountCode)
		}
		operationStatus = operationStatusFailed
		s.logger.Error("failed to save account", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to create account: %v", err)
	}

	// Seed ValuationFeatures from product type templates using upsert semantics.
	if s.valuationFeatureRepo != nil && productTypeDef != nil && len(productTypeDef.ValuationMethods) > 0 {
		seeder := vf.NewProductTypeSeeder(s.valuationFeatureRepo)
		if err := seeder.SeedFromProductType(ctx, account.ID(), productTypeDef, time.Now().UTC()); err != nil {
			s.logger.Warn("failed to seed valuation features from product type",
				"account_id", accountID,
				"product_type_code", productTypeCode,
				"error", err)
		}
	}

	// Record metric for account creation
	ibaobservability.RecordAccountCreated(accountType.String())

	s.logger.Info("created internal account",
		"account_id", accountID,
		"account_code", req.AccountCode,
		"account_type", accountType.String(),
		"product_type_code", productTypeCode)

	return &pb.InitiateInternalAccountResponse{
		AccountId: accountID,
		Facility:  toProtoFacility(account),
	}, nil
}

// UpdateInternalAccount modifies account settings.
func (s *Service) UpdateInternalAccount(ctx context.Context, req *pb.UpdateInternalAccountRequest) (*pb.UpdateInternalAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("update_internal_account", operationStatus, time.Since(start))
	}()

	// Load existing account
	accountUUID, err := uuid.Parse(req.AccountId)
	if err != nil {
		// Try finding by account_id string if not a UUID
		account, err := s.findAccountByID(ctx, req.AccountId)
		if err != nil {
			operationStatus = opStatusAccountNotFound
			return nil, err
		}
		return s.updateAccount(ctx, account, req, &operationStatus)
	}

	account, err := s.repo.FindByID(ctx, accountUUID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	return s.updateAccount(ctx, account, req, &operationStatus)
}

func (s *Service) updateAccount(ctx context.Context, account domain.InternalAccount, req *pb.UpdateInternalAccountRequest, operationStatus *string) (*pb.UpdateInternalAccountResponse, error) {
	// Check version for optimistic locking if provided
	if req.ExpectedVersion > 0 && int64(req.ExpectedVersion) != account.Version() {
		*operationStatus = opStatusVersionConflict
		return nil, status.Errorf(codes.Aborted, "version mismatch: expected %d, got %d", req.ExpectedVersion, account.Version())
	}

	// Cannot update closed accounts
	if account.Status() == domain.AccountStatusClosed {
		*operationStatus = operationStatusFailed
		return nil, status.Error(codes.FailedPrecondition, "cannot update closed account")
	}

	var err error

	// Update name if provided
	if req.Name != "" {
		account, err = account.Rename(req.Name)
		if err != nil {
			*operationStatus = operationStatusFailed
			return nil, mapDomainErrorToGRPC(err)
		}
	}

	// Update counterparty details if provided
	if req.CounterpartyDetails != nil {
		counterparty, err := domain.NewCounterpartyDetailsWithOptions(
			req.CounterpartyDetails.CounterpartyId,
			req.CounterpartyDetails.CounterpartyName,
			req.CounterpartyDetails.CounterpartyExternalRef,
			req.CounterpartyDetails.Attributes,
		)
		if err != nil {
			*operationStatus = operationStatusFailed
			return nil, status.Errorf(codes.InvalidArgument, "invalid counterparty details: %v", err)
		}
		account, err = account.UpdateCounterparty(counterparty)
		if err != nil {
			*operationStatus = operationStatusFailed
			return nil, mapDomainErrorToGRPC(err)
		}
	}

	// Persist changes
	if err := s.repo.Save(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			*operationStatus = opStatusVersionConflict
			return nil, status.Error(codes.Aborted, "account was modified by another transaction")
		}
		*operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	return &pb.UpdateInternalAccountResponse{
		Facility: toProtoFacility(account),
	}, nil
}

// ControlInternalAccount performs lifecycle state transitions.
func (s *Service) ControlInternalAccount(ctx context.Context, req *pb.ControlInternalAccountRequest) (*pb.ControlInternalAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("control_internal_account", operationStatus, time.Since(start))
	}()

	// Load existing account
	account, err := s.findAccountByID(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusAccountNotFound
		return nil, err
	}

	// Capture previous status for metrics
	previousStatus := account.Status()

	// Execute state transition based on control action
	switch req.ControlAction {
	case pb.ControlAction_CONTROL_ACTION_SUSPEND:
		account, err = account.Suspend(req.Reason)
	case pb.ControlAction_CONTROL_ACTION_ACTIVATE:
		account, err = account.Activate()
	case pb.ControlAction_CONTROL_ACTION_CLOSE:
		account, err = account.Close(req.Reason)
	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "control action must be specified")
	default:
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.InvalidArgument, "invalid control action: %v", req.ControlAction)
	}

	if err != nil {
		if errors.Is(err, domain.ErrInvalidStatusTransition) {
			operationStatus = opStatusInvalidStatusTransition
			return nil, status.Errorf(codes.FailedPrecondition, "invalid status transition: %v", err)
		}
		operationStatus = operationStatusFailed
		return nil, mapDomainErrorToGRPC(err)
	}

	// Persist changes
	if err := s.repo.Save(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			operationStatus = opStatusVersionConflict
			return nil, status.Error(codes.Aborted, "account was modified by another transaction")
		}
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	// Record status change metric
	ibaobservability.RecordAccountStatusChange(string(previousStatus), string(account.Status()))

	s.logger.Info("executed control action on internal account",
		"account_id", req.AccountId,
		"action", req.ControlAction.String(),
		"new_status", string(account.Status()))

	return &pb.ControlInternalAccountResponse{
		Facility:        toProtoFacility(account),
		ActionTimestamp: timestamppb.Now(),
	}, nil
}

// RetrieveInternalAccount fetches a single account by ID.
func (s *Service) RetrieveInternalAccount(ctx context.Context, req *pb.RetrieveInternalAccountRequest) (*pb.RetrieveInternalAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("retrieve_internal_account", operationStatus, time.Since(start))
	}()

	account, err := s.findAccountByID(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusAccountNotFound
		return nil, err
	}

	return &pb.RetrieveInternalAccountResponse{
		Facility: toProtoFacility(account),
	}, nil
}

// ListInternalAccounts queries accounts with filtering and pagination.
func (s *Service) ListInternalAccounts(ctx context.Context, req *pb.ListInternalAccountsRequest) (*pb.ListInternalAccountsResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("list_internal_accounts", operationStatus, time.Since(start))
	}()

	// Build filter
	filter := domain.ListFilter{
		Limit:  50, // Default
		Offset: 0,
	}

	// Apply behavior class filter
	if req.BehaviorClassFilter != "" {
		accountType := domain.AccountType(req.BehaviorClassFilter)
		filter.AccountType = &accountType
	}

	// Apply instrument code filter
	if req.InstrumentCodeFilter != "" {
		filter.InstrumentCode = &req.InstrumentCodeFilter
	}

	// Apply status filter
	if req.StatusFilter != pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_UNSPECIFIED {
		accountStatus, err := protoToAccountStatus(req.StatusFilter)
		if err == nil {
			filter.Status = &accountStatus
		}
	}

	// Apply clearing purpose filter
	if req.ClearingPurposeFilter != pb.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED {
		clearingPurpose, err := protoToClearingPurpose(req.ClearingPurposeFilter)
		if err == nil {
			filter.ClearingPurpose = &clearingPurpose
		}
	}

	// Apply pagination
	if req.Pagination != nil {
		if req.Pagination.PageSize > 0 {
			filter.Limit = int(req.Pagination.PageSize)
		}
		// Parse page_token as offset (simple offset-based pagination)
		if req.Pagination.PageToken != "" {
			var offset int
			if _, err := fmt.Sscanf(req.Pagination.PageToken, "%d", &offset); err == nil {
				filter.Offset = offset
			}
		}
	}

	// Query repository
	accounts, err := s.repo.List(ctx, filter)
	if err != nil {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.Internal, "failed to list accounts: %v", err)
	}

	// Convert to proto
	facilities := make([]*pb.InternalAccountFacility, len(accounts))
	for i, account := range accounts {
		facilities[i] = toProtoFacility(account)
	}

	// Build pagination response
	var nextPageToken string
	if len(accounts) == filter.Limit {
		nextPageToken = fmt.Sprintf("%d", filter.Offset+filter.Limit)
	}

	return &pb.ListInternalAccountsResponse{
		Facilities: facilities,
		Pagination: &commonpb.PaginationResponse{
			NextPageToken: nextPageToken,
		},
	}, nil
}

// GetBalance queries the balance for an internal account from Position Keeping service.
func (s *Service) GetBalance(ctx context.Context, req *pb.GetBalanceRequest) (*pb.GetBalanceResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		ibaobservability.RecordOperationDuration("get_balance", operationStatus, time.Since(start))
	}()

	if strings.TrimSpace(req.AccountId) == "" {
		operationStatus = operationStatusFailed
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}

	// Validate account exists and is active
	account, err := s.findAccountByID(ctx, req.AccountId)
	if err != nil {
		operationStatus = opStatusAccountNotFound
		return nil, err
	}

	// Only active accounts have queryable balances
	if account.Status() != domain.AccountStatusActive {
		operationStatus = operationStatusFailed
		return nil, status.Errorf(codes.FailedPrecondition, "account not active: %s", string(account.Status()))
	}

	// Position Keeping client must be configured for balance queries.
	// Decision: KEEP this nil guard (see ADR-0031). Rationale:
	//   - Provides explicit error message instead of nil pointer panic
	//   - Supports constructors that omit PK client (NewService, NewServiceWithValuationFeatures)
	//   - Zero performance cost (single pointer comparison)
	//   - Future refactoring may make PK optional for other balance sources
	if s.positionKeepingClient == nil {
		operationStatus = operationStatusFailed
		return nil, status.Error(codes.Unimplemented, "position keeping service not configured")
	}

	// Query Position Keeping service (source of truth for balance) with timeout
	pkCtx, pkCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pkCancel()

	pkStart := time.Now()
	balanceResp, err := s.positionKeepingClient.GetAccountBalances(pkCtx, &positionkeepingv1.GetAccountBalancesRequest{
		AccountId:      account.AccountID(),
		InstrumentCode: account.InstrumentCode(),
	})
	pkDuration := time.Since(pkStart)

	if err != nil {
		operationStatus = opStatusPositionKeepingError
		ibaobservability.RecordBalanceQueryDuration(operationStatusFailed, pkDuration)
		s.logger.Error("failed to query balance from Position Keeping",
			"account_id", req.AccountId,
			"duration_ms", pkDuration.Milliseconds(),
			"error", err)
		// Map Position Keeping errors to appropriate gRPC codes
		return nil, mapPositionKeepingErrorToGRPC(err)
	}

	// Record successful balance query duration (target <50ms p99)
	ibaobservability.RecordBalanceQueryDuration(operationStatusSuccess, pkDuration)

	// Resolve as_of: use Position Keeping's timestamp, fall back to current time
	asOf := balanceResp.GetAsOf()
	if asOf == nil {
		asOf = timestamppb.Now()
	}

	// Find the current balance from the response.
	var currentBalance *quantityv1.InstrumentAmount
	for _, entry := range balanceResp.GetBalances() {
		if entry.GetBalanceType() == positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT {
			currentBalance = entry.GetAmount()
			break
		}
	}

	return &pb.GetBalanceResponse{
		AccountId:      req.AccountId,
		CurrentBalance: currentBalance,
		AsOf:           asOf,
	}, nil
}

// saveAccountWithEvent saves the account and optionally publishes a FacilityCreatedEvent to the outbox
// in the same database transaction for atomicity.
func (s *Service) saveAccountWithEvent(ctx context.Context, account domain.InternalAccount) error {
	if s.outboxPublisher == nil || s.db == nil {
		return s.repo.Save(ctx, account)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.repo.SaveInTx(ctx, account, tx); err != nil {
			return err
		}
		proto := &eventsv1.FacilityCreatedEvent{
			EventId:        uuid.New().String(),
			AccountId:      account.AccountID(),
			AccountCode:    account.AccountCode(),
			AccountType:    account.AccountType().String(),
			InstrumentCode: account.InstrumentCode(),
			Timestamp:      timestamppb.New(time.Now().UTC()),
		}
		return s.outboxPublisher.Publish(ctx, tx, proto, events.PublishConfig{
			EventType:     "internal-account.facility-created.v1",
			AggregateID:   account.AccountID(),
			AggregateType: "InternalAccount",
			Topic:         topics.InternalAccountFacilityCreatedV1,
			CorrelationID: uuid.New().String(),
		})
	})
}

// mapPositionKeepingErrorToGRPC maps Position Keeping service errors to appropriate gRPC status codes.
func mapPositionKeepingErrorToGRPC(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		// Non-gRPC error - treat as unavailable
		return status.Errorf(codes.Unavailable, "position keeping service unavailable: %v", err)
	}

	//exhaustive:ignore
	switch st.Code() {
	case codes.NotFound:
		// Position/account not found in Position Keeping - internal error from our perspective
		return status.Errorf(codes.Internal, "balance not found in position keeping: %v", st.Message())
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
		// Service unavailable - map to Unavailable
		return status.Errorf(codes.Unavailable, "position keeping service unavailable: %v", st.Message())
	case codes.InvalidArgument:
		// Bad request to Position Keeping - internal error (our code is wrong)
		return status.Errorf(codes.Internal, "invalid request to position keeping: %v", st.Message())
	default:
		// Other errors - map to Internal
		return status.Errorf(codes.Internal, "failed to retrieve balance: %v", st.Message())
	}
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

	// Try to find by account code
	account, err := s.repo.FindByCode(ctx, accountID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) || errors.Is(err, persistence.ErrAccountNotFound) {
			return domain.InternalAccount{}, status.Errorf(codes.NotFound, "account not found: %s", accountID)
		}
		return domain.InternalAccount{}, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}
	return account, nil
}
