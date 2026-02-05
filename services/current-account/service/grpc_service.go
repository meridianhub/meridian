// Package service implements gRPC services for the current account domain
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// Sentinel errors are defined in errors.go for better organization.
// See errors.go for ErrRepositoryNil, ErrNilPositionLog, etc.

// Operation status constants for consistency across the service
// Note: Some constants are shared with lien_service.go in this package
const (
	operationStatusSuccess         = "success"
	operationStatusFailed          = "failed"
	operationStatusInvalidCurrency = "invalid_currency"
	opStatusIdempotent             = "idempotent"
	// Shared constants - also defined in lien_service.go:
	// opStatusAccountNotFound, opStatusRetrieveFailed, opStatusCurrencyMismatch,
	// opStatusInvalidAmount, opStatusSaveFailed, opStatusInsufficientFunds
	opStatusAmountOverflow          = "amount_overflow"
	opStatusAccountFrozen           = "account_frozen"
	opStatusAccountClosed           = "account_closed"
	opStatusMissingAccountID        = "missing_account_id"
	opStatusMissingAmount           = "missing_amount"
	opStatusMissingWithdrawalID     = "missing_withdrawal_id"
	opStatusMissingIdentifier       = "missing_identifier"
	opStatusNotImplemented          = "not_implemented"
	opStatusWithdrawalFailed        = "withdrawal_failed"
	opStatusWithdrawalNotFound      = "withdrawal_not_found"
	opStatusSagaFailed              = "saga_failed"
	opStatusInvalidStatusTransition = "invalid_status_transition"
)

// Redis idempotency constants
const (
	// idempotencyNamespace is the Redis key namespace for current-account idempotency
	idempotencyNamespace = "current-account"

	// idempotencyPendingTTL is how long a pending idempotency record remains valid
	idempotencyPendingTTL = 5 * time.Minute

	// idempotencyResultTTL is how long completed results are cached
	idempotencyResultTTL = 24 * time.Hour
)

// Kafka topic constants for account lifecycle events
const (
	// TopicAccountFrozen is the Kafka topic for account frozen events
	TopicAccountFrozen = "current-account.account-frozen.v1"
	// TopicAccountUnfrozen is the Kafka topic for account unfrozen events
	TopicAccountUnfrozen = "current-account.account-unfrozen.v1"
	// TopicAccountClosed is the Kafka topic for account closed events
	TopicAccountClosed = "current-account.account-closed.v1"
)

// AccountEventPublisher defines the interface for publishing account lifecycle events.
// Implementations should handle serialization, delivery, and error handling.
type AccountEventPublisher interface {
	// PublishWithTenant publishes a protobuf message with tenant context to a Kafka topic.
	// The key is used for partitioning - events with the same key go to the same partition.
	PublishWithTenant(ctx context.Context, topic, key string, msg proto.Message) error
}

// NoOpAccountEventPublisher is a no-operation implementation of AccountEventPublisher.
// Useful for testing and scenarios where event publishing is not configured.
type NoOpAccountEventPublisher struct{}

// WebhookNotifier defines the interface for sending webhook notifications.
// Used for regulatory compliance notifications on account lifecycle events.
type WebhookNotifier interface {
	// NotifyAccountFrozen sends a webhook notification for an account freeze event.
	// Returns nil if the tenant has no webhook URL configured (not an error case).
	NotifyAccountFrozen(ctx context.Context, tenantID, accountID, reason string, timestamp time.Time) error

	// NotifyAccountClosed sends a webhook notification for an account closure event.
	// Returns nil if the tenant has no webhook URL configured (not an error case).
	NotifyAccountClosed(ctx context.Context, tenantID, accountID, reason string, balance *WebhookBalanceInfo, timestamp time.Time) error
}

// WebhookBalanceInfo contains account balance details for webhook payloads.
type WebhookBalanceInfo struct {
	// Amount is the balance amount in minor units (e.g., cents).
	Amount int64
	// CurrencyCode is the ISO 4217 currency code.
	CurrencyCode string
}

// NoOpWebhookNotifier is a no-operation implementation of WebhookNotifier.
// Useful for testing and scenarios where webhook notifications are not configured.
type NoOpWebhookNotifier struct{}

// NotifyAccountFrozen does nothing and always returns nil.
func (n *NoOpWebhookNotifier) NotifyAccountFrozen(_ context.Context, _, _, _ string, _ time.Time) error {
	return nil
}

// NotifyAccountClosed does nothing and always returns nil.
func (n *NoOpWebhookNotifier) NotifyAccountClosed(_ context.Context, _, _, _ string, _ *WebhookBalanceInfo, _ time.Time) error {
	return nil
}

// PublishWithTenant does nothing and always returns nil.
func (p *NoOpAccountEventPublisher) PublishWithTenant(_ context.Context, _, _ string, _ proto.Message) error {
	return nil
}

// Service implements the CurrentAccountService gRPC service
type Service struct {
	pb.UnimplementedCurrentAccountServiceServer
	repo                   *persistence.Repository
	lienRepo               *persistence.LienRepository
	withdrawalRepo         *persistence.WithdrawalRepository
	outboxRepo             events.OutboxRepository // Outbox repository for reliable event delivery
	db                     *gorm.DB                // Database connection for transaction management
	posKeepingClient       PositionKeepingClient
	finAcctClient          FinancialAccountingClient
	partyClient            PartyClient
	accountConfig          *config.AccountConfig
	idempotencyService     idempotency.Service
	eventPublisher         AccountEventPublisher // Optional: publishes lifecycle events to Kafka
	webhookNotifier        WebhookNotifier       // Optional: sends webhook notifications for lifecycle events
	logger                 *slog.Logger
	tracer                 *observability.Tracer
	depositOrchestrator    *DepositOrchestrator    // Handles deposit saga orchestration
	withdrawalOrchestrator *WithdrawalOrchestrator // Handles withdrawal saga orchestration
}

// Config contains configuration for creating a new Service with external clients
type Config struct {
	Repository           *persistence.Repository
	LienRepository       *persistence.LienRepository
	WithdrawalRepository *persistence.WithdrawalRepository
	// Namespace is the Kubernetes namespace for service discovery (e.g., "default")
	Namespace string
	// PositionKeepingServiceName is the Kubernetes service name (e.g., "position-keeping")
	PositionKeepingServiceName string
	// PositionKeepingPort is the service port (e.g., 50053)
	PositionKeepingPort int
	// FinancialAccountingServiceName is the Kubernetes service name (e.g., "financial-accounting")
	FinancialAccountingServiceName string
	// FinancialAccountingPort is the service port (e.g., 50052)
	FinancialAccountingPort int
	// PartyServiceName is the Kubernetes service name (e.g., "party")
	PartyServiceName string
	// PartyPort is the service port (e.g., 50055)
	PartyPort int
	Logger    *slog.Logger
	Tracer    *observability.Tracer
}

// NewService creates a new current account service with minimal dependencies.
// This is primarily used for testing. For production use, prefer NewServiceWithClients.
// Returns an error if repository is nil.
func NewService(repo *persistence.Repository, lienRepo *persistence.LienRepository) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	return &Service{
		repo:     repo,
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}, nil
}

// NewServiceWithIdempotency creates a new current account service with idempotency support.
// This is primarily used for testing idempotency paths.
// Returns an error if repository is nil.
func NewServiceWithIdempotency(repo *persistence.Repository, lienRepo *persistence.LienRepository, idempotencyService idempotency.Service) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}
	return &Service{
		repo:               repo,
		lienRepo:           lienRepo,
		idempotencyService: idempotencyService,
		logger:             slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}, nil
}

