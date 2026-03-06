// Package refdata provides a shared InstrumentResolver interface and implementations
// for resolving instrument properties from Reference Data with in-process caching.
//
// Services use InstrumentResolver to obtain instrument metadata (dimension, precision,
// rounding mode) without directly depending on the Reference Data gRPC client.
package refdata

import (
	"context"
	"errors"
)

// InstrumentProperties contains resolved instrument metadata from Reference Data.
type InstrumentProperties struct {
	// Code is the instrument code (e.g., "USD", "KWH", "TONNE_CO2E").
	Code string

	// Dimension categorizes what this instrument measures (e.g., "MONETARY", "ENERGY", "COMPUTE").
	Dimension string

	// Precision is the number of decimal places for this instrument (0-18).
	Precision int

	// RoundingMode specifies how to round amounts for this instrument (e.g., "HALF_EVEN", "HALF_UP").
	// Defaults to "HALF_EVEN" (banker's rounding) when not explicitly set.
	RoundingMode string
}

// DefaultRoundingMode is the rounding mode used when no explicit rounding mode is set.
const DefaultRoundingMode = "HALF_EVEN"

// InstrumentResolver resolves instrument properties from Reference Data.
// Implementations must be safe for concurrent use.
type InstrumentResolver interface {
	// Resolve returns the properties for the given instrument code.
	// Returns ErrUnknownInstrument if the instrument cannot be found.
	Resolve(ctx context.Context, code string) (InstrumentProperties, error)
}

// ErrUnknownInstrument is returned when an instrument code cannot be resolved.
var ErrUnknownInstrument = errors.New("unknown instrument")
