package bucketing_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/bucketing"
)

func TestCalculateBucketID_MonetaryInstrument(t *testing.T) {
	// GBP with no attributes -> "currency_gbp"
	id := bucketing.CalculateBucketID("GBP", "CURRENCY", nil)
	assert.Equal(t, "currency_gbp", id)
}

func TestCalculateBucketID_MonetaryInstrumentUSD(t *testing.T) {
	id := bucketing.CalculateBucketID("USD", "CURRENCY", nil)
	assert.Equal(t, "currency_usd", id)
}

func TestCalculateBucketID_EnergyWithAttributes(t *testing.T) {
	// KWH with source=solar -> "energy_kwh_source=solar"
	attrs := map[string]string{"source": "solar"}
	id := bucketing.CalculateBucketID("KWH", "ENERGY", attrs)
	assert.Equal(t, "energy_kwh_source=solar", id)
}

func TestCalculateBucketID_ComputeWithAttributes(t *testing.T) {
	// GPU_HOUR with tier=premium -> "compute_gpu_hour_tier=premium"
	attrs := map[string]string{"tier": "premium"}
	id := bucketing.CalculateBucketID("GPU_HOUR", "COMPUTE", attrs)
	assert.Equal(t, "compute_gpu_hour_tier=premium", id)
}

func TestCalculateBucketID_MultipleAttributesSorted(t *testing.T) {
	// Multiple attributes must be sorted deterministically
	attrs := map[string]string{
		"region": "uk-south",
		"grade":  "A",
		"source": "wind",
	}
	id := bucketing.CalculateBucketID("KWH", "ENERGY", attrs)
	// Sorted: grade, region, source
	assert.Equal(t, "energy_kwh_grade=A_region=uk-south_source=wind", id)
}

func TestCalculateBucketID_AttributeOrderDoesNotMatter(t *testing.T) {
	// Same attributes in different map iteration order must produce same ID
	attrs1 := map[string]string{"b": "2", "a": "1", "c": "3"}
	attrs2 := map[string]string{"c": "3", "a": "1", "b": "2"}

	id1 := bucketing.CalculateBucketID("KWH", "ENERGY", attrs1)
	id2 := bucketing.CalculateBucketID("KWH", "ENERGY", attrs2)
	assert.Equal(t, id1, id2)
}

func TestCalculateBucketID_EmptyAttributes(t *testing.T) {
	// Empty attributes map should produce same result as nil attributes
	id1 := bucketing.CalculateBucketID("GBP", "CURRENCY", nil)
	id2 := bucketing.CalculateBucketID("GBP", "CURRENCY", map[string]string{})
	assert.Equal(t, id1, id2)
	assert.Equal(t, "currency_gbp", id1)
}

func TestCalculateBucketID_CarbonDimension(t *testing.T) {
	attrs := map[string]string{"vintage": "2024", "registry": "verra"}
	id := bucketing.CalculateBucketID("CARBON_CREDIT", "CARBON", attrs)
	assert.Equal(t, "carbon_carbon_credit_registry=verra_vintage=2024", id)
}

func TestCalculateBucketID_DeterministicAcrossCalls(t *testing.T) {
	// Drift prevention: same input produces identical bucket_id across 1000 calls
	attrs := map[string]string{"source": "solar", "region": "uk-south"}
	expected := bucketing.CalculateBucketID("KWH", "ENERGY", attrs)

	for i := 0; i < 1000; i++ {
		got := bucketing.CalculateBucketID("KWH", "ENERGY", attrs)
		if got != expected {
			t.Fatalf("drift detected at iteration %d: expected %q, got %q", i, expected, got)
		}
	}
}

func TestCalculateBucketID_LowercasesInstrumentCode(t *testing.T) {
	// Instrument code should be lowercased in the output
	id := bucketing.CalculateBucketID("GPU_HOUR", "COMPUTE", nil)
	assert.Equal(t, "compute_gpu_hour", id)
}

func TestCalculateBucketID_LowercasesDimension(t *testing.T) {
	// Dimension should be lowercased in the output
	id := bucketing.CalculateBucketID("KWH", "ENERGY", nil)
	assert.Equal(t, "energy_kwh", id)
}

func TestCalculateBucketID_EmptyInstrumentCode(t *testing.T) {
	// Empty instrument code returns empty string
	id := bucketing.CalculateBucketID("", "CURRENCY", nil)
	assert.Equal(t, "", id)
}

func TestCalculateBucketID_EmptyDimension(t *testing.T) {
	// Empty dimension returns empty string
	id := bucketing.CalculateBucketID("GBP", "", nil)
	assert.Equal(t, "", id)
}

