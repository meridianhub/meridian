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
	// Validate request
	if req.GetAccountId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "account_id is required")
	}

	// Convert proto balance type to domain
	balanceType, err := adapters.ToDomainBalanceType(req.GetBalanceType())
	if err != nil {
		if errors.Is(err, adapters.ErrUnspecifiedBalanceType) {
			return nil, status.Errorf(codes.InvalidArgument, "balance_type is required and cannot be UNSPECIFIED")
		}
		return nil, status.Errorf(codes.InvalidArgument, "invalid balance_type: %v", err)
	}

	// Load position logs for the account
	logs, err := s.repository.FindByAccountID(ctx, req.GetAccountId())
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.GetAccountId())
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve position logs: %v", err)
	}

	if len(logs) == 0 {
		return nil, status.Errorf(codes.NotFound, "no position logs found for account: %s", req.GetAccountId())
	}

	// Use the first log for balance computation
	// In a real scenario, you might aggregate across all logs or use the most recent one
	log := logs[0]

	// Determine opening balance and currency for balance computation.
	// IMPORTANT: If the log was created with NewFinancialPositionLogWithOpeningBalance,
	// the opening balance is already represented by a transaction entry, so we pass ZERO
	// to the LogBalanceComputer to avoid double-counting.
	var openingBalance domain.Money
	var currency domain.Instrument

	if log.HasOpeningBalance() {
		// Log was created with opening balance - the transaction entry already includes it
		// Pass zero to LogBalanceComputer, but use the instrument for currency
		currency = log.OpeningBalance.Instrument
		openingBalance = domain.NewQty[domain.Monetary](decimal.Zero, currency)
	} else if len(log.TransactionLogEntries) > 0 {
		// No opening balance set - infer currency from first transaction
		currency = log.TransactionLogEntries[0].Amount.Instrument
		openingBalance = domain.NewQty[domain.Monetary](decimal.Zero, currency)
	} else {
		// No transactions and no opening balance - default to GBP
		openingBalance = domain.MustNewMoney(decimal.Zero, domain.CurrencyGBP)
		currency = openingBalance.Instrument
	}

	// Apply instrument filter if specified (supports both currency codes and extended asset codes)
	if req.GetInstrumentCode() != "" {
		if currency.Code != req.GetInstrumentCode() {
			return nil, status.Errorf(codes.NotFound, "no balance found for instrument: %s", req.GetInstrumentCode())
		}
	}

	// Create LogBalanceComputer for balance calculation
	// Note: currentAccountClient may be nil if lien queries are not supported
	lbc, err := domain.NewLogBalanceComputer(log, openingBalance, s.currentAccountClient)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create balance computer: %v", err)
	}

	// Compute the requested balance type
	balance, err := s.computeBalance(ctx, lbc, balanceType)
	if err != nil {
		// Check for specific errors that should return different status codes
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
	// Validate request
	if req.GetAccountId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "account_id is required")
	}

	// Load position logs for the account
	logs, err := s.repository.FindByAccountID(ctx, req.GetAccountId())
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "account not found: %s", req.GetAccountId())
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve position logs: %v", err)
	}

	if len(logs) == 0 {
		return nil, status.Errorf(codes.NotFound, "no position logs found for account: %s", req.GetAccountId())
	}

	// Use the first log for balance computation
	log := logs[0]

	// Determine opening balance and currency for balance computation.
	// IMPORTANT: If the log was created with NewFinancialPositionLogWithOpeningBalance,
	// the opening balance is already represented by a transaction entry, so we pass ZERO
	// to the LogBalanceComputer to avoid double-counting.
	var openingBalance domain.Money
	var currency domain.Instrument

	if log.HasOpeningBalance() {
		// Log was created with opening balance - the transaction entry already includes it
		currency = log.OpeningBalance.Instrument
		openingBalance = domain.NewQty[domain.Monetary](decimal.Zero, currency)
	} else if len(log.TransactionLogEntries) > 0 {
		// No opening balance set - infer currency from first transaction
		currency = log.TransactionLogEntries[0].Amount.Instrument
		openingBalance = domain.NewQty[domain.Monetary](decimal.Zero, currency)
	} else {
		// No transactions and no opening balance - default to GBP
		openingBalance = domain.MustNewMoney(decimal.Zero, domain.CurrencyGBP)
		currency = openingBalance.Instrument
	}

	// Apply instrument filter if specified (supports both currency codes and extended asset codes)
	if req.GetInstrumentCode() != "" {
		if currency.Code != req.GetInstrumentCode() {
			return nil, status.Errorf(codes.NotFound, "no balances found for instrument: %s", req.GetInstrumentCode())
		}
	}

	// Create LogBalanceComputer for balance calculation
	lbc, err := domain.NewLogBalanceComputer(log, openingBalance, s.currentAccountClient)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create balance computer: %v", err)
	}

	// Compute all balance types
	asOf := time.Now().UTC()
	balanceEntries := make([]*positionkeepingv1.BalanceEntry, 0, len(allBalanceTypes))

	for _, balanceType := range allBalanceTypes {
		balance, err := s.computeBalance(ctx, lbc, balanceType)
		if err != nil {
			// Skip balance types that cannot be computed (e.g., no CurrentAccountClient)
			// Log the error but continue with other balance types
			if errors.Is(err, domain.ErrNilCurrentAccountClient) {
				continue
			}
			// For other errors, skip this balance type
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
