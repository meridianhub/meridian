package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
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
	assert.Equal(t, "GBP", result.PostingAmount.InstrumentCode)
	assert.Equal(t, "100", result.PostingAmount.Amount)
}

func TestToProtoInstrumentAmount_WholeAmount(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyUSD)
	m := domain.NewMoney(decimal.NewFromInt(42), inst)

	result := toProtoInstrumentAmount(m)
	assert.Equal(t, "USD", result.InstrumentCode)
	assert.Equal(t, "42", result.Amount)
}

func TestToProtoInstrumentAmount_FractionalAmount(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	amount, _ := decimal.NewFromString("123.456789")
	m := domain.NewMoney(amount, inst)

	result := toProtoInstrumentAmount(m)
	assert.Equal(t, "GBP", result.InstrumentCode)
	assert.Equal(t, "123.456789", result.Amount)
}

func TestFromProtoInstrumentAmount_Valid(t *testing.T) {
	ia := &quantityv1.InstrumentAmount{
		Amount:         "100.5",
		InstrumentCode: "GBP",
		Version:        1,
	}

	result, err := fromProtoInstrumentAmount(ia)
	require.NoError(t, err)
	assert.Equal(t, "100.5", result.Amount.String())
	assert.Equal(t, "GBP", result.Instrument.Code)
}

func TestFromProtoInstrumentAmount_Nil(t *testing.T) {
	_, err := fromProtoInstrumentAmount(nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilInstrumentAmount)
}

func TestFromProtoInstrumentAmount_InvalidAmount(t *testing.T) {
	ia := &quantityv1.InstrumentAmount{
		Amount:         "not-a-number",
		InstrumentCode: "USD",
		Version:        1,
	}

	_, err := fromProtoInstrumentAmount(ia)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid amount")
}

func TestFromProtoInstrumentAmount_WholeAmount(t *testing.T) {
	ia := &quantityv1.InstrumentAmount{
		Amount:         "50",
		InstrumentCode: "USD",
		Version:        1,
	}

	result, err := fromProtoInstrumentAmount(ia)
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

// =============================================================================
// InstrumentAmountConverter tests
// =============================================================================

// adapterMockInstrumentResolver implements refdata.InstrumentResolver for testing.
type adapterMockInstrumentResolver struct {
	instruments map[string]refdata.InstrumentProperties
	err         error
}

func (m *adapterMockInstrumentResolver) Resolve(_ context.Context, code string) (refdata.InstrumentProperties, error) {
	if m.err != nil {
		return refdata.InstrumentProperties{}, m.err
	}
	if props, ok := m.instruments[code]; ok {
		return props, nil
	}
	return refdata.InstrumentProperties{}, refdata.ErrUnknownInstrument
}

func TestInstrumentAmountConverter_FromProto_WithResolver(t *testing.T) {
	resolver := &adapterMockInstrumentResolver{
		instruments: map[string]refdata.InstrumentProperties{
			"GBP":        {Code: "GBP", Dimension: "CURRENCY", Precision: 2, RoundingMode: "HALF_EVEN"},
			"KWH":        {Code: "KWH", Dimension: "ENERGY", Precision: 3, RoundingMode: "HALF_UP"},
			"TONNE_CO2E": {Code: "TONNE_CO2E", Dimension: "CARBON", Precision: 6, RoundingMode: "HALF_EVEN"},
			"GPU_HOUR":   {Code: "GPU_HOUR", Dimension: "COMPUTE", Precision: 4, RoundingMode: "HALF_EVEN"},
		},
	}
	converter := NewInstrumentAmountConverter(resolver)
	ctx := context.Background()

	tests := []struct {
		name           string
		instrumentCode string
		amount         string
		wantCode       string
		wantDimension  string
		wantPrecision  int
	}{
		{"GBP currency", "GBP", "100.50", "GBP", "CURRENCY", 2},
		{"KWH energy", "KWH", "123.456", "KWH", "ENERGY", 3},
		{"TONNE_CO2E carbon", "TONNE_CO2E", "0.001234", "TONNE_CO2E", "CARBON", 6},
		{"GPU_HOUR compute", "GPU_HOUR", "24.5000", "GPU_HOUR", "COMPUTE", 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ia := &quantityv1.InstrumentAmount{
				Amount:         tt.amount,
				InstrumentCode: tt.instrumentCode,
				Version:        1,
			}

			result, err := converter.FromProto(ctx, ia)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCode, result.Instrument.Code)
			assert.Equal(t, tt.wantDimension, result.Instrument.Dimension)
			assert.Equal(t, tt.wantPrecision, result.Instrument.Precision)
		})
	}
}

