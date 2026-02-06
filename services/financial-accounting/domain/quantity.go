// Package domain re-exports the quantity types for financial-accounting service.
//
// This file provides access to the Universal Asset System's generic Qty[D] type,
// enabling the ledger to handle both monetary quantities (USD, EUR) and commodity
// quantities (kWh, carbon credits) with compile-time type safety.
package domain

import (
	"github.com/meridianhub/meridian/shared/platform/quantity"
	"github.com/shopspring/decimal"
)

// Re-export the generic Qty type and type aliases.
type (
	// Qty is the generic quantity type with compile-time dimension safety.
	Qty[D quantity.Dimension] = quantity.Qty[D]

	// Money represents a monetary quantity (currencies like USD, EUR, GBP).
	Money = quantity.Money

	// Asset represents a commodity quantity (energy, compute, carbon credits).
	Asset = quantity.Asset

	// Instrument identifies the asset type (currency code, version, dimension, precision).
	Instrument = quantity.Instrument
)

// Re-export dimension types for compile-time type safety.
type (
	// Monetary is the phantom type for monetary dimensions.
	Monetary = quantity.Monetary

	// Commodity is the phantom type for commodity dimensions.
	Commodity = quantity.Commodity

	// Dimension is the interface for dimension types.
	Dimension = quantity.Dimension
)

// Re-export sentinel errors.
var (
	// ErrInstrumentMismatch is returned when attempting arithmetic operations
	// on quantities with different instruments.
	ErrInstrumentMismatch = quantity.ErrInstrumentMismatch

	// ErrDimensionMismatch is returned when the instrument's dimension does not match
	// the expected type parameter dimension (Monetary vs Commodity).
	ErrDimensionMismatch = quantity.ErrDimensionMismatch

	// ErrDivisionByZero is returned when attempting to divide by zero.
	ErrDivisionByZero = quantity.ErrDivisionByZero

	// ErrInvalidDecimalString is returned when a string cannot be parsed as a decimal.
	ErrInvalidDecimalString = quantity.ErrInvalidDecimalString

	// ErrEmptyCode is returned when an instrument code is empty.
	ErrEmptyCode = quantity.ErrEmptyCode

	// ErrInvalidCodeFormat is returned when an instrument code doesn't match the required pattern.
	ErrInvalidCodeFormat = quantity.ErrInvalidCodeFormat

	// ErrInvalidDimension is returned when a dimension string is not recognized.
	ErrInvalidDimension = quantity.ErrInvalidDimension
)

// Re-export dimension constants.
const (
	// DimensionCurrency is the canonical name for the currency dimension.
	DimensionCurrency = quantity.DimensionCurrency
)

// NewMoney creates a new Money quantity with the given amount and instrument.
func NewMoney(amount decimal.Decimal, instrument Instrument) Money {
	return quantity.NewMoney(amount, instrument)
}

// NewMoneyFromString creates a new Money quantity by parsing the amount string.
func NewMoneyFromString(amount string, instrument Instrument) (Money, error) {
	return quantity.NewMoneyFromString(amount, instrument)
}

// NewMoneyFromInt creates a new Money quantity from an int64 amount.
func NewMoneyFromInt(amount int64, instrument Instrument) Money {
	return quantity.NewMoneyFromInt(amount, instrument)
}

// ZeroMoney creates a zero-valued Money quantity for the given instrument.
func ZeroMoney(instrument Instrument) Money {
	return quantity.ZeroMoney(instrument)
}

// NewAsset creates a new Asset quantity with the given amount and instrument.
func NewAsset(amount decimal.Decimal, instrument Instrument) Asset {
	return quantity.NewAsset(amount, instrument)
}

// NewAssetFromString creates a new Asset quantity by parsing the amount string.
func NewAssetFromString(amount string, instrument Instrument) (Asset, error) {
	return quantity.NewAssetFromString(amount, instrument)
}

// NewAssetFromInt creates a new Asset quantity from an int64 amount.
func NewAssetFromInt(amount int64, instrument Instrument) Asset {
	return quantity.NewAssetFromInt(amount, instrument)
}

// ZeroAsset creates a zero-valued Asset quantity for the given instrument.
func ZeroAsset(instrument Instrument) Asset {
	return quantity.ZeroAsset(instrument)
}

// NewInstrument creates a validated Instrument instance.
func NewInstrument(code string, version uint32, dimension string, precision int) (Instrument, error) {
	return quantity.NewInstrument(code, version, dimension, precision)
}

// NewQuantityValidated creates a new Qty with validation that the instrument's dimension
// matches the type parameter D.
func NewQuantityValidated[D Dimension](amount decimal.Decimal, inst Instrument) (Qty[D], error) {
	return quantity.NewQuantityValidated[D](amount, inst)
}
