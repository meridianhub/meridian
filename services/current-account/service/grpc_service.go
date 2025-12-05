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
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/clients"
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
)

// Operation status constants for consistency across the service
const (
	operationStatusSuccess         = "success"
	operationStatusInvalidCurrency = "invalid_currency"
)

// Service implements the CurrentAccountService gRPC service
type Service struct {
	pb.UnimplementedCurrentAccountServiceServer
	repo             *persistence.Repository
	lienRepo         *persistence.LienRepository
	posKeepingClient clients.PositionKeepingClient
	finAcctClient    clients.FinancialAccountingClient
	logger           *slog.Logger
	tracer           *observability.Tracer
}

// Config contains configuration for creating a new Service with external clients
type Config struct {
	Repository                *persistence.Repository
	LienRepository            *persistence.LienRepository
	PositionKeepingTarget     string // e.g., "positionkeeping-service:50051"
	FinancialAccountingTarget string // e.g., "financialaccounting-service:50052"
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

	return &Service{
		repo:             repo,
		lienRepo:         lienRepo,
		posKeepingClient: posKeepingClient,
		finAcctClient:    finAcctClient,
		logger:           logger,
		tracer:           tracer,
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

	return &Service{
		repo:             config.Repository,
		lienRepo:         config.LienRepository,
		posKeepingClient: resilientPosKeepingClient,
		finAcctClient:    resilientFinAcctClient,
		logger:           logger,
		tracer:           config.Tracer,
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

	// Create domain model
	account, err := domain.NewCurrentAccount(
		accountID,
		req.AccountIdentification,
		req.CustomerId,
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
	caobservability.RecordBalance(account.Balance.AmountCents(), currency)

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

	// Retrieve account
	account, err := s.repo.FindByID(req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			operationStatus = "account_not_found"
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		operationStatus = "retrieve_failed"
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Validate currency matches account currency
	if req.Amount.Amount.CurrencyCode != account.Balance.Currency() {
		operationStatus = "currency_mismatch"
		return nil, status.Errorf(codes.InvalidArgument,
			"currency mismatch: expected %s, got %s",
			account.Balance.Currency(), req.Amount.Amount.CurrencyCode)
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

	// Execute deposit on domain model
	if err := account.Deposit(amount); err != nil {
		operationStatus = "deposit_failed"
		return nil, status.Errorf(codes.InvalidArgument, "deposit failed: %v", err)
	}

	// Generate transaction ID
	transactionID := fmt.Sprintf("TXN-%s", uuid.New().String()[:8])

	// If clients are not configured, fall back to simple save (backward compatibility)
	if s.posKeepingClient == nil || s.finAcctClient == nil {
		s.logger.Info("executing deposit without transaction orchestration (clients not configured)",
			"account_id", account.AccountID,
			"transaction_id", transactionID)

		if err := s.repo.Save(ctx, account); err != nil {
			operationStatus = "save_failed"
			return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
		}

		// Record business metrics
		caobservability.RecordDeposit(account.Balance.Currency())
		caobservability.RecordBalance(account.Balance.AmountCents(), account.Balance.Currency())

		return &pb.ExecuteDepositResponse{
			AccountId:        account.AccountID,
			TransactionId:    transactionID,
			NewBalance:       toMoneyAmount(account.Balance),
			AvailableBalance: toMoneyAmount(account.AvailableBalance),
			Status:           pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
		}, nil
	}

	// Orchestrate transaction with saga pattern
	resp, err := s.orchestrateDeposit(ctx, account, amount, transactionID)
	if err != nil {
		operationStatus = "saga_failed"
		return nil, err
	}

	// Record business metrics on success
	caobservability.RecordDeposit(account.Balance.Currency())
	caobservability.RecordBalance(account.Balance.AmountCents(), account.Balance.Currency())

	return resp, nil
}

// orchestrateDeposit orchestrates the distributed transaction using saga pattern
func (s *Service) orchestrateDeposit(ctx context.Context, account *domain.CurrentAccount, amount domain.Money, transactionID string) (*pb.ExecuteDepositResponse, error) {
	sagaStart := time.Now()
	sagaStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordSagaDuration("deposit", sagaStatus, time.Since(sagaStart))
	}()

	// Extract or generate correlation ID
	correlationID := clients.ExtractCorrelationID(ctx)
	if correlationID == "" {
		correlationID = uuid.New().String()
		s.logger.Info("generated new correlation ID", "correlation_id", correlationID)
		// Add the generated correlation ID to the context so it can be propagated
		ctx = observability.WithCorrelationID(ctx, correlationID)
	} else {
		s.logger.Info("using existing correlation ID", "correlation_id", correlationID)
	}

	// Create saga orchestrator
	saga := clients.NewSagaOrchestrator(s.logger)

	// Step 1: Log position in PositionKeeping service
	var positionLogID string
	saga.AddStep("log_position",
		// Action: Create position log entry
		func(stepCtx context.Context) error {
			s.logger.Info("executing log_position step",
				"account_id", account.AccountID,
				"transaction_id", transactionID)

			// Propagate correlation ID
			stepCtx = clients.PropagateCorrelationID(stepCtx)

			// Generate entry ID for idempotency
			entryID := uuid.New().String()

			// Call PositionKeeping service
			resp, err := s.posKeepingClient.UpdateFinancialPositionLog(stepCtx,
				&positionkeepingv1.UpdateFinancialPositionLogRequest{
					LogId: account.AccountID, // Use account ID as position log ID
					NewEntry: &positionkeepingv1.TransactionLogEntry{
						EntryId:       entryID,
						TransactionId: transactionID,
						AccountId:     account.AccountID,
						Amount:        toMoneyAmount(amount),
						Direction:     commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
						Timestamp:     timestamppb.Now(),
						Description:   fmt.Sprintf("Deposit to account %s", account.AccountID),
					},
				},
			)
			if err != nil {
				caobservability.RecordExternalServiceError("position_keeping", "update_log")
				return fmt.Errorf("failed to log position: %w", err)
			}

			positionLogID = resp.Log.LogId

			s.logger.Info("log_position step completed",
				"position_log_id", positionLogID,
				"transaction_id", transactionID)

			return nil
		},
		// Compensate: Reverse position log entry
		func(stepCtx context.Context) error {
			s.logger.Info("compensating log_position step",
				"position_log_id", positionLogID,
				"transaction_id", transactionID)

			if positionLogID == "" {
				s.logger.Warn("cannot compensate log_position: position log ID not captured")
				return ErrPositionLogIDNotFound
			}

			// Propagate correlation ID
			stepCtx = clients.PropagateCorrelationID(stepCtx)

			// Generate entry ID for compensation
			compEntryID := uuid.New().String()
			compTransactionID := fmt.Sprintf("COMP-%s", transactionID)

			// Create compensating entry (debit to reverse the credit)
			_, err := s.posKeepingClient.UpdateFinancialPositionLog(stepCtx,
				&positionkeepingv1.UpdateFinancialPositionLogRequest{
					LogId: positionLogID,
					NewEntry: &positionkeepingv1.TransactionLogEntry{
						EntryId:       compEntryID,
						TransactionId: compTransactionID,
						AccountId:     account.AccountID,
						Amount:        toMoneyAmount(amount),
						Direction:     commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
						Timestamp:     timestamppb.Now(),
						Description:   fmt.Sprintf("Compensation for deposit transaction %s", transactionID),
					},
				},
			)
			if err != nil {
				caobservability.RecordExternalServiceError("position_keeping", "compensate_log")
				return fmt.Errorf("failed to compensate position log: %w", err)
			}

			// Record compensation
			caobservability.RecordSagaCompensation("deposit", "log_position")

			s.logger.Info("log_position compensation completed",
				"position_log_id", positionLogID)

			return nil
		},
	)

	// Step 2: Post to ledger in FinancialAccounting service
	var ledgerPostingID string
	saga.AddStep("post_ledger",
		// Action: Create ledger posting
		func(stepCtx context.Context) error {
			s.logger.Info("executing post_ledger step",
				"account_id", account.AccountID,
				"transaction_id", transactionID)

			// Propagate correlation ID
			stepCtx = clients.PropagateCorrelationID(stepCtx)

			// Convert MoneyAmount to google.type.Money for the request
			moneyAmt := toMoneyAmount(amount)

			// Call FinancialAccounting service
			resp, err := s.finAcctClient.CaptureLedgerPosting(stepCtx,
				&financialaccountingv1.CaptureLedgerPostingRequest{
					FinancialBookingLogId: account.AccountID, // Use account ID as booking log ID
					PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_CREDIT,
					PostingAmount:         moneyAmt.Amount,
					AccountId:             account.AccountID,
					ValueDate:             timestamppb.Now(),
					IdempotencyKey: &commonpb.IdempotencyKey{
						Key: transactionID,
					},
				},
			)
			if err != nil {
				caobservability.RecordExternalServiceError("financial_accounting", "capture_posting")
				return fmt.Errorf("failed to post to ledger: %w", err)
			}

			ledgerPostingID = resp.LedgerPosting.Id

			s.logger.Info("post_ledger step completed",
				"ledger_posting_id", ledgerPostingID,
				"transaction_id", transactionID)

			return nil
		},
		// Compensate: Reverse ledger posting
		func(stepCtx context.Context) error {
			s.logger.Info("compensating post_ledger step",
				"ledger_posting_id", ledgerPostingID,
				"transaction_id", transactionID)

			if ledgerPostingID == "" {
				s.logger.Warn("cannot compensate post_ledger: ledger posting ID not captured")
				return ErrLedgerPostingIDNotFound
			}

			// Propagate correlation ID
			stepCtx = clients.PropagateCorrelationID(stepCtx)

			// Convert MoneyAmount to google.type.Money for the request
			moneyAmt := toMoneyAmount(amount)

			// Create compensating ledger entry (debit to reverse the credit)
			compTransactionID := fmt.Sprintf("COMP-%s", transactionID)
			_, err := s.finAcctClient.CaptureLedgerPosting(stepCtx,
				&financialaccountingv1.CaptureLedgerPostingRequest{
					FinancialBookingLogId: account.AccountID,
					PostingDirection:      commonpb.PostingDirection_POSTING_DIRECTION_DEBIT,
					PostingAmount:         moneyAmt.Amount,
					AccountId:             account.AccountID,
					ValueDate:             timestamppb.Now(),
					IdempotencyKey: &commonpb.IdempotencyKey{
						Key: compTransactionID,
					},
				},
			)
			if err != nil {
				caobservability.RecordExternalServiceError("financial_accounting", "compensate_posting")
				return fmt.Errorf("failed to compensate ledger posting: %w", err)
			}

			// Record compensation
			caobservability.RecordSagaCompensation("deposit", "post_ledger")

			s.logger.Info("post_ledger compensation completed",
				"ledger_posting_id", ledgerPostingID)

			return nil
		},
	)

	// Step 3: Save account to database (only after external services succeed)
	saga.AddStep("save_account",
		// Action: Persist the updated account balance
		func(stepCtx context.Context) error {
			s.logger.Info("executing save_account step",
				"account_id", account.AccountID,
				"transaction_id", transactionID,
				"new_balance", account.Balance.AmountCents())

			if err := s.repo.Save(stepCtx, account); err != nil {
				return fmt.Errorf("failed to save account: %w", err)
			}

			s.logger.Info("save_account step completed",
				"account_id", account.AccountID,
				"new_balance", account.Balance.AmountCents())

			return nil
		},
		// Compensate: No database save needed - account never persisted
		func(_ context.Context) error {
			s.logger.Info("compensating save_account step (no-op)",
				"account_id", account.AccountID,
				"reason", "external services failed before persisting balance")

			// No action needed - if we reach here, it means the save failed
			// or we're rolling back before the save completed. The account
			// in memory has the updated balance, but it was never persisted,
			// so there's nothing to rollback in the database.
			// The external services (position log and ledger) will be
			// compensated by their respective compensation actions.

			s.logger.Info("save_account compensation completed (no-op)")
			return nil
		},
	)

	// Execute saga
	s.logger.Info("executing deposit saga",
		"account_id", account.AccountID,
		"transaction_id", transactionID,
		"correlation_id", correlationID,
		"steps", saga.StepCount())

	result := saga.Execute(ctx)

	// Handle saga result
	if !result.Success {
		sagaStatus = "failed"
		caobservability.RecordSagaFailure("deposit", result.FailedStep)

		s.logger.Error("deposit saga failed",
			"account_id", account.AccountID,
			"transaction_id", transactionID,
			"failed_step", result.FailedStep,
			"completed_steps", result.CompletedSteps,
			"compensated_steps", result.CompensatedSteps,
			"error", result.Error)

		return nil, status.Errorf(codes.Internal,
			"deposit transaction failed at step %s: %v (compensated %d/%d steps)",
			result.FailedStep, result.Error, result.CompensatedSteps, result.CompletedSteps)
	}

	s.logger.Info("deposit saga completed successfully",
		"account_id", account.AccountID,
		"transaction_id", transactionID,
		"correlation_id", correlationID,
		"completed_steps", result.CompletedSteps)

	// Return successful response
	return &pb.ExecuteDepositResponse{
		AccountId:        account.AccountID,
		TransactionId:    transactionID,
		NewBalance:       toMoneyAmount(account.Balance),
		AvailableBalance: toMoneyAmount(account.AvailableBalance),
		Status:           pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
	}, nil
}

// RetrieveCurrentAccount gets current account details
func (s *Service) RetrieveCurrentAccount(_ context.Context, req *pb.RetrieveCurrentAccountRequest) (*pb.RetrieveCurrentAccountResponse, error) {
	start := time.Now()
	operationStatus := operationStatusSuccess
	defer func() {
		caobservability.RecordOperationDuration("retrieve_account", operationStatus, time.Since(start))
	}()

	account, err := s.repo.FindByID(req.AccountId)
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

func toProtoFacility(account *domain.CurrentAccount) *pb.CurrentAccountFacility {
	return &pb.CurrentAccountFacility{
		AccountId:             account.AccountID,
		AccountIdentification: account.AccountIdentification,
		AccountStatus:         mapStatusToProto(account.Status),
		BaseCurrency:          mapCurrencyToProto(account.Balance.Currency()),
		CreatedAt:             timestamppb.New(account.CreatedAt),
		UpdatedAt:             timestamppb.New(account.UpdatedAt),
		// #nosec G115 - Version is bounded by database constraints
		Version: int32(account.Version),
		CurrentBalance: &pb.AccountBalance{
			CurrentBalance:   toMoneyAmount(account.Balance),
			AvailableBalance: toMoneyAmount(account.AvailableBalance),
			LastUpdated:      timestamppb.New(account.BalanceUpdatedAt),
		},
		OverdraftLimit: &pb.OverdraftConfiguration{
			OverdraftLimit: toMoneyAmount(account.OverdraftLimit),
			InterestRate:   account.OverdraftRate,
			IsEnabled:      account.OverdraftEnabled,
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
			CurrencyCode: m.Currency(),
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
