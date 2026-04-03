// Package adapters provides protocol translation between domain and proto types.
package adapters

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// ErrUnspecifiedBalanceType is returned when attempting to convert BALANCE_TYPE_UNSPECIFIED
// to a domain BalanceType. The UNSPECIFIED value is only valid in proto as a default/absent value.
var ErrUnspecifiedBalanceType = errors.New("BALANCE_TYPE_UNSPECIFIED cannot be converted to domain BalanceType")

// ErrNilMoneyAmount is returned when attempting to convert a nil MoneyAmount to domain Money.
var ErrNilMoneyAmount = errors.New("MoneyAmount is nil")

// ErrNilGoogleMoney is returned when MoneyAmount contains a nil google.type.Money.
var ErrNilGoogleMoney = errors.New("MoneyAmount.Amount (google.type.Money) is nil")

// ErrInvalidCurrency is returned when the currency code is empty or invalid.
var ErrInvalidCurrency = errors.New("invalid or empty currency code")

// ErrNilInstrumentResolver is returned when a nil InstrumentResolver is provided.
var ErrNilInstrumentResolver = errors.New("instrument resolver is required")

// ErrUnknownProtoBalanceType is returned when the proto BalanceType value is not recognized.
var ErrUnknownProtoBalanceType = errors.New("unknown proto BalanceType value")

// ToProtoBalanceType converts a domain BalanceType to its protobuf representation.
// Maps all 7 domain balance types to the corresponding proto enum values.
// Unknown or invalid domain balance types are mapped to BALANCE_TYPE_UNSPECIFIED.
func ToProtoBalanceType(bt domain.BalanceType) positionkeepingv1.BalanceType {
	switch bt {
	case domain.BalanceTypeOpening:
		return positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING
	case domain.BalanceTypeClosing:
		return positionkeepingv1.BalanceType_BALANCE_TYPE_CLOSING
	case domain.BalanceTypeCurrent:
		return positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT
	case domain.BalanceTypeAvailable:
		return positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE
	case domain.BalanceTypeLedger:
		return positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER
	case domain.BalanceTypeReserve:
		return positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE
	case domain.BalanceTypeFree:
		return positionkeepingv1.BalanceType_BALANCE_TYPE_FREE
	case domain.BalanceTypeUnknown:
		return positionkeepingv1.BalanceType_BALANCE_TYPE_UNSPECIFIED
	}
	// Unreachable for known domain types, but handles any future additions.
	return positionkeepingv1.BalanceType_BALANCE_TYPE_UNSPECIFIED
}

// ToDomainBalanceType converts a protobuf BalanceType to its domain representation.
// Returns an error if the proto value is BALANCE_TYPE_UNSPECIFIED, as this is not
// a valid domain balance type and typically indicates a missing or invalid value.
func ToDomainBalanceType(bt positionkeepingv1.BalanceType) (domain.BalanceType, error) {
	switch bt {
	case positionkeepingv1.BalanceType_BALANCE_TYPE_OPENING:
		return domain.BalanceTypeOpening, nil
	case positionkeepingv1.BalanceType_BALANCE_TYPE_CLOSING:
		return domain.BalanceTypeClosing, nil
	case positionkeepingv1.BalanceType_BALANCE_TYPE_CURRENT:
		return domain.BalanceTypeCurrent, nil
	case positionkeepingv1.BalanceType_BALANCE_TYPE_AVAILABLE:
		return domain.BalanceTypeAvailable, nil
	case positionkeepingv1.BalanceType_BALANCE_TYPE_LEDGER:
		return domain.BalanceTypeLedger, nil
	case positionkeepingv1.BalanceType_BALANCE_TYPE_RESERVE:
		return domain.BalanceTypeReserve, nil
	case positionkeepingv1.BalanceType_BALANCE_TYPE_FREE:
		return domain.BalanceTypeFree, nil
	case positionkeepingv1.BalanceType_BALANCE_TYPE_UNSPECIFIED:
		return domain.BalanceTypeUnknown, ErrUnspecifiedBalanceType
	default:
		return domain.BalanceTypeUnknown, fmt.Errorf("%w: %d", ErrUnknownProtoBalanceType, bt)
	}
}

