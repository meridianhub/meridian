package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOrderedMarshalSlice(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		result := orderedMarshalSlice([]any{})
		assert.Equal(t, "[]", string(result))
	})

	t.Run("single element", func(t *testing.T) {
		result := orderedMarshalSlice([]any{"hello"})
		assert.Equal(t, `["hello"]`, string(result))
	})

	t.Run("multiple elements", func(t *testing.T) {
		result := orderedMarshalSlice([]any{"a", "b", "c"})
		assert.Equal(t, `["a","b","c"]`, string(result))
	})

	t.Run("mixed types", func(t *testing.T) {
		result := orderedMarshalSlice([]any{"hello", float64(42), true})
		assert.Contains(t, string(result), `"hello"`)
		assert.Contains(t, string(result), "42")
		assert.Contains(t, string(result), "true")
	})
}
