package applier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEmbeddedApplyManifest(t *testing.T) {
	script, version, err := loadEmbeddedApplyManifest()
	require.NoError(t, err)

	assert.NotEmpty(t, script)
	assert.Equal(t, "1.1.0", version)
	assert.Contains(t, script, "apply_manifest")
	assert.Contains(t, script, "execute_apply_manifest")
	assert.Contains(t, script, "reference_data.register_instrument")
	assert.Contains(t, script, "internal_account.initiate")
}

func TestIsSemverGreater(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected bool
	}{
		{"empty b always returns true", "1.0.0", "", true},
		{"same version", "1.0.0", "1.0.0", false},
		{"major greater", "2.0.0", "1.0.0", true},
		{"major less", "1.0.0", "2.0.0", false},
		{"minor greater", "1.2.0", "1.1.0", true},
		{"minor less", "1.1.0", "1.2.0", false},
		{"patch greater", "1.0.2", "1.0.1", true},
		{"patch less", "1.0.1", "1.0.2", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSemverGreater(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewBootstrap(t *testing.T) {
	b := NewBootstrap(nil)
	require.NotNil(t, b)
	assert.NotNil(t, b.logger)
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"0", 0},
		{"1", 1},
		{"42", 42},
		{"100", 100},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseInt(tt.input))
		})
	}
}
