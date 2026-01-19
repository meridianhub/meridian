package ecb_test

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/adapters/external/ecb"
)

// Sample ECB CSV data matching the real format.
const validECBCSV = `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2024-01-15,1.0876
EXR.D.GBP.EUR.SP00.A,D,GBP,EUR,SP00,A,2024-01-15,0.8612
EXR.D.JPY.EUR.SP00.A,D,JPY,EUR,SP00,A,2024-01-15,160.89
EXR.D.CHF.EUR.SP00.A,D,CHF,EUR,SP00,A,2024-01-15,0.9412`

const multiDayECBCSV = `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2024-01-15,1.0876
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2024-01-16,1.0901
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2024-01-17,1.0888`

func TestParser_ParseCSV_ValidData(t *testing.T) {
	parser := ecb.NewParser()

	rates, err := parser.ParseCSV(strings.NewReader(validECBCSV))
	require.NoError(t, err)
	require.Len(t, rates, 4)

	// Verify first rate (USD)
	usd := rates[0]
	assert.Equal(t, "USD", usd.BaseCurrency)
	assert.Equal(t, "EUR", usd.QuoteCurrency)
	assert.True(t, decimal.NewFromFloat(1.0876).Equal(usd.Value))
	assert.Equal(t, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), usd.ObservedDate)
	assert.Equal(t, "D", usd.Frequency)
	assert.Equal(t, "SP00", usd.ExchangeRateType)
	assert.Equal(t, "A", usd.ExchangeRateSuffix)

	// Verify GBP rate
	gbp := rates[1]
	assert.Equal(t, "GBP", gbp.BaseCurrency)
	assert.True(t, decimal.NewFromFloat(0.8612).Equal(gbp.Value))

	// Verify JPY rate (larger value)
	jpy := rates[2]
	assert.Equal(t, "JPY", jpy.BaseCurrency)
	assert.True(t, decimal.NewFromFloat(160.89).Equal(jpy.Value))

	// Verify CHF rate
	chf := rates[3]
	assert.Equal(t, "CHF", chf.BaseCurrency)
	assert.True(t, decimal.NewFromFloat(0.9412).Equal(chf.Value))
}

func TestParser_ParseCSV_EmptyData(t *testing.T) {
	parser := ecb.NewParser()

	_, err := parser.ParseCSV(strings.NewReader(""))
	require.ErrorIs(t, err, ecb.ErrNoData)
}

func TestParser_ParseCSV_HeaderOnly(t *testing.T) {
	parser := ecb.NewParser()

	csv := `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE`
	_, err := parser.ParseCSV(strings.NewReader(csv))
	require.ErrorIs(t, err, ecb.ErrNoData)
}

func TestParser_ParseCSV_InvalidHeader(t *testing.T) {
	tests := []struct {
		name string
		csv  string
	}{
		{
			name: "wrong column names",
			csv:  "WRONG,COLUMNS,HERE\n1,2,3",
		},
		{
			name: "too few columns",
			csv:  "KEY,FREQ,CURRENCY\ndata,D,USD",
		},
		{
			name: "incorrect column order",
			csv:  "FREQ,KEY,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE\nD,key,USD,EUR,SP00,A,2024-01-15,1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := ecb.NewParser()
			_, err := parser.ParseCSV(strings.NewReader(tt.csv))
			require.ErrorIs(t, err, ecb.ErrInvalidCSVFormat)
		})
	}
}

func TestParser_ParseCSV_MalformedRows(t *testing.T) {
	parser := ecb.NewParser()

	// CSV with some malformed rows - parser should skip them and continue
	csv := `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2024-01-15,1.0876
EXR.D.GBP.EUR.SP00.A,D,GBP,EUR,SP00,A,INVALID-DATE,0.8612
EXR.D.JPY.EUR.SP00.A,D,JPY,EUR,SP00,A,2024-01-15,NOT_A_NUMBER
EXR.D.CHF.EUR.SP00.A,D,CHF,EUR,SP00,A,2024-01-15,0.9412`

	rates, err := parser.ParseCSV(strings.NewReader(csv))
	require.NoError(t, err)
	// Should only have valid rows (USD and CHF)
	require.Len(t, rates, 2)
	assert.Equal(t, "USD", rates[0].BaseCurrency)
	assert.Equal(t, "CHF", rates[1].BaseCurrency)
}

