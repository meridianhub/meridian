package valuationfeature

import (
	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/registry"
)

// ConversionMethod holds the resolved valuation method ID and version for a
// cross-instrument conversion.
type ConversionMethod struct {
	ValuationMethodID      uuid.UUID
	ValuationMethodVersion int
}

// ResolveConversionMethod returns the valuation method for converting
// inputInstrument into an account whose product type is productType.
//
// Resolution order:
//  1. Check for an explicit ACTIVE ValuationMethodTemplate for
//     (productType, inputInstrument.Code). If found, return that method.
//  2. If inputInstrument and the account instrument share the same Dimension,
//     use productType.DefaultConversionMethodID (same-dimension conversion).
//  3. Neither condition satisfied → return ErrNoConversionAvailable.
func ResolveConversionMethod(
	productType *accounttype.Definition,
	inputInstrument *registry.InstrumentDefinition,
	accountInstrument *registry.InstrumentDefinition,
) (*ConversionMethod, error) {
	// Tier 1: explicit cross-dimension template.
	for _, tmpl := range productType.ValuationMethods {
		if tmpl.Status == accounttype.StatusActive && tmpl.InputInstrument == inputInstrument.Code {
			return &ConversionMethod{
				ValuationMethodID:      tmpl.ValuationMethodID,
				ValuationMethodVersion: tmpl.ValuationMethodVersion,
			}, nil
		}
	}

	// Tier 2: same-dimension fallback via DefaultConversionMethodID.
	if inputInstrument.Dimension == accountInstrument.Dimension {
		if productType.DefaultConversionMethodID != nil && productType.DefaultConversionMethodVersion != nil {
			return &ConversionMethod{
				ValuationMethodID:      *productType.DefaultConversionMethodID,
				ValuationMethodVersion: *productType.DefaultConversionMethodVersion,
			}, nil
		}
	}

	return nil, ErrNoConversionAvailable
}
