package service

import (
	"context"
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
func (s *Service) InitiateCurrentAccount(ctx context.Context, req *pb.InitiateCurrentAccountRequest) (*pb.InitiateCurrentAccountResponse, error) {
	// Generate account ID
	accountID := fmt.Sprintf("ACC-%s", uuid.New().String()[:8])

	// Map currency enum to string
	currency := mapCurrency(req.BaseCurrency)

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
func (s *Service) ExecuteDeposit(ctx context.Context, req *pb.ExecuteDepositRequest) (*pb.ExecuteDepositResponse, error) {
	// Retrieve account
	account, err := s.repo.FindByID(req.AccountId)
	if err != nil {
		if err == persistence.ErrAccountNotFound {
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.AccountId)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve account: %v", err)
	}

	// Convert amount from proto (MoneyAmount wraps google.type.Money)
	amount := domain.Money{
		AmountCents: req.Amount.Amount.Units*100 + int64(req.Amount.Amount.Nanos/10000000),
		Currency:    account.Balance.Currency,
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
		Status:           "COMPLETED",
	}, nil
}

// RetrieveCurrentAccount gets current account details
func (s *Service) RetrieveCurrentAccount(ctx context.Context, req *pb.RetrieveCurrentAccountRequest) (*pb.RetrieveCurrentAccountResponse, error) {
	account, err := s.repo.FindByID(req.AccountId)
	if err != nil {
		if err == persistence.ErrAccountNotFound {
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
		Version:               int32(account.Version),
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
	return &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: m.Currency,
			Units:        m.AmountCents / 100,
			Nanos:        int32((m.AmountCents % 100) * 10000000),
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
	case "GBP":
		return commonpb.Currency_CURRENCY_GBP
	case "USD":
		return commonpb.Currency_CURRENCY_USD
	case "EUR":
		return commonpb.Currency_CURRENCY_EUR
	default:
		return commonpb.Currency_CURRENCY_UNSPECIFIED
	}
}

func mapCurrency(currency commonpb.Currency) string {
	switch currency {
	case commonpb.Currency_CURRENCY_GBP:
		return "GBP"
	case commonpb.Currency_CURRENCY_USD:
		return "USD"
	case commonpb.Currency_CURRENCY_EUR:
		return "EUR"
	default:
		return "GBP"
	}
}