func TestParser_ParseCSV_EmptyRateValue(t *testing.T) {
	parser := ecb.NewParser()

	csv := `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2024-01-15,
EXR.D.GBP.EUR.SP00.A,D,GBP,EUR,SP00,A,2024-01-15,0.8612`

	rates, err := parser.ParseCSV(strings.NewReader(csv))
	require.NoError(t, err)
	// Should only have the valid GBP row
	require.Len(t, rates, 1)
	assert.Equal(t, "GBP", rates[0].BaseCurrency)
}

func TestParser_ParseCSV_HighPrecisionDecimals(t *testing.T) {
	parser := ecb.NewParser()

	csv := `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2024-01-15,1.08765432109876`

	rates, err := parser.ParseCSV(strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, rates, 1)

	expected, _ := decimal.NewFromString("1.08765432109876")
	assert.True(t, expected.Equal(rates[0].Value),
		"expected %s, got %s", expected.String(), rates[0].Value.String())
}

func TestParser_ParseCSV_NegativeRate(t *testing.T) {
	parser := ecb.NewParser()

	// While unusual for FX rates, the parser should handle negative numbers
	csv := `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2024-01-15,-0.5`

	rates, err := parser.ParseCSV(strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, rates, 1)

	expected := decimal.NewFromFloat(-0.5)
	assert.True(t, expected.Equal(rates[0].Value))
}

func TestParser_ParseCSV_TrimWhitespace(t *testing.T) {
	parser := ecb.NewParser()

	csv := `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE
EXR.D.USD.EUR.SP00.A, D , USD , EUR , SP00 , A , 2024-01-15 , 1.0876 `

	rates, err := parser.ParseCSV(strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, rates, 1)
	assert.Equal(t, "D", rates[0].Frequency)
	assert.Equal(t, "USD", rates[0].BaseCurrency)
	assert.Equal(t, "EUR", rates[0].QuoteCurrency)
}

func TestParser_ParseCSV_CaseInsensitiveHeaders(t *testing.T) {
	parser := ecb.NewParser()

	csv := `key,freq,currency,currency_denom,exr_type,exr_suffix,time_period,obs_value
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2024-01-15,1.0876`

	rates, err := parser.ParseCSV(strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, rates, 1)
}

func TestParser_ParseCSV_ReaderError(t *testing.T) {
	parser := ecb.NewParser()

	_, err := parser.ParseCSV(&errorReader{})
	require.Error(t, err)
}

// errorReader is a reader that always returns an error.
type errorReader struct{}

func (e *errorReader) Read(_ []byte) (n int, err error) {
	return 0, io.ErrUnexpectedEOF
}

func TestTransformToObservations_ValidRates(t *testing.T) {
	rates := []ecb.Rate{
		{
			BaseCurrency:       "USD",
			QuoteCurrency:      "EUR",
			Value:              decimal.NewFromFloat(1.0876),
			ObservedDate:       time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Frequency:          "D",
			ExchangeRateType:   "SP00",
			ExchangeRateSuffix: "A",
		},
		{
			BaseCurrency:       "GBP",
			QuoteCurrency:      "EUR",
			Value:              decimal.NewFromFloat(0.8612),
			ObservedDate:       time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Frequency:          "D",
			ExchangeRateType:   "SP00",
			ExchangeRateSuffix: "A",
		},
	}

	cfg := ecb.DefaultTransformConfig()
	requests := ecb.TransformToObservations(rates, cfg)

	require.Len(t, requests, 2)

	// Verify USD observation
	usdReq := requests[0]
	assert.Equal(t, "USD_EUR_FX", usdReq.DatasetCode)
	assert.Equal(t, int32(0), usdReq.DatasetVersion)
	assert.Equal(t, "1.0876", usdReq.Value)
	assert.Equal(t, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL, usdReq.Quality)
	assert.Equal(t, "ECB", usdReq.SourceCode)

	// Verify timestamps
	expectedTime := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, expectedTime, usdReq.ObservedAt.AsTime())
	assert.Equal(t, expectedTime, usdReq.ValidFrom.AsTime())

	// Verify attributes
	require.NotNil(t, usdReq.Attributes)
	attrMap := make(map[string]string)
	for _, attr := range usdReq.Attributes {
		attrMap[attr.Key] = attr.Value
	}
	assert.Equal(t, "ecb-feed-2024-01-15", attrMap["causation_id"])
	assert.Equal(t, "D", attrMap["frequency"])
	assert.Equal(t, "SP00", attrMap["exchange_rate_type"])
	assert.Equal(t, "A", attrMap["exchange_rate_suffix"])
	assert.Equal(t, "USD", attrMap["base_currency"])
	assert.Equal(t, "EUR", attrMap["quote_currency"])

	// Verify GBP observation
	gbpReq := requests[1]
	assert.Equal(t, "GBP_EUR_FX", gbpReq.DatasetCode)
	assert.Equal(t, "0.8612", gbpReq.Value)
}

