// Package service implements gRPC services for the current account domain
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/internal/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/internal/current-account/domain"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Service implements the CurrentAccountService gRPC service
type Service struct {
	pb.UnimplementedCurrentAccountServiceServer
	repo *persistence.Repository
}

// NewService creates a new current account service
func NewService(repo *persistence.Repository) *Service {
	return &Service{
		repo: repo,
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
	account := domain.NewCurrentAccount(
		accountID,
		req.AccountIdentification,
		req.CustomerId,
		currency,
	)

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
func (s *Service) ExecuteDeposit(_ context.Context, req *pb.ExecuteDepositRequest) (*pb.ExecuteDepositResponse, error) {
	// Retrieve account
	account, err := s.repo.FindByID(req.AccountId)
	if err != nil {
		if errors.Is(err, persistence.ErrAccountNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Validate currency matches account currency
	if req.Amount.Amount.CurrencyCode != account.Balance.Currency {
		return nil, status.Errorf(codes.InvalidArgument,
			"currency mismatch: expected %s, got %s",
			account.Balance.Currency, req.Amount.Amount.CurrencyCode)
	}

	// Convert amount from proto (MoneyAmount wraps google.type.Money)
	amount := domain.Money{
		AmountCents: req.Amount.Amount.Units*100 + int64(req.Amount.Amount.Nanos/10000000),
		Currency:    req.Amount.Amount.CurrencyCode,
	}

	// Execute deposit
	if err := account.Deposit(amount); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "deposit failed: %v", err)
	}

	// Save updated account
	if err := s.repo.Save(account); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save account: %v", err)
	}

	// Generate transaction ID
	transactionID := fmt.Sprintf("TXN-%s", uuid.New().String()[:8])

	// Return response
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
		BaseCurrency:          mapCurrencyToProto(account.Balance.Currency),
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
	cents := m.AmountCents % 100
	if cents < 0 {
		cents = -cents
	}
	// #nosec G115 - cents is always 0-99, multiplication result fits in int32
	nanos := int32(cents * 10000000)

	return &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: m.Currency,
			Units:        m.AmountCents / 100,
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
