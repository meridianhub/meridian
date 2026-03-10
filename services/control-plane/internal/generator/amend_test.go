package generator_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
)

func TestComputeAmendImpact_NoChanges(t *testing.T) {
	yaml := `
instruments:
  - code: GBP
    name: British Pound
account_types:
  - code: CURRENT
    name: Current Account
sagas:
  - name: simple_transfer
    script: |
      result = {}
`
	impact := generator.ComputeAmendImpact(yaml, yaml)

	assert.Empty(t, impact.Added)
	assert.Empty(t, impact.Removed)
	// All existing resources are marked as modified (conservative approach).
	assert.Contains(t, impact.Modified, "instrument:GBP")
	assert.Contains(t, impact.Modified, "account_type:CURRENT")
	assert.Contains(t, impact.Modified, "saga:simple_transfer")
}

func TestComputeAmendImpact_AddedResources(t *testing.T) {
	original := `
instruments:
  - code: GBP
    name: British Pound
`
	amended := `
instruments:
  - code: GBP
    name: British Pound
  - code: EUR
    name: Euro
`
	impact := generator.ComputeAmendImpact(original, amended)

	assert.Contains(t, impact.Added, "instrument:EUR")
	assert.Contains(t, impact.Modified, "instrument:GBP")
	assert.Empty(t, impact.Removed)
}

func TestComputeAmendImpact_RemovedResources(t *testing.T) {
	original := `
instruments:
  - code: GBP
    name: British Pound
  - code: USD
    name: US Dollar
account_types:
  - code: CURRENT
    name: Current Account
  - code: SAVINGS
    name: Savings Account
`
	amended := `
instruments:
  - code: GBP
    name: British Pound
account_types:
  - code: CURRENT
    name: Current Account
`
	impact := generator.ComputeAmendImpact(original, amended)

	assert.Contains(t, impact.Removed, "instrument:USD")
	assert.Contains(t, impact.Removed, "account_type:SAVINGS")
	assert.NotContains(t, impact.Removed, "instrument:GBP")
}

func TestComputeAmendImpact_MixedChanges(t *testing.T) {
	original := `
instruments:
  - code: GBP
  - code: USD
sagas:
  - name: payment_flow
`
	amended := `
instruments:
  - code: GBP
  - code: EUR
sagas:
  - name: payment_flow
  - name: carbon_offset_flow
`
	impact := generator.ComputeAmendImpact(original, amended)

	assert.Contains(t, impact.Added, "instrument:EUR")
	assert.Contains(t, impact.Added, "saga:carbon_offset_flow")
	assert.Contains(t, impact.Removed, "instrument:USD")
	assert.Contains(t, impact.Modified, "instrument:GBP")
	assert.Contains(t, impact.Modified, "saga:payment_flow")
}

func TestComputeAmendImpact_InvalidYAML_ReturnsEmpty(t *testing.T) {
	impact := generator.ComputeAmendImpact("not: [valid yaml", "instruments:\n  - code: GBP")
	assert.Empty(t, impact.Added)
	assert.Empty(t, impact.Modified)
	assert.Empty(t, impact.Removed)
}

func TestAmendImpact_ToDecisions(t *testing.T) {
	impact := generator.AmendImpact{
		Added:    []string{"instrument:EUR"},
		Modified: []string{"instrument:GBP"},
		Removed:  []string{"instrument:USD"},
	}

	decisions := impact.ToDecisions()
	assert.Contains(t, decisions, "Added instrument:EUR")
	assert.Contains(t, decisions, "Modified instrument:GBP")
	assert.Contains(t, decisions, "Warning: Removed instrument:USD (was present in original manifest)")
}

func TestAmendImpact_ToDecisions_Empty(t *testing.T) {
	impact := generator.AmendImpact{}
	decisions := impact.ToDecisions()
	assert.Empty(t, decisions)
}