// NewServiceWithExistingClients creates a new service with pre-created client instances.
// This constructor is useful when clients need to be shared with other components
// (e.g., health checkers) to avoid creating duplicate connections.
//
// Optional parameters:
//   - accountConfig: Static clearing account configuration (environment variables)
//   - accountResolver: Dynamic clearing account resolution from Internal Bank Account service
//   - fungibilityValidator: Validates fungibility for non-fungible instruments
//
// If both accountConfig and accountResolver are provided, the resolver takes precedence
// with fallback to static config.
func NewServiceWithExistingClients(
	repo *persistence.Repository,
	lienRepo *persistence.LienRepository,
	withdrawalRepo *persistence.WithdrawalRepository,
	outboxRepo events.OutboxRepository, // Outbox repository for reliable event delivery
	db *gorm.DB, // Database connection for transaction management
	posKeepingClient PositionKeepingClient,
	finAcctClient FinancialAccountingClient,
	partyClient PartyClient,
	accountConfig *config.AccountConfig,
	idempotencyService idempotency.Service,
	logger *slog.Logger,
	tracer *observability.Tracer,
	accountResolver *AccountResolver, // Optional: dynamic clearing account resolution
	fungibilityValidator *FungibilityValidator, // Optional: validates fungibility for non-fungible instruments
) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}

	// Apply default logger if not provided
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	// Load saga scripts from reference-data canonical source
	depositScript, err := loadSagaAsset(filepath.Join("services", "reference-data", "saga", "defaults", "deposit", "v1.0.0.star"))
	if err != nil {
		return nil, fmt.Errorf("failed to load deposit saga script: %w", err)
	}
	withdrawalScript, err := loadSagaAsset(filepath.Join("services", "reference-data", "saga", "defaults", "withdrawal", "v1.0.0.star"))
	if err != nil {
		return nil, fmt.Errorf("failed to load withdrawal saga script: %w", err)
	}

	// Create saga handler registry
	handlerRegistry := saga.NewHandlerRegistry()
	if err := RegisterCurrentAccountHandlers(handlerRegistry); err != nil {
		return nil, fmt.Errorf("failed to register saga handlers: %w", err)
	}

	// Load schema registry from handlers.yaml
	schemaContent, err := loadSagaAsset(filepath.Join("shared", "pkg", "saga", "schema", "handlers.yaml"))
	if err != nil {
		return nil, fmt.Errorf("failed to read handlers schema: %w", err)
	}
	schemaRegistryData := []byte(schemaContent)
	schemaRegistry := schema.NewRegistry()
	if err := schemaRegistry.LoadFromYAML(schemaRegistryData); err != nil {
		return nil, fmt.Errorf("failed to load schema: %w", err)
	}

	// Build service modules for Starlark
	serviceModules, err := schema.BuildServiceModules(handlerRegistry, schemaRegistry)
	if err != nil {
		return nil, fmt.Errorf("failed to build service modules: %w", err)
	}

	// Create Starlark saga runtime
	runtime, err := saga.NewRuntime(logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create saga runtime: %w", err)
	}

	// Create Starlark saga runner
	sagaRunner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       handlerRegistry,
		ServiceModules: serviceModules,
		Logger:         logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create saga runner: %w", err)
	}

	// Create deposit orchestrator
	depositOrchestrator, err := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:               logger,
		Repo:                 repo,
		PosKeepingClient:     posKeepingClient,
		FinAcctClient:        finAcctClient,
		AccountConfig:        accountConfig,
		AccountResolver:      accountResolver,
		FungibilityValidator: fungibilityValidator,
		SagaRunner:           sagaRunner,
		DepositScript:        depositScript,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create deposit orchestrator: %w", err)
	}

	// Create withdrawal orchestrator
	withdrawalOrchestrator, err := NewWithdrawalOrchestrator(WithdrawalOrchestratorConfig{
		Logger:               logger,
		Repo:                 repo,
		PosKeepingClient:     posKeepingClient,
		FinAcctClient:        finAcctClient,
		AccountConfig:        accountConfig,
		AccountResolver:      accountResolver,
		FungibilityValidator: fungibilityValidator,
		SagaRunner:           sagaRunner,
		WithdrawalScript:     withdrawalScript,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create withdrawal orchestrator: %w", err)
	}

	return &Service{
		repo:                   repo,
		lienRepo:               lienRepo,
		withdrawalRepo:         withdrawalRepo,
		outboxRepo:             outboxRepo,
		db:                     db,
		posKeepingClient:       posKeepingClient,
		finAcctClient:          finAcctClient,
		partyClient:            partyClient,
		accountConfig:          accountConfig,
		idempotencyService:     idempotencyService,
		logger:                 logger,
		tracer:                 tracer,
		depositOrchestrator:    depositOrchestrator,
		withdrawalOrchestrator: withdrawalOrchestrator,
	}, nil
}

// InitiateCurrentAccount creates a new current account facility
func (s *Service) InitiateCurrentAccount(ctx context.Context, req *pb.InitiateCurrentAccountRequest) (*pb.InitiateCurrentAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("initiate_account", operationStatus, time.Since(start))
	}()

	// Generate account ID
	accountID := fmt.Sprintf("ACC-%s", uuid.New().String()[:8])

	// Map currency enum to string
	currency := mapCurrency(req.BaseCurrency)
	if currency == "" {
		operationStatus = operationStatusInvalidCurrency
		return nil, status.Errorf(codes.InvalidArgument, "unsupported currency: %v", req.BaseCurrency)
	}

	// Validate party exists and is active (if party client is configured)
	if s.partyClient != nil {
		partyValidationStart := time.Now()
		s.logger.Info("validating party for account creation",
			"party_id", req.PartyId,
			"account_id", accountID)

		if err := s.partyClient.ValidateParty(ctx, req.PartyId); err != nil {
			caobservability.RecordPartyValidationDuration(time.Since(partyValidationStart), false)

			if errors.Is(err, ErrPartyNotFound) {
				operationStatus = "party_not_found"
				s.logger.Warn("party not found during account creation",
					"party_id", req.PartyId,
					"account_id", accountID)
				return nil, status.Errorf(codes.InvalidArgument, "party not found: %s", req.PartyId)
			}
			if errors.Is(err, ErrPartyNotActive) {
				operationStatus = "party_not_active"
				s.logger.Warn("party not active during account creation",
					"party_id", req.PartyId,
					"account_id", accountID)
				return nil, status.Errorf(codes.FailedPrecondition, "party not active: %s", req.PartyId)
			}
			operationStatus = "party_validation_failed"
			s.logger.Error("party validation failed during account creation",
				"party_id", req.PartyId,
				"account_id", accountID,
				"error", err)
			caobservability.RecordExternalServiceError("party", "validate_party")
			return nil, status.Errorf(codes.Internal, "party validation failed: %v", err)
		}

		caobservability.RecordPartyValidationDuration(time.Since(partyValidationStart), true)
		s.logger.Info("party validated successfully",
			"party_id", req.PartyId,
			"account_id", accountID)
	}

	// Create domain model (now returns value, not pointer)
	account, err := domain.NewCurrentAccount(
		accountID,
		req.AccountIdentification,
		req.PartyId,
		currency,
	)
	if err != nil {
		operationStatus = "domain_error"
		return nil, status.Errorf(codes.InvalidArgument, "failed to create account: %v", err)
	}

	// Save to database (context carries audit user info for created_by/updated_by fields)
	if err := s.repo.Save(ctx, account); err != nil {
		operationStatus = opStatusSaveFailed
		return nil, status.Errorf(codes.Internal, "failed to create account: %v", err)
	}

	// Record initial balance
	caobservability.RecordBalance(safeMinorUnits(account.Balance()), currency)

	// Convert to proto response
	return &pb.InitiateCurrentAccountResponse{
		AccountId: accountID,
		Facility:  toProtoFacility(account),
	}, nil
}

// ExecuteDeposit processes a deposit transaction with Redis-based idempotency protection.
//
// Concurrency: This method relies on optimistic locking in the repository layer
// to handle concurrent modifications to the same account. If two requests attempt
// to modify the same account simultaneously, one will succeed and the other will
// receive ErrVersionConflict, which surfaces as an Internal error to the client.
// Redis-based idempotency provides request deduplication for retried requests.
func (s *Service) ExecuteDeposit(ctx context.Context, req *pb.ExecuteDepositRequest) (*pb.ExecuteDepositResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("execute_deposit", operationStatus, time.Since(start))
	}()

	// Get idempotency key if provided
	var idempotencyKey string
	if req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		idempotencyKey = req.IdempotencyKey.Key
	}

	// Build idempotency key structure for Redis
	var idempKey idempotency.Key
	var idempotencyLockAcquired bool
	if idempotencyKey != "" && s.idempotencyService != nil {
		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			s.logger.Debug("tenant not found in context for idempotency key",
				"account_id", req.AccountId)
		}
		idempKey = idempotency.Key{
			TenantID:  string(tenantID),
			Namespace: idempotencyNamespace,
			Operation: "deposit",
			EntityID:  req.AccountId,
			RequestID: idempotencyKey,
		}

		// Check Redis for existing result
		result, err := s.idempotencyService.Check(ctx, idempKey)
		if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) && result != nil && result.Data != nil {
			var cachedResp pb.ExecuteDepositResponse
			unmarshalErr := proto.Unmarshal(result.Data, &cachedResp)
			if unmarshalErr == nil {
				s.logger.Info("returning cached deposit response from Redis",
					"account_id", req.AccountId,
					"transaction_id", cachedResp.TransactionId,
					"idempotency_key", idempotencyKey)
				operationStatus = opStatusIdempotent
				return &cachedResp, nil
			}
			s.logger.Warn("failed to unmarshal cached idempotency result",
				"error", unmarshalErr)
		} else if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
			s.logger.Error("idempotency check failed", "error", err)
			return nil, status.Error(codes.Internal, "failed to check idempotency")
		}

		// Mark operation as pending (distributed lock)
		if err := s.idempotencyService.MarkPending(ctx, idempKey, idempotencyPendingTTL); err != nil {
			// Check if another request is already processing this operation
			if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
				s.logger.Info("operation already in progress, please retry",
					"idempotency_key", idempotencyKey)
				return nil, status.Error(codes.Aborted, "operation already in progress, please retry")
			}
			s.logger.Error("failed to mark operation pending", "error", err)
			return nil, status.Error(codes.Aborted, "failed to acquire idempotency lock, please retry")
		}
		idempotencyLockAcquired = true

		// Cleanup pending state on failure - ensures retries aren't blocked for 5 minutes
		defer func() {
			if idempotencyLockAcquired && operationStatus != operationStatusSuccess {
				if delErr := s.idempotencyService.Delete(ctx, idempKey); delErr != nil {
					s.logger.Warn("failed to cleanup pending idempotency state",
						"error", delErr,
						"idempotency_key", idempotencyKey)
				}
			}
		}()
	}

	// Retrieve account (context carries organization for multi-tenant routing)
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Validate currency matches account currency
	if req.Amount.Amount.CurrencyCode != account.Balance().CurrencyCode() {
		operationStatus = opStatusCurrencyMismatch
		return nil, status.Errorf(codes.InvalidArgument,
			"currency mismatch: expected %s, got %s",
			account.Balance().CurrencyCode(), req.Amount.Amount.CurrencyCode)
	}

	// Convert amount from proto (MoneyAmount wraps google.type.Money)
	// Validate overflow: Units*100 must not overflow int64
	if req.Amount.Amount.Units > math.MaxInt64/100 || req.Amount.Amount.Units < math.MinInt64/100 {
		operationStatus = opStatusAmountOverflow
		return nil, status.Errorf(codes.InvalidArgument,
			"amount too large: units %d would overflow", req.Amount.Amount.Units)
	}

	// Convert to cents preserving precision
	unitsCents := req.Amount.Amount.Units * 100
	// Round nanos to nearest cent (0.5 rounds up)
	nanosCents := (req.Amount.Amount.Nanos + 5000000) / 10000000

	// Use Money.Add to safely handle potential overflow from adding nanosCents
	centsMoney, err := domain.NewMoney(req.Amount.Amount.CurrencyCode, unitsCents)
	if err != nil {
		operationStatus = operationStatusInvalidCurrency
		return nil, status.Errorf(codes.InvalidArgument, "invalid currency: %v", err)
	}

	nanosMoney, err := domain.NewMoney(req.Amount.Amount.CurrencyCode, int64(nanosCents))
	if err != nil {
		operationStatus = operationStatusInvalidCurrency
		return nil, status.Errorf(codes.InvalidArgument, "invalid currency: %v", err)
	}

	amount, err := centsMoney.Add(nanosMoney)
	if err != nil {
		operationStatus = operationStatusInvalidCurrency
		return nil, status.Errorf(codes.InvalidArgument, "invalid currency: %v", err)
	}

	// Validate amount is positive
	amountCents, err := amount.ToMinorUnits()
	if err != nil {
		operationStatus = opStatusAmountOverflow
		return nil, status.Errorf(codes.InvalidArgument,
			"deposit amount overflow: %v", err)
	}
	if amountCents <= 0 {
		operationStatus = opStatusInvalidAmount
		return nil, status.Errorf(codes.InvalidArgument,
			"deposit amount must be positive, got %d cents", amountCents)
	}

	// Generate transaction ID (full UUID required by position-keeping service)
	transactionID := uuid.New().String()

	// Prepare account for credit transaction (validates status, increments version for optimistic locking)
	account, err = account.PrepareForCredit()
	if err != nil {
		operationStatus = "deposit_failed"
		if errors.Is(err, domain.ErrAccountFrozen) || errors.Is(err, domain.ErrAccountClosed) {
			return nil, status.Errorf(codes.FailedPrecondition, "deposit failed: %v", err)
		}
		return nil, status.Errorf(codes.InvalidArgument, "deposit failed: %v", err)
	}

	// Orchestrate transaction with saga pattern - Position Keeping is the source of truth for balance
	resp, err := s.depositOrchestrator.Orchestrate(ctx, account, amount, transactionID, req.Attributes)
	if err != nil {
		operationStatus = opStatusSagaFailed
		return nil, err
	}

	// Record deposit transaction (the deposit itself succeeded regardless of balance fetch)
	caobservability.RecordDeposit(string(amount.Currency()))

	// After saga completes, query Position Keeping for the new balance
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		s.logger.Error("failed to retrieve updated balance from Position Keeping after deposit",
			"account_id", account.AccountID(),
			"transaction_id", transactionID,
			"error", err)
		// Transaction succeeded but balance fetch failed - leave balance fields nil
		// Client should call RetrieveCurrentAccount to get accurate balance
	} else {
		// Update response with balance from Position Keeping
		resp.NewBalance = toMoneyAmount(account.Balance())
		resp.AvailableBalance = toMoneyAmount(account.AvailableBalance())
		// Record balance gauge only when we have accurate post-transaction balance
		caobservability.RecordBalance(safeMinorUnits(account.Balance()), string(account.Balance().Currency()))
	}

	// Store successful result in Redis for future idempotency checks
	if idempotencyKey != "" && s.idempotencyService != nil {
		responseData, marshalErr := proto.Marshal(resp)
		if marshalErr == nil {
			storeErr := s.idempotencyService.StoreResult(ctx, idempotency.Result{
				Key:         idempKey,
				Status:      idempotency.StatusCompleted,
				Data:        responseData,
				CompletedAt: time.Now(),
				TTL:         idempotencyResultTTL,
			})
			if storeErr != nil {
				s.logger.Error("failed to store idempotency result", "error", storeErr)
				// Continue - operation succeeded, caching is optimization
			}
		} else {
			s.logger.Error("failed to marshal response for idempotency cache", "error", marshalErr)
		}
	}

	return resp, nil
}

