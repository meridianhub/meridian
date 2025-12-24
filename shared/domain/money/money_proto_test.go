package money_test

import (
	"testing"

	"github.com/meridianhub/meridian/shared/domain/money"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	moneypb "google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"
)

// toProtoMoney converts domain Money to protobuf google.type.Money.
// This mirrors the implementation in services/financial-accounting/service/adapters.go
// to test the precision guarantees of the conversion.
func toProtoMoney(m money.Money) *moneypb.Money {
	amount := m.Amount()
	units := amount.IntPart()
	fraction := amount.Sub(amount.Truncate(0))
	nanos := fraction.Mul(decimal.NewFromInt(1_000_000_000)).IntPart()

	// Clamp nanos to int32 range to prevent overflow
	if nanos > 999_999_999 {
		nanos = 999_999_999
	} else if nanos < -999_999_999 {
		nanos = -999_999_999
	}

	return &moneypb.Money{
		CurrencyCode: string(m.Currency()),
		Units:        units,
		Nanos:        int32(nanos),
	}
}

// fromProtoMoney converts protobuf Money to domain Money.
// This mirrors the implementation in services/financial-accounting/service/adapters.go
// to test the precision guarantees of the conversion.
func fromProtoMoney(protoMoney *moneypb.Money) (money.Money, error) {
	if protoMoney == nil {
		return money.Money{}, assert.AnError
	}

	// Convert units and nanos to decimal
	amount := decimal.NewFromInt(protoMoney.Units)
	if protoMoney.Nanos != 0 {
		nanosPart := decimal.NewFromInt(int64(protoMoney.Nanos)).Div(decimal.NewFromInt(1000000000))
		amount = amount.Add(nanosPart)
	}

	currency, err := money.ParseCurrency(protoMoney.CurrencyCode)
	if err != nil {
		return money.Money{}, err
	}

	return money.New(amount, currency)
}

func TestMoney_ProtobufRoundTrip_StandardPrecision(t *testing.T) {
	tests := []struct {
		name         string
		amountStr    string
		currency     money.Currency
		wantUnits    int64
		wantNanos    int32
		expectExact  bool
		maxDeviation string // Maximum acceptable deviation if not exact
	}{
		{
			name:        "GBP 100.00 - exact 2 decimals",
			amountStr:   "100.00",
			currency:    money.CurrencyGBP,
			wantUnits:   100,
			wantNanos:   0,
			expectExact: true,
		},
		{
			name:        "USD 123.45 - exact 2 decimals",
			amountStr:   "123.45",
			currency:    money.CurrencyUSD,
			wantUnits:   123,
			wantNanos:   450000000,
			expectExact: true,
		},
		{
			name:        "EUR 0.01 - minimum positive",
			amountStr:   "0.01",
			currency:    money.CurrencyEUR,
			wantUnits:   0,
			wantNanos:   10000000,
			expectExact: true,
		},
		{
			name:        "JPY 1000 - zero decimal currency",
			amountStr:   "1000",
			currency:    money.CurrencyJPY,
			wantUnits:   1000,
			wantNanos:   0,
			expectExact: true,
		},
		{
			name:        "GBP -50.25 - negative amount",
			amountStr:   "-50.25",
			currency:    money.CurrencyGBP,
			wantUnits:   -50,
			wantNanos:   -250000000,
			expectExact: true,
		},
		{
			name:        "USD 0.00 - zero amount",
			amountStr:   "0.00",
			currency:    money.CurrencyUSD,
			wantUnits:   0,
			wantNanos:   0,
			expectExact: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse the decimal amount
			amount, err := decimal.NewFromString(tt.amountStr)
			require.NoError(t, err, "test setup: invalid decimal string")

			// Create domain Money
			originalMoney, err := money.New(amount, tt.currency)
			require.NoError(t, err, "failed to create Money")

			// Convert to protobuf
			protoMoney := toProtoMoney(originalMoney)
			require.NotNil(t, protoMoney)

			// Verify protobuf structure
			assert.Equal(t, string(tt.currency), protoMoney.CurrencyCode, "currency code mismatch")
			assert.Equal(t, tt.wantUnits, protoMoney.Units, "units mismatch")
			assert.Equal(t, tt.wantNanos, protoMoney.Nanos, "nanos mismatch")

			// Serialize to bytes (this is what happens during transport)
			serialized, err := proto.Marshal(protoMoney)
			require.NoError(t, err, "failed to serialize protobuf")

			// Deserialize from bytes
			deserializedProto := &moneypb.Money{}
			err = proto.Unmarshal(serialized, deserializedProto)
			require.NoError(t, err, "failed to deserialize protobuf")

			// Convert back to domain Money
			roundTrippedMoney, err := fromProtoMoney(deserializedProto)
			require.NoError(t, err, "failed to convert from protobuf")

			// Verify exact equality
			if tt.expectExact {
				assert.True(t, originalMoney.Equals(roundTrippedMoney),
					"round-trip failed: expected %s, got %s",
					originalMoney.Amount().String(), roundTrippedMoney.Amount().String())
			} else {
				// For cases where we don't expect exact equality (very high precision),
				// verify the deviation is within acceptable bounds
				maxDev, _ := decimal.NewFromString(tt.maxDeviation)
				diff := originalMoney.Amount().Sub(roundTrippedMoney.Amount()).Abs()
				assert.True(t, diff.LessThanOrEqual(maxDev),
					"deviation %s exceeds maximum %s", diff, maxDev)
			}

			// Verify currency is preserved
			assert.Equal(t, originalMoney.Currency(), roundTrippedMoney.Currency(),
				"currency changed during round-trip")
		})
	}
}

