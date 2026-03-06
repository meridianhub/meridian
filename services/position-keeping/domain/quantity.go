// Package domain re-exports quantity types from shared/platform/quantity for position-keeping service.
//
// This module provides the Universal Asset System quantity types which support both
// monetary (Money) and commodity (Asset) quantities with compile-time type safety.
//
// The Money type (Qty[Monetary]) replaces the previous money.Money type while maintaining
// backward compatibility for existing fiat currency use cases.
//
// The Amount type from shared/pkg/amount provides a dimension-agnostic value type that
// accepts any valid dimension (CURRENCY, ENERGY, CARBON, COMPUTE, etc.) and is the
// recommended type for cross-service communication involving non-currency instruments.
package domain

import (
	"github.com/meridianhub/meridian/shared/domain/money"
	sharedamount "github.com/meridianhub/meridian/shared/pkg/amount"
	"github.com/meridianhub/meridian/shared/platform/quantity"
	"github.com/shopspring/decimal"
)

// =============================================================================
// Core Quantity Types
// =============================================================================

// Qty is the generic quantity type with compile-time dimension safety.
// Use Money (Qty[Monetary]) for currencies and Asset (Qty[Commodity]) for other assets.
type Qty[D quantity.Dimension] = quantity.Qty[D]

// Money is a monetary quantity (Qty[Monetary]).
// Use this for all currency-denominated values (USD, EUR, GBP, etc.).
type Money = quantity.Money

// Asset is a commodity quantity (Qty[Commodity]).
// Use this for non-monetary assets like energy (KWH), compute (GPU_HOUR), carbon credits, etc.
type Asset = quantity.Asset

// Amount is a dimension-agnostic value type from shared/pkg/amount.
// Unlike Money (currency-only) and Asset (commodity-only), Amount accepts any valid dimension
// (CURRENCY, ENERGY, CARBON, COMPUTE, etc.) and is the recommended type for cross-service
// communication and persistence when the instrument dimension is not known at compile time.
type Amount = sharedamount.Amount

// Dimension types for compile-time safety.
type (
	// Monetary is a phantom type for monetary dimensions (currencies).
	Monetary = quantity.Monetary
	// Commodity is a phantom type for commodity dimensions.
	Commodity = quantity.Commodity
)

// Instrument identifies an asset type with version and precision information.
type Instrument = quantity.Instrument

// =============================================================================
// Errors
// =============================================================================

// Re-export errors from quantity package for consistency.
var (
	// ErrInstrumentMismatch is returned when attempting arithmetic on quantities
	// with different instruments.
	ErrInstrumentMismatch = quantity.ErrInstrumentMismatch

	// ErrDivisionByZero is returned when dividing by zero.
	ErrDivisionByZero = quantity.ErrDivisionByZero

	// ErrInvalidDecimalString is returned when a string cannot be parsed as decimal.
	ErrInvalidDecimalString = quantity.ErrInvalidDecimalString

	// ErrDimensionMismatch is returned when instrument dimension doesn't match type parameter.
	ErrDimensionMismatch = quantity.ErrDimensionMismatch

	// ErrEmptyCode is returned when an instrument code is empty.
	ErrEmptyCode = quantity.ErrEmptyCode

	// ErrInvalidCodeFormat is returned when instrument code format is invalid.
	ErrInvalidCodeFormat = quantity.ErrInvalidCodeFormat

	// ErrCodeTooLong is returned when instrument code exceeds max length.
	ErrCodeTooLong = quantity.ErrCodeTooLong

	// ErrInvalidDimension is returned when dimension string is not recognized.
	ErrInvalidDimension = quantity.ErrInvalidDimension

	// ErrNegativePrecision is returned when precision is negative.
	ErrNegativePrecision = quantity.ErrNegativePrecision

	// ErrPrecisionTooHigh is returned when precision exceeds maximum (18).
	ErrPrecisionTooHigh = quantity.ErrPrecisionTooHigh
)

// =============================================================================
// Legacy Currency Support (backward compatibility with money.go API)
// =============================================================================

// Currency represents an ISO 4217 currency code.
// This maintains backward compatibility with the previous money.Money implementation.
type Currency = money.Currency

// Currency constants for backward compatibility.
const (
	CurrencyGBP = money.CurrencyGBP
	CurrencyUSD = money.CurrencyUSD
	CurrencyEUR = money.CurrencyEUR
	CurrencyJPY = money.CurrencyJPY
	CurrencyCHF = money.CurrencyCHF
	CurrencyCAD = money.CurrencyCAD
	CurrencyAUD = money.CurrencyAUD
)

