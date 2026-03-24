package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPositionKeepingClient is a configurable test implementation of PositionKeepingClient.
type stubPositionKeepingClient struct {
	summary *PositionSummary
	err     error
}

func (s *stubPositionKeepingClient) GetPositionSummary(_ context.Context, _, _ string) (*PositionSummary, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.summary, nil
}

func TestPositionSummary_Fields(t *testing.T) {
	summary := &PositionSummary{
		InstrumentCode: "GBP",
		TotalDebits:    decimal.NewFromFloat(50000.00),
		TotalCredits:   decimal.NewFromFloat(50000.00),
	}

	assert.Equal(t, "GBP", summary.InstrumentCode)
	assert.True(t, decimal.NewFromFloat(50000.00).Equal(summary.TotalDebits))
	assert.True(t, decimal.NewFromFloat(50000.00).Equal(summary.TotalCredits))
}

func TestPositionSummary_Balanced(t *testing.T) {
	summary := &PositionSummary{
		InstrumentCode: "EUR",
		TotalDebits:    decimal.NewFromFloat(1234.56),
		TotalCredits:   decimal.NewFromFloat(1234.56),
	}

	imbalance := summary.TotalDebits.Sub(summary.TotalCredits)
	assert.True(t, imbalance.IsZero(), "balanced summary should have zero imbalance")
}

func TestPositionSummary_Imbalanced(t *testing.T) {
	summary := &PositionSummary{
		InstrumentCode: "GBP",
		TotalDebits:    decimal.NewFromFloat(1000.00),
		TotalCredits:   decimal.NewFromFloat(900.00),
	}

	imbalance := summary.TotalDebits.Sub(summary.TotalCredits)
	assert.True(t, decimal.NewFromFloat(100.00).Equal(imbalance))
	assert.False(t, imbalance.IsZero())
}

func TestPositionKeepingClient_Success(t *testing.T) {
	expected := &PositionSummary{
		InstrumentCode: "GBP",
		TotalDebits:    decimal.NewFromFloat(50000.00),
		TotalCredits:   decimal.NewFromFloat(50000.00),
	}

	var client PositionKeepingClient = &stubPositionKeepingClient{summary: expected}

	result, err := client.GetPositionSummary(context.Background(), "ACC-001", "GBP")

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "GBP", result.InstrumentCode)
	assert.True(t, expected.TotalDebits.Equal(result.TotalDebits))
	assert.True(t, expected.TotalCredits.Equal(result.TotalCredits))
}

func TestPositionKeepingClient_Error(t *testing.T) {
	var client PositionKeepingClient = &stubPositionKeepingClient{
		err: errors.New("connection refused"),
	}

	result, err := client.GetPositionSummary(context.Background(), "ACC-001", "GBP")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestPositionKeepingClient_ZeroBalances(t *testing.T) {
	expected := &PositionSummary{
		InstrumentCode: "GBP",
		TotalDebits:    decimal.Zero,
		TotalCredits:   decimal.Zero,
	}

	var client PositionKeepingClient = &stubPositionKeepingClient{summary: expected}

	result, err := client.GetPositionSummary(context.Background(), "ACC-001", "GBP")

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.TotalDebits.IsZero())
	assert.True(t, result.TotalCredits.IsZero())
}

func TestPositionKeepingClient_CrossAccountQuery(t *testing.T) {
	// Empty accountID for cross-account queries
	expected := &PositionSummary{
		InstrumentCode: "GBP",
		TotalDebits:    decimal.NewFromFloat(200000.00),
		TotalCredits:   decimal.NewFromFloat(200000.00),
	}

	var client PositionKeepingClient = &stubPositionKeepingClient{summary: expected}

	result, err := client.GetPositionSummary(context.Background(), "", "GBP")

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, expected.TotalDebits.Equal(result.TotalDebits))
}

func TestPositionKeepingClient_NonCurrencyInstruments(t *testing.T) {
	instruments := []struct {
		code    string
		debits  string
		credits string
	}{
		{"KWH", "1000000.000", "1000000.000"},
		{"TONNE_CO2E", "500.50", "500.50"},
		{"GPU_HOUR", "48.75", "48.75"},
	}

	for _, tc := range instruments {
		t.Run(tc.code, func(t *testing.T) {
			debits := decimal.RequireFromString(tc.debits)
			credits := decimal.RequireFromString(tc.credits)

			var client PositionKeepingClient = &stubPositionKeepingClient{
				summary: &PositionSummary{
					InstrumentCode: tc.code,
					TotalDebits:    debits,
					TotalCredits:   credits,
				},
			}

			result, err := client.GetPositionSummary(context.Background(), "ACC-001", tc.code)

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tc.code, result.InstrumentCode)
			assert.True(t, debits.Equal(result.TotalDebits))
			assert.True(t, credits.Equal(result.TotalCredits))
		})
	}
}

func TestPositionKeepingClient_HighPrecisionAmounts(t *testing.T) {
	debits := decimal.RequireFromString("123456789.123456789")
	credits := decimal.RequireFromString("123456789.123456789")

	var client PositionKeepingClient = &stubPositionKeepingClient{
		summary: &PositionSummary{
			InstrumentCode: "GBP",
			TotalDebits:    debits,
			TotalCredits:   credits,
		},
	}

	result, err := client.GetPositionSummary(context.Background(), "ACC-001", "GBP")

	require.NoError(t, err)
	assert.True(t, debits.Equal(result.TotalDebits), "high-precision amounts must be preserved")
	assert.True(t, credits.Equal(result.TotalCredits))
}