// RetrieveCurrentAccount gets current account details including balance from Position Keeping.
func (s *Service) RetrieveCurrentAccount(ctx context.Context, req *pb.RetrieveCurrentAccountRequest) (*pb.RetrieveCurrentAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("retrieve_account", operationStatus, time.Since(start))
	}()

	// Context carries organization for multi-tenant routing
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Hydrate account with balance from Position Keeping service.
	// Balance is no longer persisted locally - Position Keeping is the source of truth.
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to retrieve balance from Position Keeping",
			"account_id", req.AccountId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	return &pb.RetrieveCurrentAccountResponse{
		Facility: toProtoFacility(account),
	}, nil
}

// ExecuteWithdrawal processes a withdrawal transaction with Redis-based idempotency protection.
//
// This method supports two modes:
//  1. Direct withdrawal: Provide account_id and amount for immediate execution
//  2. Execute pending withdrawal: Provide withdrawal_id to execute a previously initiated withdrawal
//
// Concurrency: This method relies on optimistic locking in the repository layer
// to handle concurrent modifications to the same account. If two requests attempt
// to modify the same account simultaneously, one will succeed and the other will
// receive ErrVersionConflict, which surfaces as an Internal error to the client.
// Redis-based idempotency provides request deduplication for retried requests.
func (s *Service) ExecuteWithdrawal(ctx context.Context, req *pb.ExecuteWithdrawalRequest) (*pb.ExecuteWithdrawalResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("execute_withdrawal", operationStatus, time.Since(start))
	}()

	// Determine which account to use - either from withdrawal_id lookup or direct account_id
	var accountID string
	var reqAmount *commonpb.MoneyAmount
	var pendingWithdrawal *domain.Withdrawal

	if req.WithdrawalId != "" {
		// Look up pending withdrawal by reference
		if s.withdrawalRepo == nil {
			operationStatus = opStatusNotImplemented
			return nil, status.Error(codes.Unimplemented, "withdrawal persistence not configured")
		}

		var err error
		pendingWithdrawal, err = s.withdrawalRepo.FindByReference(ctx, req.WithdrawalId)
		if err != nil {
			if errors.Is(err, persistence.ErrWithdrawalNotFound) {
				operationStatus = opStatusWithdrawalNotFound
				return nil, status.Errorf(codes.NotFound, "withdrawal not found: %s", req.WithdrawalId)
			}
			operationStatus = opStatusRetrieveFailed
			s.logger.Error("failed to retrieve withdrawal",
				"withdrawal_id", req.WithdrawalId,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to retrieve withdrawal: %v", err)
		}

		// Verify withdrawal is in pending status
		if !pendingWithdrawal.IsPending() {
			operationStatus = "withdrawal_not_pending"
			return nil, status.Errorf(codes.FailedPrecondition,
				"withdrawal %s is not pending (status: %s)", req.WithdrawalId, pendingWithdrawal.Status)
		}

		// Get account by UUID to retrieve the business account ID
		account, err := s.repo.FindByUUID(ctx, pendingWithdrawal.AccountID)
		if err != nil {
			if errors.Is(err, persistence.ErrAccountNotFound) {
				operationStatus = opStatusAccountNotFound
				return nil, status.Errorf(codes.NotFound, "account not found for withdrawal: %s", req.WithdrawalId)
			}
			operationStatus = opStatusRetrieveFailed
			return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
		}

		accountID = account.AccountID()
		// Convert domain Money to proto MoneyAmount
		reqAmount = toMoneyAmount(pendingWithdrawal.Amount)

		s.logger.Info("executing pending withdrawal",
			"withdrawal_id", req.WithdrawalId,
			"account_id", accountID)
	} else {
		// Direct withdrawal mode
		if req.AccountId == "" {
			operationStatus = opStatusMissingAccountID
			return nil, status.Error(codes.InvalidArgument, "account_id is required for direct withdrawal")
		}
		if req.Amount == nil || req.Amount.Amount == nil {
			operationStatus = opStatusMissingAmount
			return nil, status.Error(codes.InvalidArgument, "amount is required for direct withdrawal")
		}
		accountID = req.AccountId
		reqAmount = req.Amount
	}

	// Get idempotency key if provided
	var idempotencyKey string
	if req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		idempotencyKey = req.IdempotencyKey.Key
	}

	// Build idempotency key structure for Redis
	var idempKey idempotency.Key
	var idempotencyLockAcquired bool
	if idempotencyKey != "" && s.idempotencyService != nil {
		tenantID, ok := tenant.FromContext(ctx)
		if !ok {
			s.logger.Debug("tenant not found in context for idempotency key",
				"account_id", accountID)
		}
		idempKey = idempotency.Key{
			TenantID:  string(tenantID),
			Namespace: idempotencyNamespace,
			Operation: "withdrawal",
			EntityID:  accountID,
			RequestID: idempotencyKey,
		}

		// Check Redis for existing result
		result, err := s.idempotencyService.Check(ctx, idempKey)
		if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) && result != nil && result.Data != nil {
			var cachedResp pb.ExecuteWithdrawalResponse
			unmarshalErr := proto.Unmarshal(result.Data, &cachedResp)
			if unmarshalErr == nil {
				s.logger.Info("returning cached withdrawal response from Redis",
					"account_id", accountID,
					"transaction_id", cachedResp.TransactionId,
					"idempotency_key", idempotencyKey)
				operationStatus = opStatusIdempotent
				return &cachedResp, nil
			}
			s.logger.Warn("failed to unmarshal cached idempotency result",
				"error", unmarshalErr)
		} else if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
			s.logger.Error("idempotency check failed", "error", err)
			return nil, status.Error(codes.Internal, "failed to check idempotency")
		}

		// Mark operation as pending (distributed lock)
		if err := s.idempotencyService.MarkPending(ctx, idempKey, idempotencyPendingTTL); err != nil {
			if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
				s.logger.Info("operation already in progress, please retry",
					"idempotency_key", idempotencyKey)
				return nil, status.Error(codes.Aborted, "operation already in progress, please retry")
			}
			s.logger.Error("failed to mark operation pending", "error", err)
			return nil, status.Error(codes.Aborted, "failed to acquire idempotency lock, please retry")
		}
		idempotencyLockAcquired = true

		// Cleanup pending state on failure
		defer func() {
			if idempotencyLockAcquired && operationStatus != operationStatusSuccess {
				if delErr := s.idempotencyService.Delete(ctx, idempKey); delErr != nil {
					s.logger.Warn("failed to cleanup pending idempotency state",
						"error", delErr,
						"idempotency_key", idempotencyKey)
				}
			}
		}()
	}

	// Retrieve account (context carries organization for multi-tenant routing)
	account, err := s.repo.FindByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", accountID)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Hydrate account with balance from Position Keeping (balance no longer persisted locally)
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to hydrate account balance from Position Keeping",
			"account_id", accountID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	// Check account status - cannot withdraw from frozen or closed accounts
	if account.Status() == domain.AccountStatusFrozen {
		operationStatus = opStatusAccountFrozen
		return nil, status.Errorf(codes.FailedPrecondition, "cannot withdraw from frozen account: %s", accountID)
	}
	if account.Status() == domain.AccountStatusClosed {
		operationStatus = opStatusAccountClosed
		return nil, status.Errorf(codes.FailedPrecondition, "cannot withdraw from closed account: %s", accountID)
	}

	// Validate currency matches account currency
	if reqAmount.Amount.CurrencyCode != account.Balance().CurrencyCode() {
		operationStatus = opStatusCurrencyMismatch
		return nil, status.Errorf(codes.InvalidArgument,
			"currency mismatch: expected %s, got %s",
			account.Balance().CurrencyCode(), reqAmount.Amount.CurrencyCode)
	}

	// Convert amount from proto (MoneyAmount wraps google.type.Money)
	// Validate overflow: Units*100 must not overflow int64
	if reqAmount.Amount.Units > math.MaxInt64/100 || reqAmount.Amount.Units < math.MinInt64/100 {
		operationStatus = opStatusAmountOverflow
		return nil, status.Errorf(codes.InvalidArgument,
			"amount too large: units %d would overflow", reqAmount.Amount.Units)
	}

	// Convert to cents preserving precision
	unitsCents := reqAmount.Amount.Units * 100
	// Round nanos to nearest cent (0.5 rounds up)
	nanosCents := (reqAmount.Amount.Nanos + 5000000) / 10000000

	// Use Money.Add to safely handle potential overflow from adding nanosCents
	centsMoney, err := domain.NewMoney(reqAmount.Amount.CurrencyCode, unitsCents)
	if err != nil {
		operationStatus = operationStatusInvalidCurrency
		return nil, status.Errorf(codes.InvalidArgument, "invalid currency: %v", err)
	}

	nanosMoney, err := domain.NewMoney(reqAmount.Amount.CurrencyCode, int64(nanosCents))
	if err != nil {
		operationStatus = operationStatusInvalidCurrency
		return nil, status.Errorf(codes.InvalidArgument, "invalid currency: %v", err)
	}

	amount, err := centsMoney.Add(nanosMoney)
	if err != nil {
		operationStatus = operationStatusInvalidCurrency
		return nil, status.Errorf(codes.InvalidArgument, "invalid currency: %v", err)
	}

	// Validate amount is positive
	amountCents, err := amount.ToMinorUnits()
	if err != nil {
		operationStatus = opStatusAmountOverflow
		return nil, status.Errorf(codes.InvalidArgument,
			"withdrawal amount overflow: %v", err)
	}
	if amountCents <= 0 {
		operationStatus = opStatusInvalidAmount
		return nil, status.Errorf(codes.InvalidArgument,
			"withdrawal amount must be positive, got %d cents", amountCents)
	}

	// Generate transaction ID (full UUID required by position-keeping service)
	transactionID := uuid.New().String()

	// Prepare account for debit transaction (validates status, funds, increments version)
	account, err = account.PrepareForDebit(amount)
	if err != nil {
		if errors.Is(err, domain.ErrInsufficientFunds) {
			operationStatus = opStatusInsufficientFunds
			availCents, _ := account.AvailableBalance().ToMinorUnits()
			return nil, status.Errorf(codes.FailedPrecondition,
				"insufficient funds: requested %d cents, available %d cents", amountCents, availCents)
		}
		if errors.Is(err, domain.ErrAccountFrozen) {
			operationStatus = opStatusAccountFrozen
			return nil, status.Errorf(codes.FailedPrecondition, "account is frozen")
		}
		if errors.Is(err, domain.ErrAccountClosed) {
			operationStatus = opStatusAccountClosed
			return nil, status.Errorf(codes.FailedPrecondition, "account is closed")
		}
		operationStatus = opStatusWithdrawalFailed
		return nil, status.Errorf(codes.InvalidArgument, "withdrawal failed: %v", err)
	}

	// Orchestrate transaction with saga pattern - Position Keeping is the source of truth for balance
	resp, err := s.withdrawalOrchestrator.Orchestrate(ctx, account, amount, transactionID, req.Attributes)
	if err != nil {
		operationStatus = opStatusSagaFailed
		return nil, err
	}

	// Record withdrawal transaction (the withdrawal itself succeeded regardless of balance fetch)
	caobservability.RecordWithdrawal(string(amount.Currency()))

	// After saga completes, query Position Keeping for the new balance
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		s.logger.Error("failed to retrieve updated balance from Position Keeping after withdrawal",
			"account_id", account.AccountID(),
			"transaction_id", transactionID,
			"error", err)
		// Transaction succeeded but balance fetch failed - leave balance fields nil
		// Client should call RetrieveCurrentAccount to get accurate balance
	} else {
		// Update response with balance from Position Keeping
		resp.NewBalance = toMoneyAmount(account.Balance())
		resp.AvailableBalance = toMoneyAmount(account.AvailableBalance())
		// Record balance gauge only when we have accurate post-transaction balance
		caobservability.RecordBalance(safeMinorUnits(account.Balance()), string(account.Balance().Currency()))
	}

	// Mark pending withdrawal as completed (if executing a pending withdrawal)
	// Uses transactional outbox pattern to ensure atomicity between status update and event publication.
	// If the outbox write fails, the withdrawal status update is rolled back, ensuring consistency.
	if pendingWithdrawal != nil && s.withdrawalRepo != nil {
		// Use internal UUID from withdrawal (not the business account ID which is like "ACC-xxxx")
		accountUUID := pendingWithdrawal.AccountID
		if err := s.completeWithdrawalWithOutbox(ctx, pendingWithdrawal, accountUUID); err != nil {
			// CRITICAL: Outbox write failed but funds already moved. Must not leave withdrawal PENDING
			// as that would allow re-execution. Fall back to direct status update without outbox.
			s.logger.Error("outbox withdrawal completion failed, attempting fallback direct update",
				"withdrawal_id", pendingWithdrawal.Reference,
				"account_id", accountUUID,
				"outbox_error", err)

			// Fallback: Mark withdrawal completed directly (idempotent, safe to retry)
			if fallbackErr := pendingWithdrawal.Complete(); fallbackErr != nil {
				s.logger.Error("fallback withdrawal completion also failed - withdrawal stuck in PENDING",
					"withdrawal_id", pendingWithdrawal.Reference,
					"fallback_error", fallbackErr,
					"original_error", err)
				// Don't fail the RPC - funds already moved, but log critical issue
			} else if fallbackErr := s.withdrawalRepo.Update(ctx, pendingWithdrawal); fallbackErr != nil {
				s.logger.Error("fallback withdrawal persistence failed - withdrawal stuck in PENDING",
					"withdrawal_id", pendingWithdrawal.Reference,
					"fallback_error", fallbackErr,
					"original_error", err)
			} else {
				s.logger.Warn("withdrawal marked completed via fallback (outbox events lost)",
					"withdrawal_id", pendingWithdrawal.Reference)
			}
		}
	}

	// Store successful result in Redis for future idempotency checks
	if idempotencyKey != "" && s.idempotencyService != nil {
		responseData, marshalErr := proto.Marshal(resp)
		if marshalErr == nil {
			storeErr := s.idempotencyService.StoreResult(ctx, idempotency.Result{
				Key:         idempKey,
				Status:      idempotency.StatusCompleted,
				Data:        responseData,
				CompletedAt: time.Now(),
				TTL:         idempotencyResultTTL,
			})
			if storeErr != nil {
				s.logger.Error("failed to store idempotency result", "error", storeErr)
				// Continue - operation succeeded, caching is optimization
			}
		} else {
			s.logger.Error("failed to marshal response for idempotency cache", "error", marshalErr)
		}
	}

	return resp, nil
}