func TestMoney_ProtobufRoundTrip_ExtendedPrecision(t *testing.T) {
	tests := []struct {
		name        string
		amountStr   string
		currency    money.Currency
		expectExact bool
	}{
		{
			name:        "3 decimal places - exact",
			amountStr:   "100.123",
			currency:    money.CurrencyGBP,
			expectExact: true,
		},
		{
			name:        "4 decimal places - exact",
			amountStr:   "100.1234",
			currency:    money.CurrencyUSD,
			expectExact: true,
		},
		{
			name:        "5 decimal places - exact",
			amountStr:   "100.12345",
			currency:    money.CurrencyEUR,
			expectExact: true,
		},
		{
			name:        "6 decimal places - exact",
			amountStr:   "100.123456",
			currency:    money.CurrencyGBP,
			expectExact: true,
		},
		{
			name:        "7 decimal places - exact",
			amountStr:   "100.1234567",
			currency:    money.CurrencyUSD,
			expectExact: true,
		},
		{
			name:        "8 decimal places - exact",
			amountStr:   "100.12345678",
			currency:    money.CurrencyEUR,
			expectExact: true,
		},
		{
			name:        "9 decimal places - maximum protobuf precision (nanos)",
			amountStr:   "100.123456789",
			currency:    money.CurrencyGBP,
			expectExact: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, err := decimal.NewFromString(tt.amountStr)
			require.NoError(t, err, "test setup: invalid decimal string")

			originalMoney, err := money.New(amount, tt.currency)
			require.NoError(t, err, "failed to create Money")

			// Convert to protobuf
			protoMoney := toProtoMoney(originalMoney)

			// Serialize and deserialize
			serialized, err := proto.Marshal(protoMoney)
			require.NoError(t, err)

			deserializedProto := &moneypb.Money{}
			err = proto.Unmarshal(serialized, deserializedProto)
			require.NoError(t, err)

			// Convert back to domain Money
			roundTrippedMoney, err := fromProtoMoney(deserializedProto)
			require.NoError(t, err)

			if tt.expectExact {
				assert.True(t, originalMoney.Equals(roundTrippedMoney),
					"round-trip precision lost: original=%s, round-tripped=%s",
					originalMoney.Amount().String(), roundTrippedMoney.Amount().String())
			}
		})
	}
}

func TestMoney_ProtobufRoundTrip_ExtremeValues(t *testing.T) {
	tests := []struct {
		name      string
		amountStr string
		currency  money.Currency
	}{
		{
			name:      "very large positive amount",
			amountStr: "999999999999999.99",
			currency:  money.CurrencyUSD,
		},
		{
			name:      "very large negative amount",
			amountStr: "-999999999999999.99",
			currency:  money.CurrencyGBP,
		},
		{
			name:      "very small positive amount",
			amountStr: "0.000000001",
			currency:  money.CurrencyEUR,
		},
		{
			name:      "very small negative amount",
			amountStr: "-0.000000001",
			currency:  money.CurrencyUSD,
		},
		{
			name:      "maximum precision positive",
			amountStr: "123456789.987654321",
			currency:  money.CurrencyGBP,
		},
		{
			name:      "maximum precision negative",
			amountStr: "-123456789.987654321",
			currency:  money.CurrencyEUR,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, err := decimal.NewFromString(tt.amountStr)
			require.NoError(t, err)

			originalMoney, err := money.New(amount, tt.currency)
			require.NoError(t, err)

			// Round-trip through protobuf
			protoMoney := toProtoMoney(originalMoney)
			serialized, err := proto.Marshal(protoMoney)
			require.NoError(t, err)

			deserializedProto := &moneypb.Money{}
			err = proto.Unmarshal(serialized, deserializedProto)
			require.NoError(t, err)

			roundTrippedMoney, err := fromProtoMoney(deserializedProto)
			require.NoError(t, err)

			// For extreme values, verify precision is maintained within 1 nano unit (10^-9)
			diff := originalMoney.Amount().Sub(roundTrippedMoney.Amount()).Abs()
			maxAcceptableDiff := decimal.NewFromFloat(0.000000001) // 1 nanosecond
			assert.True(t, diff.LessThanOrEqual(maxAcceptableDiff),
				"precision loss exceeds 1 nano: diff=%s", diff)
		})
	}
}

