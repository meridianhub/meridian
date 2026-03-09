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
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
)

var (
	// ErrEmptyUUID is returned when a UUID string is empty.
	// This error is wrapped in InvalidArgument gRPC status codes.
	ErrEmptyUUID = errors.New("UUID cannot be empty")

	// ErrNilMoney is returned when proto money is nil
	ErrNilMoney = errors.New("money cannot be nil")
)

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

// fromProtoMoney converts protobuf Money to domain Money.
//
// The conversion creates an Instrument from the proto currency code and constructs
// a Qty[Monetary] type. The proto google.type.Money uses currency codes (ISO 4217)
// which are mapped to instruments with dimension "CURRENCY".
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

	// Parse and validate currency code
	currency, err := domain.ParseCurrency(protoMoney.CurrencyCode)
	if err != nil {
		return domain.Money{}, fmt.Errorf("invalid currency: %w", err)
	}

	// Convert Currency to Instrument for Qty[Monetary] construction
	instrument, err := domain.CurrencyToInstrument(currency)
	if err != nil {
		return domain.Money{}, fmt.Errorf("failed to create instrument: %w", err)
	}

	return domain.NewMoney(amount, instrument), nil
}

// toProtoMoney converts domain Money to protobuf google.type.Money.
// Preserves full decimal precision up to 9 decimal places (nanosecond precision).
//
// The conversion extracts the instrument code (which is the currency code for monetary
// instruments) and the decimal amount to construct the proto message.
func toProtoMoney(m domain.Money) *money.Money {
	// Convert decimal amount to units and nanos
	// For example: 123.456789 USD -> units: 123, nanos: 456789000
	amount := m.Amount
	units := amount.IntPart()
	fraction := amount.Sub(amount.Truncate(0))
	nanos := fraction.Mul(decimal.NewFromInt(1_000_000_000)).IntPart()

	// Clamp nanos to int32 range to prevent overflow
	// This handles edge cases with extreme precision
	if nanos > 999_999_999 {
		nanos = 999_999_999
	} else if nanos < -999_999_999 {
		nanos = -999_999_999
	}

	return &money.Money{
		CurrencyCode: m.Instrument.Code, // Use instrument code (e.g., "USD", "GBP")
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

// toProtoLedgerPosting converts a domain LedgerPosting to protobuf.
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
		AccountServiceDomain:  toProtoAccountServiceDomain(posting.AccountServiceDomain),
		ValueDate:             timestamppb.New(posting.ValueDate),
		PostingResult:         posting.PostingResult,
		CreatedAt:             timestamppb.New(posting.CreatedAt),
		Status:                toProtoTransactionStatus(posting.Status),
	}
}

// toProtoFinancialBookingLog converts a domain FinancialBookingLog to protobuf.
func toProtoFinancialBookingLog(log *domain.FinancialBookingLog) *financialaccountingv1.FinancialBookingLog {
	if log == nil {
		return nil
	}

	// Convert postings to protobuf (using defensive copy method)
	postings := log.Postings()
	protoPostings := make([]*financialaccountingv1.LedgerPosting, len(postings))
	for i, posting := range postings {
		protoPostings[i] = toProtoLedgerPosting(posting)
	}

	return &financialaccountingv1.FinancialBookingLog{
		Id:                      log.ID.String(),
		FinancialAccountType:    toProtoAccountType(log.FinancialAccountType),
		ProductServiceReference: log.ProductServiceReference,
		BusinessUnitReference:   log.BusinessUnitReference,
		ChartOfAccountsRules:    log.ChartOfAccountsRules,
		BaseInstrumentCode:      string(log.BaseCurrency),
		Status:                  toProtoTransactionStatus(log.Status),
		CreatedAt:               timestamppb.New(log.CreatedAt),
		UpdatedAt:               timestamppb.New(log.UpdatedAt),
		Postings:                protoPostings,
	}
}

// toProtoAccountType converts domain account type string to protobuf string field.
func toProtoAccountType(accountType string) string {
	return accountType
}

// fromProtoAccountType converts protobuf string field to domain account type string.
func fromProtoAccountType(accountType string) string {
	return accountType
}

// toProtoAccountServiceDomain converts domain AccountServiceDomain string to protobuf.
func toProtoAccountServiceDomain(domain string) commonv1.AccountServiceDomain {
	switch domain {
	case "CURRENT_ACCOUNT":
		return commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_CURRENT_ACCOUNT
	case "INTERNAL_ACCOUNT":
		return commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_INTERNAL_ACCOUNT
	default:
		return commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_UNSPECIFIED
	}
}

// fromProtoAccountServiceDomain converts protobuf AccountServiceDomain to domain string.
func fromProtoAccountServiceDomain(domain commonv1.AccountServiceDomain) string {
	switch domain {
	case commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_UNSPECIFIED:
		return ""
	case commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_CURRENT_ACCOUNT:
		return "CURRENT_ACCOUNT"
	case commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_INTERNAL_ACCOUNT:
		return "INTERNAL_ACCOUNT"
	default:
		return ""
	}
}