// completeWithdrawalWithOutbox atomically updates withdrawal status and writes status change event to outbox.
// This ensures that the withdrawal status update and event publication happen in the same transaction,
// providing at-least-once delivery guarantees for withdrawal completion events.
func (s *Service) completeWithdrawalWithOutbox(ctx context.Context, withdrawal *domain.Withdrawal, accountID uuid.UUID) error {
	// If outbox repo is not configured, fall back to direct update (graceful degradation)
	if s.outboxRepo == nil || s.db == nil {
		if err := withdrawal.Complete(); err != nil {
			return fmt.Errorf("failed to transition withdrawal to completed status: %w", err)
		}
		if err := s.withdrawalRepo.Update(ctx, withdrawal); err != nil {
			return fmt.Errorf("failed to persist withdrawal completion: %w", err)
		}
		return nil
	}

	// Use transaction to ensure atomicity between withdrawal update and outbox entry
	return s.db.Transaction(func(tx *gorm.DB) error {
		// Update withdrawal status within transaction
		if err := withdrawal.Complete(); err != nil {
			return fmt.Errorf("failed to transition withdrawal to completed status: %w", err)
		}

		// Save withdrawal state using transactional repository
		withdrawalRepoTx := s.withdrawalRepo.WithTx(tx)
		if err := withdrawalRepoTx.Update(ctx, withdrawal); err != nil {
			return fmt.Errorf("failed to persist withdrawal completion: %w", err)
		}

		// Create simple event payload (JSON) for publication
		// TODO: Replace with proper protobuf event definition once WithdrawalStatusUpdatedEvent is defined
		eventData := map[string]interface{}{
			"withdrawal_id": withdrawal.Reference,
			"account_id":    accountID.String(),
			"status":        "COMPLETED",
			"updated_at":    time.Now().Format(time.RFC3339),
		}

		// Marshal event payload as JSON
		eventPayload, err := json.Marshal(eventData)
		if err != nil {
			return fmt.Errorf("failed to marshal withdrawal status event: %w", err)
		}

		// Create outbox entry within the same transaction
		outboxEntry := &events.EventOutbox{
			ID:            uuid.New(),
			EventType:     "WithdrawalStatusUpdated",
			AggregateID:   withdrawal.Reference,
			AggregateType: "Withdrawal",
			EventPayload:  eventPayload,
			Status:        events.StatusPending,
			Topic:         "current-account.withdrawal.status",
			PartitionKey:  accountID.String(),
			CreatedAt:     time.Now(),
			RetryCount:    0,
			ServiceName:   "current-account",
		}

		// Insert outbox entry within the transaction
		if err := s.outboxRepo.Insert(ctx, tx, outboxEntry); err != nil {
			return fmt.Errorf("failed to create outbox entry: %w", err)
		}

		return nil
	})
}

