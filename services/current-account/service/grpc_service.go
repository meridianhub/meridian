// Package service implements gRPC services for the current account domain
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/clients"
	"github.com/meridianhub/meridian/services/current-account/config"
	"github.com/meridianhub/meridian/services/current-account/domain"
	caobservability "github.com/meridianhub/meridian/services/current-account/observability"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
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

// Service implements the CurrentAccountService gRPC service
type Service struct {
	pb.UnimplementedCurrentAccountServiceServer
	repo                   *persistence.Repository
	lienRepo               *persistence.LienRepository
	posKeepingClient       clients.PositionKeepingClient
	finAcctClient          clients.FinancialAccountingClient
	partyClient            clients.PartyClient
	accountConfig          *config.AccountConfig
	idempotencyService     idempotency.Service
	logger                 *slog.Logger
	tracer                 *observability.Tracer
	depositOrchestrator    *DepositOrchestrator    // Handles deposit saga orchestration
	withdrawalOrchestrator *WithdrawalOrchestrator // Handles withdrawal saga orchestration
}

// Config contains configuration for creating a new Service with external clients
type Config struct {
	Repository     *persistence.Repository
	LienRepository *persistence.LienRepository
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
func NewServiceWithExistingClients(
	repo *persistence.Repository,
	lienRepo *persistence.LienRepository,
	posKeepingClient clients.PositionKeepingClient,
	finAcctClient clients.FinancialAccountingClient,
	partyClient clients.PartyClient,
	accountConfig *config.AccountConfig,
	idempotencyService idempotency.Service,
	logger *slog.Logger,
	tracer *observability.Tracer,
) (*Service, error) {
	if repo == nil {
		return nil, ErrRepositoryNil
	}

	// Apply default logger if not provided
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	// Create deposit orchestrator
	depositOrchestrator, err := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:           logger,
		Repo:             repo,
		PosKeepingClient: posKeepingClient,
		FinAcctClient:    finAcctClient,
		AccountConfig:    accountConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create deposit orchestrator: %w", err)
	}

	// Create withdrawal orchestrator
	withdrawalOrchestrator, err := NewWithdrawalOrchestrator(WithdrawalOrchestratorConfig{
		Logger:           logger,
		Repo:             repo,
		PosKeepingClient: posKeepingClient,
		FinAcctClient:    finAcctClient,
		AccountConfig:    accountConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create withdrawal orchestrator: %w", err)
	}

	return &Service{
		repo:                   repo,
		lienRepo:               lienRepo,
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

// NewServiceWithClients creates a new current account service with full external client dependencies.
// This factory handles client creation, wrapping with resilience patterns (circuit breaker, retry),
// and validation of all required configuration.
func NewServiceWithClients(config Config) (*Service, error) {
	// Validate required dependencies
	if config.Repository == nil {
		return nil, ErrRepositoryNil
	}
	if config.PositionKeepingServiceName == "" {
		return nil, ErrPositionKeepingServiceNameEmpty
	}
	if config.FinancialAccountingServiceName == "" {
		return nil, ErrFinancialAccountingServiceNameEmpty
	}

	// Apply default logger if not provided
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	// Create Position Keeping client with DNS-based load balancing
	posKeepingGRPCClient, err := clients.NewPositionKeepingClient(&clients.PositionKeepingClientConfig{
		ServiceName: config.PositionKeepingServiceName,
		Namespace:   config.Namespace,
		Port:        config.PositionKeepingPort,
		Timeout:     30 * time.Second,
		Tracer:      config.Tracer,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create position keeping client: %w", err)
	}

	// Wrap with resilience patterns (circuit breaker + retry)
	resilientPosKeepingClient := clients.NewResilientPositionKeepingClient(
		posKeepingGRPCClient,
		sharedclients.ResilientClientConfig{
			Logger: logger,
		},
	)

	// Create Financial Accounting client with DNS-based load balancing
	finAcctGRPCClient, err := clients.NewFinancialAccountingClient(&clients.FinancialAccountingClientConfig{
		ServiceName: config.FinancialAccountingServiceName,
		Namespace:   config.Namespace,
		Port:        config.FinancialAccountingPort,
		Timeout:     30 * time.Second,
		Tracer:      config.Tracer,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create financial accounting client: %w", err)
	}

	// Wrap with resilience patterns (circuit breaker + retry)
	resilientFinAcctClient := clients.NewResilientFinancialAccountingClient(
		finAcctGRPCClient,
		sharedclients.ResilientClientConfig{
			Logger: logger,
		},
	)

	// Create Party client (optional - nil client provides backward compatibility)
	var resilientPartyClient clients.PartyClient
	if config.PartyServiceName != "" {
		partyGRPCClient, err := clients.NewPartyClient(&sharedclients.PartyClientConfig{
			ServiceName: config.PartyServiceName,
			Namespace:   config.Namespace,
			Port:        config.PartyPort,
			Timeout:     30 * time.Second,
			Tracer:      config.Tracer,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create party client: %w", err)
		}

		resilientPartyClient = clients.NewResilientPartyClient(
			partyGRPCClient,
			sharedclients.ResilientClientConfig{
				Logger: logger,
			},
		)
	}

	// Create deposit orchestrator
	depositOrchestrator, err := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:           logger,
		Repo:             config.Repository,
		PosKeepingClient: resilientPosKeepingClient,
		FinAcctClient:    resilientFinAcctClient,
		AccountConfig:    nil, // Not passed in Config - will use defaults
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create deposit orchestrator: %w", err)
	}

	// Create withdrawal orchestrator
	withdrawalOrchestrator, err := NewWithdrawalOrchestrator(WithdrawalOrchestratorConfig{
		Logger:           logger,
		Repo:             config.Repository,
		PosKeepingClient: resilientPosKeepingClient,
		FinAcctClient:    resilientFinAcctClient,
		AccountConfig:    nil, // Not passed in Config - will use defaults
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create withdrawal orchestrator: %w", err)
	}

	return &Service{
		repo:                   config.Repository,
		lienRepo:               config.LienRepository,
		posKeepingClient:       resilientPosKeepingClient,
		finAcctClient:          resilientFinAcctClient,
		partyClient:            resilientPartyClient,
		logger:                 logger,
		tracer:                 config.Tracer,
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

	// Execute deposit on domain model (returns new account, original unchanged)
	account, err = account.Deposit(amount)
	if err != nil {
		operationStatus = "deposit_failed"
		return nil, status.Errorf(codes.InvalidArgument, "deposit failed: %v", err)
	}

	// Generate transaction ID (full UUID required by position-keeping service)
	transactionID := uuid.New().String()

	var resp *pb.ExecuteDepositResponse

	// If clients are not configured, fall back to simple save (backward compatibility)
	if s.posKeepingClient == nil || s.finAcctClient == nil {
		s.logger.Info("executing deposit without transaction orchestration (clients not configured)",
			"account_id", account.AccountID(),
			"transaction_id", transactionID)

		if err := s.repo.Save(ctx, account); err != nil {
			operationStatus = opStatusSaveFailed
			return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
		}

		// Record business metrics
		caobservability.RecordDeposit(string(account.Balance().Currency()))
		caobservability.RecordBalance(safeMinorUnits(account.Balance()), string(account.Balance().Currency()))

		resp = &pb.ExecuteDepositResponse{
			AccountId:        account.AccountID(),
			TransactionId:    transactionID,
			NewBalance:       toMoneyAmount(account.Balance()),
			AvailableBalance: toMoneyAmount(account.AvailableBalance()),
			Status:           pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
		}
	} else {
		// Orchestrate transaction with saga pattern using dedicated orchestrator
		resp, err = s.depositOrchestrator.Orchestrate(ctx, account, amount, transactionID)
		if err != nil {
			operationStatus = opStatusSagaFailed
			return nil, err
		}

		// Record business metrics on success
		caobservability.RecordDeposit(string(account.Balance().Currency()))
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

// RetrieveCurrentAccount gets current account details
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

	if req.WithdrawalId != "" {
		// TODO: In a full implementation, we would look up the pending withdrawal
		// and get the account_id and amount from there. For now, require direct execution.
		operationStatus = "withdrawal_id_not_implemented"
		return nil, status.Error(codes.Unimplemented, "executing pending withdrawals by withdrawal_id is not yet implemented; use direct withdrawal with account_id and amount")
	}

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

	// Check sufficient available balance before attempting withdrawal
	cmp, _ := amount.Compare(account.AvailableBalance())
	if cmp > 0 {
		operationStatus = opStatusInsufficientFunds
		availCents, _ := account.AvailableBalance().ToMinorUnits()
		return nil, status.Errorf(codes.FailedPrecondition,
			"insufficient funds: requested %d cents, available %d cents", amountCents, availCents)
	}

	// Execute withdrawal on domain model (returns new account, original unchanged)
	account, err = account.Withdraw(amount)
	if err != nil {
		if errors.Is(err, domain.ErrInsufficientFunds) {
			operationStatus = opStatusInsufficientFunds
			return nil, status.Errorf(codes.FailedPrecondition, "insufficient funds for withdrawal")
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

	// Generate transaction ID (full UUID required by position-keeping service)
	transactionID := uuid.New().String()

	var resp *pb.ExecuteWithdrawalResponse

	// If clients are not configured, fall back to simple save (backward compatibility)
	if s.posKeepingClient == nil || s.finAcctClient == nil {
		s.logger.Info("executing withdrawal without transaction orchestration (clients not configured)",
			"account_id", account.AccountID(),
			"transaction_id", transactionID)

		if err := s.repo.Save(ctx, account); err != nil {
			operationStatus = opStatusSaveFailed
			return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
		}

		// Record business metrics
		caobservability.RecordWithdrawal(string(account.Balance().Currency()))
		caobservability.RecordBalance(safeMinorUnits(account.Balance()), string(account.Balance().Currency()))

		resp = &pb.ExecuteWithdrawalResponse{
			AccountId:        account.AccountID(),
			TransactionId:    transactionID,
			NewBalance:       toMoneyAmount(account.Balance()),
			AvailableBalance: toMoneyAmount(account.AvailableBalance()),
			Status:           pb.WithdrawalStatus_WITHDRAWAL_STATUS_COMPLETED,
			Timestamp:        timestamppb.Now(),
		}
	} else {
		// Orchestrate transaction with saga pattern using dedicated orchestrator
		resp, err = s.withdrawalOrchestrator.Orchestrate(ctx, account, amount, transactionID)
		if err != nil {
			operationStatus = opStatusSagaFailed
			return nil, err
		}

		// Record business metrics on success
		caobservability.RecordWithdrawal(string(account.Balance().Currency()))
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

	// Generate withdrawal ID
	withdrawalID := fmt.Sprintf("WTH-%s", uuid.New().String()[:8])
	now := timestamppb.Now()

	// Create withdrawal record
	withdrawal := &pb.Withdrawal{
		WithdrawalId: withdrawalID,
		AccountId:    req.AccountId,
		Amount:       req.Amount,
		Status:       pb.WithdrawalStatus_WITHDRAWAL_STATUS_INITIATED,
		Description:  req.Description,
		Reference:    req.Reference,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// TODO: Persist withdrawal to database
	// For now, we return the withdrawal without persistence (in-memory only)
	// A full implementation would save to a withdrawals table

	s.logger.Info("withdrawal initiated",
		"withdrawal_id", withdrawalID,
		"account_id", req.AccountId,
		"amount_cents", amountCents)

	return &pb.InitiateWithdrawalResponse{
		Withdrawal:         withdrawal,
		ValidationPassed:   len(validationMessages) == 0,
		ValidationMessages: validationMessages,
	}, nil
}

// UpdateWithdrawal modifies a pending withdrawal before execution.
// Only withdrawals with INITIATED status can be updated.
func (s *Service) UpdateWithdrawal(_ context.Context, req *pb.UpdateWithdrawalRequest) (*pb.UpdateWithdrawalResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("update_withdrawal", operationStatus, time.Since(start))
	}()

	if req.WithdrawalId == "" {
		operationStatus = opStatusMissingWithdrawalID
		return nil, status.Error(codes.InvalidArgument, "withdrawal_id is required")
	}

	// TODO: Implement withdrawal lookup and update from database
	// For now, return unimplemented as we don't have withdrawal persistence yet
	operationStatus = opStatusNotImplemented
	return nil, status.Error(codes.Unimplemented, "UpdateWithdrawal is not yet implemented - withdrawal persistence required")
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

	// Single withdrawal lookup
	if req.WithdrawalId != "" {
		// TODO: Implement withdrawal lookup from database
		operationStatus = opStatusNotImplemented
		return nil, status.Error(codes.Unimplemented, "RetrieveWithdrawal by withdrawal_id is not yet implemented - withdrawal persistence required")
	}

	// List withdrawals by account
	if req.AccountId != "" {
		// Validate account exists
		_, err := s.repo.FindByID(ctx, req.AccountId)
		if err != nil {
			if errors.Is(err, persistence.ErrAccountNotFound) {
				operationStatus = opStatusAccountNotFound
				return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
			}
			operationStatus = opStatusRetrieveFailed
			return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
		}

		// TODO: Implement withdrawal listing from database
		// For now, return empty list
		return &pb.RetrieveWithdrawalResponse{
			Withdrawals: []*pb.Withdrawal{},
			Pagination: &commonpb.PaginationResponse{
				NextPageToken: "",
				TotalCount:    0,
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

	// TODO: Emit Kafka events for account lifecycle changes
	// Events to emit based on action:
	// - FREEZE: account.frozen with reason and timestamp
	// - UNFREEZE: account.unfrozen with timestamp
	// - CLOSE: account.closed with reason and timestamp
	// This requires Kafka producer integration (future task)

	// TODO: Emit webhook notifications for FREEZE and CLOSE actions (regulatory compliance)
	// This requires webhook integration (future task)

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
