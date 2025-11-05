// Package service implements gRPC services for the current account domain
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/internal/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/internal/current-account/clients"
	"github.com/meridianhub/meridian/internal/current-account/domain"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Service implements the CurrentAccountService gRPC service
type Service struct {
	pb.UnimplementedCurrentAccountServiceServer
	repo             *persistence.Repository
	positionClient   clients.PositionKeepingClient
	accountingClient clients.FinancialAccountingClient
	logger           *slog.Logger
}

// NewService creates a new current account service
func NewService(
	repo *persistence.Repository,
	positionClient clients.PositionKeepingClient,
	accountingClient clients.FinancialAccountingClient,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		repo:             repo,
		positionClient:   positionClient,
		accountingClient: accountingClient,
		logger:           logger,
	}
}

// InitiateCurrentAccount creates a new current account facility
func (s *Service) InitiateCurrentAccount(_ context.Context, req *pb.InitiateCurrentAccountRequest) (*pb.InitiateCurrentAccountResponse, error) {
	// Generate account ID
	accountID := fmt.Sprintf("ACC-%s", uuid.New().String()[:8])

	// Map currency enum to string
	currency := mapCurrency(req.BaseCurrency)
	if currency == "" {
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
		return nil, status.Errorf(codes.InvalidArgument, "failed to create account: %v", err)
	}

	// Save to database
	if err := s.repo.Save(account); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create account: %v", err)
	}

	// Convert to proto response
	return &pb.InitiateCurrentAccountResponse{
		AccountId: accountID,
		Facility:  toProtoFacility(account),
	}, nil
}

// ExecuteDeposit processes a deposit transaction
func (s *Service) ExecuteDeposit(ctx context.Context, req *pb.ExecuteDepositRequest) (*pb.ExecuteDepositResponse, error) {
	// Extract or generate correlation ID
	correlationID := ExtractCorrelationID(ctx)

	s.logger.Info("executing deposit",
		"account_id", req.AccountId,
		"correlation_id", correlationID)

	// Retrieve account
	account, err := s.repo.FindByID(req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Validate currency matches account currency
	if req.Amount.Amount.CurrencyCode != account.Balance.Currency() {
		return nil, status.Errorf(codes.InvalidArgument,
			"currency mismatch: expected %s, got %s",
			account.Balance.Currency(), req.Amount.Amount.CurrencyCode)
	}

	// Convert amount from proto (MoneyAmount wraps google.type.Money)
	// Validate overflow: Units*100 must not overflow int64
	if req.Amount.Amount.Units > math.MaxInt64/100 || req.Amount.Amount.Units < math.MinInt64/100 {
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
		return nil, status.Errorf(codes.InvalidArgument, "invalid currency: %v", err)
	}

	nanosMoney, err := domain.NewMoney(req.Amount.Amount.CurrencyCode, int64(nanosCents))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid currency: %v", err)
	}

	amount, err := centsMoney.Add(nanosMoney)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid currency: %v", err)
	}

	// Validate amount is positive
	if amount.AmountCents() <= 0 {
		return nil, status.Errorf(codes.InvalidArgument,
			"deposit amount must be positive, got %d cents", amount.AmountCents())
	}

	// Generate transaction ID
	transactionID := generateTransactionID()

	// Create transaction context for saga orchestration
	txCtx := &DepositTransactionContext{
		AccountID:     account.AccountID,
		TransactionID: transactionID,
		Amount:        amount,
		Description:   req.Description,
		Reference:     req.Reference,
		CorrelationID: correlationID,
		Timestamp:     time.Now(),
		Account:       account,
	}

	// Execute saga orchestration
	if err := s.orchestrateDeposit(ctx, txCtx); err != nil {
		s.logger.Error("deposit orchestration failed",
			"account_id", req.AccountId,
			"transaction_id", transactionID,
			"correlation_id", correlationID,
			"error", err)
		return nil, status.Errorf(codes.Internal, "deposit transaction failed: %v", err)
	}

	// Reload account to get final state after saga
	finalAccount, err := s.repo.FindByID(req.AccountId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to reload account: %v", err)
	}

	// Return response
	return &pb.ExecuteDepositResponse{
		AccountId:        finalAccount.AccountID,
		TransactionId:    transactionID,
		NewBalance:       toMoneyAmount(finalAccount.Balance),
		AvailableBalance: toMoneyAmount(finalAccount.AvailableBalance),
		Status:           pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
	}, nil
}

// RetrieveCurrentAccount gets current account details
func (s *Service) RetrieveCurrentAccount(_ context.Context, req *pb.RetrieveCurrentAccountRequest) (*pb.RetrieveCurrentAccountResponse, error) {
	account, err := s.repo.FindByID(req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
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
