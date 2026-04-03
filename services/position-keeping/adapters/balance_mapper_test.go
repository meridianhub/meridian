package adapters_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/position-keeping/adapters"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
)

// stubResolver is a test InstrumentResolver that returns predefined properties.
type stubResolver struct {
	instruments map[string]refdata.InstrumentProperties
}

func (s *stubResolver) Resolve(_ context.Context, code string) (refdata.InstrumentProperties, error) {
	props, ok := s.instruments[code]
	if !ok {
		return refdata.InstrumentProperties{}, refdata.ErrUnknownInstrument
	}
	return props, nil
}

func newTestResolver() *stubResolver {
	return &stubResolver{
		instruments: map[string]refdata.InstrumentProperties{
			"GBP":          {Code: "GBP", Dimension: "CURRENCY", Precision: 2, RoundingMode: "HALF_EVEN"},
			"USD":          {Code: "USD", Dimension: "CURRENCY", Precision: 2, RoundingMode: "HALF_EVEN"},
			"EUR":          {Code: "EUR", Dimension: "CURRENCY", Precision: 2, RoundingMode: "HALF_EVEN"},
			"JPY":          {Code: "JPY", Dimension: "CURRENCY", Precision: 0, RoundingMode: "HALF_EVEN"},
			"CHF":          {Code: "CHF", Dimension: "CURRENCY", Precision: 2, RoundingMode: "HALF_EVEN"},
			"CAD":          {Code: "CAD", Dimension: "CURRENCY", Precision: 2, RoundingMode: "HALF_EVEN"},
			"AUD":          {Code: "AUD", Dimension: "CURRENCY", Precision: 2, RoundingMode: "HALF_EVEN"},
			"KWH":          {Code: "KWH", Dimension: "ENERGY", Precision: 6, RoundingMode: "HALF_EVEN"},
			"GPU_HOUR":     {Code: "GPU_HOUR", Dimension: "COMPUTE", Precision: 6, RoundingMode: "HALF_EVEN"},
			"CARBON_TONNE": {Code: "CARBON_TONNE", Dimension: "CARBON", Precision: 3, RoundingMode: "HALF_EVEN"},
		},
	}
}

