// Package money provides the shared instrument-aware Money type for use across all services.
//
// Money wraps [quantity.Money] (Qty[Monetary]) and is restricted to the CURRENCY dimension,
// providing compile-time safety against mixing monetary and commodity quantities.
//
// # Usage
//
//	m, err := money.New("GBP", 10000)                          // £100.00 from minor units
//	m, err := money.NewFromDecimal(decimal.NewFromInt(100), money.CurrencyGBP)
//
//	m.Amount()      // decimal.Decimal
//	m.Currency()    // money.Currency ("GBP")
//	m.Instrument()  // quantity.Instrument (full instrument metadata)
//
// For multi-dimensional quantities (ENERGY, COMPUTE, etc.) use [amount.Amount] instead.
package money
