package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSagaDefinition_TableName(t *testing.T) {
	assert.Equal(t, "saga_definitions", SagaDefinition{}.TableName())
}

func TestComputeSagaDefinitionScriptHash(t *testing.T) {
	t.Run("returns 64-char hex digest", func(t *testing.T) {
		hash := ComputeSagaDefinitionScriptHash("def main(): pass")
		assert.Len(t, hash, 64)
		// SHA-256 hex output should be lower-case hex
		for _, c := range hash {
			assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
				"hash should contain only lowercase hex chars, got %q", c)
		}
	})

	t.Run("is deterministic", func(t *testing.T) {
		script := "def main(): return {'ok': True}"
		assert.Equal(t,
			ComputeSagaDefinitionScriptHash(script),
			ComputeSagaDefinitionScriptHash(script),
		)
	})

	t.Run("differs for different scripts", func(t *testing.T) {
		assert.NotEqual(t,
			ComputeSagaDefinitionScriptHash("def a(): pass"),
			ComputeSagaDefinitionScriptHash("def b(): pass"),
		)
	})

	t.Run("differs for whitespace changes", func(t *testing.T) {
		// Hashes pin exact byte content. Any whitespace edit must produce a
		// different hash, otherwise version pinning would silently accept
		// material script edits.
		assert.NotEqual(t,
			ComputeSagaDefinitionScriptHash("def main(): pass"),
			ComputeSagaDefinitionScriptHash("def main():  pass"),
		)
	})

	t.Run("empty script has stable hash", func(t *testing.T) {
		// SHA-256 of the empty string is a well-known constant.
		const sha256OfEmpty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		assert.Equal(t, sha256OfEmpty, ComputeSagaDefinitionScriptHash(""))
	})
}
