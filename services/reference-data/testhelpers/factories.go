// Package testhelpers provides shared test factories and utilities for reference-data tests.
package testhelpers

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/meridianhub/meridian/services/reference-data/valuation"
)

// NewInstrumentDefinition creates a test instrument definition with the given code and version.
func NewInstrumentDefinition(code string, version int) *registry.InstrumentDefinition {
	return &registry.InstrumentDefinition{
		ID:        uuid.New(),
		Code:      code,
		Version:   version,
		Dimension: registry.DimensionMonetary,
		Precision: 2,
		Status:    registry.StatusActive,
	}
}

// NewValuationMethod creates a test valuation method.
func NewValuationMethod(name, input, output string) *valuation.Method {
	return &valuation.Method{
		Name:             name,
		Version:          1,
		InputInstrument:  input,
		OutputInstrument: output,
		LogicScript:      "def evaluate(amount, rate, context):\n    return amount\n",
		RequiredPolicies: []string{},
		Description:      fmt.Sprintf("Test method: %s", name),
	}
}

// NewValuationPolicy creates a test valuation policy.
func NewValuationPolicy(name string) *valuation.Policy {
	return &valuation.Policy{
		Name:          name,
		Version:       1,
		CelExpression: "amount",
		OutputType:    "string",
		EstimatedCost: 1,
		Description:   fmt.Sprintf("Test policy: %s", name),
	}
}
