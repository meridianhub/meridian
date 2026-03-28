package service

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/adapters"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

// All 7 BIAN-compliant balance types that can be computed.
var allBalanceTypes = []domain.BalanceType{
	domain.BalanceTypeOpening,
	domain.BalanceTypeClosing,
	domain.BalanceTypeCurrent,
	domain.BalanceTypeAvailable,
	domain.BalanceTypeLedger,
	domain.BalanceTypeReserve,
	domain.BalanceTypeFree,
}

// GetAccountBalance retrieves a specific balance type for an account.
func (s *PositionKeepingService) GetAccountBalance(
	ctx context.Context,
	req *positionkeepingv1.GetAccountBalanceRequest,
) (*positionkeepingv1.GetAccountBalanceResponse, error) {
	if req.GetAccountId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "account_id is required")
	}

	balanceType, err := adapters.ToDomainBalanceType(req.GetBalanceType())
	if err != nil {
		if errors.Is(err, adapters.ErrUnspecifiedBalanceType) {
			return nil, status.Errorf(codes.InvalidArgument, "balance_type is required and cannot be UNSPECIFIED")
		}
		return nil, status.Errorf(codes.InvalidArgument, "invalid balance_type: %v", err)
	}

	log, openingBalance, currency, err := s.loadLogForBalance(ctx, req.GetAccountId())
	if err != nil {
		return nil, err
	}

	if req.GetInstrumentCode() != "" {
		if currency.Code != req.GetInstrumentCode() {
			return nil, status.Errorf(codes.NotFound, "no balance found for instrument: %s", req.GetInstrumentCode())
		}
	}

	lbc, err := domain.NewLogBalanceComputer(log, openingBalance, s.currentAccountClient)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create balance computer: %v", err)
	}

	balance, err := s.computeBalance(ctx, lbc, balanceType)
	if err != nil {
		if errors.Is(err, domain.ErrNilCurrentAccountClient) {
			return nil, status.Errorf(codes.FailedPrecondition, "reserve/available/free balance requires current account client configuration")
		}
		return nil, status.Errorf(codes.Internal, "failed to compute %s balance: %v", balanceType, err)
	}

	return &positionkeepingv1.GetAccountBalanceResponse{
		AccountId:   req.GetAccountId(),
		BalanceType: adapters.ToProtoBalanceType(balance.Type),
		Amount:      adapters.ToProtoInstrumentAmount(balance.Amount),
		AsOf:        timestamppb.New(balance.AsOf),
	}, nil
}

// GetAccountBalances retrieves all balance types for an account.
func (s *PositionKeepingService) GetAccountBalances(
	ctx context.Context,
	req *positionkeepingv1.GetAccountBalancesRequest,
) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	if req.GetAccountId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "account_id is required")
	}

	log, openingBalance, currency, err := s.loadLogForBalance(ctx, req.GetAccountId())
	if err != nil {
		return nil, err
	}

	if req.GetInstrumentCode() != "" {
		if currency.Code != req.GetInstrumentCode() {
			return nil, status.Errorf(codes.NotFound, "no balances found for instrument: %s", req.GetInstrumentCode())
		}
	}

	lbc, err := domain.NewLogBalanceComputer(log, openingBalance, s.currentAccountClient)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create balance computer: %v", err)
	}

	asOf := time.Now().UTC()
	balanceEntries := make([]*positionkeepingv1.BalanceEntry, 0, len(allBalanceTypes))

	for _, balanceType := range allBalanceTypes {
		balance, err := s.computeBalance(ctx, lbc, balanceType)
		if err != nil {
			// Skip balance types that cannot be computed (e.g., no CurrentAccountClient)
			continue
		}

		balanceEntries = append(balanceEntries, &positionkeepingv1.BalanceEntry{
			BalanceType: adapters.ToProtoBalanceType(balance.Type),
			Amount:      adapters.ToProtoInstrumentAmount(balance.Amount),
		})
	}

	return &positionkeepingv1.GetAccountBalancesResponse{
		AccountId: req.GetAccountId(),
		Balances:  balanceEntries,
		AsOf:      timestamppb.New(asOf),
	}, nil
}

// loadLogForBalance loads the first position log for an account and resolves
// the opening balance for balance computation. Returns a gRPC status error on failure.
func (s *PositionKeepingService) loadLogForBalance(
	ctx context.Context,
	accountID string,
) (*domain.FinancialPositionLog, domain.Money, domain.Instrument, error) {
	logs, err := s.repository.FindByAccountID(ctx, accountID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.Money{}, domain.Instrument{}, status.Errorf(codes.NotFound, "account not found: %s", accountID)
		}
		return nil, domain.Money{}, domain.Instrument{}, status.Errorf(codes.Internal, "failed to retrieve position logs: %v", err)
	}

	if len(logs) == 0 {
		return nil, domain.Money{}, domain.Instrument{}, status.Errorf(codes.NotFound, "no position logs found for account: %s", accountID)
	}

	log := logs[0]
	openingBalance, currency := resolveOpeningBalance(log)
	return log, openingBalance, currency, nil
}

// resolveOpeningBalance determines the opening balance and currency for balance computation.
// If the log was created with NewFinancialPositionLogWithOpeningBalance, the opening balance
// is already represented by a transaction entry, so we return ZERO to avoid double-counting.
func resolveOpeningBalance(log *domain.FinancialPositionLog) (domain.Money, domain.Instrument) {
	if log.HasOpeningBalance() {
		currency := log.OpeningBalance.Instrument
		return domain.NewQty[domain.Monetary](decimal.Zero, currency), currency
	}
	if len(log.TransactionLogEntries) > 0 {
		currency := log.TransactionLogEntries[0].Amount.Instrument
		return domain.NewQty[domain.Monetary](decimal.Zero, currency), currency
	}
	openingBalance := domain.MustNewMoney(decimal.Zero, domain.CurrencyGBP)
	return openingBalance, openingBalance.Instrument
}

// computeBalance computes the specified balance type using the LogBalanceComputer.
func (s *PositionKeepingService) computeBalance(
	ctx context.Context,
	lbc *domain.LogBalanceComputer,
	balanceType domain.BalanceType,
) (domain.Balance, error) {
	switch balanceType {
	case domain.BalanceTypeOpening:
		return lbc.OpeningBalance(), nil

	case domain.BalanceTypeClosing:
		// For closing balance, use current time as period end
		return lbc.ClosingBalance(time.Now().UTC())

	case domain.BalanceTypeCurrent:
		return lbc.CurrentBalance()

	case domain.BalanceTypeLedger:
		return lbc.LedgerBalance()

	case domain.BalanceTypeReserve:
		return lbc.ReserveBalance(ctx)

	case domain.BalanceTypeAvailable:
		// Available balance requires overdraft limit - use zero for now
		// In production, this would come from account configuration
		zeroOverdraft := domain.MustNewMoney(decimal.Zero, domain.CurrencyGBP)
		return lbc.AvailableBalance(ctx, zeroOverdraft, false)

	case domain.BalanceTypeFree:
		return lbc.FreeBalance(ctx)

	case domain.BalanceTypeUnknown:
		return domain.Balance{}, status.Errorf(codes.InvalidArgument, "unknown balance type")

	default:
		return domain.Balance{}, status.Errorf(codes.InvalidArgument, "unsupported balance type: %s", balanceType)
	}
}
