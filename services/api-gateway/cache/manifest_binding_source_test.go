package cache

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifestJSON_ExtractAPIBindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		json     string
		expected map[string]string
	}{
		{
			name: "multiple api triggers",
			json: `{
				"sagas": [
					{"name": "process_payment", "trigger": "api:/v1/payments"},
					{"name": "settle_trade", "trigger": "api:/v1/settlements"}
				]
			}`,
			expected: map[string]string{
				"/v1/payments":    "process_payment",
				"/v1/settlements": "settle_trade",
			},
		},
		{
			name: "mixed trigger types",
			json: `{
				"sagas": [
					{"name": "process_payment", "trigger": "api:/v1/payments"},
					{"name": "daily_recon", "trigger": "scheduled:daily_reconciliation"},
					{"name": "stripe_webhook", "trigger": "webhook:stripe_payment"},
					{"name": "settle_trade", "trigger": "api:/v1/settlements"},
					{"name": "on_capture", "trigger": "event:position-keeping.transaction-captured.v1"}
				]
			}`,
			expected: map[string]string{
				"/v1/payments":    "process_payment",
				"/v1/settlements": "settle_trade",
			},
		},
		{
			name:     "no sagas",
			json:     `{}`,
			expected: map[string]string{},
		},
		{
			name: "no api triggers",
			json: `{
				"sagas": [
					{"name": "daily_recon", "trigger": "scheduled:daily_reconciliation"}
				]
			}`,
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var manifest manifestJSON
			err := json.Unmarshal([]byte(tt.json), &manifest)
			require.NoError(t, err)

			bindings := extractAPIBindings(manifest.Sagas, "test-tenant")

			assert.Equal(t, tt.expected, bindings)
		})
	}
}