func TestTransformToObservations_CustomConfig(t *testing.T) {
	rates := []ecb.Rate{
		{
			BaseCurrency:       "USD",
			QuoteCurrency:      "EUR",
			Value:              decimal.NewFromFloat(1.0876),
			ObservedDate:       time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Frequency:          "D",
			ExchangeRateType:   "SP00",
			ExchangeRateSuffix: "A",
		},
	}

	cfg := ecb.TransformConfig{
		SourceCode:          "ECB_DAILY",
		DatasetCodeTemplate: "FX_%s%s",
	}
	requests := ecb.TransformToObservations(rates, cfg)

	require.Len(t, requests, 1)
	assert.Equal(t, "FX_USDEUR", requests[0].DatasetCode)
	assert.Equal(t, "ECB_DAILY", requests[0].SourceCode)
}

func TestTransformToObservations_DefaultConfigFallback(t *testing.T) {
	rates := []ecb.Rate{
		{
			BaseCurrency:       "USD",
			QuoteCurrency:      "EUR",
			Value:              decimal.NewFromFloat(1.0876),
			ObservedDate:       time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			Frequency:          "D",
			ExchangeRateType:   "SP00",
			ExchangeRateSuffix: "A",
		},
	}

	// Empty config should use defaults
	requests := ecb.TransformToObservations(rates, ecb.TransformConfig{})

	require.Len(t, requests, 1)
	assert.Equal(t, "USD_EUR_FX", requests[0].DatasetCode)
	assert.Equal(t, "ECB", requests[0].SourceCode)
}

func TestTransformToObservations_EmptyRates(t *testing.T) {
	cfg := ecb.DefaultTransformConfig()
	requests := ecb.TransformToObservations([]ecb.Rate{}, cfg)

	require.Empty(t, requests)
}

func TestTransformToObservations_CausationIDFormat(t *testing.T) {
	rates := []ecb.Rate{
		{
			BaseCurrency:  "USD",
			QuoteCurrency: "EUR",
			Value:         decimal.NewFromFloat(1.0876),
			ObservedDate:  time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
		},
	}

	cfg := ecb.DefaultTransformConfig()
	requests := ecb.TransformToObservations(rates, cfg)

	require.Len(t, requests, 1)
	attrMap := make(map[string]string)
	for _, attr := range requests[0].Attributes {
		attrMap[attr.Key] = attr.Value
	}
	assert.Equal(t, "ecb-feed-2024-12-31", attrMap["causation_id"])
}

func TestTransformToObservations_PreservesDecimalPrecision(t *testing.T) {
	// Note: decimal.Decimal normalizes trailing zeros, so we use a value without them
	highPrecisionRate, _ := decimal.NewFromString("1.0876543210987654321")

	rates := []ecb.Rate{
		{
			BaseCurrency:  "USD",
			QuoteCurrency: "EUR",
			Value:         highPrecisionRate,
			ObservedDate:  time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		},
	}

	cfg := ecb.DefaultTransformConfig()
	requests := ecb.TransformToObservations(rates, cfg)

	require.Len(t, requests, 1)
	// Verify the value can be parsed back to the same decimal
	parsedValue, err := decimal.NewFromString(requests[0].Value)
	require.NoError(t, err)
	assert.True(t, highPrecisionRate.Equal(parsedValue),
		"expected %s, got %s", highPrecisionRate.String(), parsedValue.String())
}