// InitiateWithdrawal creates a pending withdrawal for validation before execution.
// This implements a two-phase withdrawal pattern where the withdrawal is first validated
// and created with INITIATED status, then can be executed later via ExecuteWithdrawal.
func (s *Service) InitiateWithdrawal(ctx context.Context, req *pb.InitiateWithdrawalRequest) (*pb.InitiateWithdrawalResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("initiate_withdrawal", operationStatus, time.Since(start))
	}()

	// Validate required fields
	if req.AccountId == "" {
		operationStatus = opStatusMissingAccountID
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	if req.Amount == nil || req.Amount.Amount == nil {
		operationStatus = opStatusMissingAmount
		return nil, status.Error(codes.InvalidArgument, "amount is required")
	}

	// Retrieve account to validate
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Hydrate account with balance from Position Keeping (balance no longer persisted locally)
	account, err = s.hydrateAccountWithBalance(ctx, account)
	if err != nil {
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to hydrate account balance from Position Keeping",
			"account_id", req.AccountId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
	}

	var validationMessages []string

	// Validate account status
	if account.Status() == domain.AccountStatusFrozen {
		operationStatus = opStatusAccountFrozen
		return nil, status.Errorf(codes.FailedPrecondition, "cannot initiate withdrawal on frozen account: %s", req.AccountId)
	}
	if account.Status() == domain.AccountStatusClosed {
		operationStatus = opStatusAccountClosed
		return nil, status.Errorf(codes.FailedPrecondition, "cannot initiate withdrawal on closed account: %s", req.AccountId)
	}

	// Validate currency matches
	if req.Amount.Amount.CurrencyCode != account.Balance().CurrencyCode() {
		operationStatus = opStatusCurrencyMismatch
		return nil, status.Errorf(codes.InvalidArgument,
			"currency mismatch: expected %s, got %s",
			account.Balance().CurrencyCode(), req.Amount.Amount.CurrencyCode)
	}

	// Validate overflow: Units*100 must not overflow int64
	if req.Amount.Amount.Units > math.MaxInt64/100 || req.Amount.Amount.Units < math.MinInt64/100 {
		operationStatus = opStatusAmountOverflow
		return nil, status.Errorf(codes.InvalidArgument,
			"amount too large: units %d would overflow", req.Amount.Amount.Units)
	}

	// Convert and validate amount
	unitsCents := req.Amount.Amount.Units * 100
	nanosCents := (req.Amount.Amount.Nanos + 5000000) / 10000000
	amountCents := unitsCents + int64(nanosCents)

	if amountCents <= 0 {
		operationStatus = opStatusInvalidAmount
		return nil, status.Errorf(codes.InvalidArgument, "withdrawal amount must be positive")
	}

	// Check available balance (warning only - balance could change before execution)
	availCents, _ := account.AvailableBalance().ToMinorUnits()
	if amountCents > availCents {
		validationMessages = append(validationMessages,
			fmt.Sprintf("Warning: requested amount (%d cents) exceeds current available balance (%d cents)", amountCents, availCents))
	}

	// Use provided reference if available, otherwise generate one
	reference := req.Reference
	if reference == "" {
		reference = fmt.Sprintf("WTH-%s", uuid.New().String()[:8])
	}

	// Create domain Money from amount cents
	amount, err := domain.NewMoney(req.Amount.Amount.CurrencyCode, amountCents)
	if err != nil {
		operationStatus = operationStatusInvalidCurrency
		return nil, status.Errorf(codes.InvalidArgument, "invalid currency: %v", err)
	}

	// Create domain withdrawal
	domainWithdrawal, err := domain.NewWithdrawal(account.ID(), amount, reference)
	if err != nil {
		operationStatus = opStatusWithdrawalFailed
		return nil, status.Errorf(codes.InvalidArgument, "failed to create withdrawal: %v", err)
	}

	// Persist withdrawal to database (if repository is configured)
	if s.withdrawalRepo != nil {
		if err := s.withdrawalRepo.Create(ctx, domainWithdrawal); err != nil {
			operationStatus = opStatusSaveFailed
			s.logger.Error("failed to persist withdrawal",
				"withdrawal_id", domainWithdrawal.ID,
				"account_id", req.AccountId,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to persist withdrawal: %v", err)
		}
	}

	s.logger.Info("withdrawal initiated",
		"withdrawal_id", domainWithdrawal.ID,
		"withdrawal_reference", reference,
		"account_id", req.AccountId,
		"amount_cents", amountCents)

	// Convert domain withdrawal to proto
	withdrawal := toProtoWithdrawal(domainWithdrawal, req.AccountId)
	withdrawal.Description = req.Description // Add description from request

	return &pb.InitiateWithdrawalResponse{
		Withdrawal:         withdrawal,
		ValidationPassed:   len(validationMessages) == 0,
		ValidationMessages: validationMessages,
	}, nil
}

// UpdateWithdrawal modifies a pending withdrawal before execution.
// Only withdrawals with INITIATED status can be updated.
// Note: Currently supports retrieval and validation only. Field updates (amount, description)
// are not yet supported as the domain model treats these as immutable.
func (s *Service) UpdateWithdrawal(ctx context.Context, req *pb.UpdateWithdrawalRequest) (*pb.UpdateWithdrawalResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("update_withdrawal", operationStatus, time.Since(start))
	}()

	if req.WithdrawalId == "" {
		operationStatus = opStatusMissingWithdrawalID
		return nil, status.Error(codes.InvalidArgument, "withdrawal_id is required")
	}

	// Check if withdrawal repository is configured
	if s.withdrawalRepo == nil {
		operationStatus = opStatusNotImplemented
		return nil, status.Error(codes.Unimplemented, "withdrawal persistence not configured")
	}

	// Lookup withdrawal by reference
	withdrawal, err := s.withdrawalRepo.FindByReference(ctx, req.WithdrawalId)
	if err != nil {
		if errors.Is(err, persistence.ErrWithdrawalNotFound) {
			operationStatus = opStatusWithdrawalNotFound
			return nil, status.Errorf(codes.NotFound, "withdrawal not found: %s", req.WithdrawalId)
		}
		operationStatus = opStatusRetrieveFailed
		s.logger.Error("failed to retrieve withdrawal",
			"withdrawal_id", req.WithdrawalId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to retrieve withdrawal: %v", err)
	}

	// Only pending withdrawals can be updated
	if !withdrawal.IsPending() {
		operationStatus = "withdrawal_not_pending"
		return nil, status.Errorf(codes.FailedPrecondition,
			"cannot update withdrawal %s: not in pending status (current: %s)",
			req.WithdrawalId, withdrawal.Status)
	}

	// Get account to retrieve business account ID for response
	account, err := s.repo.FindByUUID(ctx, withdrawal.AccountID)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found for withdrawal")
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	var validationMessages []string

	// Note: Field updates (amount, description, reference) are not yet implemented
	// as the domain model treats these as immutable after creation.
	// This would require domain model enhancements.
	if req.Amount != nil {
		validationMessages = append(validationMessages,
			"Warning: amount updates are not yet supported; withdrawal amount unchanged")
	}
	if req.Description != "" {
		validationMessages = append(validationMessages,
			"Warning: description updates are not yet supported")
	}
	if req.Reference != "" && req.Reference != withdrawal.Reference {
		validationMessages = append(validationMessages,
			"Warning: reference updates are not yet supported")
	}

	s.logger.Info("withdrawal update requested",
		"withdrawal_id", req.WithdrawalId,
		"has_amount_update", req.Amount != nil,
		"has_description_update", req.Description != "",
		"warnings", len(validationMessages))

	return &pb.UpdateWithdrawalResponse{
		Withdrawal:         toProtoWithdrawal(withdrawal, account.AccountID()),
		ValidationPassed:   len(validationMessages) == 0,
		ValidationMessages: validationMessages,
	}, nil
}

// RetrieveWithdrawal gets withdrawal details by ID or lists withdrawals by account.
// When withdrawal_id is provided, returns a single withdrawal.
// When account_id is provided, returns a paginated list of withdrawals for that account.
func (s *Service) RetrieveWithdrawal(ctx context.Context, req *pb.RetrieveWithdrawalRequest) (*pb.RetrieveWithdrawalResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("retrieve_withdrawal", operationStatus, time.Since(start))
	}()

	// Validate that at least one identifier is provided first
	if req.WithdrawalId == "" && req.AccountId == "" {
		operationStatus = opStatusMissingIdentifier
		return nil, status.Error(codes.InvalidArgument, "either withdrawal_id or account_id is required")
	}

	// Check if withdrawal repository is configured
	if s.withdrawalRepo == nil {
		// For backward compatibility, return empty list when listing by account ID
		// but error when looking up by withdrawal ID
		if req.WithdrawalId != "" {
			operationStatus = opStatusNotImplemented
			return nil, status.Error(codes.Unimplemented, "withdrawal persistence not configured")
		}
		// Validate account exists before returning empty list
		_, err := s.repo.FindByID(ctx, req.AccountId)
		if err != nil {
			if errors.Is(err, persistence.ErrAccountNotFound) {
				operationStatus = opStatusAccountNotFound
				return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
			}
			operationStatus = opStatusRetrieveFailed
			return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
		}
		// Return empty list for account queries when repo not configured
		return &pb.RetrieveWithdrawalResponse{
			Withdrawals: []*pb.Withdrawal{},
			Pagination: &commonpb.PaginationResponse{
				NextPageToken: "",
				TotalCount:    0,
			},
		}, nil
	}

	// Single withdrawal lookup by ID
	if req.WithdrawalId != "" {
		withdrawal, err := s.withdrawalRepo.FindByReference(ctx, req.WithdrawalId)
		if err != nil {
			if errors.Is(err, persistence.ErrWithdrawalNotFound) {
				operationStatus = opStatusWithdrawalNotFound
				return nil, status.Errorf(codes.NotFound, "withdrawal not found: %s", req.WithdrawalId)
			}
			operationStatus = opStatusRetrieveFailed
			s.logger.Error("failed to retrieve withdrawal",
				"withdrawal_id", req.WithdrawalId,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to retrieve withdrawal: %v", err)
		}

		// Get account to retrieve business account ID for response
		account, err := s.repo.FindByUUID(ctx, withdrawal.AccountID)
		if err != nil {
			if errors.Is(err, persistence.ErrAccountNotFound) {
				operationStatus = opStatusAccountNotFound
				return nil, status.Errorf(codes.NotFound, "account not found for withdrawal")
			}
			operationStatus = opStatusRetrieveFailed
			return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
		}

		s.logger.Debug("withdrawal retrieved",
			"withdrawal_id", req.WithdrawalId,
			"account_id", account.AccountID(),
			"status", withdrawal.Status)

		return &pb.RetrieveWithdrawalResponse{
			Withdrawals: []*pb.Withdrawal{toProtoWithdrawal(withdrawal, account.AccountID())},
			Pagination: &commonpb.PaginationResponse{
				TotalCount: 1,
			},
		}, nil
	}

	// List withdrawals by account
	if req.AccountId != "" {
		// Validate account exists and get the internal UUID
		account, err := s.repo.FindByID(ctx, req.AccountId)
		if err != nil {
			if errors.Is(err, persistence.ErrAccountNotFound) {
				operationStatus = opStatusAccountNotFound
				return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
			}
			operationStatus = opStatusRetrieveFailed
			return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
		}

		// Parse pagination parameters
		pagination := persistence.PaginationParams{
			Limit:  50, // Default limit
			Offset: 0,
		}
		if req.Pagination != nil {
			if req.Pagination.PageSize > 0 {
				pagination.Limit = int(req.Pagination.PageSize)
			}
			// Simple offset-based pagination from page token
			// In production, consider cursor-based pagination for better performance
			if req.Pagination.PageToken != "" {
				// Page token contains the offset
				var offset int
				if _, err := fmt.Sscanf(req.Pagination.PageToken, "%d", &offset); err != nil {
					operationStatus = opStatusRetrieveFailed
					return nil, status.Errorf(codes.InvalidArgument, "invalid page token: %s", req.Pagination.PageToken)
				}
				pagination.Offset = offset
			}
		}

		// List withdrawals for the account
		withdrawals, err := s.withdrawalRepo.List(ctx, account.ID(), pagination)
		if err != nil {
			operationStatus = opStatusRetrieveFailed
			s.logger.Error("failed to list withdrawals",
				"account_id", req.AccountId,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to list withdrawals: %v", err)
		}

		// Convert domain withdrawals to proto
		protoWithdrawals := make([]*pb.Withdrawal, 0, len(withdrawals))
		for _, w := range withdrawals {
			protoWithdrawals = append(protoWithdrawals, toProtoWithdrawal(w, req.AccountId))
		}

		// Build pagination response
		var nextPageToken string
		if len(withdrawals) == pagination.Limit {
			// There might be more results
			nextPageToken = fmt.Sprintf("%d", pagination.Offset+pagination.Limit)
		}

		s.logger.Debug("withdrawals listed",
			"account_id", req.AccountId,
			"count", len(withdrawals),
			"has_more", nextPageToken != "")

		return &pb.RetrieveWithdrawalResponse{
			Withdrawals: protoWithdrawals,
			Pagination: &commonpb.PaginationResponse{
				NextPageToken: nextPageToken,
				TotalCount:    int64(len(withdrawals)),
			},
		}, nil
	}

	operationStatus = opStatusMissingIdentifier
	return nil, status.Error(codes.InvalidArgument, "either withdrawal_id or account_id is required")
}

// Helper functions

func toProtoFacility(account domain.CurrentAccount) *pb.CurrentAccountFacility {
	return &pb.CurrentAccountFacility{
		AccountId:             account.AccountID(),
		AccountIdentification: account.AccountIdentification(),
		AccountStatus:         mapStatusToProto(account.Status()),
		BaseCurrency:          mapCurrencyToProto(string(account.Balance().Currency())),
		CreatedAt:             timestamppb.New(account.CreatedAt()),
		UpdatedAt:             timestamppb.New(account.UpdatedAt()),
		// #nosec G115 - Version is bounded by database constraints
		Version: int32(account.Version()),
		CurrentBalance: &pb.AccountBalance{
			CurrentBalance:   toMoneyAmount(account.Balance()),
			AvailableBalance: toMoneyAmount(account.AvailableBalance()),
			LastUpdated:      timestamppb.New(account.BalanceUpdatedAt()),
		},
		OverdraftLimit: &pb.OverdraftConfiguration{
			OverdraftLimit: toMoneyAmount(account.OverdraftLimit()),
			InterestRate:   account.OverdraftRate(),
			IsEnabled:      account.OverdraftEnabled(),
			LastUpdated:    timestamppb.New(time.Now()),
		},
	}
}

// safeMinorUnits converts Money to minor units (cents) with overflow protection.
// Returns 0 if overflow occurs (should not happen in practice for valid accounts).
// Used for logging and metrics where returning an error is not practical.
func safeMinorUnits(m domain.Money) int64 {
	cents, err := m.ToMinorUnits()
	if err != nil {
		// This should never happen in practice - int64 max is ~92 quadrillion cents
		// Log the anomaly for visibility, then return 0 rather than panicking
		slog.Error("amount overflow in metrics conversion",
			"currency", m.Currency(),
			"error", err)
		return 0
	}
	return cents
}

func toMoneyAmount(m domain.Money) *commonpb.MoneyAmount {
	amountCents := safeMinorUnits(m)
	units := amountCents / 100
	remainder := amountCents % 100

	// Convert remainder to nanos (9 digits, but we only use 8 for cents precision)
	// Per google.type.Money spec: nanos MUST share the sign of units
	// - Positive amounts: both units and nanos are positive or zero
	// - Negative amounts: both units and nanos are negative or zero
	// Example: -£1.23 = Units=-1, Nanos=-230000000
	// #nosec G115 - remainder is always -99 to 99, multiplication result fits in int32
	nanos := int32(remainder * 10000000)

	return &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: string(m.Currency()),
			Units:        units,
			Nanos:        nanos,
		},
	}
}