func TestCalculateBucketID_SingleAttribute(t *testing.T) {
	attrs := map[string]string{"grade": "A"}
	id := bucketing.CalculateBucketID("RICE", "COUNT", attrs)
	assert.Equal(t, "count_rice_grade=A", id)
}

func TestGetDimension(t *testing.T) {
	tests := []struct {
		name           string
		instrumentCode string
		expected       string
	}{
		{"GBP is CURRENCY", "GBP", "CURRENCY"},
		{"USD is CURRENCY", "USD", "CURRENCY"},
		{"EUR is CURRENCY", "EUR", "CURRENCY"},
		{"JPY is CURRENCY", "JPY", "CURRENCY"},
		{"KWH is ENERGY", "KWH", "ENERGY"},
		{"GPU_HOUR is COMPUTE", "GPU_HOUR", "COMPUTE"},
		{"CPU_HOUR is COMPUTE", "CPU_HOUR", "COMPUTE"},
		{"STORAGE_GB is DATA", "STORAGE_GB", "DATA"},
		{"BANDWIDTH_GB is DATA", "BANDWIDTH_GB", "DATA"},
		{"CARBON_CREDIT is CARBON", "CARBON_CREDIT", "CARBON"},
		{"WATER_LITRE is VOLUME", "WATER_LITRE", "VOLUME"}, //nolint:misspell // British spelling matches domain convention
		{"unknown returns empty", "UNKNOWN_INSTRUMENT", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := bucketing.GetDimension(tc.instrumentCode)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestGetDimension_AllCurrencies(t *testing.T) {
	// Common ISO 4217 currencies should all return CURRENCY
	currencies := []string{"GBP", "USD", "EUR", "JPY", "CHF", "CAD", "AUD", "NZD", "SEK", "NOK", "DKK", "CNY", "INR", "BRL", "MXN", "ZAR", "SGD", "HKD", "KRW", "TWD"}
	for _, code := range currencies {
		t.Run(code, func(t *testing.T) {
			assert.Equal(t, "CURRENCY", bucketing.GetDimension(code))
		})
	}
}

func TestRegisterDimension(t *testing.T) {
	// Custom instrument registration
	bucketing.RegisterDimension("HYDROGEN_KG", "MASS")
	got := bucketing.GetDimension("HYDROGEN_KG")
	assert.Equal(t, "MASS", got)
}

func TestCalculateBucketID_WithRegisteredDimension(t *testing.T) {
	bucketing.RegisterDimension("HYDROGEN_KG", "MASS")
	attrs := map[string]string{"purity": "99.9"}
	id := bucketing.CalculateBucketID("HYDROGEN_KG", "MASS", attrs)
	assert.Equal(t, "mass_hydrogen_kg_purity=99.9", id)
}

func TestValidateBucketID(t *testing.T) {
	tests := []struct {
		name     string
		bucketID string
		wantErr  bool
	}{
		{"valid currency", "currency_gbp", false},
		{"valid energy with attrs", "energy_kwh_source=solar", false},
		{"valid compute with attrs", "compute_gpu_hour_tier=premium", false},
		{"valid multi-attr", "energy_kwh_grade=A_source=wind", false},
		{"empty string", "", true},
		{"no underscore", "currencygbp", true},
		{"single segment", "currency", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := bucketing.ValidateBucketID(tc.bucketID)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestParseBucketID(t *testing.T) {
	parts, err := bucketing.ParseBucketID("energy_kwh_source=solar_region=uk")
	require.NoError(t, err)
	assert.Equal(t, "energy", parts.Dimension)
	assert.Equal(t, "kwh", parts.InstrumentCode)
	assert.Equal(t, map[string]string{"source": "solar", "region": "uk"}, parts.Attributes)
}

func TestParseBucketID_NoAttributes(t *testing.T) {
	parts, err := bucketing.ParseBucketID("currency_gbp")
	require.NoError(t, err)
	assert.Equal(t, "currency", parts.Dimension)
	assert.Equal(t, "gbp", parts.InstrumentCode)
	assert.Empty(t, parts.Attributes)
}

func TestParseBucketID_Invalid(t *testing.T) {
	_, err := bucketing.ParseBucketID("")
	require.Error(t, err)

	_, err = bucketing.ParseBucketID("single")
	require.Error(t, err)
}

func TestRoundTrip(t *testing.T) {
	// Generate a bucket ID and parse it back - should be lossless
	attrs := map[string]string{"source": "solar", "region": "uk-south"}
	id := bucketing.CalculateBucketID("KWH", "ENERGY", attrs)

	parts, err := bucketing.ParseBucketID(id)
	require.NoError(t, err)
	assert.Equal(t, "energy", parts.Dimension)
	assert.Equal(t, "kwh", parts.InstrumentCode)
	assert.Equal(t, map[string]string{"source": "solar", "region": "uk-south"}, parts.Attributes)
}
