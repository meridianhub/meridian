package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
)

func TestFromProtoPostingDirection(t *testing.T) {
	tests := []struct {
		name     string
		input    commonv1.PostingDirection
		expected domain.PostingDirection
	}{
		{"debit", commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, domain.PostingDirectionDebit},
		{"credit", commonv1.PostingDirection_POSTING_DIRECTION_CREDIT, domain.PostingDirectionCredit},
		{"unspecified defaults to debit", commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED, domain.PostingDirectionDebit},
		{"unknown value defaults to debit", commonv1.PostingDirection(99), domain.PostingDirectionDebit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fromProtoPostingDirection(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToProtoPostingDirection(t *testing.T) {
	tests := []struct {
		name     string
		input    domain.PostingDirection
		expected commonv1.PostingDirection
	}{
		{"debit", domain.PostingDirectionDebit, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT},
		{"credit", domain.PostingDirectionCredit, commonv1.PostingDirection_POSTING_DIRECTION_CREDIT},
		{"unknown defaults to unspecified", domain.PostingDirection("UNKNOWN"), commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toProtoPostingDirection(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFromProtoTransactionStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    commonv1.TransactionStatus
		expected domain.TransactionStatus
	}{
		{"pending", commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, domain.TransactionStatusPending},
		{"posted", commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, domain.TransactionStatusPosted},
		{"failed", commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED, domain.TransactionStatusFailed},
		{"cancelled", commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED, domain.TransactionStatusCancelled},
		{"reversed", commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED, domain.TransactionStatusReversed},
		{"unspecified defaults to pending", commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED, domain.TransactionStatusPending},
		{"unknown value defaults to pending", commonv1.TransactionStatus(99), domain.TransactionStatusPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fromProtoTransactionStatus(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToProtoTransactionStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    domain.TransactionStatus
		expected commonv1.TransactionStatus
	}{
		{"pending", domain.TransactionStatusPending, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING},
		{"posted", domain.TransactionStatusPosted, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED},
		{"failed", domain.TransactionStatusFailed, commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED},
		{"cancelled", domain.TransactionStatusCancelled, commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED},
		{"reversed", domain.TransactionStatusReversed, commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED},
		{"unknown defaults to unspecified", domain.TransactionStatus("UNKNOWN"), commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toProtoTransactionStatus(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToProtoAccountServiceDomain(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected commonv1.AccountServiceDomain
	}{
		{"current account", "CURRENT_ACCOUNT", commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_CURRENT_ACCOUNT},
		{"internal account", "INTERNAL_ACCOUNT", commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_INTERNAL_ACCOUNT},
		{"empty string defaults to unspecified", "", commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_UNSPECIFIED},
		{"unknown defaults to unspecified", "SAVINGS_ACCOUNT", commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toProtoAccountServiceDomain(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFromProtoAccountServiceDomain(t *testing.T) {
	tests := []struct {
		name     string
		input    commonv1.AccountServiceDomain
		expected string
	}{
		{"current account", commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_CURRENT_ACCOUNT, "CURRENT_ACCOUNT"},
		{"internal account", commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_INTERNAL_ACCOUNT, "INTERNAL_ACCOUNT"},
		{"unspecified returns empty", commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_UNSPECIFIED, ""},
		{"unknown returns empty", commonv1.AccountServiceDomain(99), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fromProtoAccountServiceDomain(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToProtoLedgerPosting_NilInput(t *testing.T) {
	result := toProtoLedgerPosting(nil)
	assert.Nil(t, result)
}

func TestToProtoLedgerPosting_ValidPosting(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount := domain.NewMoney(decimal.NewFromInt(100), inst)
	bookingLogID := uuid.New()
	postingID := uuid.New()
	valueDate := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	createdAt := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	posting := &domain.LedgerPosting{
		ID:                    postingID,
		FinancialBookingLogID: bookingLogID,
		Direction:             domain.PostingDirectionDebit,
		Amount:                amount,
		AccountID:             "ACC-001",
		AccountServiceDomain:  "CURRENT_ACCOUNT",
		ValueDate:             valueDate,
		PostingResult:         "OK",
		Status:                domain.TransactionStatusPosted,
		CreatedAt:             createdAt,
	}

	result := toProtoLedgerPosting(posting)

	require.NotNil(t, result)
	assert.Equal(t, postingID.String(), result.Id)
	assert.Equal(t, bookingLogID.String(), result.FinancialBookingLogId)
	assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, result.PostingDirection)
	assert.Equal(t, "ACC-001", result.AccountId)
	assert.Equal(t, commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_CURRENT_ACCOUNT, result.AccountServiceDomain)
	assert.Equal(t, "OK", result.PostingResult)
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, result.Status)
	assert.NotNil(t, result.PostingAmount)
	assert.Equal(t, "GBP", result.PostingAmount.CurrencyCode)
}

func TestToProtoMoney_WholeAmount(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyUSD)
	m := domain.NewMoney(decimal.NewFromInt(42), inst)

	result := toProtoMoney(m)
	assert.Equal(t, "USD", result.CurrencyCode)
	assert.Equal(t, int64(42), result.Units)
	assert.Equal(t, int32(0), result.Nanos)
}

func TestToProtoMoney_FractionalAmount(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount, _ := decimal.NewFromString("123.456789")
	m := domain.NewMoney(amount, inst)

	result := toProtoMoney(m)
	assert.Equal(t, "GBP", result.CurrencyCode)
	assert.Equal(t, int64(123), result.Units)
	assert.Equal(t, int32(456789000), result.Nanos)
}

func TestFromProtoMoney_ValidMoney(t *testing.T) {
	protoMoney := &money.Money{
		CurrencyCode: "GBP",
		Units:        100,
		Nanos:        500000000, // 0.5
	}

	result, err := fromProtoMoney(protoMoney)
	require.NoError(t, err)
	assert.Equal(t, "100.5", result.Amount.String())
	assert.Equal(t, "GBP", result.Instrument.Code)
}

func TestFromProtoMoney_NilMoney(t *testing.T) {
	_, err := fromProtoMoney(nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilMoney)
}

func TestFromProtoMoney_InvalidCurrency(t *testing.T) {
	protoMoney := &money.Money{
		CurrencyCode: "INVALID",
		Units:        100,
	}

	_, err := fromProtoMoney(protoMoney)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid currency")
}

func TestFromProtoMoney_ZeroNanos(t *testing.T) {
	protoMoney := &money.Money{
		CurrencyCode: "USD",
		Units:        50,
		Nanos:        0,
	}

	result, err := fromProtoMoney(protoMoney)
	require.NoError(t, err)
	assert.Equal(t, "50", result.Amount.String())
}

func TestParseUUID_Valid(t *testing.T) {
	expected := uuid.New()
	result, err := parseUUID(expected.String())
	require.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestParseUUID_Empty(t *testing.T) {
	_, err := parseUUID("")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyUUID)
}

func TestParseUUID_Invalid(t *testing.T) {
	_, err := parseUUID("not-a-uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid UUID format")
}
