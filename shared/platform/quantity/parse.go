package quantity

import (
	"errors"

	"github.com/shopspring/decimal"
)

// ErrUnknownDimension is returned when a dimension string is not recognized.
// This should rarely occur since Instrument validation already checks dimensions.
var ErrUnknownDimension = errors.New("unknown dimension: cannot parse quantity with unrecognized dimension")

// ParseQuantity creates a Value from a decimal amount and instrument.
//
// This function bridges runtime data (from database or proto) to the type-safe
// Qty[D] system. It examines the instrument's dimension to determine whether
// to create a Money (Qty[Monetary]) or Asset (Qty[Commodity]) quantity.
//
// The decision is based on the instrument's Dimension field:
//   - "CURRENCY" -> returns Money (Qty[Monetary])
//   - Any other valid dimension -> returns Asset (Qty[Commodity])
//
// Since Instrument.Dimension is validated during instrument creation, this
// function trusts the dimension value and should not return ErrUnknownDimension
// for properly created instruments.
//
// Example usage for database deserialization:
//
//	func loadFromDB(row *sql.Row) (Value, error) {
//	    var amountStr string
//	    var code, dimension string
//	    var version uint32
//	    var precision int
//	    // ... scan row ...
//
//	    inst, err := NewInstrument(code, version, dimension, precision)
//	    if err != nil {
//	        return nil, err
//	    }
//
//	    amount, err := decimal.NewFromString(amountStr)
//	    if err != nil {
//	        return nil, err
//	    }
//
//	    return ParseQuantity(amount, inst)
//	}
//
// Example usage for proto conversion:
//
//	func fromProto(pb *quantitypb.Quantity) (Value, error) {
//	    inst, err := instrumentFromProto(pb.GetInstrument())
//	    if err != nil {
//	        return nil, err
//	    }
//	    amount, err := decimal.NewFromString(pb.GetAmount())
//	    if err != nil {
//	        return nil, err
//	    }
//	    return ParseQuantity(amount, inst)
//	}
func ParseQuantity(amount decimal.Decimal, inst Instrument) (Value, error) {
	switch inst.Dimension {
	case DimensionCurrency:
		return New[Monetary](amount, inst), nil
	case "":
		return nil, ErrUnknownDimension
	default:
		// Use ValidDimensions map to avoid duplicating dimension list.
		// For properly validated instruments, this check should always pass.
		if !ValidDimensions[inst.Dimension] {
			return nil, ErrUnknownDimension
		}
		return New[Commodity](amount, inst), nil
	}
}

// ParseQuantityFromString creates a Value from a string amount and instrument.
//
// This is a convenience wrapper around ParseQuantity that handles decimal parsing.
// Returns an error if the amount string is not a valid decimal.
func ParseQuantityFromString(amount string, inst Instrument) (Value, error) {
	d, err := decimal.NewFromString(amount)
	if err != nil {
		return nil, ErrInvalidDecimalString
	}
	return ParseQuantity(d, inst)
}
