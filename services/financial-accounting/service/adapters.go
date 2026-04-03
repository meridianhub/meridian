package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
)

var (
	// ErrEmptyUUID is returned when a UUID string is empty.
	// This error is wrapped in InvalidArgument gRPC status codes.
	ErrEmptyUUID = errors.New("UUID cannot be empty")

	// ErrNilInstrumentAmount is returned when proto InstrumentAmount is nil
	ErrNilInstrumentAmount = errors.New("instrument amount cannot be nil")

	// ErrEmptyInstrumentCode is returned when the instrument code is empty
	ErrEmptyInstrumentCode = errors.New("instrument code cannot be empty")
)

// InstrumentAmountConverter converts between proto InstrumentAmount and domain Money
// using an InstrumentResolver for proper instrument metadata lookup.
type InstrumentAmountConverter struct {
	resolver refdata.InstrumentResolver
}

// NewInstrumentAmountConverter creates a converter with the given resolver.
// If resolver is nil, conversions fall back to legacy currency-based resolution.
func NewInstrumentAmountConverter(resolver refdata.InstrumentResolver) *InstrumentAmountConverter {
	return &InstrumentAmountConverter{resolver: resolver}
}

// FromProto converts protobuf InstrumentAmount to domain Money using the InstrumentResolver.
// Falls back to legacy ParseCurrency logic when the resolver is nil or returns an error.
func (c *InstrumentAmountConverter) FromProto(ctx context.Context, ia *quantityv1.InstrumentAmount) (domain.Money, error) {
	if ia == nil {
		return domain.Money{}, ErrNilInstrumentAmount
	}
	if ia.InstrumentCode == "" {
		return domain.Money{}, ErrEmptyInstrumentCode
	}

	amount, err := decimal.NewFromString(ia.Amount)
	if err != nil {
		return domain.Money{}, fmt.Errorf("invalid amount: %w", err)
	}

	// Try resolver first for proper instrument metadata
	if c.resolver != nil {
		props, resolveErr := c.resolver.Resolve(ctx, ia.InstrumentCode)
		if resolveErr == nil {
			inst, instErr := domain.NewInstrument(props.Code, uint32(ia.Version), props.Dimension, props.Precision)
			if instErr == nil {
				return domain.NewMoney(amount, inst), nil
			}
		}
	}

	// Fall back to legacy resolution
	return fromProtoInstrumentAmount(ia)
}

// ToProtoInstrumentAmount converts domain Money to protobuf InstrumentAmount.
// Preserves full decimal precision via string representation.
func ToProtoInstrumentAmount(m domain.Money) *quantityv1.InstrumentAmount {
	return toProtoInstrumentAmount(m)
}

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

// fromProtoInstrumentAmount converts protobuf InstrumentAmount to domain Money.
//
// The conversion parses the string amount and creates an Instrument from the instrument code.
// This supports any asset type (currencies, energy, commodities, etc.).
//
// For known ISO 4217 currencies (GBP, USD, EUR, etc.), the instrument is created via
// CurrencyToInstrument which sets the correct dimension and precision. For unknown
// instrument codes, precision is inferred from the amount string's decimal places.
func fromProtoInstrumentAmount(ia *quantityv1.InstrumentAmount) (domain.Money, error) {
	if ia == nil {
		return domain.Money{}, ErrNilInstrumentAmount
	}

	amount, err := decimal.NewFromString(ia.Amount)
	if err != nil {
		return domain.Money{}, fmt.Errorf("invalid amount: %w", err)
	}

	// Try known currency codes first for backward compatibility.
	// This preserves the correct dimension ("CURRENCY") and precision (e.g., 2 for GBP).
	if currency, currErr := domain.ParseCurrency(ia.InstrumentCode); currErr == nil {
		instrument, instErr := domain.CurrencyToInstrument(currency)
		if instErr == nil {
			return domain.NewMoney(amount, instrument), nil
		}
	}

	// For non-currency instrument codes, infer precision from the amount string.
	// We use a struct literal here because the proto InstrumentAmount message does
	// not carry dimension or precision fields - these are reference data concerns.
	// The Dimension field is intentionally empty; downstream consumers that need
	// dimension-aware validation should resolve from the instrument registry.
	// Once InstrumentAmount proto gains dimension/precision fields, this should
	// migrate to domain.NewInstrument() for full validation.
	precision := inferPrecisionFromAmount(ia.Amount)

	instrument := domain.Instrument{
		Code:      ia.InstrumentCode,
		Version:   uint32(ia.Version),
		Precision: precision,
	}

	return domain.NewMoney(amount, instrument), nil
}

// inferPrecisionFromAmount derives the number of decimal places from an amount string.
// For example: "100.50" returns 2, "1.234567" returns 6, "100" returns 0.
func inferPrecisionFromAmount(amount string) int {
	for i := len(amount) - 1; i >= 0; i-- {
		if amount[i] == '.' {
			return len(amount) - 1 - i
		}
	}
	return 0
}

// toProtoInstrumentAmount converts domain Money to protobuf InstrumentAmount.
// Preserves full decimal precision via string representation.
func toProtoInstrumentAmount(m domain.Money) *quantityv1.InstrumentAmount {
	return &quantityv1.InstrumentAmount{
		Amount:         m.Amount.String(),
		InstrumentCode: m.Instrument.Code,
		Version:        int32(m.Instrument.Version),
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
		PostingAmount:         toProtoInstrumentAmount(posting.Amount),
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