// TestToProtoBalanceType tests conversion of all 7 domain balance types to proto.
func TestToProtoBalanceType(t *testing.T) {
	tests := []struct {
		name     string
		domain   domain.BalanceType
		expected positionkeepingv1.BalanceType
	}{
		{
			name:     "opening balance type",
			domain:   domain.BalanceTypeOpening,
			expected: positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING,
		},
		{
			name:     "closing balance type",
			domain:   domain.BalanceTypeClosing,
			expected: positionkeepingv1.BalanceType_BALANCE_TYPE_CLOSING,
		},
		{
			name:     "current balance type",
			domain:   domain.BalanceTypeCurrent,
			expected: positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
		},
		{
			name:     "available balance type",
			domain:   domain.BalanceTypeAvailable,
			expected: positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
		},
		{
			name:     "ledger balance type",
			domain:   domain.BalanceTypeLedger,
			expected: positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER,
		},
		{
			name:     "reserve balance type",
			domain:   domain.BalanceTypeReserve,
			expected: positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE,
		},
		{
			name:     "free balance type",
			domain:   domain.BalanceTypeFree,
			expected: positionkeepingv1.BalanceType_BALANCE_TYPE_FREE,
		},
		{
			name:     "unknown balance type maps to unspecified",
			domain:   domain.BalanceTypeUnknown,
			expected: positionkeepingv1.BalanceType_BALANCE_TYPE_UNSPECIFIED,
		},
		{
			name:     "invalid/custom balance type maps to unspecified",
			domain:   domain.BalanceType("INVALID"),
			expected: positionkeepingv1.BalanceType_BALANCE_TYPE_UNSPECIFIED,
		},
		{
			name:     "empty balance type maps to unspecified",
			domain:   domain.BalanceType(""),
			expected: positionkeepingv1.BalanceType_BALANCE_TYPE_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adapters.ToProtoBalanceType(tt.domain)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestToDomainBalanceType tests conversion of proto balance types to domain.
func TestToDomainBalanceType(t *testing.T) {
	tests := []struct {
		name        string
		proto       positionkeepingv1.BalanceType
		expected    domain.BalanceType
		expectError bool
		errorIs     error
	}{
		{
			name:        "opening balance type",
			proto:       positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING,
			expected:    domain.BalanceTypeOpening,
			expectError: false,
		},
		{
			name:        "closing balance type",
			proto:       positionkeepingv1.BalanceType_BALANCE_TYPE_CLOSING,
			expected:    domain.BalanceTypeClosing,
			expectError: false,
		},
		{
			name:        "current balance type",
			proto:       positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT,
			expected:    domain.BalanceTypeCurrent,
			expectError: false,
		},
		{
			name:        "available balance type",
			proto:       positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE,
			expected:    domain.BalanceTypeAvailable,
			expectError: false,
		},
		{
			name:        "ledger balance type",
			proto:       positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER,
			expected:    domain.BalanceTypeLedger,
			expectError: false,
		},
		{
			name:        "reserve balance type",
			proto:       positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE,
			expected:    domain.BalanceTypeReserve,
			expectError: false,
		},
		{
			name:        "free balance type",
			proto:       positionkeepingv1.BalanceType_BALANCE_TYPE_FREE,
			expected:    domain.BalanceTypeFree,
			expectError: false,
		},
		{
			name:        "unspecified returns error",
			proto:       positionkeepingv1.BalanceType_BALANCE_TYPE_UNSPECIFIED,
			expected:    domain.BalanceTypeUnknown,
			expectError: true,
			errorIs:     adapters.ErrUnspecifiedBalanceType,
		},
		{
			name:        "unknown proto value returns error",
			proto:       positionkeepingv1.BalanceType(999),
			expected:    domain.BalanceTypeUnknown,
			expectError: true,
			errorIs:     adapters.ErrUnknownProtoBalanceType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := adapters.ToDomainBalanceType(tt.proto)

			if tt.expectError {
				require.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestToProtoMoneyAmount tests conversion of domain Money to proto MoneyAmount.
func TestToProtoMoneyAmount(t *testing.T) {
	tests := []struct {
		name          string
		money         domain.Money
		expectedUnits int64
		expectedNanos int32
		expectedCode  string
	}{
		{
			name:          "GBP with 2 decimal places",
			money:         mustNewMoney("123.45", domain.CurrencyGBP),
			expectedUnits: 123,
			expectedNanos: 450000000,
			expectedCode:  "GBP",
		},
		{
			name:          "GBP with fractional cents",
			money:         mustNewMoney("123.456789", domain.CurrencyGBP),
			expectedUnits: 123,
			expectedNanos: 456789000,
			expectedCode:  "GBP",
		},
		{
			name:          "JPY with 0 decimal places",
			money:         mustNewMoney("12345", domain.CurrencyJPY),
			expectedUnits: 12345,
			expectedNanos: 0,
			expectedCode:  "JPY",
		},
		{
			name:          "USD with cents",
			money:         mustNewMoney("100.99", domain.CurrencyUSD),
			expectedUnits: 100,
			expectedNanos: 990000000,
			expectedCode:  "USD",
		},
		{
			name:          "EUR zero amount",
			money:         mustNewMoney("0.00", domain.CurrencyEUR),
			expectedUnits: 0,
			expectedNanos: 0,
			expectedCode:  "EUR",
		},
		{
			name:          "negative amount",
			money:         mustNewMoney("-50.25", domain.CurrencyGBP),
			expectedUnits: -50,
			expectedNanos: -250000000,
			expectedCode:  "GBP",
		},
		{
			name:          "large amount",
			money:         mustNewMoney("999999999.99", domain.CurrencyUSD),
			expectedUnits: 999999999,
			expectedNanos: 990000000,
			expectedCode:  "USD",
		},
		{
			name:          "very small fractional amount",
			money:         mustNewMoney("0.001", domain.CurrencyGBP),
			expectedUnits: 0,
			expectedNanos: 1000000,
			expectedCode:  "GBP",
		},
		{
			name:          "CHF currency",
			money:         mustNewMoney("75.50", domain.CurrencyCHF),
			expectedUnits: 75,
			expectedNanos: 500000000,
			expectedCode:  "CHF",
		},
		{
			name:          "CAD currency",
			money:         mustNewMoney("200.15", domain.CurrencyCAD),
			expectedUnits: 200,
			expectedNanos: 150000000,
			expectedCode:  "CAD",
		},
		{
			name:          "AUD currency",
			money:         mustNewMoney("150.33", domain.CurrencyAUD),
			expectedUnits: 150,
			expectedNanos: 330000000,
			expectedCode:  "AUD",
		},
		{
			name:          "nanos clamped to max positive overflow",
			money:         mustNewMoney("1.999999999999", domain.CurrencyGBP),
			expectedUnits: 1,
			expectedNanos: 999999999, // Clamped to int32 max nanos
			expectedCode:  "GBP",
		},
		{
			name:          "nanos clamped to min negative overflow",
			money:         mustNewMoney("-1.999999999999", domain.CurrencyGBP),
			expectedUnits: -1,
			expectedNanos: -999999999, // Clamped to int32 min nanos
			expectedCode:  "GBP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := adapters.ToProtoMoneyAmount(tt.money)

			require.NotNil(t, proto)
			require.NotNil(t, proto.Amount)
			assert.Equal(t, tt.expectedCode, proto.Amount.CurrencyCode)
			assert.Equal(t, tt.expectedUnits, proto.Amount.Units)
			assert.Equal(t, tt.expectedNanos, proto.Amount.Nanos)
		})
	}
}

// TestToDomainMoney tests conversion of proto MoneyAmount to domain Money.
func TestToDomainMoney(t *testing.T) {
	resolver := newTestResolver()
	ctx := context.Background()
	tests := []struct {
		name           string
		proto          *commonv1.MoneyAmount
		expectedAmount string
		expectedCur    domain.Currency
		expectError    bool
		errorIs        error
	}{
		{
			name: "GBP with cents",
			proto: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        123,
					Nanos:        450000000,
				},
			},
			expectedAmount: "123.45",
			expectedCur:    domain.CurrencyGBP,
			expectError:    false,
		},
		{
			name: "USD with fractional nanos",
			proto: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "USD",
					Units:        100,
					Nanos:        456789000,
				},
			},
			expectedAmount: "100.456789",
			expectedCur:    domain.CurrencyUSD,
			expectError:    false,
		},
		{
			name: "JPY zero nanos",
			proto: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "JPY",
					Units:        12345,
					Nanos:        0,
				},
			},
			expectedAmount: "12345",
			expectedCur:    domain.CurrencyJPY,
			expectError:    false,
		},
		{
			name: "EUR zero amount",
			proto: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "EUR",
					Units:        0,
					Nanos:        0,
				},
			},
			expectedAmount: "0",
			expectedCur:    domain.CurrencyEUR,
			expectError:    false,
		},
		{
			name: "negative amount",
			proto: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "GBP",
					Units:        -50,
					Nanos:        -250000000,
				},
			},
			expectedAmount: "-50.25",
			expectedCur:    domain.CurrencyGBP,
			expectError:    false,
		},
		{
			name: "CHF currency",
			proto: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "CHF",
					Units:        75,
					Nanos:        500000000,
				},
			},
			expectedAmount: "75.5",
			expectedCur:    domain.CurrencyCHF,
			expectError:    false,
		},
		{
			name:        "nil MoneyAmount returns error",
			proto:       nil,
			expectError: true,
			errorIs:     adapters.ErrNilMoneyAmount,
		},
		{
			name: "nil google.type.Money returns error",
			proto: &commonv1.MoneyAmount{
				Amount: nil,
			},
			expectError: true,
			errorIs:     adapters.ErrNilGoogleMoney,
		},
		{
			name: "empty currency code returns error",
			proto: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "",
					Units:        100,
					Nanos:        0,
				},
			},
			expectError: true,
			errorIs:     adapters.ErrInvalidCurrency,
		},
		{
			name: "invalid currency code returns error",
			proto: &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: "XXX",
					Units:        100,
					Nanos:        0,
				},
			},
			expectError: true,
			errorIs:     refdata.ErrUnknownInstrument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := adapters.ToDomainMoney(ctx, resolver, tt.proto)

			if tt.expectError {
				require.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
				return
			}

			require.NoError(t, err)
			expectedDec, _ := decimal.NewFromString(tt.expectedAmount)
			assert.True(t, result.Amount.Equal(expectedDec),
				"expected %s, got %s", expectedDec.String(), result.Amount.String())
			assert.Equal(t, tt.expectedCur, domain.MoneyCurrency(result))
		})
	}
}

