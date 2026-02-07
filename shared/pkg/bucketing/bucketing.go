// Package bucketing provides canonical bucket ID calculation for the Universal Asset System.
//
// All services MUST use this library to compute bucket IDs. Direct string construction
// of bucket IDs is forbidden to prevent Bucket Drift - the condition where the same
// instrument+attributes produce different bucket IDs across services.
//
// Bucket ID format: "dimension_instrument_attr1=val1_attr2=val2"
// Examples:
//   - GBP (CURRENCY dimension)                -> "currency_gbp"
//   - KWH (ENERGY dimension, source=solar)    -> "energy_kwh_source=solar"
//   - GPU_HOUR (COMPUTE dimension, tier=prem) -> "compute_gpu_hour_tier=prem"
package bucketing

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Errors for bucket ID operations.
var (
	ErrEmptyBucketID = errors.New("bucket ID cannot be empty")
	ErrInvalidFormat = errors.New("bucket ID must contain at least dimension and instrument code separated by underscore")
	ErrMalformedAttr = errors.New("bucket ID contains malformed attribute (missing '=')")
)

// BucketIDParts contains the parsed components of a bucket ID.
type BucketIDParts struct {
	Dimension      string
	InstrumentCode string
	Attributes     map[string]string
}

// dimensionRegistry maps instrument codes to their dimension.
// Protected by dimensionMu for concurrent access.
var (
	dimensionMu       sync.RWMutex
	dimensionRegistry = map[string]string{
		// ISO 4217 currencies -> CURRENCY
		"GBP": "CURRENCY", "USD": "CURRENCY", "EUR": "CURRENCY", "JPY": "CURRENCY",
		"CHF": "CURRENCY", "CAD": "CURRENCY", "AUD": "CURRENCY", "NZD": "CURRENCY",
		"SEK": "CURRENCY", "NOK": "CURRENCY", "DKK": "CURRENCY", "CNY": "CURRENCY",
		"INR": "CURRENCY", "BRL": "CURRENCY", "MXN": "CURRENCY", "ZAR": "CURRENCY",
		"SGD": "CURRENCY", "HKD": "CURRENCY", "KRW": "CURRENCY", "TWD": "CURRENCY",
		"PLN": "CURRENCY", "CZK": "CURRENCY", "HUF": "CURRENCY", "TRY": "CURRENCY",
		"THB": "CURRENCY", "IDR": "CURRENCY", "MYR": "CURRENCY", "PHP": "CURRENCY",
		"AED": "CURRENCY", "SAR": "CURRENCY", "ILS": "CURRENCY", "CLP": "CURRENCY",
		"COP": "CURRENCY", "PEN": "CURRENCY", "ARS": "CURRENCY", "EGP": "CURRENCY",
		"NGN": "CURRENCY", "KES": "CURRENCY", "GHS": "CURRENCY", "RUB": "CURRENCY",

		// Energy -> ENERGY
		"KWH":   "ENERGY",
		"MWH":   "ENERGY",
		"THERM": "ENERGY",
		"BTU":   "ENERGY",

		// Compute -> COMPUTE
		"GPU_HOUR": "COMPUTE",
		"CPU_HOUR": "COMPUTE",

		// Data -> DATA
		"STORAGE_GB":   "DATA",
		"BANDWIDTH_GB": "DATA",

		// Carbon -> CARBON
		"CARBON_CREDIT": "CARBON",
		"CARBON_TONNE":  "CARBON",

		// Volume -> VOLUME
		"WATER_LITRE": "VOLUME", //nolint:misspell // British spelling matches domain convention

		// Mass -> MASS
		"CARBON_TONNES": "CARBON",
	}
)

