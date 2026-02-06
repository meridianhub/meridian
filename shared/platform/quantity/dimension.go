// Package quantity provides dimension types for compile-time type safety
// in the Universal Asset System.
//
// # Phantom Types Pattern
//
// Monetary and Commodity are phantom types - empty structs used purely for
// compile-time type distinction. When used as type parameters in Quantity[D],
// they prevent accidental mixing of monetary values (USD, EUR) with commodity
// values (KWH, GPU-hours, carbon credits).
//
// Example compile-time safety:
//
//	var money Quantity[Monetary]  // monetary quantity
//	var energy Quantity[Commodity] // commodity quantity
//	money = energy                 // compile error: cannot use energy as Quantity[Monetary]
//
// The empty struct design means these types have zero runtime overhead - they
// exist only for the type system.
package quantity

// Monetary is a phantom type representing monetary dimensions (currencies).
// Use as a type parameter: Quantity[Monetary] for USD, EUR, GBP, etc.
type Monetary struct{}

// String returns the canonical dimension name for monetary values.
func (Monetary) String() string {
	return "Monetary"
}

// Validate always returns nil for Monetary dimensions.
// Monetary is always a valid dimension.
func (Monetary) Validate() error {
	return nil
}

// Commodity is a phantom type representing commodity dimensions.
// Use as a type parameter: Quantity[Commodity] for energy (KWH),
// compute (GPU-hours), carbon credits, and other physical or digital assets.
type Commodity struct{}

// String returns the canonical dimension name for commodity values.
func (Commodity) String() string {
	return "Commodity"
}

// Validate always returns nil for Commodity dimensions.
// Commodity is always a valid dimension.
func (Commodity) Validate() error {
	return nil
}
