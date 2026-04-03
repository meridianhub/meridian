package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildPostingAmount(t *testing.T) {
	tests := []struct {
		name            string
		instrumentCode  string
		amountMinorUnit int64
		precision       int
		wantAmount      string
		wantCode        string
	}{
		{
			name:            "standard currency GBP (precision 2)",
			instrumentCode:  "GBP",
			amountMinorUnit: 10050,
			precision:       2,
			wantAmount:      "100.50",
			wantCode:        "GBP",
		},
		{
			name:            "zero amount",
			instrumentCode:  "USD",
			amountMinorUnit: 0,
			precision:       2,
			wantAmount:      "0.00",
			wantCode:        "USD",
		},
		{
			name:            "negative amount",
			instrumentCode:  "GBP",
			amountMinorUnit: -150,
			precision:       2,
			wantAmount:      "-1.50",
			wantCode:        "GBP",
		},
		{
			name:            "zero-precision currency like JPY",
			instrumentCode:  "JPY",
			amountMinorUnit: 1000,
			precision:       0,
			wantAmount:      "1000",
			wantCode:        "JPY",
		},
		{
			name:            "high precision instrument (precision 3, KWD)",
			instrumentCode:  "KWD",
			amountMinorUnit: 12345,
			precision:       3,
			wantAmount:      "12.345",
			wantCode:        "KWD",
		},
		{
			name:            "sub-unit only amount",
			instrumentCode:  "GBP",
			amountMinorUnit: 5,
			precision:       2,
			wantAmount:      "0.05",
			wantCode:        "GBP",
		},
		{
			name:            "negative sub-unit only amount",
			instrumentCode:  "EUR",
			amountMinorUnit: -5,
			precision:       2,
			wantAmount:      "-0.05",
			wantCode:        "EUR",
		},
		{
			name:            "energy instrument with precision 6",
			instrumentCode:  "KWH",
			amountMinorUnit: 1234567890,
			precision:       6,
			wantAmount:      "1234.567890",
			wantCode:        "KWH",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPostingAmount(tt.instrumentCode, tt.amountMinorUnit, tt.precision)

			assert.Equal(t, tt.wantAmount, result.Amount)
			assert.Equal(t, tt.wantCode, result.InstrumentCode)
			assert.Equal(t, int32(1), result.Version)
		})
	}
}
