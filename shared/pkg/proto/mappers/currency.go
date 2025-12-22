// Package mappers provides conversion utilities between domain types and protobuf types.
package mappers

import (
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	"github.com/meridianhub/meridian/shared/domain/money"
)

// DomainCurrencyToProto converts a domain currency to its protobuf Currency enum equivalent.
// Returns Currency_CURRENCY_UNSPECIFIED for unsupported currencies.
func DomainCurrencyToProto(currency money.Currency) commonpb.Currency {
	switch currency {
	case money.CurrencyGBP:
		return commonpb.Currency_CURRENCY_GBP
	case money.CurrencyUSD:
		return commonpb.Currency_CURRENCY_USD
	case money.CurrencyEUR:
		return commonpb.Currency_CURRENCY_EUR
	case money.CurrencyJPY:
		return commonpb.Currency_CURRENCY_JPY
	case money.CurrencyCHF:
		return commonpb.Currency_CURRENCY_CHF
	case money.CurrencyCAD:
		return commonpb.Currency_CURRENCY_CAD
	case money.CurrencyAUD:
		return commonpb.Currency_CURRENCY_AUD
	default:
		return commonpb.Currency_CURRENCY_UNSPECIFIED
	}
}
