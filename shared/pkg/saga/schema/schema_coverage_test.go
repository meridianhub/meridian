package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry_HasHandler(t *testing.T) {
	r := NewRegistry()

	yamlData := []byte(`
service: position_keeping
version: "1.0"
handlers:
  position_keeping.initiate_log:
    description: "Initiate a log entry"
    compensation_strategy: none
    params:
      amount:
        type: Decimal
        required: true
    returns:
      log_id:
        type: string
        required: true
`)

	err := r.LoadFromYAML(yamlData)
	require.NoError(t, err)

	t.Run("existing handler returns true", func(t *testing.T) {
		assert.True(t, r.HasHandler("position_keeping.initiate_log"))
	})

	t.Run("nonexistent handler returns false", func(t *testing.T) {
		assert.False(t, r.HasHandler("position_keeping.nonexistent"))
		assert.False(t, r.HasHandler("totally_unknown"))
	})
}