// Legacy errors for backward compatibility.
var (
	// ErrCurrencyMismatch is returned when attempting arithmetic on different currencies.
	// Use ErrInstrumentMismatch for the new quantity API.
	ErrCurrencyMismatch = money.ErrCurrencyMismatch

	// ErrInvalidCurrency is returned when a currency code is not valid.
	ErrInvalidCurrency = money.ErrInvalidCurrency

	// ErrAmountOverflow is returned when converting an Amount to minor units would overflow int64.
	ErrAmountOverflow = sharedamount.ErrAmountOverflow
)

// DimensionCurrency is the canonical dimension name for currencies.
const DimensionCurrency = quantity.DimensionCurrency

// =============================================================================
// Instrument Factory Functions
// =============================================================================

// NewInstrument creates a validated Instrument instance.
func NewInstrument(code string, version uint32, dimension string, precision int) (Instrument, error) {
	return quantity.NewInstrument(code, version, dimension, precision)
}

// MustNewInstrument creates an Instrument, panicking on validation failure.
// Use only in tests or when parameters are known valid at compile time.
func MustNewInstrument(code string, version uint32, dimension string, precision int) Instrument {
	inst, err := quantity.NewInstrument(code, version, dimension, precision)
	if err != nil {
		panic(err)
	}
	return inst
}

// currencyToInstrument converts a Currency to an Instrument for the quantity API.
func currencyToInstrument(currency Currency) Instrument {
	precision := 2
	if currency == CurrencyJPY {
		precision = 0
	}
	// Safe to ignore error - these are known valid currencies
	inst, _ := quantity.NewInstrument(string(currency), 1, DimensionCurrency, precision)
	return inst
}

// =============================================================================
// Money Factory Functions (backward compatible with money.go API)
// =============================================================================

// NewMoney creates a new Money instance with the given decimal amount and currency.
// This maintains the same signature as the previous money.go implementation.
func NewMoney(amount decimal.Decimal, currency Currency) (Money, error) {
	if !currency.IsValid() {
		return Money{}, ErrInvalidCurrency
	}
	inst := currencyToInstrument(currency)
	return quantity.NewMoney(amount, inst), nil
}

// MustNewMoney creates a Money instance, panicking on invalid currency.
// Use only in tests or when currency is known valid.
func MustNewMoney(amount decimal.Decimal, currency Currency) Money {
	m, err := NewMoney(amount, currency)
	if err != nil {
		panic(err)
	}
	return m
}

// Zero returns a zero Money value for the given currency.
func Zero(currency Currency) (Money, error) {
	if !currency.IsValid() {
		return Money{}, ErrInvalidCurrency
	}
	inst := currencyToInstrument(currency)
	return quantity.ZeroMoney(inst), nil
}

// NewMoneyFromMinorUnits creates Money from minor units (cents, pence, etc.)
// and a currency string. This provides compatibility with services using
// the minor units API (current-account, payment-order).
//
// Example: NewMoneyFromMinorUnits("GBP", 10000) creates 100.00 GBP
func NewMoneyFromMinorUnits(currencyCode string, minorUnits int64) (Money, error) {
	cur, err := money.ParseCurrency(currencyCode)
	if err != nil {
		return Money{}, err
	}

	decimalPlaces := cur.DecimalPlaces()
	amount := decimal.NewFromInt(minorUnits).Shift(-decimalPlaces)
	inst := currencyToInstrument(cur)

	return quantity.NewMoney(amount, inst), nil
}

// =============================================================================
// Asset Factory Functions
// =============================================================================

// NewAsset creates a new Asset quantity with the given amount and instrument.
func NewAsset(amount decimal.Decimal, instrument Instrument) Asset {
	return quantity.NewAsset(amount, instrument)
}

// NewAssetFromString creates a new Asset by parsing the amount string.
func NewAssetFromString(amount string, instrument Instrument) (Asset, error) {
	return quantity.NewAssetFromString(amount, instrument)
}

// NewAssetFromInt creates a new Asset from an int64 amount.
func NewAssetFromInt(amount int64, instrument Instrument) Asset {
	return quantity.NewAssetFromInt(amount, instrument)
}

// ZeroAsset creates a zero-valued Asset for the given instrument.
func ZeroAsset(instrument Instrument) Asset {
	return quantity.ZeroAsset(instrument)
}

// =============================================================================
// Amount Factory Functions (dimension-agnostic cross-service type)
// =============================================================================

// NewAmount creates an Amount from an existing instrument and a minor-unit amount.
// Amount supports any dimension (CURRENCY, ENERGY, CARBON, COMPUTE, etc.).
func NewAmount(inst Instrument, amountMinorUnits int64) Amount {
	return sharedamount.New(inst, amountMinorUnits)
}

// NewAmountFromDecimal creates an Amount from an existing instrument and a decimal major-unit amount.
func NewAmountFromDecimal(inst Instrument, majorUnits decimal.Decimal) Amount {
	return sharedamount.NewFromDecimal(inst, majorUnits)
}