// toProtoWithdrawal converts a domain Withdrawal to a proto Withdrawal.
// Note: accountID is the business account ID (e.g., "ACC-xxx") which is passed separately
// since the domain withdrawal only stores the internal UUID.
func toProtoWithdrawal(w *domain.Withdrawal, accountID string) *pb.Withdrawal {
	return &pb.Withdrawal{
		WithdrawalId: w.Reference, // Reference is the business ID (e.g., "WTH-xxx")
		AccountId:    accountID,
		Amount:       toMoneyAmount(w.Amount),
		Status:       mapWithdrawalStatusToProto(w.Status),
		Reference:    w.Reference,
		CreatedAt:    timestamppb.New(w.CreatedAt),
		UpdatedAt:    timestamppb.New(w.UpdatedAt),
	}
}

// mapWithdrawalStatusToProto converts domain WithdrawalStatus to proto WithdrawalStatus
func mapWithdrawalStatusToProto(status domain.WithdrawalStatus) pb.WithdrawalStatus {
	switch status {
	case domain.WithdrawalStatusPending:
		return pb.WithdrawalStatus_WITHDRAWAL_STATUS_INITIATED
	case domain.WithdrawalStatusCompleted:
		return pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED
	case domain.WithdrawalStatusFailed:
		return pb.WithdrawalStatus_WITHDRAWAL_STATUS_FAILED
	case domain.WithdrawalStatusCancelled:
		return pb.WithdrawalStatus_WITHDRAWAL_STATUS_CANCELLED
	default:
		return pb.WithdrawalStatus_WITHDRAWAL_STATUS_UNSPECIFIED
	}
}

func mapStatusToProto(status domain.AccountStatus) pb.AccountStatus {
	switch status {
	case domain.AccountStatusActive:
		return pb.AccountStatus_ACCOUNT_STATUS_ACTIVE
	case domain.AccountStatusFrozen:
		return pb.AccountStatus_ACCOUNT_STATUS_FROZEN
	case domain.AccountStatusClosed:
		return pb.AccountStatus_ACCOUNT_STATUS_CLOSED
	default:
		return pb.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED
	}
}

func mapCurrencyToProto(currency string) commonpb.Currency {
	switch currency {
	case currencyGBP:
		return commonpb.Currency_CURRENCY_GBP
	case currencyUSD:
		return commonpb.Currency_CURRENCY_USD
	case currencyEUR:
		return commonpb.Currency_CURRENCY_EUR
	default:
		return commonpb.Currency_CURRENCY_UNSPECIFIED
	}
}

const (
	currencyGBP = "GBP"
	currencyUSD = "USD"
	currencyEUR = "EUR"
)