// TestBalanceTypeRoundTrip tests that converting domain->proto->domain preserves the value.
func TestBalanceTypeRoundTrip(t *testing.T) {
	balanceTypes := []domain.BalanceType{
		domain.BalanceTypeOpening,
		domain.BalanceTypeClosing,
		domain.BalanceTypeCurrent,
		domain.BalanceTypeAvailable,
		domain.BalanceTypeLedger,
		domain.BalanceTypeReserve,
		domain.BalanceTypeFree,
	}

	for _, bt := range balanceTypes {
		t.Run(string(bt), func(t *testing.T) {
			// Domain -> Proto
			proto := adapters.ToProtoBalanceType(bt)

			// Proto -> Domain
			result, err := adapters.ToDomainBalanceType(proto)
			require.NoError(t, err)
			assert.Equal(t, bt, result)
		})
	}
}

// TestMoneyRoundTrip tests that converting domain->proto->domain preserves the value.
func TestMoneyRoundTrip(t *testing.T) {
	resolver := newTestResolver()
	ctx := context.Background()

	testCases := []struct {
		amount   string
		currency domain.Currency
	}{
		{"123.45", domain.CurrencyGBP},
		{"0.00", domain.CurrencyUSD},
		{"-999.99", domain.CurrencyEUR},
		{"1000000.50", domain.CurrencyCHF},
		{"12345", domain.CurrencyJPY},
		{"0.01", domain.CurrencyCAD},
		{"0.123456789", domain.CurrencyAUD},
	}

	for _, tc := range testCases {
		t.Run(tc.amount+"_"+string(tc.currency), func(t *testing.T) {
			// Create domain Money
			original := mustNewMoney(tc.amount, tc.currency)

			// Domain -> Proto
			proto := adapters.ToProtoMoneyAmount(original)

			// Proto -> Domain
			result, err := adapters.ToDomainMoney(ctx, resolver, proto)
			require.NoError(t, err)

			// Compare amounts (may have precision differences for sub-nano values)
			originalDec, _ := decimal.NewFromString(tc.amount)

			// For values that exceed nano precision, we only compare up to 9 decimal places
			originalRounded := originalDec.RoundBank(9)
			resultRounded := result.Amount.RoundBank(9)
			assert.True(t, originalRounded.Equal(resultRounded),
				"expected %s, got %s", originalRounded.String(), resultRounded.String())

			// Currency must match exactly
			assert.Equal(t, tc.currency, domain.MoneyCurrency(result))
		})
	}
}