// NewAmountFromInstrument creates an Amount from persisted instrument_code, dimension, precision,
// and a minor-unit amount. For CURRENCY dimension, precision is resolved from the currency registry.
func NewAmountFromInstrument(code, dimension string, precision int, amountMinorUnits int64) (Amount, error) {
	return sharedamount.NewFromInstrument(code, dimension, precision, amountMinorUnits)
}

// ZeroAmount creates a zero Amount for the given instrument.
func ZeroAmount(inst Instrument) Amount {
	return sharedamount.Zero(inst)
}

// =============================================================================
// Generic Quantity Factory Functions
// =============================================================================

// NewQty creates a new Qty with the given amount and instrument.
func NewQty[D quantity.Dimension](amount decimal.Decimal, instrument Instrument) Qty[D] {
	return quantity.New[D](amount, instrument)
}

// NewQtyFromString creates a new Qty by parsing the amount string.
func NewQtyFromString[D quantity.Dimension](amount string, instrument Instrument) (Qty[D], error) {
	return quantity.NewFromString[D](amount, instrument)
}

// NewQtyFromInt creates a new Qty from an int64 amount.
func NewQtyFromInt[D quantity.Dimension](amount int64, instrument Instrument) Qty[D] {
	return quantity.NewFromInt[D](amount, instrument)
}

// ZeroQty creates a zero-valued Qty for the given instrument.
func ZeroQty[D quantity.Dimension](instrument Instrument) Qty[D] {
	return quantity.Zero[D](instrument)
}

// =============================================================================
// Utility Functions
// =============================================================================

// ParseCurrency converts a string to a Currency type with validation.
func ParseCurrency(s string) (Currency, error) {
	return money.ParseCurrency(s)
}

// NewMoneyFromInstrumentCode creates a Money value from any instrument code (currency or non-currency).
// For valid ISO 4217 currencies (GBP, USD, etc.), it uses the standard currency path with correct precision.
// For non-currency instrument codes (KWH, GPU_HOUR, etc.), it creates a Money value using the code directly,
// bypassing currency validation. This enables position-keeping to track non-fiat instruments while
// reusing the same Money type for persistence and domain logic.
func NewMoneyFromInstrumentCode(amount decimal.Decimal, code string) (Money, error) {
	if code == "" {
		return Money{}, ErrEmptyCode
	}

	// Try currency path first (preserves correct precision for fiat)
	cur := Currency(code)
	if cur.IsValid() {
		return NewMoney(amount, cur)
	}

	// Non-currency instrument: use precision 2 to match the persistence layer's
	// decimalToCents/centsToDecimal which assumes 2 decimal places for all instruments.
	// Use ENERGY as the default non-currency dimension; this is a pragmatic choice since
	// the dimension is not stored in the transaction_log_entry table and only the code matters
	// for persistence round-trips.
	inst, err := quantity.NewInstrument(code, 1, "ENERGY", 2)
	if err != nil {
		return Money{}, err
	}
	return quantity.NewMoney(amount, inst), nil
}

// =============================================================================
// Money Accessor Functions (backward compatibility helpers)
// =============================================================================

// MoneyCurrency extracts the Currency from a Money quantity.
// Returns the instrument code as a Currency type.
// This provides backward compatibility for code using the old money.Currency() method.
func MoneyCurrency(m Money) Currency {
	return Currency(m.Instrument.Code)
}

// MoneyToMinorUnits converts a Money amount to minor units (cents, pence, sen, etc.).
// This is currency-aware: JPY has 0 decimal places, others have 2.
// Uses banker's rounding (round-half-to-even) for fractional minor units.
//
// Returns an error if the result would overflow int64.
func MoneyToMinorUnits(m Money) (int64, error) {
	decimalPlaces := int32(m.Instrument.Precision)
	shifted := m.Amount.Shift(decimalPlaces)

	// Round to nearest integer using banker's rounding
	rounded := shifted.RoundBank(0)

	// Check for overflow before converting to int64
	maxInt64 := decimal.NewFromInt(9223372036854775807)  // math.MaxInt64
	minInt64 := decimal.NewFromInt(-9223372036854775808) // math.MinInt64
	if rounded.GreaterThan(maxInt64) || rounded.LessThan(minInt64) {
		return 0, money.ErrOverflow
	}

	return rounded.IntPart(), nil
}

// MoneyToMinorUnitsUnchecked converts a Money amount to minor units without overflow checking.
// Uses banker's rounding (round-half-to-even) for fractional minor units.
// Use only when you're certain the value won't overflow (e.g., validated input).
func MoneyToMinorUnitsUnchecked(m Money) int64 {
	decimalPlaces := int32(m.Instrument.Precision)
	return m.Amount.Shift(decimalPlaces).RoundBank(0).IntPart()
}