func mapCurrency(currency commonpb.Currency) string {
	switch currency {
	case commonpb.Currency_CURRENCY_GBP:
		return currencyGBP
	case commonpb.Currency_CURRENCY_USD:
		return currencyUSD
	case commonpb.Currency_CURRENCY_EUR:
		return currencyEUR
	case commonpb.Currency_CURRENCY_UNSPECIFIED,
		commonpb.Currency_CURRENCY_JPY,
		commonpb.Currency_CURRENCY_CHF,
		commonpb.Currency_CURRENCY_CAD,
		commonpb.Currency_CURRENCY_AUD:
		// Return empty string for unsupported currencies
		// Caller should validate and return error
		return ""
	default:
		return ""
	}
}

// UpdateCurrentAccount modifies account configuration settings.
// BIAN: Update Control Record (UpCR) - Updates overdraft settings.
// Uses optimistic locking to prevent lost updates from concurrent modifications.
func (s *Service) UpdateCurrentAccount(ctx context.Context, req *pb.UpdateCurrentAccountRequest) (*pb.UpdateCurrentAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("update_account", operationStatus, time.Since(start))
	}()

	// Validate required fields
	if req.AccountId == "" {
		operationStatus = opStatusMissingAccountID
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}

	// Retrieve account (context carries organization for multi-tenant routing)
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Check if account is closed - cannot update closed accounts
	if account.Status() == domain.AccountStatusClosed {
		operationStatus = opStatusAccountClosed
		return nil, status.Errorf(codes.FailedPrecondition, "cannot update closed account: %s", req.AccountId)
	}

	// Track if any updates were made
	updated := false

	// Apply overdraft settings updates if any overdraft fields are provided
	if req.OverdraftLimit != nil || req.OverdraftEnabled != nil || req.OverdraftRate != nil {
		// Determine new overdraft values, using current values as defaults
		newLimit := account.OverdraftLimit()
		newRate := account.OverdraftRate()
		newEnabled := account.OverdraftEnabled()

		// Apply overdraft limit if provided
		if req.OverdraftLimit != nil {
			limitCurrency := req.OverdraftLimit.Amount.CurrencyCode
			if limitCurrency != account.Balance().CurrencyCode() {
				operationStatus = opStatusCurrencyMismatch
				return nil, status.Errorf(codes.InvalidArgument,
					"overdraft limit currency mismatch: expected %s, got %s",
					account.Balance().CurrencyCode(), limitCurrency)
			}

			// Convert to minor units
			limitCents := req.OverdraftLimit.Amount.Units*100 + int64(req.OverdraftLimit.Amount.Nanos/10000000)
			var err error
			newLimit, err = domain.NewMoney(limitCurrency, limitCents)
			if err != nil {
				operationStatus = operationStatusInvalidCurrency
				return nil, status.Errorf(codes.InvalidArgument, "invalid overdraft limit: %v", err)
			}
		}

		// Apply overdraft rate if provided
		if req.OverdraftRate != nil {
			newRate = *req.OverdraftRate
		}

		// Apply overdraft enabled if provided
		if req.OverdraftEnabled != nil {
			newEnabled = *req.OverdraftEnabled
		}

		// Use domain method to update overdraft settings with validation
		account, err = account.UpdateOverdraftSettings(newLimit, newRate, newEnabled)
		if err != nil {
			if errors.Is(err, domain.ErrNegativeOverdraftRate) {
				operationStatus = opStatusInvalidAmount
				return nil, status.Errorf(codes.InvalidArgument, "invalid overdraft rate: %v", err)
			}
			operationStatus = "update_overdraft_failed"
			return nil, status.Errorf(codes.InvalidArgument, "failed to update overdraft settings: %v", err)
		}
		updated = true

		s.logger.Info("overdraft settings updated",
			"account_id", req.AccountId,
			"overdraft_enabled", newEnabled,
			"overdraft_rate", newRate)
	}

	// If no updates were made, return current state
	if !updated {
		s.logger.Debug("no changes to apply for UpdateCurrentAccount",
			"account_id", req.AccountId)
		return &pb.UpdateCurrentAccountResponse{
			Facility: toProtoFacility(account),
			Version:  account.Version(),
		}, nil
	}

	// Persist with optimistic locking
	if err := s.repo.Save(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			operationStatus = "version_conflict"
			s.logger.Warn("version conflict during account update",
				"account_id", req.AccountId)
			return nil, status.Errorf(codes.Aborted, "version conflict: account was modified by another transaction, please retry")
		}
		operationStatus = opStatusSaveFailed
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	s.logger.Info("account updated successfully",
		"account_id", req.AccountId,
		"new_version", account.Version())

	return &pb.UpdateCurrentAccountResponse{
		Facility: toProtoFacility(account),
		Version:  account.Version(),
	}, nil
}

// ControlCurrentAccount performs lifecycle state transitions on an account.
// BIAN: Control Control Record (CoCR) - Freeze, Unfreeze, or Close accounts.
// All control actions are logged with timestamps and reasons for audit compliance.
//
// State transitions:
//   - FREEZE: ACTIVE → FROZEN (requires reason of at least 10 characters)
//   - UNFREEZE: FROZEN → ACTIVE
//   - CLOSE: ACTIVE or FROZEN → CLOSED (requires zero balance and no active liens)
func (s *Service) ControlCurrentAccount(ctx context.Context, req *pb.ControlCurrentAccountRequest) (*pb.ControlCurrentAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("control_account", operationStatus, time.Since(start))
	}()

	// Validate required fields
	if req.AccountId == "" {
		operationStatus = opStatusMissingAccountID
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}

	// Retrieve account
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = opStatusAccountNotFound
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = opStatusRetrieveFailed
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	actionTimestamp := time.Now()

	// Apply control action based on the action type
	switch req.ControlAction {
	case pb.ControlAction_CONTROL_ACTION_FREEZE:
		// Validate reason length (domain layer also validates, but we provide clearer error message)
		if len(req.Reason) < 10 {
			operationStatus = "invalid_freeze_reason"
			return nil, status.Errorf(codes.InvalidArgument, "freeze reason must be at least 10 characters, got %d", len(req.Reason))
		}

		account, err = account.Freeze(req.Reason)
		if err != nil {
			if errors.Is(err, domain.ErrInvalidStatusTransition) {
				operationStatus = opStatusInvalidStatusTransition
				return nil, status.Errorf(codes.FailedPrecondition, "cannot freeze account in status %s: only ACTIVE accounts can be frozen", account.Status())
			}
			if errors.Is(err, domain.ErrInvalidFreezeReason) {
				operationStatus = "invalid_freeze_reason"
				return nil, status.Errorf(codes.InvalidArgument, "freeze reason must be at least 10 characters")
			}
			operationStatus = "freeze_failed"
			return nil, status.Errorf(codes.Internal, "failed to freeze account: %v", err)
		}

		s.logger.Info("account frozen",
			"account_id", req.AccountId,
			"reason", req.Reason)

	case pb.ControlAction_CONTROL_ACTION_UNFREEZE:
		account, err = account.Unfreeze()
		if err != nil {
			if errors.Is(err, domain.ErrInvalidStatusTransition) {
				operationStatus = opStatusInvalidStatusTransition
				return nil, status.Errorf(codes.FailedPrecondition, "cannot unfreeze account in status %s: only FROZEN accounts can be unfrozen", account.Status())
			}
			operationStatus = "unfreeze_failed"
			return nil, status.Errorf(codes.Internal, "failed to unfreeze account: %v", err)
		}

		s.logger.Info("account unfrozen",
			"account_id", req.AccountId)

	case pb.ControlAction_CONTROL_ACTION_CLOSE:
		// Hydrate account with balance from Position Keeping before close validation
		account, err = s.hydrateAccountWithBalance(ctx, account)
		if err != nil {
			operationStatus = opStatusRetrieveFailed
			s.logger.Error("failed to hydrate account balance from Position Keeping for close validation",
				"account_id", req.AccountId,
				"error", err)
			return nil, status.Errorf(codes.Internal, "failed to retrieve account balance: %v", err)
		}

		// Validate balance is zero before attempting close
		if !account.Balance().IsZero() {
			operationStatus = "non_zero_balance"
			balanceCents, _ := account.Balance().ToMinorUnits()
			return nil, status.Errorf(codes.FailedPrecondition, "cannot close account with non-zero balance: %d cents", balanceCents)
		}

		// Check for active liens (requires lienRepo)
		if s.lienRepo != nil {
			activeLienCount, err := s.lienRepo.CountActiveByAccountID(ctx, account.ID())
			if err != nil {
				operationStatus = "lien_check_failed"
				s.logger.Error("failed to check active liens for account close",
					"account_id", req.AccountId,
					"error", err)
				return nil, status.Errorf(codes.Internal, "failed to check active liens: %v", err)
			}
			if activeLienCount > 0 {
				operationStatus = "active_liens_exist"
				return nil, status.Errorf(codes.FailedPrecondition, "cannot close account with %d active liens", activeLienCount)
			}
		}

		// Attempt to close via domain (pass reason for audit trail)
		account, err = account.Close(req.Reason)
		if err != nil {
			if errors.Is(err, domain.ErrInvalidStatusTransition) {
				operationStatus = opStatusInvalidStatusTransition
				return nil, status.Errorf(codes.FailedPrecondition, "cannot close account in status %s: account is already closed", account.Status())
			}
			if errors.Is(err, domain.ErrNonZeroBalance) {
				operationStatus = "non_zero_balance"
				return nil, status.Errorf(codes.FailedPrecondition, "cannot close account with non-zero balance")
			}
			operationStatus = "close_failed"
			return nil, status.Errorf(codes.Internal, "failed to close account: %v", err)
		}

		s.logger.Info("account closed",
			"account_id", req.AccountId,
			"reason", req.Reason)

	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		operationStatus = "unspecified_action"
		return nil, status.Error(codes.InvalidArgument, "control_action is required and cannot be UNSPECIFIED")

	default:
		operationStatus = "unknown_action"
		return nil, status.Errorf(codes.InvalidArgument, "unknown control action: %v", req.ControlAction)
	}

	// Persist with optimistic locking
	if err := s.repo.Save(ctx, account); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			operationStatus = "version_conflict"
			s.logger.Warn("version conflict during control action",
				"account_id", req.AccountId,
				"action", req.ControlAction.String())
			return nil, status.Errorf(codes.Aborted, "version conflict: account was modified by another transaction, please retry")
		}
		operationStatus = opStatusSaveFailed
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	// Emit Kafka events for account lifecycle changes (fire-and-forget pattern)
	// Event publishing errors are logged but don't fail the operation to ensure
	// the business operation completes successfully regardless of messaging issues.
	s.publishControlActionEvent(ctx, req, &account, actionTimestamp)

	// Emit webhook notifications for FREEZE and CLOSE actions (regulatory compliance)
	// Webhooks are sent asynchronously with retry logic - errors are logged but don't fail the operation.
	// Note: UNFREEZE does not require webhook notification per regulatory requirements.
	s.sendControlActionWebhook(ctx, req, &account, actionTimestamp)

	s.logger.Info("control action executed successfully",
		"account_id", req.AccountId,
		"action", req.ControlAction.String(),
		"new_status", account.Status(),
		"new_version", account.Version())

	return &pb.ControlCurrentAccountResponse{
		Facility:        toProtoFacility(account),
		ActionTimestamp: timestamppb.New(actionTimestamp),
	}, nil
}

