// Package amount provides the shared dimension-agnostic Amount type for use across all services.
//
// Unlike the money package which is restricted to the CURRENCY dimension, Amount accepts any
// valid dimension from quantity.ValidDimensions (CURRENCY, ENERGY, CARBON, COMPUTE, etc.).
// For CURRENCY instruments it delegates precision lookup to the currency package.
//
// # Usage
//
//	a, err := amount.NewFromInstrument("GBP", "CURRENCY", 2, 10000)  // £100.00
//	a, err := amount.NewFromInstrument("KWH", "ENERGY", 3, 1500)     // 1.500 KWH
//
//	inst, _ := quantity.NewInstrument("KWH", 0, "ENERGY", 3)
//	a := amount.New(inst, 1500)  // 1.500 KWH
//
// See [money] for the currency-only variant with stricter typing.
package amount