func TestMoney_ProtobufRoundTrip_CurrencySpecificDecimals(t *testing.T) {
	tests := []struct {
		name      string
		amountStr string
		currency  money.Currency
		decimals  int32
	}{
		{
			name:      "JPY - 0 decimal places",
			amountStr: "10000",
			currency:  money.CurrencyJPY,
			decimals:  0,
		},
		{
			name:      "JPY - fractional input rounds",
			amountStr: "10000.56",
			currency:  money.CurrencyJPY,
			decimals:  0,
		},
		{
			name:      "GBP - 2 decimal places",
			amountStr: "100.50",
			currency:  money.CurrencyGBP,
			decimals:  2,
		},
		{
			name:      "USD - 2 decimal places",
			amountStr: "99.99",
			currency:  money.CurrencyUSD,
			decimals:  2,
		},
		{
			name:      "EUR - 2 decimal places",
			amountStr: "1234.56",
			currency:  money.CurrencyEUR,
			decimals:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, err := decimal.NewFromString(tt.amountStr)
			require.NoError(t, err)

			originalMoney, err := money.New(amount, tt.currency)
			require.NoError(t, err)

			// Verify currency decimal places
			assert.Equal(t, tt.decimals, tt.currency.DecimalPlaces())

			// Round-trip
			protoMoney := toProtoMoney(originalMoney)
			serialized, err := proto.Marshal(protoMoney)
			require.NoError(t, err)

			deserializedProto := &moneypb.Money{}
			err = proto.Unmarshal(serialized, deserializedProto)
			require.NoError(t, err)

			roundTrippedMoney, err := fromProtoMoney(deserializedProto)
			require.NoError(t, err)

			// The protobuf representation should preserve the full precision
			// (up to 9 decimal places), even if the currency typically uses fewer
			assert.Equal(t, tt.currency, roundTrippedMoney.Currency())

			// For currencies with 0 or 2 decimal places, the round-trip should be exact
			// even if we store extra precision in protobuf
			if tt.decimals <= 2 {
				assert.True(t, originalMoney.Equals(roundTrippedMoney),
					"exact precision expected for currency with %d decimals", tt.decimals)
			}
		})
	}
}

func TestMoney_ProtobufSerialization_ByteLevelEquality(t *testing.T) {
	// Test that identical Money values produce identical protobuf bytes
	amount := decimal.NewFromFloat(123.45)
	money1, err := money.New(amount, money.CurrencyUSD)
	require.NoError(t, err)

	money2, err := money.New(amount, money.CurrencyUSD)
	require.NoError(t, err)

	proto1 := toProtoMoney(money1)
	proto2 := toProtoMoney(money2)

	bytes1, err := proto.Marshal(proto1)
	require.NoError(t, err)

	bytes2, err := proto.Marshal(proto2)
	require.NoError(t, err)

	assert.Equal(t, bytes1, bytes2, "identical Money values should produce identical protobuf bytes")
}

func TestMoney_ProtobufRoundTrip_BHDThreeDecimals(t *testing.T) {
	// BHD (Bahraini Dinar) uses 3 decimal places, but our current implementation
	// only supports the currencies defined in money.Currency.
	// This test documents the behavior for potential future support.
	// For now, we test with EUR and verify 3-decimal precision works.

	amount, err := decimal.NewFromString("100.123")
	require.NoError(t, err)

	originalMoney, err := money.New(amount, money.CurrencyEUR)
	require.NoError(t, err)

	// Round-trip
	protoMoney := toProtoMoney(originalMoney)
	serialized, err := proto.Marshal(protoMoney)
	require.NoError(t, err)

	deserializedProto := &moneypb.Money{}
	err = proto.Unmarshal(serialized, deserializedProto)
	require.NoError(t, err)

	roundTrippedMoney, err := fromProtoMoney(deserializedProto)
	require.NoError(t, err)

	// Verify exact 3-decimal precision is maintained
	assert.True(t, originalMoney.Equals(roundTrippedMoney),
		"3-decimal precision should be exact: original=%s, round-tripped=%s",
		originalMoney.Amount().String(), roundTrippedMoney.Amount().String())
}

