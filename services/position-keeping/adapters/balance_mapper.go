// Package adapters provides protocol translation between domain and proto types.
package adapters

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
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
// Returns an error if the MoneyAmount or its inner google.type.Money is nil,
// or if the currency code is invalid.
func ToDomainMoney(protoMoney *commonv1.MoneyAmount) (domain.Money, error) {
	if protoMoney == nil {
		return domain.Money{}, ErrNilMoneyAmount
	}
	if protoMoney.Amount == nil {
		return domain.Money{}, ErrNilGoogleMoney
	}

	googleMoney := protoMoney.Amount

	// Validate and parse currency
	if googleMoney.CurrencyCode == "" {
		return domain.Money{}, ErrInvalidCurrency
	}

	currency, err := domain.ParseCurrency(googleMoney.CurrencyCode)
	if err != nil {
		return domain.Money{}, fmt.Errorf("%w: %s", ErrInvalidCurrency, googleMoney.CurrencyCode)
	}

	// Convert units and nanos back to decimal
	// units: 123, nanos: 456789000 -> 123.456789
	unitsDecimal := decimal.NewFromInt(googleMoney.Units)
	nanosDecimal := decimal.NewFromInt(int64(googleMoney.Nanos)).Div(decimal.NewFromInt(1000000000))
	amount := unitsDecimal.Add(nanosDecimal)

	return domain.MustNewMoney(amount, currency), nil
}
