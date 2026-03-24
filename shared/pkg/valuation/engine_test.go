package valuation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_defaults(t *testing.T) {
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