// publishControlActionEvent emits lifecycle events to Kafka based on the control action.
// This method uses fire-and-forget semantics - errors are logged but don't fail the operation.
// The account balance and reason information is captured from the domain object and request.
func (s *Service) publishControlActionEvent(
	ctx context.Context,
	req *pb.ControlCurrentAccountRequest,
	account *domain.CurrentAccount,
	actionTimestamp time.Time,
) {
	// Skip if event publisher is not configured
	if s.eventPublisher == nil {
		return
	}

	// Extract actor identity from auth context (falls back to "system" if not available)
	actorID := "system"
	if userID, ok := auth.GetUserIDFromContext(ctx); ok && userID != "" {
		actorID = userID
	}

	// Generate correlation ID for event tracing
	correlationID := uuid.New().String()

	// Generate event timestamp
	now := time.Now().UTC()

	// Use AccountID() which returns the business account ID as string
	accountID := account.AccountID()

	switch req.ControlAction {
	case pb.ControlAction_CONTROL_ACTION_FREEZE:
		event := &eventsv1.AccountFrozenEvent{
			EventId:       uuid.New().String(),
			AccountId:     accountID,
			Reason:        req.Reason,
			FrozenAt:      timestamppb.New(actionTimestamp),
			FrozenBy:      actorID,
			CorrelationId: correlationID,
			CausationId:   correlationID,
			Timestamp:     timestamppb.New(now),
			Version:       account.Version(),
		}
		if err := s.eventPublisher.PublishWithTenant(ctx, TopicAccountFrozen, accountID, event); err != nil {
			s.logger.Error("failed to publish account frozen event",
				"account_id", accountID,
				"error", err)
		} else {
			s.logger.Debug("published account frozen event",
				"account_id", accountID,
				"event_id", event.EventId,
				"correlation_id", correlationID)
		}

	case pb.ControlAction_CONTROL_ACTION_UNFREEZE:
		event := &eventsv1.AccountUnfrozenEvent{
			EventId:       uuid.New().String(),
			AccountId:     accountID,
			UnfrozenAt:    timestamppb.New(actionTimestamp),
			UnfrozenBy:    actorID,
			CorrelationId: correlationID,
			CausationId:   correlationID,
			Timestamp:     timestamppb.New(now),
			Version:       account.Version(),
		}
		if err := s.eventPublisher.PublishWithTenant(ctx, TopicAccountUnfrozen, accountID, event); err != nil {
			s.logger.Error("failed to publish account unfrozen event",
				"account_id", accountID,
				"error", err)
		} else {
			s.logger.Debug("published account unfrozen event",
				"account_id", accountID,
				"event_id", event.EventId,
				"correlation_id", correlationID)
		}

	case pb.ControlAction_CONTROL_ACTION_CLOSE:
		// Convert domain balance to google.type.Money
		balanceCents, _ := account.Balance().ToMinorUnits()
		closingBalance := &money.Money{
			CurrencyCode: account.Balance().CurrencyCode(),
			Units:        balanceCents / 100,
			Nanos:        int32((balanceCents % 100) * 10000000),
		}

		event := &eventsv1.AccountClosedEvent{
			EventId:        uuid.New().String(),
			AccountId:      accountID,
			ClosingBalance: closingBalance,
			ClosureReason:  req.Reason,
			ClosedBy:       actorID,
			ClosureDate:    timestamppb.New(actionTimestamp),
			CorrelationId:  correlationID,
			CausationId:    correlationID,
			Timestamp:      timestamppb.New(now),
			Version:        account.Version(),
		}
		if err := s.eventPublisher.PublishWithTenant(ctx, TopicAccountClosed, accountID, event); err != nil {
			s.logger.Error("failed to publish account closed event",
				"account_id", accountID,
				"error", err)
		} else {
			s.logger.Debug("published account closed event",
				"account_id", accountID,
				"event_id", event.EventId,
				"correlation_id", correlationID)
		}

	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		// No event for unspecified action (validation catches this earlier)
	}
}

// sendControlActionWebhook sends webhook notifications for regulatory compliance events.
// This method uses fire-and-forget semantics with async delivery - errors are logged but don't fail the operation.
// Only FREEZE and CLOSE actions trigger webhooks per regulatory requirements (UNFREEZE does not).
func (s *Service) sendControlActionWebhook(
	ctx context.Context,
	req *pb.ControlCurrentAccountRequest,
	account *domain.CurrentAccount,
	actionTimestamp time.Time,
) {
	// Skip if webhook notifier is not configured
	if s.webhookNotifier == nil {
		return
	}

	// Extract tenant ID from context
	tenantID, ok := tenant.FromContext(ctx)
	if !ok || tenantID.String() == "" {
		s.logger.Warn("cannot send webhook: no tenant ID in context",
			"account_id", req.AccountId,
			"action", req.ControlAction.String())
		return
	}

	// Use AccountID() which returns the business account ID as string
	accountID := account.AccountID()

	switch req.ControlAction {
	case pb.ControlAction_CONTROL_ACTION_FREEZE:
		// Send webhook notification asynchronously (fire-and-forget)
		// Using background context intentionally - webhook delivery must complete
		// even if the original request context is cancelled
		//nolint:contextcheck // Intentionally using background context for async webhook delivery
		go s.sendFreezeWebhook(tenantID.String(), accountID, req.Reason, actionTimestamp)

	case pb.ControlAction_CONTROL_ACTION_CLOSE:
		// Capture balance info for the webhook payload
		balanceCents, _ := account.Balance().ToMinorUnits()
		balanceInfo := &WebhookBalanceInfo{
			Amount:       balanceCents,
			CurrencyCode: account.Balance().CurrencyCode(),
		}

		// Send webhook notification asynchronously (fire-and-forget)
		// Using background context intentionally - webhook delivery must complete
		// even if the original request context is cancelled
		//nolint:contextcheck // Intentionally using background context for async webhook delivery
		go s.sendCloseWebhook(tenantID.String(), accountID, req.Reason, balanceInfo, actionTimestamp)

	case pb.ControlAction_CONTROL_ACTION_UNFREEZE:
		// No webhook for unfreeze action per regulatory requirements
		s.logger.Debug("skipping webhook for unfreeze action (not required)",
			"account_id", accountID)

	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		// No webhook for unspecified action (validation catches this earlier)
	}
}

// sendFreezeWebhook sends a webhook notification for an account freeze event.
// This is a helper method to avoid inline goroutines which cause contextcheck linter issues.
// Uses background context intentionally to ensure delivery continues after request completes.
func (s *Service) sendFreezeWebhook(tenantID, accountID, reason string, timestamp time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.webhookNotifier.NotifyAccountFrozen(ctx, tenantID, accountID, reason, timestamp); err != nil {
		s.logger.Error("failed to send account frozen webhook",
			"account_id", accountID,
			"tenant_id", tenantID,
			"error", err)
	} else {
		s.logger.Debug("account frozen webhook sent successfully",
			"account_id", accountID,
			"tenant_id", tenantID)
	}
}

// sendCloseWebhook sends a webhook notification for an account close event.
// This is a helper method to avoid inline goroutines which cause contextcheck linter issues.
// Uses background context intentionally to ensure delivery continues after request completes.
func (s *Service) sendCloseWebhook(tenantID, accountID, reason string, balance *WebhookBalanceInfo, timestamp time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.webhookNotifier.NotifyAccountClosed(ctx, tenantID, accountID, reason, balance, timestamp); err != nil {
		s.logger.Error("failed to send account closed webhook",
			"account_id", accountID,
			"tenant_id", tenantID,
			"error", err)
	} else {
		s.logger.Debug("account closed webhook sent successfully",
			"account_id", accountID,
			"tenant_id", tenantID)
	}
}

// loadSagaAsset loads a saga asset (script or schema) from a configurable base directory.
// Resolves assets from SAGA_ASSET_DIR environment variable if set, otherwise falls back
// to the directory containing the executable. This makes asset loading independent of
// build paths and working directory, supporting containerized deployments.
func loadSagaAsset(relativePath string) (string, error) {
	baseDir := os.Getenv("SAGA_ASSET_DIR")
	if baseDir == "" {
		// Fallback to executable directory
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("failed to resolve saga asset dir: %w", err)
		}
		baseDir = filepath.Dir(exe)
	}

	assetPath := filepath.Join(baseDir, relativePath)
	content, err := os.ReadFile(assetPath)
	if err != nil {
		return "", fmt.Errorf("failed to read saga asset %s: %w", assetPath, err)
	}
	return string(content), nil
}
