package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
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

	lbc, err := s.loadBalanceComputer(ctx, req.GetAccountId(), req.GetInstrumentCode())
	if err != nil {
		return nil, err
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

	lbc, err := s.loadBalanceComputer(ctx, req.GetAccountId(), req.GetInstrumentCode())
	if err != nil {
		return nil, err
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

// loadBalanceComputer aggregates the account's net transaction movement across
// ALL of its position logs and returns a LogBalanceComputer that computes every
// BIAN balance type over the aggregated position.
//
// Each account transaction (deposit, withdrawal, settlement, ...) is recorded as
// a separate FinancialPositionLog, so a correct balance must sum every entry of
// every log - not just the most recent log. The summation is performed by the
// repository as a single SQL aggregate query (the balance read hot path) and
// materialized here as a synthetic single-entry log so the existing
// LogBalanceComputer logic is reused unchanged.
func (s *PositionKeepingService) loadBalanceComputer(
	ctx context.Context,
	accountID string,
	instrumentCode string,
) (*domain.LogBalanceComputer, error) {
	balances, hasLogs, err := s.repository.SumAccountBalances(ctx, accountID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to aggregate account balances: %v", err)
	}

	if !hasLogs {
		return nil, status.Errorf(codes.NotFound, "no position logs found for account: %s", accountID)
	}

	netMovement, err := resolveAggregateInstrument(balances, instrumentCode)
	if err != nil {
		return nil, err
	}

	aggregateLog, err := buildAggregateLog(accountID, netMovement)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to build aggregate position log: %v", err)
	}

	// The opening balance passed to the computer is ZERO: opening balances are
	// recorded as transaction entries and are therefore already included in the
	// aggregated net movement. The instrument carries through so that a zero
	// balance still reports the correct instrument.
	openingBalance := domain.NewQty[domain.Monetary](decimal.Zero, netMovement.Instrument)

	lbc, err := domain.NewLogBalanceComputer(aggregateLog, openingBalance, s.currentAccountClient)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create balance computer: %v", err)
	}

	return lbc, nil
}

// resolveAggregateInstrument selects the aggregated net movement for the balance
// query. When instrumentCode is provided, the matching instrument's movement is
// returned (or NotFound if the account holds no balance in that instrument).
// When instrumentCode is empty, the account must hold exactly one instrument; a
// multi-instrument account requires the caller to disambiguate.
func resolveAggregateInstrument(
	balances []domain.AccountInstrumentBalance,
	instrumentCode string,
) (domain.Money, error) {
	if instrumentCode != "" {
		for _, b := range balances {
			if b.Instrument.Code == instrumentCode {
				return b.NetMovement, nil
			}
		}
		return domain.Money{}, status.Errorf(codes.NotFound, "no balance found for instrument: %s", instrumentCode)
	}

	switch len(balances) {
	case 0:
		// The account has logs but no transaction entries (e.g. a zero opening
		// balance). Report a zero balance in the platform default instrument.
		return domain.MustNewMoney(decimal.Zero, domain.CurrencyGBP), nil
	case 1:
		return balances[0].NetMovement, nil
	default:
		return domain.Money{}, status.Errorf(codes.InvalidArgument, "account holds multiple instruments; instrument_code is required")
	}
}

// buildAggregateLog constructs a synthetic FinancialPositionLog whose net
// movement equals the aggregated movement across all of the account's logs. A
// positive movement is represented as a single DEBIT entry, a negative movement
// as a single CREDIT entry, and a zero movement as a log with no entries. This
// lets the existing LogBalanceComputer derive every BIAN balance type from one
// representative position.
func buildAggregateLog(accountID string, netMovement domain.Money) (*domain.FinancialPositionLog, error) {
	if netMovement.IsZero() {
		return domain.NewFinancialPositionLog(accountID, nil, nil)
	}

	direction := domain.PostingDirectionDebit
	amount := netMovement
	if netMovement.IsNegative() {
		direction = domain.PostingDirectionCredit
		amount = netMovement.Negate()
	}

	entry, err := domain.NewTransactionLogEntry(
		uuid.New(),
		accountID,
		amount,
		direction,
		time.Now().UTC(),
		"aggregated net movement",
		"",
		domain.TransactionSourceManual,
	)
	if err != nil {
		return nil, err
	}

	return domain.NewFinancialPositionLog(accountID, entry, nil)
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
