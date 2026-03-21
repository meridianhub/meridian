package mappers

import (
	"testing"

	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	"github.com/meridianhub/meridian/shared/domain/money"
	"github.com/stretchr/testify/assert"
)

func TestCurrencyCodeToProto(t *testing.T) {
	tests := []struct {
		code string
		want commonpb.Currency
	}{
		{"GBP", commonpb.Currency_CURRENCY_GBP},
		{"USD", commonpb.Currency_CURRENCY_USD},
		{"EUR", commonpb.Currency_CURRENCY_EUR},
		{"JPY", commonpb.Currency_CURRENCY_JPY},
		{"CHF", commonpb.Currency_CURRENCY_CHF},
		{"CAD", commonpb.Currency_CURRENCY_CAD},
		{"AUD", commonpb.Currency_CURRENCY_AUD},
		{"UNKNOWN", commonpb.Currency_CURRENCY_UNSPECIFIED},
		{"", commonpb.Currency_CURRENCY_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			got := CurrencyCodeToProto(tt.code)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDomainCurrencyToProto(t *testing.T) {
	tests := []struct {
		currency money.Currency
		want     commonpb.Currency
	}{
		{money.CurrencyGBP, commonpb.Currency_CURRENCY_GBP},
		{money.CurrencyUSD, commonpb.Currency_CURRENCY_USD},
		{money.CurrencyEUR, commonpb.Currency_CURRENCY_EUR},
	}

	for _, tt := range tests {
		t.Run(string(tt.currency), func(t *testing.T) {
			got := DomainCurrencyToProto(tt.currency)
			assert.Equal(t, tt.want, got)
		})
	}
}
