package service

import (
	"fmt"
	"log/slog"
	"math"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func toProtoFacility(account domain.CurrentAccount) *pb.CurrentAccountFacility {
	return &pb.CurrentAccountFacility{
		AccountId:          account.AccountID(),
		ExternalIdentifier: account.ExternalIdentifier(),
		AccountStatus:      mapStatusToProto(account.Status()),
		InstrumentCode:     account.InstrumentCode(),
		Dimension:          account.Dimension(),
		CreatedAt:          timestamppb.New(account.CreatedAt()),
		UpdatedAt:          timestamppb.New(account.UpdatedAt()),
		// #nosec G115 - Version is bounded by database constraints
		Version: int32(account.Version()),
		CurrentBalance: &pb.AccountBalance{
			CurrentBalance:   toMoneyAmount(account.Balance()),
			AvailableBalance: toMoneyAmount(account.AvailableBalance()),
			LastUpdated:      timestamppb.New(account.BalanceUpdatedAt()),
		},
		ProductTypeCode: account.ProductTypeCode(),
		// #nosec G115 - ProductTypeVersion is bounded by database constraints
		ProductTypeVersion: int32(account.ProductTypeVersion()),
		BehaviorClass:      account.BehaviorClass(),
		PartyId:            account.PartyID(),
	}
}

// safeMinorUnits converts Money to minor units (cents) with overflow protection.
// Returns 0 if overflow occurs (should not happen in practice for valid accounts).
// Used for logging and metrics where returning an error is not practical.
func safeMinorUnits(m domain.Amount) int64 {
	cents, err := m.ToMinorUnits()
	if err != nil {
		// This should never happen in practice - int64 max is ~92 quadrillion cents
		// Log the anomaly for visibility, then return 0 rather than panicking
		slog.Error("amount overflow in metrics conversion",
			"currency", m.InstrumentCode(),
			"error", err)
		return 0
	}
	return cents
}

func toMoneyAmount(m domain.Amount) *commonpb.MoneyAmount {
	amountMinor := safeMinorUnits(m)
	precision := m.Instrument().Precision
	// #nosec G115 - precision is bounded by instrument definition (0-9 in practice)
	scale := int64(math.Pow10(precision))
	nanosPerMinor := int64(math.Pow10(9 - precision))

	units := amountMinor / scale
	remainder := amountMinor % scale

	// Per google.type.Money spec: nanos MUST share the sign of units.
	// - Positive amounts: both units and nanos are positive or zero
	// - Negative amounts: both units and nanos are negative or zero
	// remainder already shares the sign of amountMinor due to Go's truncating division.
	// #nosec G115 - remainder is always -(scale-1) to (scale-1); product fits in int32
	nanos := int32(remainder * nanosPerMinor)

	return &commonpb.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: m.InstrumentCode(),
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

// mapRegistryDimension converts a reference-data registry dimension string to the
// domain quantity dimension string used by the current-account service.
//
// The reference-data registry uses "MONETARY" for currency instruments, while the
// domain quantity package uses "CURRENCY". All other dimension values are identical.
func mapRegistryDimension(registryDimension string) string {
	if registryDimension == "MONETARY" {
		return "CURRENCY"
	}
	return registryDimension
}

// protoMoneyToAmount converts a proto MoneyAmount to a domain Amount using the account's
// instrument for dimension-agnostic precision support. The proto CurrencyCode field carries
// the instrument code (e.g. "GBP" or "KWH"); the account's dimension and precision are used
// to construct the correct minor-unit Amount regardless of instrument type.
//
// The conversion formula is precision-aware:
//
//	scale        = 10^precision
//	nanosPerUnit = 10^(9-precision)
//	totalMinor   = Units * scale + round(Nanos / nanosPerUnit)
//
// For 2-decimal CURRENCY (e.g. GBP): scale=100, nanosPerUnit=10_000_000
// For 3-decimal ENERGY (e.g. KWH):   scale=1000, nanosPerUnit=1_000_000
func protoMoneyToAmount(amount *commonpb.MoneyAmount, account domain.CurrentAccount) (domain.Amount, error) {
	if amount == nil || amount.Amount == nil {
		return domain.Amount{}, ErrAmountRequired
	}

	precision := account.Balance().Instrument().Precision
	// google.type.Money uses nanos (10^9), so precision must be in [0..9].
	// Values outside this range cannot be represented in nanos without losing precision.
	if precision < 0 || precision > 9 {
		return domain.Amount{}, fmt.Errorf("%w: got %d", ErrInvalidPrecision, precision)
	}
	// #nosec G115 - precision is bounded by instrument definition (0-9 in practice)
	scale := int64(math.Pow10(precision))
	nanosPerUnit := int64(math.Pow10(9 - precision))

	// Overflow guard: reject units that would cause Units*scale to overflow int64.
	// Using the same boundary as math.MaxInt64/scale (integer division) which equals
	// the largest units value where units*scale does not overflow.
	if amount.Amount.Units > math.MaxInt64/scale || amount.Amount.Units < math.MinInt64/scale {
		return domain.Amount{}, fmt.Errorf("%w: units %d would overflow for precision %d",
			ErrAmountOverflow, amount.Amount.Units, precision)
	}

	unitsMinor := amount.Amount.Units * scale

	// Round nanos to nearest minor unit using half-away-from-zero (banker's-round alternative).
	// Simple half-up (+ half) rounds negative nanos toward zero, which is incorrect.
	// We must branch on sign so that -5_000_001 rounds to -1, not 0.
	nanos := int64(amount.Amount.Nanos)
	half := nanosPerUnit / 2
	var nanosMinor int64
	if nanos >= 0 {
		nanosMinor = (nanos + half) / nanosPerUnit
	} else {
		nanosMinor = (nanos - half) / nanosPerUnit
	}

	// Post-rounding overflow guard: unitsMinor + nanosMinor must not overflow int64.
	if (nanosMinor > 0 && unitsMinor > math.MaxInt64-nanosMinor) ||
		(nanosMinor < 0 && unitsMinor < math.MinInt64-nanosMinor) {
		return domain.Amount{}, fmt.Errorf("%w: total minor units would overflow after nanos rounding",
			ErrAmountOverflow)
	}
	totalMinor := unitsMinor + nanosMinor

	return domain.NewAmountFromInstrument(
		account.Balance().InstrumentCode(),
		account.Balance().Dimension(),
		precision,
		totalMinor,
	)
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
