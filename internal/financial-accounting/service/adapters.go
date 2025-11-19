package service

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/internal/financial-accounting/domain"
)

var (
	// ErrEmptyUUID is returned when UUID string is empty
	ErrEmptyUUID = errors.New("UUID string is empty")

	// ErrNilMoney is returned when proto money is nil
	ErrNilMoney = errors.New("money cannot be nil")
)

// parseUUID parses a string as a UUID, returning an error if invalid.
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

// fromProtoMoney converts protobuf Money to domain Money.
func fromProtoMoney(protoMoney *money.Money) (domain.Money, error) {
	if protoMoney == nil {
		return domain.Money{}, ErrNilMoney
	}

	// Convert units and nanos to decimal
	// For example: units: 123, nanos: 456789000 -> 123.456789
	amount := decimal.NewFromInt(protoMoney.Units)
	if protoMoney.Nanos != 0 {
		nanosPart := decimal.NewFromInt(int64(protoMoney.Nanos)).Div(decimal.NewFromInt(1000000000))
		amount = amount.Add(nanosPart)
	}

	currency, err := domain.ParseCurrency(protoMoney.CurrencyCode)
	if err != nil {
		return domain.Money{}, fmt.Errorf("invalid currency: %w", err)
	}

	return domain.NewMoney(amount, currency)
}

// toProtoMoney converts domain Money to protobuf Money.
func toProtoMoney(domainMoney domain.Money) *money.Money {
	// Convert decimal to units and nanos
	// For example: 123.456789 GBP -> units: 123, nanos: 456789000
	amount := domainMoney.Amount()
	units := amount.IntPart()
	fraction := amount.Sub(amount.Truncate(0))
	nanos := fraction.Mul(decimal.NewFromInt(1000000000)).IntPart()

	// Clamp nanos to int32 range to prevent overflow
	if nanos > 999999999 {
		nanos = 999999999
	} else if nanos < -999999999 {
		nanos = -999999999
	}

	return &money.Money{
		CurrencyCode: domainMoney.Currency().String(),
		Units:        units,
		Nanos:        int32(nanos), // #nosec G115 -- Safely clamped to int32 range above
	}
}

// fromProtoPostingDirection converts protobuf PostingDirection to domain.
func fromProtoPostingDirection(direction commonv1.PostingDirection) domain.PostingDirection {
	switch direction {
	case commonv1.PostingDirection_POSTING_DIRECTION_DEBIT:
		return domain.PostingDirectionDebit
	case commonv1.PostingDirection_POSTING_DIRECTION_CREDIT:
		return domain.PostingDirectionCredit
	case commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED:
		return domain.PostingDirectionDebit // Default to debit if unspecified
	default:
		return domain.PostingDirectionDebit
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

// fromProtoTransactionStatus converts protobuf TransactionStatus to domain.
func fromProtoTransactionStatus(status commonv1.TransactionStatus) domain.TransactionStatus {
	switch status {
	case commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING:
		return domain.TransactionStatusPending
	case commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED:
		return domain.TransactionStatusPosted
	case commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED:
		return domain.TransactionStatusFailed
	case commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED:
		return domain.TransactionStatusCancelled
	case commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED:
		return domain.TransactionStatusReversed
	case commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED:
		return domain.TransactionStatusPending // Default to pending if unspecified
	default:
		return domain.TransactionStatusPending
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

// toProtoLedgerPosting converts domain LedgerPosting to protobuf.
func toProtoLedgerPosting(posting *domain.LedgerPosting) *financialaccountingv1.LedgerPosting {
	if posting == nil {
		return nil
	}

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
