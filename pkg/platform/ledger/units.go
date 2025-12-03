// Package ledger provides types for multi-asset ledger operations with
// compile-time unit safety.
package ledger

// UnitMarker is a constraint interface for unit types that provide
// compile-time safety for quantity operations. Different unit types
// cannot be mixed in arithmetic operations.
type UnitMarker interface {
	~string
	UnitType() string
	DecimalPlaces() int32
}

// CurrencyUnit represents fiat or cryptocurrency currency codes (ISO 4217 or crypto).
type CurrencyUnit string

// UnitType returns the type identifier for currency units.
func (CurrencyUnit) UnitType() string { return "currency" }

// DecimalPlaces returns the number of decimal places for this currency.
// Different currencies have different decimal places:
//   - Zero: JPY, KRW, VND
//   - Two (default): USD, EUR, GBP, etc.
//   - Three: BHD, KWD, OMR
//   - Eight: BTC
//   - Eighteen: ETH
func (c CurrencyUnit) DecimalPlaces() int32 {
	switch c {
	case JPY:
		return 0
	case "KRW", "VND":
		return 0
	case "BHD", "KWD", "OMR":
		return 3
	case BTC:
		return 8
	case ETH:
		return 18
	case USD, EUR, GBP:
		return 2
	default:
		return 2
	}
}

// AirMilesUnit represents airline loyalty program miles.
type AirMilesUnit string

// UnitType returns the type identifier for air miles.
func (AirMilesUnit) UnitType() string { return "air-miles" }

// DecimalPlaces returns 0 since miles are always whole numbers.
func (AirMilesUnit) DecimalPlaces() int32 { return 0 }

// KWhUnit represents kilowatt-hours for energy tracking.
type KWhUnit string

// UnitType returns the type identifier for kilowatt-hours.
func (KWhUnit) UnitType() string { return "kwh" }

// DecimalPlaces returns 3 for energy tracking precision.
func (KWhUnit) DecimalPlaces() int32 { return 3 }

// CarbonCreditUnit represents carbon credits for environmental accounting.
type CarbonCreditUnit string

// UnitType returns the type identifier for carbon credits.
func (CarbonCreditUnit) UnitType() string { return "carbon-credit" }

// DecimalPlaces returns 2 for carbon credit precision.
func (CarbonCreditUnit) DecimalPlaces() int32 { return 2 }

// LoyaltyPointUnit represents generic loyalty or reward points.
type LoyaltyPointUnit string

// UnitType returns the type identifier for loyalty points.
func (LoyaltyPointUnit) UnitType() string { return "loyalty-point" }

// DecimalPlaces returns 0 since points are always whole numbers.
func (LoyaltyPointUnit) DecimalPlaces() int32 { return 0 }

// TokenUnit represents blockchain tokens with configurable decimal places.
type TokenUnit struct {
	Symbol   string
	Decimals int32
}

// UnitType returns the type identifier for blockchain tokens.
func (t TokenUnit) UnitType() string { return "token" }

// DecimalPlaces returns the configured decimal places for this token.
func (t TokenUnit) DecimalPlaces() int32 { return t.Decimals }

// Common unit values for convenience.
const (
	USD CurrencyUnit = "USD"
	EUR CurrencyUnit = "EUR"
	GBP CurrencyUnit = "GBP"
	JPY CurrencyUnit = "JPY"
	BTC CurrencyUnit = "BTC"
	ETH CurrencyUnit = "ETH"
)