func TestParser_ParseAndTransform(t *testing.T) {
	parser := ecb.NewParser()
	cfg := ecb.DefaultTransformConfig()

	requests, err := parser.ParseAndTransform(strings.NewReader(validECBCSV), cfg)
	require.NoError(t, err)
	require.Len(t, requests, 4)

	// Verify all currencies are present
	currencies := make(map[string]bool)
	for _, req := range requests {
		// Extract currency from dataset code (e.g., "USD_EUR_FX" -> "USD")
		parts := strings.Split(req.DatasetCode, "_")
		if len(parts) > 0 {
			currencies[parts[0]] = true
		}
	}
	assert.True(t, currencies["USD"])
	assert.True(t, currencies["GBP"])
	assert.True(t, currencies["JPY"])
	assert.True(t, currencies["CHF"])
}

func TestParser_ParseAndTransform_Error(t *testing.T) {
	parser := ecb.NewParser()
	cfg := ecb.DefaultTransformConfig()

	_, err := parser.ParseAndTransform(strings.NewReader(""), cfg)
	require.ErrorIs(t, err, ecb.ErrNoData)
}

func TestParser_ParseCSV_MultiDay(t *testing.T) {
	parser := ecb.NewParser()

	rates, err := parser.ParseCSV(strings.NewReader(multiDayECBCSV))
	require.NoError(t, err)
	require.Len(t, rates, 3)

	// Verify dates are different
	assert.Equal(t, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), rates[0].ObservedDate)
	assert.Equal(t, time.Date(2024, 1, 16, 0, 0, 0, 0, time.UTC), rates[1].ObservedDate)
	assert.Equal(t, time.Date(2024, 1, 17, 0, 0, 0, 0, time.UTC), rates[2].ObservedDate)

	// Verify rates are different
	assert.True(t, decimal.NewFromFloat(1.0876).Equal(rates[0].Value))
	assert.True(t, decimal.NewFromFloat(1.0901).Equal(rates[1].Value))
	assert.True(t, decimal.NewFromFloat(1.0888).Equal(rates[2].Value))
}

func TestDefaultTransformConfig(t *testing.T) {
	cfg := ecb.DefaultTransformConfig()

	assert.Equal(t, "ECB", cfg.SourceCode)
	assert.Equal(t, "%s_%s_FX", cfg.DatasetCodeTemplate)
}

func TestParser_ParseCSV_ExtraColumns(t *testing.T) {
	parser := ecb.NewParser()

	// CSV with extra columns should still work
	csv := `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE,EXTRA1,EXTRA2
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2024-01-15,1.0876,extra,data`

	rates, err := parser.ParseCSV(strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, rates, 1)
	assert.Equal(t, "USD", rates[0].BaseCurrency)
}

func TestTransformToObservations_QualityLevel(t *testing.T) {
	rates := []ecb.Rate{
		{
			BaseCurrency:  "USD",
			QuoteCurrency: "EUR",
			Value:         decimal.NewFromFloat(1.0876),
			ObservedDate:  time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		},
	}

	cfg := ecb.DefaultTransformConfig()
	requests := ecb.TransformToObservations(rates, cfg)

	require.Len(t, requests, 1)
	// ECB data should always be ACTUAL quality
	assert.Equal(t, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL, requests[0].Quality)
}

func TestParser_ParseCSV_AllMalformedRows(t *testing.T) {
	parser := ecb.NewParser()

	// All data rows are malformed
	csv := `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,INVALID-DATE,1.0876
EXR.D.GBP.EUR.SP00.A,D,GBP,EUR,SP00,A,2024-01-15,NOT_A_NUMBER`

	_, err := parser.ParseCSV(strings.NewReader(csv))
	require.ErrorIs(t, err, ecb.ErrNoData)
}

func TestParser_ParseCSV_TooFewColumnsInRow(t *testing.T) {
	parser := ecb.NewParser()

	csv := `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE
EXR.D.USD.EUR.SP00.A,D,USD,EUR
EXR.D.GBP.EUR.SP00.A,D,GBP,EUR,SP00,A,2024-01-15,0.8612`

	rates, err := parser.ParseCSV(strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, rates, 1)
	assert.Equal(t, "GBP", rates[0].BaseCurrency)
}
