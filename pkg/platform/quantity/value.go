// Package quantity provides the QuantityValue interface for runtime type handling.
//
// # Runtime vs Compile-Time Type Safety
//
// While Qty[D] provides compile-time dimension safety through generics, there are
// situations where quantities must be handled at runtime without knowing the dimension
// type in advance. These include:
//
//   - Database deserialization: Loading quantities from storage where the dimension
//     is stored as a string column
//   - Protocol buffer parsing: Converting from proto messages where dimension is an enum
//   - Mixed collections: Handling collections of quantities with different dimensions
//   - API responses: Returning quantities to clients without exposing internal type info
//
// QuantityValue provides a common interface that both Money (Qty[Monetary]) and
// Asset (Qty[Commodity]) implement, enabling runtime polymorphism while preserving
// the option to convert back to typed quantities via AsMoney() and AsAsset().
package quantity

import "github.com/shopspring/decimal"

// Value is a runtime interface for handling quantities of unknown dimension.
//
// This interface bridges the gap between compile-time type safety (via Qty[D]) and
// runtime flexibility needed for database/proto deserialization. It provides:
//
//   - Type-erased access to quantity data (amount, instrument)
//   - Type-safe conversion methods (AsMoney, AsAsset) for recovering concrete types
//   - Dimension inspection via DimensionName() for runtime switching
//
// Example usage for database deserialization:
//
//	func loadQuantity(row *sql.Row) (Value, error) {
//	    var amount decimal.Decimal
//	    var inst Instrument
//	    // ... scan from database ...
//	    return ParseQuantity(amount, inst)
//	}
//
//	// Later, convert to typed quantity:
//	qv, _ := loadQuantity(row)
//	if money, ok := qv.AsMoney(); ok {
//	    // Handle as Money (Qty[Monetary])
//	}
type Value interface {
	// DimensionName returns the dimension string from the instrument.
	// Returns "CURRENCY" for monetary quantities, or the specific dimension
	// (e.g., "ENERGY", "COMPUTE") for commodity quantities.
	DimensionName() string

	// GetAmount returns the decimal amount of this quantity.
	GetAmount() decimal.Decimal

	// GetInstrument returns the instrument identifying this quantity's asset type.
	GetInstrument() Instrument

	// AsMoney attempts to convert this quantity to a Money (Qty[Monetary]) type.
	// Returns (value, true) if this is a monetary quantity (instrument.Dimension == "CURRENCY"),
	// or (zero, false) if this is a commodity quantity.
	//
	// Example:
	//   if money, ok := qv.AsMoney(); ok {
	//       total, err := money.Add(otherMoney)
	//   }
	AsMoney() (Money, bool)

	// AsAsset attempts to convert this quantity to an Asset (Qty[Commodity]) type.
	// Returns (value, true) if this is a commodity quantity (instrument.Dimension != "CURRENCY"),
	// or (zero, false) if this is a monetary quantity.
	//
	// Example:
	//   if asset, ok := qv.AsAsset(); ok {
	//       total, err := asset.Add(otherAsset)
	//   }
	AsAsset() (Asset, bool)
}