func TestInstrumentAmountConverter_FromProto_NilInput(t *testing.T) {
	converter := NewInstrumentAmountConverter(nil)
	_, err := converter.FromProto(context.Background(), nil)
	require.ErrorIs(t, err, ErrNilInstrumentAmount)
}

func TestInstrumentAmountConverter_FromProto_EmptyInstrumentCode(t *testing.T) {
	converter := NewInstrumentAmountConverter(nil)
	ia := &quantityv1.InstrumentAmount{Amount: "100", InstrumentCode: ""}
	_, err := converter.FromProto(context.Background(), ia)
	require.ErrorIs(t, err, ErrEmptyInstrumentCode)
}

func TestInstrumentAmountConverter_FromProto_InvalidAmount(t *testing.T) {
	converter := NewInstrumentAmountConverter(nil)
	ia := &quantityv1.InstrumentAmount{Amount: "not-a-number", InstrumentCode: "GBP"}
	_, err := converter.FromProto(context.Background(), ia)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid amount")
}

func TestInstrumentAmountConverter_FromProto_FallbackToLegacy(t *testing.T) {
	// Resolver returns error - should fall back to ParseCurrency for GBP
	resolver := &adapterMockInstrumentResolver{err: refdata.ErrUnknownInstrument}
	converter := NewInstrumentAmountConverter(resolver)

	ia := &quantityv1.InstrumentAmount{
		Amount:         "100.00",
		InstrumentCode: "GBP",
		Version:        1,
	}

	result, err := converter.FromProto(context.Background(), ia)
	require.NoError(t, err)
	assert.Equal(t, "GBP", result.Instrument.Code)
	assert.Equal(t, "100", result.Amount.String())
}

func TestInstrumentAmountConverter_FromProto_NilResolver(t *testing.T) {
	// No resolver - should use legacy fromProtoInstrumentAmount
	converter := NewInstrumentAmountConverter(nil)

	ia := &quantityv1.InstrumentAmount{
		Amount:         "50.25",
		InstrumentCode: "USD",
		Version:        1,
	}

	result, err := converter.FromProto(context.Background(), ia)
	require.NoError(t, err)
	assert.Equal(t, "USD", result.Instrument.Code)
	assert.Equal(t, "50.25", result.Amount.String())
}

func TestInstrumentAmountConverter_Roundtrip(t *testing.T) {
	resolver := &adapterMockInstrumentResolver{
		instruments: map[string]refdata.InstrumentProperties{
			"KWH": {Code: "KWH", Dimension: "ENERGY", Precision: 3, RoundingMode: "HALF_UP"},
		},
	}
	converter := NewInstrumentAmountConverter(resolver)

	ia := &quantityv1.InstrumentAmount{
		Amount:         "123.456",
		InstrumentCode: "KWH",
		Version:        1,
	}

	money, err := converter.FromProto(context.Background(), ia)
	require.NoError(t, err)

	roundtripped := ToProtoInstrumentAmount(money)
	assert.Equal(t, "123.456", roundtripped.Amount)
	assert.Equal(t, "KWH", roundtripped.InstrumentCode)
}

func TestToProtoInstrumentAmount_Exported(t *testing.T) {
	inst := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
	m := domain.NewMoney(decimal.NewFromInt(42), inst)

	result := ToProtoInstrumentAmount(m)
	assert.Equal(t, "GBP", result.InstrumentCode)
	assert.Equal(t, "42", result.Amount)
}
