// Package service implements gRPC services for the current account domain
//
//nolint:staticcheck // Uses AmountCents() for balance/deposit operations (deprecated for backward compatibility)
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
	"github.com/meridianhub/meridian/shared/platform/observability"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Sentinel errors for consistent error handling
var (
	ErrRepositoryNil                  = errors.New("repository cannot be nil")
	ErrPositionKeepingTargetEmpty     = errors.New("position keeping target cannot be empty")
	ErrFinancialAccountingTargetEmpty = errors.New("financial accounting target cannot be empty")
	ErrOriginalAccountStateNotFound   = errors.New("original account state not available for compensation")
	ErrPositionLogIDNotFound          = errors.New("position log ID not available for compensation")
	ErrLedgerPostingIDNotFound        = errors.New("ledger posting ID not available for compensation")

	// Party validation errors (re-exported from clients package for convenience)
	ErrPartyNotFound  = clients.ErrPartyNotFound
	ErrPartyNotActive = clients.ErrPartyNotActive
)

// Operation status constants for consistency across the service
const (
	operationStatusSuccess         = "success"
	operationStatusInvalidCurrency = "invalid_currency"
)

// Service implements the CurrentAccountService gRPC service
type Service struct {
	pb.UnimplementedCurrentAccountServiceServer
	repo               *persistence.Repository
	lienRepo           *persistence.LienRepository
	posKeepingClient   clients.PositionKeepingClient
	finAcctClient      clients.FinancialAccountingClient
	partyClient        clients.PartyClient
	accountConfig      *config.AccountConfig
	logger             *slog.Logger
	tracer             *observability.Tracer
	depositOrchestator *DepositOrchestrator // Handles deposit saga orchestration
}

// Config contains configuration for creating a new Service with external clients
type Config struct {
	Repository                *persistence.Repository
	LienRepository            *persistence.LienRepository
	PositionKeepingTarget     string // e.g., "positionkeeping-service:50051"
	FinancialAccountingTarget string // e.g., "financialaccounting-service:50052"
	PartyServiceTarget        string // e.g., "party-service:50055"
	Logger                    *slog.Logger
	Tracer                    *observability.Tracer
}

// NewService creates a new current account service with minimal dependencies.
// This is primarily used for testing. For production use, prefer NewServiceWithClients.
func NewService(repo *persistence.Repository, lienRepo *persistence.LienRepository) *Service {
	if repo == nil {
		panic("repository cannot be nil")
	}
	return &Service{
		repo:     repo,
		lienRepo: lienRepo,
		logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
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
	depositOrchestator := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:           logger,
		Repo:             repo,
		PosKeepingClient: posKeepingClient,
		FinAcctClient:    finAcctClient,
		AccountConfig:    accountConfig,
	})

	return &Service{
		repo:               repo,
		lienRepo:           lienRepo,
		posKeepingClient:   posKeepingClient,
		finAcctClient:      finAcctClient,
		partyClient:        partyClient,
		accountConfig:      accountConfig,
		logger:             logger,
		tracer:             tracer,
		depositOrchestator: depositOrchestator,
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
	if config.PositionKeepingTarget == "" {
		return nil, ErrPositionKeepingTargetEmpty
	}
	if config.FinancialAccountingTarget == "" {
		return nil, ErrFinancialAccountingTargetEmpty
	}

	// Apply default logger if not provided
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}

	// Create Position Keeping client
	posKeepingGRPCClient, err := clients.NewPositionKeepingClient(&clients.PositionKeepingClientConfig{
		Target:  config.PositionKeepingTarget,
		Timeout: 30 * time.Second,
		Tracer:  config.Tracer,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create position keeping client: %w", err)
	}

	// Wrap with resilience patterns (circuit breaker + retry)
	resilientPosKeepingClient := clients.NewResilientPositionKeepingClient(
		posKeepingGRPCClient,
		clients.ResilientClientConfig{
			Logger: logger,
		},
	)

	// Create Financial Accounting client
	finAcctGRPCClient, err := clients.NewFinancialAccountingClient(&clients.FinancialAccountingClientConfig{
		Target:  config.FinancialAccountingTarget,
		Timeout: 30 * time.Second,
		Tracer:  config.Tracer,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create financial accounting client: %w", err)
	}

	// Wrap with resilience patterns (circuit breaker + retry)
	resilientFinAcctClient := clients.NewResilientFinancialAccountingClient(
		finAcctGRPCClient,
		clients.ResilientClientConfig{
			Logger: logger,
		},
	)

	// Create Party client (optional - nil client provides backward compatibility)
	var resilientPartyClient clients.PartyClient
	if config.PartyServiceTarget != "" {
		partyGRPCClient, err := clients.NewPartyClient(&clients.PartyClientConfig{
			Target:  config.PartyServiceTarget,
			Timeout: 30 * time.Second,
			Tracer:  config.Tracer,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create party client: %w", err)
		}

		resilientPartyClient = clients.NewResilientPartyClient(
			partyGRPCClient,
			clients.ResilientClientConfig{
				Logger: logger,
			},
		)
	}

	// Create deposit orchestrator
	depositOrchestator := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:           logger,
		Repo:             config.Repository,
		PosKeepingClient: resilientPosKeepingClient,
		FinAcctClient:    resilientFinAcctClient,
		AccountConfig:    nil, // Not passed in Config - will use defaults
	})

	return &Service{
		repo:               config.Repository,
		lienRepo:           config.LienRepository,
		posKeepingClient:   resilientPosKeepingClient,
		finAcctClient:      resilientFinAcctClient,
		partyClient:        resilientPartyClient,
		logger:             logger,
		tracer:             config.Tracer,
		depositOrchestator: depositOrchestator,
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
		operationStatus = "save_failed"
		return nil, status.Errorf(codes.Internal, "failed to create account: %v", err)
	}

	// Record initial balance
	caobservability.RecordBalance(account.Balance().AmountCents(), currency)

	// Convert to proto response
	return &pb.InitiateCurrentAccountResponse{
		AccountId: accountID,
		Facility:  toProtoFacility(account),
	}, nil
}

