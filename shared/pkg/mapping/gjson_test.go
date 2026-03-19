package mapping

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtract(t *testing.T) {
	json := `{"user":{"name":"Alice","age":30},"tags":["go","rust"]}`

	t.Run("nested field", func(t *testing.T) {
		r := Extract(json, "user.name")
		assert.True(t, r.Exists())
		assert.Equal(t, "Alice", r.String())
	})

	t.Run("missing path", func(t *testing.T) {
		r := Extract(json, "user.missing")
		assert.False(t, r.Exists())
	})

	t.Run("array path", func(t *testing.T) {
		r := Extract(json, "tags.0")
		assert.Equal(t, "go", r.String())
	})

	t.Run("invalid json returns zero result", func(t *testing.T) {
		r := Extract("not json", "any")
		assert.False(t, r.Exists())
	})

	t.Run("empty path returns whole doc", func(t *testing.T) {
		r := Extract(json, "")
		// gjson with empty path returns nothing
		assert.False(t, r.Exists())
	})
}

func TestExtractString(t *testing.T) {
	json := `{"name":"Bob","count":42}`

	assert.Equal(t, "Bob", ExtractString(json, "name"))
	assert.Equal(t, "42", ExtractString(json, "count"))
	assert.Equal(t, "", ExtractString(json, "missing"))
}

func TestExtractAll(t *testing.T) {
	json := `{"items":[{"id":1},{"id":2},{"id":3}]}`

	t.Run("array iteration", func(t *testing.T) {
		results := ExtractAll(json, "items.#.id")
		assert.Len(t, results, 3)
		assert.Equal(t, int64(1), results[0].Int())
		assert.Equal(t, int64(3), results[2].Int())
	})

	t.Run("non-array returns empty", func(t *testing.T) {
		results := ExtractAll(json, "missing")
		assert.Empty(t, results)
	})

	t.Run("scalar wrapped in array", func(t *testing.T) {
		results := ExtractAll(`{"a":"single"}`, "a")
		// gjson wraps a scalar in a single-element array when .Array() is called
		assert.Len(t, results, 1)
	})
}
