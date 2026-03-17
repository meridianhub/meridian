package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateScript_WithinLimit(t *testing.T) {
	cfg := DefaultConfig()
	script := "x = 1"

	err := ValidateScript(script, cfg)
	assert.NoError(t, err)
}

func TestValidateScript_ExactlyAtLimit(t *testing.T) {
	cfg := Config{MaxScriptSize: 10}
	script := strings.Repeat("x", 10)

	err := ValidateScript(script, cfg)
	assert.NoError(t, err)
}

func TestValidateScript_ExceedsLimit(t *testing.T) {
	cfg := Config{MaxScriptSize: 10}
	script := strings.Repeat("x", 11)

	err := ValidateScript(script, cfg)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrScriptTooLarge)
	assert.Contains(t, err.Error(), "11 bytes exceeds 10")
}

func TestValidateScript_EmptyScript(t *testing.T) {
	cfg := DefaultConfig()
	err := ValidateScript("", cfg)
	assert.NoError(t, err)
}