// ExecuteDeposit processes a deposit transaction
func (s *Service) ExecuteDeposit(ctx context.Context, req *pb.ExecuteDepositRequest) (*pb.ExecuteDepositResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("execute_deposit", operationStatus, time.Since(start))
	}()

	// Retrieve account (context carries organization for multi-tenant routing)
	account, err := s.repo.FindByID(ctx, req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = "account_not_found"
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = "retrieve_failed"
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Validate currency matches account currency
	if req.Amount.Amount.CurrencyCode != account.Balance().CurrencyCode() {
		operationStatus = "currency_mismatch"
		return nil, status.Errorf(codes.InvalidArgument,
			"currency mismatch: expected %s, got %s",
			account.Balance().CurrencyCode(), req.Amount.Amount.CurrencyCode)
	}

	// Convert amount from proto (MoneyAmount wraps google.type.Money)
	// Validate overflow: Units*100 must not overflow int64
	if req.Amount.Amount.Units > math.MaxInt64/100 || req.Amount.Amount.Units < math.MinInt64/100 {
		operationStatus = "amount_overflow"
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
	if amount.AmountCents() <= 0 {
		operationStatus = "invalid_amount"
		return nil, status.Errorf(codes.InvalidArgument,
			"deposit amount must be positive, got %d cents", amount.AmountCents())
	}

	// Execute deposit on domain model (returns new account, original unchanged)
	account, err = account.Deposit(amount)
	if err != nil {
		operationStatus = "deposit_failed"
		return nil, status.Errorf(codes.InvalidArgument, "deposit failed: %v", err)
	}

	// Generate transaction ID (full UUID required by position-keeping service)
	transactionID := uuid.New().String()

	// If clients are not configured, fall back to simple save (backward compatibility)
	if s.posKeepingClient == nil || s.finAcctClient == nil {
		s.logger.Info("executing deposit without transaction orchestration (clients not configured)",
			"account_id", account.AccountID(),
			"transaction_id", transactionID)

		if err := s.repo.Save(ctx, account); err != nil {
			operationStatus = "save_failed"
			return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
		}

		// Record business metrics
		caobservability.RecordDeposit(string(account.Balance().Currency()))
		caobservability.RecordBalance(account.Balance().AmountCents(), string(account.Balance().Currency()))

		return &pb.ExecuteDepositResponse{
			AccountId:        account.AccountID(),
			TransactionId:    transactionID,
			NewBalance:       toMoneyAmount(account.Balance()),
			AvailableBalance: toMoneyAmount(account.AvailableBalance()),
			Status:           pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
		}, nil
	}

	// Orchestrate transaction with saga pattern
	resp, err := s.depositOrchestator.Orchestrate(ctx, account, amount, transactionID)
	if err != nil {
		operationStatus = "saga_failed"
		return nil, err
	}

	// Record business metrics on success
	caobservability.RecordDeposit(string(account.Balance().Currency()))
	caobservability.RecordBalance(account.Balance().AmountCents(), string(account.Balance().Currency()))

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
			operationStatus = "account_not_found"
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = "retrieve_failed"
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	return &pb.RetrieveCurrentAccountResponse{
		Facility: toProtoFacility(account),
	}, nil
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

func toMoneyAmount(m domain.Money) *commonpb.MoneyAmount {
	amountCents := m.AmountCents()
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