// ToProtoMoneyAmount converts a domain Money to its protobuf MoneyAmount representation.
// The conversion splits the decimal amount into units and nanos components as required
// by google.type.Money, clamping nanos to the valid int32 range.
func ToProtoMoneyAmount(domainMoney domain.Money) *commonv1.MoneyAmount {
	// Convert decimal to units and nanos
	// For example: 123.456789 GBP -> units: 123, nanos: 456789000
	amount := domainMoney.Amount
	units := amount.IntPart()
	fraction := amount.Sub(amount.Truncate(0))
	nanos := fraction.Mul(decimal.NewFromInt(1000000000)).IntPart()

	// Clamp nanos to int32 range to prevent overflow
	if nanos > 999999999 {
		nanos = 999999999
	} else if nanos < -999999999 {
		nanos = -999999999
	}

	return &commonv1.MoneyAmount{
		Amount: &money.Money{
			CurrencyCode: string(domain.MoneyCurrency(domainMoney)),
			Units:        units,
			Nanos:        int32(nanos), // #nosec G115 -- Safely clamped to int32 range above
		},
	}
}

// ToDomainMoney converts a protobuf MoneyAmount to its domain Money representation.
// Resolves instrument properties from Reference Data via the provided resolver instead of
// using hardcoded currency parsing, enabling support for any registered instrument code.
// Returns an error if the MoneyAmount or its inner google.type.Money is nil,
// or if the instrument code cannot be resolved.
func ToDomainMoney(ctx context.Context, resolver refdata.InstrumentResolver, protoMoney *commonv1.MoneyAmount) (domain.Money, error) {
	if resolver == nil {
		return domain.Money{}, ErrNilInstrumentResolver
	}
	if protoMoney == nil {
		return domain.Money{}, ErrNilMoneyAmount
	}
	if protoMoney.Amount == nil {
		return domain.Money{}, ErrNilGoogleMoney
	}

	googleMoney := protoMoney.Amount

	if googleMoney.CurrencyCode == "" {
		return domain.Money{}, ErrInvalidCurrency
	}

	// Resolve from Reference Data to get correct precision and dimension
	props, err := resolver.Resolve(ctx, googleMoney.CurrencyCode)
	if err != nil {
		return domain.Money{}, fmt.Errorf("resolving instrument %q: %w", googleMoney.CurrencyCode, err)
	}

	// Convert units and nanos back to decimal
	// units: 123, nanos: 456789000 -> 123.456789
	unitsDecimal := decimal.NewFromInt(googleMoney.Units)
	nanosDecimal := decimal.NewFromInt(int64(googleMoney.Nanos)).Div(decimal.NewFromInt(1000000000))
	amount := unitsDecimal.Add(nanosDecimal)

	inst, err := quantity.NewInstrument(googleMoney.CurrencyCode, 1, props.Dimension, props.Precision)
	if err != nil {
		return domain.Money{}, fmt.Errorf("creating instrument for %q: %w", googleMoney.CurrencyCode, err)
	}
	return quantity.NewMoney(amount, inst), nil
}

// ErrNilInstrumentAmount is returned when attempting to convert a nil InstrumentAmount.
var ErrNilInstrumentAmount = errors.New("InstrumentAmount is nil")

// ErrInvalidAmount is returned when the amount string cannot be parsed as a decimal.
var ErrInvalidAmount = errors.New("invalid amount string")

// ErrInvalidInstrumentCode is returned when the instrument code is empty or invalid.
var ErrInvalidInstrumentCode = errors.New("invalid or empty instrument code")

// ErrInvalidVersion is returned when the instrument version is negative.
var ErrInvalidVersion = errors.New("invalid instrument version")

// ToProtoInstrumentAmount converts a domain Money to its protobuf InstrumentAmount representation.
// This supports the Universal Asset System for multi-asset position tracking by representing
// monetary quantities as InstrumentAmount with the currency code as instrument_code.
func ToProtoInstrumentAmount(domainMoney domain.Money) *quantityv1.InstrumentAmount {
	// Use the instrument precision for fixed-point string representation
	precision := int32(domainMoney.Instrument.Precision)

	return &quantityv1.InstrumentAmount{
		Amount:         domainMoney.Amount.StringFixed(precision),
		InstrumentCode: domainMoney.Instrument.Code,
		Version:        int32(domainMoney.Instrument.Version),
	}
}

