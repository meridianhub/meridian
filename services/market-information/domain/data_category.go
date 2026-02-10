// Package domain contains the domain models for the Market Information service.
package domain

// DataCategory represents the category of data within a dataset definition.
// This determines how the data is treated for validation and routing purposes.
type DataCategory string

// Data category constants for dataset definitions.
const (
	// DataCategoryPricing indicates the dataset contains pricing data.
	// Pricing data is subject to strict validation and audit requirements.
	DataCategoryPricing DataCategory = "PRICING"

	// DataCategoryContextual indicates the dataset contains contextual/reference data.
	// Contextual data provides supporting information for pricing data.
	DataCategoryContextual DataCategory = "CONTEXTUAL"

	// DataCategoryUtilization indicates the dataset contains platform utilization metrics.
	// Utilization data tracks resource consumption (transactions, API calls, storage, compute, network).
	DataCategoryUtilization DataCategory = "UTILIZATION"
)

// validDataCategories contains all valid data categories for efficient lookup.
var validDataCategories = map[DataCategory]bool{
	DataCategoryPricing:     true,
	DataCategoryContextual:  true,
	DataCategoryUtilization: true,
}

// IsValid returns true if the data category is a recognized valid type.
func (c DataCategory) IsValid() bool {
	return validDataCategories[c]
}

// String returns the string representation of the data category.
func (c DataCategory) String() string {
	return string(c)
}
