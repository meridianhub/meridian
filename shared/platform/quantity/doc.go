// Package quantity provides the generic Qty[D] type for the Universal Asset System.
//
// Qty[D] represents an amount of a specific instrument with compile-time dimension safety.
// The type parameter D prevents mixing monetary and commodity quantities at compile time:
//
//	var m quantity.Money   // monetary quantity (USD, EUR)
//	var e quantity.Asset   // commodity quantity (KWH, GPU_HOUR)
//	m = e                  // compile error!
//
// # Type Aliases
//
//   - Money = Qty[Monetary]   for currency quantities
//   - Asset = Qty[Commodity]  for non-monetary asset quantities
//
// # Arithmetic
//
// Add and Subtract require the same instrument (code and version). Multiply and Divide
// operate on a scalar and do not require instrument matching.
//
// # Instruments and Dimensions
//
// An [Instrument] carries the code, precision, rounding mode, and dimension. Use
// [NewInstrument] to construct one and [Parse] to deserialise from a string.
package quantity
