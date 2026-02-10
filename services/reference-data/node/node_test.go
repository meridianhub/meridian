package node_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reference-data/node"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuilder_Build(t *testing.T) {
	t.Run("builds valid root node", func(t *testing.T) {
		n, err := node.NewBuilder("tenant-1").
			WithNodeType("region").
			WithAttributes(map[string]any{"name": "UK"}).
			Build()

		require.NoError(t, err)
		assert.Equal(t, "tenant-1", n.TenantID)
		assert.Equal(t, "region", n.NodeType)
		assert.Nil(t, n.ParentID)
		assert.NotEqual(t, uuid.Nil, n.ID)
		assert.Equal(t, int64(1), n.Version)
		assert.True(t, n.IsActive())
		assert.Equal(t, map[string]any{"name": "UK"}, n.Attributes)
	})

	t.Run("builds valid child node", func(t *testing.T) {
		parentID := uuid.New()
		n, err := node.NewBuilder("tenant-1").
			WithNodeType("zone").
			WithParentID(parentID).
			Build()

		require.NoError(t, err)
		require.NotNil(t, n.ParentID)
		assert.Equal(t, parentID, *n.ParentID)
	})

	t.Run("sets explicit ID", func(t *testing.T) {
		id := uuid.New()
		n, err := node.NewBuilder("tenant-1").
			WithNodeType("region").
			WithID(id).
			Build()

		require.NoError(t, err)
		assert.Equal(t, id, n.ID)
	})

	t.Run("sets explicit valid_from", func(t *testing.T) {
		from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		n, err := node.NewBuilder("tenant-1").
			WithNodeType("region").
			WithValidFrom(from).
			Build()

		require.NoError(t, err)
		assert.Equal(t, from, n.ValidFrom)
	})

	t.Run("sets valid_to", func(t *testing.T) {
		from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
		n, err := node.NewBuilder("tenant-1").
			WithNodeType("region").
			WithValidFrom(from).
			WithValidTo(to).
			Build()

		require.NoError(t, err)
		require.NotNil(t, n.ValidTo)
		assert.Equal(t, to, *n.ValidTo)
		assert.False(t, n.IsActive())
	})

	t.Run("rejects empty tenant_id", func(t *testing.T) {
		_, err := node.NewBuilder("").
			WithNodeType("region").
			Build()

		require.ErrorIs(t, err, node.ErrInvalidNode)
		assert.Contains(t, err.Error(), "tenant_id is required")
	})

	t.Run("rejects empty node_type", func(t *testing.T) {
		_, err := node.NewBuilder("tenant-1").
			Build()

		require.ErrorIs(t, err, node.ErrInvalidNode)
		assert.Contains(t, err.Error(), "node_type is required")
	})

	t.Run("rejects valid_to before valid_from", func(t *testing.T) {
		from := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

		_, err := node.NewBuilder("tenant-1").
			WithNodeType("region").
			WithValidFrom(from).
			WithValidTo(to).
			Build()

		require.ErrorIs(t, err, node.ErrInvalidNode)
		assert.Contains(t, err.Error(), "valid_to must be after valid_from")
	})

	t.Run("collects multiple validation errors", func(t *testing.T) {
		_, err := node.NewBuilder("").
			Build()

		require.ErrorIs(t, err, node.ErrInvalidNode)
		assert.Contains(t, err.Error(), "tenant_id is required")
		assert.Contains(t, err.Error(), "node_type is required")
	})

	t.Run("defaults attributes to empty map", func(t *testing.T) {
		n, err := node.NewBuilder("tenant-1").
			WithNodeType("region").
			Build()

		require.NoError(t, err)
		assert.NotNil(t, n.Attributes)
		assert.Empty(t, n.Attributes)
	})
}

func TestComputeResolutionKey(t *testing.T) {
	t.Run("root node has simple key", func(t *testing.T) {
		id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
		key := node.ComputeResolutionKey("region", id, nil)
		assert.Equal(t, "region:11111111-1111-1111-1111-111111111111", key)
	})

	t.Run("child node includes parent", func(t *testing.T) {
		parentID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
		childID := uuid.MustParse("22222222-2222-2222-2222-222222222222")

		parent := &node.Node{
			ID:       parentID,
			NodeType: "region",
		}

		key := node.ComputeResolutionKey("zone", childID, []*node.Node{parent})
		assert.Equal(t, "region:11111111-1111-1111-1111-111111111111/zone:22222222-2222-2222-2222-222222222222", key)
	})

	t.Run("three-level hierarchy", func(t *testing.T) {
		rootID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
		midID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
		leafID := uuid.MustParse("33333333-3333-3333-3333-333333333333")

		root := &node.Node{ID: rootID, NodeType: "dno"}
		mid := &node.Node{ID: midID, NodeType: "gsp"}

		// Ancestors are ordered parent-first (immediate parent, then grandparent, etc.)
		key := node.ComputeResolutionKey("meter", leafID, []*node.Node{mid, root})
		assert.Equal(t,
			"dno:11111111-1111-1111-1111-111111111111/gsp:22222222-2222-2222-2222-222222222222/meter:33333333-3333-3333-3333-333333333333",
			key,
		)
	})

	t.Run("empty ancestors same as nil", func(t *testing.T) {
		id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
		key := node.ComputeResolutionKey("region", id, []*node.Node{})
		assert.Equal(t, "region:11111111-1111-1111-1111-111111111111", key)
	})
}

func TestNode_IsActive(t *testing.T) {
	t.Run("active when valid_to is nil", func(t *testing.T) {
		n := &node.Node{ValidTo: nil}
		assert.True(t, n.IsActive())
	})

	t.Run("not active when valid_to is set", func(t *testing.T) {
		now := time.Now()
		n := &node.Node{ValidTo: &now}
		assert.False(t, n.IsActive())
	})
}
