package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandlerMetadata_Basics tests the handler metadata types compile.
func TestHandlerMetadata_Basics(t *testing.T) {
	md := &HandlerMetadata{
		Category:            HandlerCategorySettlement,
		ProducesInstruments: []string{"USD", "NZD"},
	}

	assert.Equal(t, HandlerCategorySettlement, md.Category)
	assert.Equal(t, []string{"USD", "NZD"}, md.ProducesInstruments)
}

// TestHandlerCategory_Constants tests handler category constants.
func TestHandlerCategory_Constants(t *testing.T) {
	assert.Equal(t, HandlerCategory("ingestion"), HandlerCategoryIngestion)
	assert.Equal(t, HandlerCategory("settlement"), HandlerCategorySettlement)
	assert.Equal(t, HandlerCategory("valuation"), HandlerCategoryValuation)
}

// TestHandlerRegistry_MetadataOperations tests metadata-aware registry operations.
func TestHandlerRegistry_MetadataOperations(t *testing.T) {
	registry := NewHandlerRegistry()

	handler := func(_ *StarlarkContext, _ map[string]any) (any, error) {
		return "result", nil
	}

	t.Run("RegisterWithMetadata and GetWithMetadata", func(t *testing.T) {
		metadata := &HandlerMetadata{
			Category:            HandlerCategorySettlement,
			ProducesInstruments: []string{"USD"},
		}

		err := registry.RegisterWithMetadata("test.handler", handler, metadata)
		require.NoError(t, err)

		// Verify handler is registered
		assert.True(t, registry.Has("test.handler"))

		// Verify metadata is stored
		h, md, err := registry.GetWithMetadata("test.handler")
		require.NoError(t, err)
		require.NotNil(t, h)
		require.NotNil(t, md)
		assert.Equal(t, HandlerCategorySettlement, md.Category)
		assert.Equal(t, []string{"USD"}, md.ProducesInstruments)
	})

	t.Run("Register without metadata - backward compatibility", func(t *testing.T) {
		err := registry.Register("test.compat", handler)
		require.NoError(t, err)

		// Get with metadata should return nil metadata
		h, md, err := registry.GetWithMetadata("test.compat")
		require.NoError(t, err)
		require.NotNil(t, h)
		assert.Nil(t, md, "handlers registered without metadata should return nil metadata")
	})

	t.Run("Get backward compatibility", func(t *testing.T) {
		metadata := &HandlerMetadata{
			Category:            HandlerCategoryValuation,
			ProducesInstruments: []string{"KWH"},
		}

		err := registry.RegisterWithMetadata("test.val", handler, metadata)
		require.NoError(t, err)

		// Old Get method should still work
		h, err := registry.Get("test.val")
		require.NoError(t, err)
		require.NotNil(t, h)
	})
}
