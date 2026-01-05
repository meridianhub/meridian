package csv

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractAttributeKeys(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		want       []string
	}{
		{
			name:       "empty expression",
			expression: "",
			want:       nil,
		},
		{
			name:       "no attributes",
			expression: "bucket_key(['static_value'])",
			want:       nil,
		},
		{
			name:       "single dot notation",
			expression: "attributes.region",
			want:       []string{"region"},
		},
		{
			name:       "single bracket double quote",
			expression: `attributes["region"]`,
			want:       []string{"region"},
		},
		{
			name:       "single bracket single quote",
			expression: `attributes['region']`,
			want:       []string{"region"},
		},
		{
			name:       "multiple attributes dot notation",
			expression: `bucket_key([attributes.region, attributes.grade])`,
			want:       []string{"grade", "region"},
		},
		{
			name:       "mixed notation",
			expression: `bucket_key([attributes.region, attributes["batch_id"], attributes['quality']])`,
			want:       []string{"batch_id", "quality", "region"},
		},
		{
			name:       "duplicate attributes",
			expression: `attributes.region + "-" + attributes.region`,
			want:       []string{"region"},
		},
		{
			name:       "complex expression with string concat",
			expression: `attributes.region + "-" + attributes.grade + "-" + attributes.batch_id`,
			want:       []string{"batch_id", "grade", "region"},
		},
		{
			name:       "nested in bucket_key function",
			expression: `bucket_key([attributes.vintage_year, attributes.origin_country, attributes.certification_type])`,
			want:       []string{"certification_type", "origin_country", "vintage_year"},
		},
		{
			name:       "attributes with underscores",
			expression: `attributes.data_center_id + attributes.rack_number`,
			want:       []string{"data_center_id", "rack_number"},
		},
		{
			name:       "real world energy example",
			expression: `bucket_key([attributes.grid_zone, attributes.measurement_type, attributes.tariff_code])`,
			want:       []string{"grid_zone", "measurement_type", "tariff_code"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractAttributeKeys(tt.expression)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateAttributeKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want bool
	}{
		{
			name: "valid simple key",
			key:  "region",
			want: true,
		},
		{
			name: "valid snake_case",
			key:  "grid_zone",
			want: true,
		},
		{
			name: "valid with numbers",
			key:  "zone1",
			want: true,
		},
		{
			name: "valid complex",
			key:  "data_center_rack_42",
			want: true,
		},
		{
			name: "empty string",
			key:  "",
			want: false,
		},
		{
			name: "starts with number",
			key:  "1region",
			want: false,
		},
		{
			name: "starts with underscore",
			key:  "_region",
			want: false,
		},
		{
			name: "contains uppercase",
			key:  "Region",
			want: false,
		},
		{
			name: "contains dash",
			key:  "grid-zone",
			want: false,
		},
		{
			name: "contains space",
			key:  "grid zone",
			want: false,
		},
		{
			name: "too long",
			key:  "this_is_a_very_long_attribute_key_that_exceeds_the_maximum_allowed_length_of_64_characters",
			want: false,
		},
		{
			name: "exactly 64 chars",
			key:  "abcdefghijklmnopqrstuvwxyz_abcdefghijklmnopqrstuvwxyz_1234567890",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateAttributeKey(tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeHeaderToAttributeKey(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{
			name:   "already valid",
			header: "region",
			want:   "region",
		},
		{
			name:   "uppercase to lowercase",
			header: "REGION",
			want:   "region",
		},
		{
			name:   "mixed case",
			header: "GridZone",
			want:   "gridzone",
		},
		{
			name:   "spaces to underscores",
			header: "Grid Zone",
			want:   "grid_zone",
		},
		{
			name:   "dashes to underscores",
			header: "grid-zone",
			want:   "grid_zone",
		},
		{
			name:   "leading/trailing whitespace",
			header: "  region  ",
			want:   "region",
		},
		{
			name:   "multiple spaces",
			header: "grid  zone",
			want:   "grid_zone",
		},
		{
			name:   "complex normalization",
			header: "  Data Center - ID  ",
			want:   "data_center_id",
		},
		{
			name:   "empty string",
			header: "",
			want:   "",
		},
		{
			name:   "only whitespace",
			header: "   ",
			want:   "",
		},
		{
			name:   "invalid after normalization - starts with number",
			header: "123 Region",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeHeaderToAttributeKey(tt.header)
			assert.Equal(t, tt.want, got)
		})
	}
}