// CalculateBucketID computes the canonical bucket ID for an instrument with optional attributes.
// Returns empty string if instrumentCode or dimension is empty.
//
// The format is: "dimension_instrumentcode_attr1=val1_attr2=val2"
// - Dimension and instrument code are lowercased
// - Attributes are sorted alphabetically by key for determinism
// - Attribute values are included as-is (case preserved)
func CalculateBucketID(instrumentCode, dimension string, attributes map[string]string) string {
	if instrumentCode == "" || dimension == "" {
		return ""
	}

	parts := []string{
		strings.ToLower(dimension),
		strings.ToLower(instrumentCode),
	}

	if len(attributes) > 0 {
		keys := make([]string, 0, len(attributes))
		for k := range attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%s", k, attributes[k]))
		}
	}

	return strings.Join(parts, "_")
}

// GetDimension returns the dimension for a known instrument code.
// Returns empty string if the instrument code is not registered.
//
// Common mappings:
//   - ISO 4217 currency codes (GBP, USD, EUR, ...) -> "CURRENCY"
//   - KWH, MWH -> "ENERGY"
//   - GPU_HOUR, CPU_HOUR -> "COMPUTE"
//   - CARBON_CREDIT -> "CARBON"
//   - STORAGE_GB, BANDWIDTH_GB -> "DATA"
//   - WATER_LITER -> "VOLUME"
func GetDimension(instrumentCode string) string {
	dimensionMu.RLock()
	defer dimensionMu.RUnlock()
	return dimensionRegistry[instrumentCode]
}

// RegisterDimension adds a custom instrument code to dimension mapping.
// This is safe for concurrent use and allows services to register
// tenant-specific or domain-specific instruments at startup.
func RegisterDimension(instrumentCode, dimension string) {
	dimensionMu.Lock()
	defer dimensionMu.Unlock()
	dimensionRegistry[instrumentCode] = dimension
}

// ValidateBucketID checks that a bucket ID string has valid format.
// A valid bucket ID has at least two segments (dimension and instrument code)
// separated by underscores, and any attribute segments must contain '='.
func ValidateBucketID(bucketID string) error {
	if bucketID == "" {
		return ErrEmptyBucketID
	}

	_, err := ParseBucketID(bucketID)
	return err
}

// ParseBucketID decomposes a bucket ID string into its constituent parts.
// The first segment is the dimension, the second is the instrument code,
// and remaining segments are key=value attribute pairs.
func ParseBucketID(bucketID string) (BucketIDParts, error) {
	if bucketID == "" {
		return BucketIDParts{}, ErrEmptyBucketID
	}

	// Split into segments. We need to handle the case where instrument codes
	// contain underscores (e.g., "gpu_hour"). The dimension is always one word,
	// so we find where the attributes start (first segment with '=') and work backwards.
	segments := strings.Split(bucketID, "_")
	if len(segments) < 2 {
		return BucketIDParts{}, ErrInvalidFormat
	}

	// Find the first attribute segment (contains '=')
	firstAttrIdx := -1
	for i, seg := range segments {
		if strings.Contains(seg, "=") {
			firstAttrIdx = i
			break
		}
	}

	var dimension, instrumentCode string
	var attrs map[string]string

	if firstAttrIdx == -1 {
		// No attributes: first segment is dimension, rest is instrument code
		dimension = segments[0]
		instrumentCode = strings.Join(segments[1:], "_")
	} else {
		if firstAttrIdx < 2 {
			return BucketIDParts{}, ErrInvalidFormat
		}
		dimension = segments[0]
		instrumentCode = strings.Join(segments[1:firstAttrIdx], "_")

		attrs = make(map[string]string, len(segments)-firstAttrIdx)
		for _, seg := range segments[firstAttrIdx:] {
			eqIdx := strings.Index(seg, "=")
			if eqIdx < 0 {
				return BucketIDParts{}, fmt.Errorf("%w: segment %q", ErrMalformedAttr, seg)
			}
			attrs[seg[:eqIdx]] = seg[eqIdx+1:]
		}
	}

	return BucketIDParts{
		Dimension:      dimension,
		InstrumentCode: instrumentCode,
		Attributes:     attrs,
	}, nil
}
