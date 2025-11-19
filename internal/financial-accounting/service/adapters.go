package service

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/internal/financial-accounting/domain"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// decimalHundred is used for converting between decimal and cents
var decimalHundred = decimal.NewFromInt(100)

// ErrEmptyUUID is returned when a UUID string is empty.
// This error is wrapped in InvalidArgument gRPC status codes.
var ErrEmptyUUID = errors.New("UUID cannot be empty")

// parseUUID parses and validates a UUID string.
//
// Returns ErrEmptyUUID if the string is empty.
// Returns an error if the UUID format is invalid.
func parseUUID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, ErrEmptyUUID
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid UUID format: %w", err)
	}
	return id, nil
}

// toProtoLedgerPosting converts a domain LedgerPosting to protobuf.
func toProtoLedgerPosting(posting *domain.LedgerPosting) *financialaccountingv1.LedgerPosting {
	return &financialaccountingv1.LedgerPosting{
		Id:                    posting.ID.String(),
		FinancialBookingLogId: posting.FinancialBookingLogID.String(),
		PostingDirection:      toProtoPostingDirection(posting.Direction),
		PostingAmount:         toProtoMoney(posting.Amount),
		AccountId:             posting.AccountID,
		ValueDate:             timestamppb.New(posting.ValueDate),
		PostingResult:         posting.PostingResult,
		CreatedAt:             timestamppb.New(posting.CreatedAt),
		Status:                toProtoTransactionStatus(posting.Status),
	}
}

// toProtoPostingDirection converts domain PostingDirection to protobuf.
func toProtoPostingDirection(direction domain.PostingDirection) commonv1.PostingDirection {
	switch direction {
	case domain.PostingDirectionDebit:
		return commonv1.PostingDirection_POSTING_DIRECTION_DEBIT
	case domain.PostingDirectionCredit:
		return commonv1.PostingDirection_POSTING_DIRECTION_CREDIT
	default:
		return commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED
	}
}

// toProtoTransactionStatus converts domain TransactionStatus to protobuf.
func toProtoTransactionStatus(status domain.TransactionStatus) commonv1.TransactionStatus {
	switch status {
	case domain.TransactionStatusPending:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING
	case domain.TransactionStatusPosted:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED
	case domain.TransactionStatusFailed:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED
	case domain.TransactionStatusCancelled:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED
	case domain.TransactionStatusReversed:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED
	default:
		return commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED
	}
}

// toProtoMoney converts domain Money to protobuf google.type.Money.
func toProtoMoney(m domain.Money) *money.Money {
	// Get amount in smallest units (cents)
	cents := m.Amount().Mul(decimalHundred).IntPart()

	// Split into units (dollars) and nanos (fractional cents)
	units := cents / 100
	// Safe conversion: cents % 100 is always -99 to 99, preserving sign for negative amounts
	centsPart := int32(cents % 100) // #nosec G115 -- Modulo operation guarantees -99 to 99 range, always safe for int32
	nanos := centsPart * 10_000_000 // Convert cents to nanos (1 cent = 10M nanos)

	return &money.Money{
		CurrencyCode: string(m.Currency()),
		Units:        units,
		Nanos:        nanos,
	}
}