func TestMoney_ProtobufRoundTrip_PrecisionBeyondNanos(t *testing.T) {
	// Test values with more than 9 decimal places
	// These should be truncated to nanosecond precision
	tests := []struct {
		name      string
		amountStr string
		currency  money.Currency
	}{
		{
			name:      "10 decimal places",
			amountStr: "100.1234567890",
			currency:  money.CurrencyUSD,
		},
		{
			name:      "15 decimal places",
			amountStr: "100.123456789012345",
			currency:  money.CurrencyGBP,
		},
		{
			name:      "28 decimal places (shopspring/decimal max)",
			amountStr: "100.1234567890123456789012345678",
			currency:  money.CurrencyEUR,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, err := decimal.NewFromString(tt.amountStr)
			require.NoError(t, err)

			originalMoney, err := money.New(amount, tt.currency)
			require.NoError(t, err)

			// Round-trip
			protoMoney := toProtoMoney(originalMoney)
			serialized, err := proto.Marshal(protoMoney)
			require.NoError(t, err)

			deserializedProto := &moneypb.Money{}
			err = proto.Unmarshal(serialized, deserializedProto)
			require.NoError(t, err)

			roundTrippedMoney, err := fromProtoMoney(deserializedProto)
			require.NoError(t, err)

			// The round-tripped value should match to 9 decimal places (nanos precision)
			// Any digits beyond that will be lost
			originalTruncated := originalMoney.Amount().Truncate(9)
			roundTrippedTruncated := roundTrippedMoney.Amount().Truncate(9)

			assert.True(t, originalTruncated.Equal(roundTrippedTruncated),
				"values should match to 9 decimal places: original=%s, round-tripped=%s",
				originalTruncated.String(), roundTrippedTruncated.String())

			// Document that precision beyond 9 decimals is lost
			if !originalMoney.Equals(roundTrippedMoney) {
				t.Logf("Note: Precision beyond 9 decimals was truncated as expected")
				t.Logf("Original:       %s", originalMoney.Amount().String())
				t.Logf("Round-tripped:  %s", roundTrippedMoney.Amount().String())
			}
		})
	}
}

func TestMoney_ProtobufNilHandling(t *testing.T) {
	// Test that nil protobuf money is handled gracefully
	_, err := fromProtoMoney(nil)
	assert.Error(t, err, "nil protobuf money should return error")
}

func TestMoney_ProtobufInvalidCurrency(t *testing.T) {
	// Test protobuf with invalid currency code
	protoMoney := &moneypb.Money{
		CurrencyCode: "INVALID",
		Units:        100,
		Nanos:        0,
	}

	_, err := fromProtoMoney(protoMoney)
	assert.Error(t, err, "invalid currency code should return error")
}

func TestMoney_ProtobufEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		units    int64
		nanos    int32
		currency string
	}{
		{
			name:     "maximum nanos value",
			units:    100,
			nanos:    999999999,
			currency: "USD",
		},
		{
			name:     "minimum nanos value",
			units:    -100,
			nanos:    -999999999,
			currency: "GBP",
		},
		{
			name:     "zero units with nanos",
			units:    0,
			nanos:    500000000,
			currency: "EUR",
		},
		{
			name:     "negative zero units with negative nanos",
			units:    0,
			nanos:    -500000000,
			currency: "USD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			protoMoney := &moneypb.Money{
				CurrencyCode: tt.currency,
				Units:        tt.units,
				Nanos:        tt.nanos,
			}

			// Serialize
			serialized, err := proto.Marshal(protoMoney)
			require.NoError(t, err)

			// Deserialize
			deserializedProto := &moneypb.Money{}
			err = proto.Unmarshal(serialized, deserializedProto)
			require.NoError(t, err)

			// Verify structure is preserved
			assert.Equal(t, tt.currency, deserializedProto.CurrencyCode)
			assert.Equal(t, tt.units, deserializedProto.Units)
			assert.Equal(t, tt.nanos, deserializedProto.Nanos)

			// Convert to domain money
			domainMoney, err := fromProtoMoney(deserializedProto)
			require.NoError(t, err)

			// Convert back to proto
			roundTrippedProto := toProtoMoney(domainMoney)

			// Verify proto values match
			assert.Equal(t, deserializedProto.CurrencyCode, roundTrippedProto.CurrencyCode)
			assert.Equal(t, deserializedProto.Units, roundTrippedProto.Units)
			assert.Equal(t, deserializedProto.Nanos, roundTrippedProto.Nanos)
		})
	}
}
