// Package mappers provides conversion utilities between domain types and protobuf types.
package mappers

import (
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	"github.com/meridianhub/meridian/shared/domain/money"
)

// DomainCurrencyToProto converts a domain currency to its protobuf Currency enum equivalent.
// Returns Currency_CURRENCY_UNSPECIFIED for unsupported currencies.
//
// Deprecated: Use string instrument codes directly instead of the Currency enum.
// Pass the currency code string (e.g., "GBP") directly to proto fields that accept instrument_code.
func DomainCurrencyToProto(currency money.Currency) commonpb.Currency {
	return CurrencyCodeToProto(string(currency))
}

// CurrencyCodeToProto converts a currency code string to its protobuf Currency enum equivalent.
// This function supports the new quantity.Money type which uses string currency codes.
// Returns Currency_CURRENCY_UNSPECIFIED for unsupported currencies.
//
// Deprecated: Use string instrument codes directly instead of the Currency enum.
// Pass the currency code string (e.g., "GBP") directly to proto fields that accept instrument_code.
func CurrencyCodeToProto(currencyCode string) commonpb.Currency {
	switch currencyCode {
	case "GBP":
		return commonpb.Currency_CURRENCY_GBP
	case "USD":
		return commonpb.Currency_CURRENCY_USD
	case "EUR":
		return commonpb.Currency_CURRENCY_EUR
	case "JPY":
		return commonpb.Currency_CURRENCY_JPY
	case "CHF":
		return commonpb.Currency_CURRENCY_CHF
	case "CAD":
		return commonpb.Currency_CURRENCY_CAD
	case "AUD":
		return commonpb.Currency_CURRENCY_AUD
	default:
		return commonpb.Currency_CURRENCY_UNSPECIFIED
	}
}
