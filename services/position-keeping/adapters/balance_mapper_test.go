package adapters_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/adapters"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

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
			errorIs:     adapters.ErrInvalidCurrency,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := adapters.ToDomainMoney(tt.proto)

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
			result, err := adapters.ToDomainMoney(proto)
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
			domainMoney, err := adapters.ToDomainMoney(original)
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