// TestProtoToProtoMoneyRoundTrip tests proto->domain->proto preserves original proto values.
func TestProtoToProtoMoneyRoundTrip(t *testing.T) {
	resolver := newTestResolver()
	ctx := context.Background()

	testCases := []struct {
		name         string
		currencyCode string
		units        int64
		nanos        int32
	}{
		{"simple_gbp", "GBP", 100, 500000000},
		{"zero_nanos", "USD", 50, 0},
		{"zero_units", "EUR", 0, 250000000},
		{"negative", "GBP", -75, -330000000},
		{"jpy_no_decimals", "JPY", 10000, 0},
		{"large_amount", "CHF", 999999999, 999999999},
		{"small_fractional", "CAD", 0, 1000000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create original proto
			original := &commonv1.MoneyAmount{
				Amount: &money.Money{
					CurrencyCode: tc.currencyCode,
					Units:        tc.units,
					Nanos:        tc.nanos,
				},
			}

			// Proto -> Domain
			domainMoney, err := adapters.ToDomainMoney(ctx, resolver, original)
			require.NoError(t, err)

			// Domain -> Proto
			result := adapters.ToProtoMoneyAmount(domainMoney)

			// Compare
			assert.Equal(t, tc.currencyCode, result.Amount.CurrencyCode)
			assert.Equal(t, tc.units, result.Amount.Units)
			assert.Equal(t, tc.nanos, result.Amount.Nanos)
		})
	}
}

