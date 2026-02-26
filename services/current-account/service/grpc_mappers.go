package service

import (
	"log/slog"

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