// ToProtoInstrumentAmountFromAsset converts a domain Asset to its protobuf InstrumentAmount representation.
// This supports non-monetary quantities like energy (KWH), compute (GPU_HOUR), and carbon credits.
func ToProtoInstrumentAmountFromAsset(domainAsset domain.Asset) *quantityv1.InstrumentAmount {
	// Use the instrument precision for fixed-point string representation
	precision := int32(domainAsset.Instrument.Precision)

	return &quantityv1.InstrumentAmount{
		Amount:         domainAsset.Amount.StringFixed(precision),
		InstrumentCode: domainAsset.Instrument.Code,
		Version:        int32(domainAsset.Instrument.Version),
	}
}

// ToDomainMoneyFromInstrumentAmount converts a protobuf InstrumentAmount to its domain Money representation.
// Resolves instrument properties from Reference Data via the provided resolver, supporting any
// registered instrument code (currencies, energy units, compute hours, carbon credits, etc.).
// Returns an error if the amount is invalid or the instrument code cannot be resolved.
func ToDomainMoneyFromInstrumentAmount(ctx context.Context, resolver refdata.InstrumentResolver, protoAmount *quantityv1.InstrumentAmount) (domain.Money, error) {
	if resolver == nil {
		return domain.Money{}, ErrNilInstrumentResolver
	}
	if protoAmount == nil {
		return domain.Money{}, ErrNilInstrumentAmount
	}

	if protoAmount.InstrumentCode == "" {
		return domain.Money{}, ErrInvalidInstrumentCode
	}

	// Reject negative version values
	if protoAmount.Version < 0 {
		return domain.Money{}, fmt.Errorf("%w: negative version %d", ErrInvalidVersion, protoAmount.Version)
	}

	amount, err := decimal.NewFromString(protoAmount.Amount)
	if err != nil {
		return domain.Money{}, fmt.Errorf("%w: %s", ErrInvalidAmount, protoAmount.Amount)
	}

	// Resolve from Reference Data to get correct precision and dimension
	props, err := resolver.Resolve(ctx, protoAmount.InstrumentCode)
	if err != nil {
		return domain.Money{}, fmt.Errorf("resolving instrument %q: %w", protoAmount.InstrumentCode, err)
	}

	version := uint32(protoAmount.Version)
	if version == 0 {
		version = 1
	}

	inst, err := quantity.NewInstrument(protoAmount.InstrumentCode, version, props.Dimension, props.Precision)
	if err != nil {
		return domain.Money{}, fmt.Errorf("creating instrument for %q: %w", protoAmount.InstrumentCode, err)
	}
	return quantity.NewMoney(amount, inst), nil
}

// ToDomainAssetFromInstrumentAmount converts a protobuf InstrumentAmount to its domain Asset representation.
// This function is for non-monetary quantities like energy (KWH), compute (GPU_HOUR), and carbon credits.
// Resolves instrument dimension and precision from Reference Data via the provided resolver.
// Returns an error if the amount is invalid, the instrument code is empty, or the instrument is unknown.
func ToDomainAssetFromInstrumentAmount(ctx context.Context, resolver refdata.InstrumentResolver, protoAmount *quantityv1.InstrumentAmount) (domain.Asset, error) {
	if protoAmount == nil {
		return domain.Asset{}, ErrNilInstrumentAmount
	}

	if protoAmount.InstrumentCode == "" {
		return domain.Asset{}, ErrInvalidInstrumentCode
	}

	amount, err := decimal.NewFromString(protoAmount.Amount)
	if err != nil {
		return domain.Asset{}, fmt.Errorf("%w: %s", ErrInvalidAmount, protoAmount.Amount)
	}

	// Resolve dimension and precision from Reference Data
	props, err := resolver.Resolve(ctx, protoAmount.InstrumentCode)
	if err != nil {
		return domain.Asset{}, fmt.Errorf("failed to resolve instrument %q: %w", protoAmount.InstrumentCode, err)
	}

	// Reject negative version values (would wrap to large uint32)
	if protoAmount.Version < 0 {
		return domain.Asset{}, fmt.Errorf("%w: negative version %d", ErrInvalidVersion, protoAmount.Version)
	}

	version := uint32(protoAmount.Version)
	if version == 0 {
		version = 1 // Default version
	}

	instrument, err := domain.NewInstrument(protoAmount.InstrumentCode, version, props.Dimension, props.Precision)
	if err != nil {
		return domain.Asset{}, fmt.Errorf("%w: %w", ErrInvalidInstrumentCode, err)
	}

	return domain.NewAsset(amount, instrument), nil
}
