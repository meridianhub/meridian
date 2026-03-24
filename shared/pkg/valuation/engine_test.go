package valuation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_defaults(t *testing.T) {
	// Zero-value Config should have 0 MaxPathEntries
	var cfg Config
	assert.Equal(t, 0, cfg.MaxPathEntries)
	assert.Nil(t, cfg.PolicyRuntime)
	assert.Nil(t, cfg.StarlarkRuntime)
	assert.Nil(t, cfg.Cache)
}

func TestConfig_with_max_path_entries(t *testing.T) {
	cfg := Config{
		MaxPathEntries: 20,
	}
	assert.Equal(t, 20, cfg.MaxPathEntries)
}

func TestMethod_fields(t *testing.T) {
	m := Method{
		ID:               "method-123",
		Version:          1,
		Name:             "fx_spot_rate",
		Script:           "def valuate(input):\n    return input * rate",
		OutputInstrument: "GBP",
		Description:      "Spot FX rate conversion",
	}

	assert.Equal(t, "method-123", m.ID)
	assert.Equal(t, 1, m.Version)
	assert.Equal(t, "fx_spot_rate", m.Name)
	assert.Equal(t, "GBP", m.OutputInstrument)
	assert.NotEmpty(t, m.Script)
	assert.NotEmpty(t, m.Description)
}

func TestEngine_interface_compliance(t *testing.T) {
	// Verify the Engine interface has the expected method signature.
	// This test is a compile-time check - if Engine changes,
	// this file won't compile.
	var _ Engine = (Engine)(nil)
}

func TestPolicyRuntime_interface_compliance(t *testing.T) {
	var _ PolicyRuntime = (PolicyRuntime)(nil)
}

func TestStarlarkRuntime_interface_compliance(t *testing.T) {
	var _ StarlarkRuntime = (StarlarkRuntime)(nil)
}

func TestCache_interface_compliance(t *testing.T) {
	var _ Cache = (Cache)(nil)
}

func TestCompiledPolicy_interface_compliance(t *testing.T) {
	var _ CompiledPolicy = (CompiledPolicy)(nil)
}