// Helper function to create domain Money for tests.
func mustNewMoney(amount string, currency domain.Currency) domain.Money {
	dec, err := decimal.NewFromString(amount)
	if err != nil {
		panic(err)
	}
	m, err := domain.NewMoney(dec, currency)
	if err != nil {
		panic(err)
	}
	return m
}

// =============================================================================
// InstrumentAmount Adapter Tests
// =============================================================================

// TestToProtoInstrumentAmount tests conversion of domain Money to proto InstrumentAmount.
func TestToProtoInstrumentAmount(t *testing.T) {
	tests := []struct {
		name           string
		domainMoney    domain.Money
		expectedAmount string
		expectedCode   string
		expectedVer    int32
	}{
		{
			name:           "GBP with 2 decimal places",
			domainMoney:    mustNewMoney("123.45", domain.CurrencyGBP),
			expectedAmount: "123.45",
			expectedCode:   "GBP",
			expectedVer:    1,
		},
		{
			name:           "USD with fractional cents",
			domainMoney:    mustNewMoney("100.999", domain.CurrencyUSD),
			expectedAmount: "101.00", // Rounded to precision
			expectedCode:   "USD",
			expectedVer:    1,
		},
		{
			name:           "JPY with 0 decimal places",
			domainMoney:    mustNewMoney("1000", domain.CurrencyJPY),
			expectedAmount: "1000",
			expectedCode:   "JPY",
			expectedVer:    1,
		},
		{
			name:           "EUR zero amount",
			domainMoney:    mustNewMoney("0", domain.CurrencyEUR),
			expectedAmount: "0.00",
			expectedCode:   "EUR",
			expectedVer:    1,
		},
		{
			name:           "negative amount",
			domainMoney:    mustNewMoney("-50.25", domain.CurrencyGBP),
			expectedAmount: "-50.25",
			expectedCode:   "GBP",
			expectedVer:    1,
		},
		{
			name:           "large amount",
			domainMoney:    mustNewMoney("9999999.99", domain.CurrencyGBP),
			expectedAmount: "9999999.99",
			expectedCode:   "GBP",
			expectedVer:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adapters.ToProtoInstrumentAmount(tt.domainMoney)

			assert.Equal(t, tt.expectedAmount, result.Amount)
			assert.Equal(t, tt.expectedCode, result.InstrumentCode)
			assert.Equal(t, tt.expectedVer, result.Version)
		})
	}
}

