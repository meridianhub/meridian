// Package templates provides built-in Starlark forecasting algorithm templates
// embedded at compile time. Each template implements the compute_forecast(ctx)
// entry point and uses ForecastContext builtins.
package templates

import "embed"

// FS holds the embedded Starlark template files.
//
//go:embed *.star
var FS embed.FS

// Template names for programmatic access.
const (
	MovingAverage         = "moving_average.star"
	SeasonalDecomposition = "seasonal_decomposition.star"
	CapacityPricing       = "capacity_pricing.star"
	ExternalBlend         = "external_blend.star"
)

// All returns the names of all built-in templates.
func All() []string {
	return []string{
		MovingAverage,
		SeasonalDecomposition,
		CapacityPricing,
		ExternalBlend,
	}
}

// Load reads a template by name from the embedded filesystem.
func Load(name string) (string, error) {
	data, err := FS.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
