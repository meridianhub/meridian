package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloneStringMap(t *testing.T) {
	t.Run("nil map returns nil", func(t *testing.T) {
		result := cloneStringMap(nil)
		assert.Nil(t, result)
	})

	t.Run("empty map returns empty map", func(t *testing.T) {
		result := cloneStringMap(map[string]string{})
		require.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("populated map is deep copied", func(t *testing.T) {
		original := map[string]string{"a": "1", "b": "2"}
		clone := cloneStringMap(original)
		require.NotNil(t, clone)
		assert.Equal(t, original, clone)

		// Modify clone - original should not change
		clone["c"] = "3"
		assert.NotContains(t, original, "c")
	})
}

func TestCloneHandlerConversion(t *testing.T) {
	t.Run("empty conversion", func(t *testing.T) {
		conv := HandlerConversion{}
		clone := cloneHandlerConversion(conv)
		assert.Equal(t, conv, clone)
	})

	t.Run("conversion with maps is deep copied", func(t *testing.T) {
		conv := HandlerConversion{
			FromVersion:  1,
			FromName:     "old.handler",
			ParamMapping: map[string]string{"old_param": "new_param"},
			Defaults:     map[string]string{"new_field": "default_val"},
		}
		clone := cloneHandlerConversion(conv)
		assert.Equal(t, conv.FromVersion, clone.FromVersion)
		assert.Equal(t, conv.FromName, clone.FromName)
		assert.Equal(t, conv.ParamMapping, clone.ParamMapping)
		assert.Equal(t, conv.Defaults, clone.Defaults)

		// Modify clone maps - original should not change
		clone.ParamMapping["extra"] = "value"
		assert.NotContains(t, conv.ParamMapping, "extra")
		clone.Defaults["extra"] = "value"
		assert.NotContains(t, conv.Defaults, "extra")
	})

	t.Run("conversion with nil maps", func(t *testing.T) {
		conv := HandlerConversion{
			FromVersion:  2,
			ParamMapping: nil,
			Defaults:     nil,
		}
		clone := cloneHandlerConversion(conv)
		assert.Nil(t, clone.ParamMapping)
		assert.Nil(t, clone.Defaults)
	})
}

func TestCloneHandlerMetadata(t *testing.T) {
	t.Run("nil metadata returns nil", func(t *testing.T) {
		result := cloneHandlerMetadata(nil)
		assert.Nil(t, result)
	})

	t.Run("metadata with all fields", func(t *testing.T) {
		reqTrue := true
		meta := &HandlerMetadata{
			ProducesInstruments: []string{"GBP", "USD"},
			ParamOverrides: map[string]ParamOverride{
				"amount": {
					Type:     "Decimal",
					Required: &reqTrue,
				},
			},
			Conversions: []HandlerConversion{
				{
					FromVersion:  1,
					ParamMapping: map[string]string{"old": "new"},
					Defaults:     map[string]string{"field": "val"},
				},
			},
		}
		clone := cloneHandlerMetadata(meta)
		require.NotNil(t, clone)

		// Verify deep copy
		assert.Equal(t, meta.ProducesInstruments, clone.ProducesInstruments)
		assert.Equal(t, meta.ParamOverrides, clone.ParamOverrides)
		assert.Len(t, clone.Conversions, 1)

		// Modify clone - original should not change
		clone.ProducesInstruments = append(clone.ProducesInstruments, "EUR")
		assert.Len(t, meta.ProducesInstruments, 2)

		clone.Conversions[0].ParamMapping["extra"] = "val"
		assert.NotContains(t, meta.Conversions[0].ParamMapping, "extra")

		// Verify Required pointer is deep copied
		*clone.ParamOverrides["amount"].Required = false
		assert.True(t, *meta.ParamOverrides["amount"].Required)
	})

	t.Run("metadata with nil slices", func(t *testing.T) {
		meta := &HandlerMetadata{}
		clone := cloneHandlerMetadata(meta)
		require.NotNil(t, clone)
		assert.Nil(t, clone.ProducesInstruments)
		assert.Nil(t, clone.ParamOverrides)
		assert.Nil(t, clone.Conversions)
	})
}