// TestToProtoInstrumentAmountFromAsset tests conversion of domain Asset to proto InstrumentAmount.
func TestToProtoInstrumentAmountFromAsset(t *testing.T) {
	tests := []struct {
		name           string
		instrument     domain.Instrument
		amount         decimal.Decimal
		expectedAmount string
		expectedCode   string
	}{
		{
			name:           "KWH energy asset",
			instrument:     domain.MustNewInstrument("KWH", 1, "ENERGY", 6),
			amount:         decimal.NewFromFloat(1234.567890),
			expectedAmount: "1234.567890",
			expectedCode:   "KWH",
		},
		{
			name:           "GPU_HOUR compute asset",
			instrument:     domain.MustNewInstrument("GPU_HOUR", 1, "COMPUTE", 6),
			amount:         decimal.NewFromFloat(100.5),
			expectedAmount: "100.500000",
			expectedCode:   "GPU_HOUR",
		},
		{
			name:           "CARBON_TONNE asset",
			instrument:     domain.MustNewInstrument("CARBON_TONNE", 1, "CARBON", 3),
			amount:         decimal.NewFromFloat(500.123),
			expectedAmount: "500.123",
			expectedCode:   "CARBON_TONNE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asset := domain.NewAsset(tt.amount, tt.instrument)
			result := adapters.ToProtoInstrumentAmountFromAsset(asset)

			assert.Equal(t, tt.expectedAmount, result.Amount)
			assert.Equal(t, tt.expectedCode, result.InstrumentCode)
			assert.Equal(t, int32(1), result.Version)
		})
	}
}

// TestToDomainMoneyFromInstrumentAmount tests conversion of proto InstrumentAmount to domain Money.
func TestToDomainMoneyFromInstrumentAmount(t *testing.T) {
	resolver := newTestResolver()
	ctx := context.Background()

	tests := []struct {
		name         string
		proto        *quantityv1.InstrumentAmount
		expectError  bool
		errContains  string
		expectAmount string
		expectCode   string
	}{
		{
			name: "valid GBP amount",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "123.45",
				InstrumentCode: "GBP",
				Version:        1,
			},
			expectError:  false,
			expectAmount: "123.45",
			expectCode:   "GBP",
		},
		{
			name: "valid USD zero amount",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "0",
				InstrumentCode: "USD",
				Version:        1,
			},
			expectError:  false,
			expectAmount: "0",
			expectCode:   "USD",
		},
		{
			name: "valid negative amount",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "-50.25",
				InstrumentCode: "EUR",
				Version:        1,
			},
			expectError:  false,
			expectAmount: "-50.25",
			expectCode:   "EUR",
		},
		{
			name: "valid KWH non-fiat instrument",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "500.123456",
				InstrumentCode: "KWH",
				Version:        1,
			},
			expectError:  false,
			expectAmount: "500.123456",
			expectCode:   "KWH",
		},
		{
			name: "valid CARBON_TONNE non-fiat instrument",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "12.5",
				InstrumentCode: "CARBON_TONNE",
				Version:        1,
			},
			expectError:  false,
			expectAmount: "12.5",
			expectCode:   "CARBON_TONNE",
		},
		{
			name:        "nil InstrumentAmount returns error",
			proto:       nil,
			expectError: true,
			errContains: "nil",
		},
		{
			name: "empty instrument code returns error",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "",
				Version:        1,
			},
			expectError: true,
			errContains: "instrument code",
		},
		{
			name: "invalid amount string returns error",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "not-a-number",
				InstrumentCode: "GBP",
				Version:        1,
			},
			expectError: true,
			errContains: "invalid amount",
		},
		{
			name: "unknown instrument code returns error",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "UNKNOWN_ASSET",
				Version:        1,
			},
			expectError: true,
			errContains: "unknown instrument",
		},
		{
			name: "negative version returns error",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "GBP",
				Version:        -1,
			},
			expectError: true,
			errContains: "version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := adapters.ToDomainMoneyFromInstrumentAmount(ctx, resolver, tt.proto)

			if tt.expectError {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectAmount, result.Amount.String())
			assert.Equal(t, tt.expectCode, result.Instrument.Code)
		})
	}
}

// TestToDomainAssetFromInstrumentAmount tests conversion of proto InstrumentAmount to domain Asset.
func TestToDomainAssetFromInstrumentAmount(t *testing.T) {
	resolver := newTestResolver()
	ctx := context.Background()

	tests := []struct {
		name         string
		proto        *quantityv1.InstrumentAmount
		expectError  bool
		errContains  string
		expectAmount string
		expectCode   string
	}{
		{
			name: "valid KWH asset",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "1234.567890",
				InstrumentCode: "KWH",
				Version:        1,
			},
			expectError:  false,
			expectAmount: "1234.56789",
			expectCode:   "KWH",
		},
		{
			name: "valid GPU_HOUR asset",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "100.5",
				InstrumentCode: "GPU_HOUR",
				Version:        1,
			},
			expectError:  false,
			expectAmount: "100.5",
			expectCode:   "GPU_HOUR",
		},
		{
			name: "valid CARBON_TONNE asset",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "500.123",
				InstrumentCode: "CARBON_TONNE",
				Version:        1,
			},
			expectError:  false,
			expectAmount: "500.123",
			expectCode:   "CARBON_TONNE",
		},
		{
			name: "zero version defaults to 1",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "KWH",
				Version:        0, // Should default to 1
			},
			expectError:  false,
			expectAmount: "100",
			expectCode:   "KWH",
		},
		{
			name:        "nil InstrumentAmount returns error",
			proto:       nil,
			expectError: true,
			errContains: "nil",
		},
		{
			name: "empty instrument code returns error",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "",
				Version:        1,
			},
			expectError: true,
			errContains: "instrument code",
		},
		{
			name: "invalid amount string returns error",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "not-a-number",
				InstrumentCode: "KWH",
				Version:        1,
			},
			expectError: true,
			errContains: "invalid amount",
		},
		{
			name: "negative version returns error",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "KWH",
				Version:        -1,
			},
			expectError: true,
			errContains: "version",
		},
		{
			name: "unknown instrument returns error",
			proto: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "UNKNOWN_ASSET",
				Version:        1,
			},
			expectError: true,
			errContains: "instrument",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := adapters.ToDomainAssetFromInstrumentAmount(ctx, resolver, tt.proto)

			if tt.expectError {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectAmount, result.Amount.String())
			assert.Equal(t, tt.expectCode, result.Instrument.Code)
		})
	}
}

// TestRoundTripMoneyToInstrumentAmount tests round-trip conversion preserves precision.
func TestRoundTripMoneyToInstrumentAmount(t *testing.T) {
	resolver := newTestResolver()
	ctx := context.Background()

	tests := []struct {
		name     string
		amount   string
		currency domain.Currency
	}{
		{
			name:     "GBP round trip",
			amount:   "123.45",
			currency: domain.CurrencyGBP,
		},
		{
			name:     "USD round trip",
			amount:   "1000.00",
			currency: domain.CurrencyUSD,
		},
		{
			name:     "EUR round trip with zero fraction",
			amount:   "500.00",
			currency: domain.CurrencyEUR,
		},
		{
			name:     "JPY round trip (no decimals)",
			amount:   "1000",
			currency: domain.CurrencyJPY,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Domain -> Proto
			original := mustNewMoney(tt.amount, tt.currency)
			proto := adapters.ToProtoInstrumentAmount(original)

			// Proto -> Domain
			result, err := adapters.ToDomainMoneyFromInstrumentAmount(ctx, resolver, proto)
			require.NoError(t, err)

			// Compare
			assert.True(t, original.Amount.Equal(result.Amount),
				"Expected %s, got %s", original.Amount.String(), result.Amount.String())
			assert.Equal(t, original.Instrument.Code, result.Instrument.Code)
		})
	}
}
