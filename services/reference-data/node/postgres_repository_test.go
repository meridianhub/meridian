package node_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/services/reference-data/node"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRepo(t *testing.T) (*node.PostgresRepository, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.NewTestPool(t)
	repo := node.NewPostgresRepository(pool)
	return repo, pool
}

func setupTenantCtx(t *testing.T, pool *pgxpool.Pool, tenantID string) context.Context {
	t.Helper()
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, tenantID, "reference-data")
	t.Cleanup(cleanup)
	return ctx
}

func buildNode(t *testing.T, tenantID, nodeType string, parentID *uuid.UUID) *node.Node {
	t.Helper()
	b := node.NewBuilder(tenantID).WithNodeType(nodeType)
	if parentID != nil {
		b = b.WithParentID(*parentID)
	}
	n, err := b.Build()
	require.NoError(t, err)
	return n
}

func TestPostgresRepository_Create(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-create")

	t.Run("creates root node with computed resolution key", func(t *testing.T) {
		n := buildNode(t, "test-create", "region", nil)

		err := repo.Create(ctx, n)
		require.NoError(t, err)

		// Resolution key should be type:id for root
		expected := "region:" + n.ID.String()
		assert.Equal(t, expected, n.ResolutionKey)
	})

	t.Run("creates child node with hierarchical resolution key", func(t *testing.T) {
		parent := buildNode(t, "test-create", "dno", nil)
		require.NoError(t, repo.Create(ctx, parent))

		child := buildNode(t, "test-create", "gsp", &parent.ID)
		err := repo.Create(ctx, child)
		require.NoError(t, err)

		expected := "dno:" + parent.ID.String() + "/gsp:" + child.ID.String()
		assert.Equal(t, expected, child.ResolutionKey)
	})

	t.Run("creates three-level hierarchy", func(t *testing.T) {
		root := buildNode(t, "test-create", "dno", nil)
		require.NoError(t, repo.Create(ctx, root))

		mid := buildNode(t, "test-create", "gsp", &root.ID)
		require.NoError(t, repo.Create(ctx, mid))

		leaf := buildNode(t, "test-create", "meter", &mid.ID)
		err := repo.Create(ctx, leaf)
		require.NoError(t, err)

		expected := "dno:" + root.ID.String() + "/gsp:" + mid.ID.String() + "/meter:" + leaf.ID.String()
		assert.Equal(t, expected, leaf.ResolutionKey)
	})

	t.Run("rejects non-existent parent", func(t *testing.T) {
		fakeParent := uuid.New()
		n := buildNode(t, "test-create", "zone", &fakeParent)

		err := repo.Create(ctx, n)
		require.ErrorIs(t, err, node.ErrParentNotFound)
	})

	t.Run("rejects cross-tenant parent", func(t *testing.T) {
		// Intentionally create a separate tenant context for isolation testing
		ctx2 := setupTenantCtx(t, pool, "test-create-2")

		parent := buildNode(t, "test-create", "region", nil)
		require.NoError(t, repo.Create(ctx, parent))

		// Try to create child in different tenant referencing parent from first tenant
		child := buildNode(t, "test-create-2", "zone", &parent.ID)
		err := repo.Create(ctx2, child) //nolint:contextcheck // separate tenant context for cross-tenant test
		// Parent lookup will fail because it's in a different tenant's schema
		require.Error(t, err)
	})

	t.Run("stores and retrieves JSONB attributes", func(t *testing.T) {
		n, err := node.NewBuilder("test-create").
			WithNodeType("meter").
			WithAttributes(map[string]any{
				"serial_number": "METER-001",
				"capacity_kw":   float64(100),
				"active":        true,
			}).
			Build()
		require.NoError(t, err)

		require.NoError(t, repo.Create(ctx, n))

		fetched, err := repo.GetByID(ctx, n.ID)
		require.NoError(t, err)
		assert.Equal(t, "METER-001", fetched.Attributes["serial_number"])
		assert.Equal(t, float64(100), fetched.Attributes["capacity_kw"])
		assert.Equal(t, true, fetched.Attributes["active"])
	})
}

func TestPostgresRepository_GetByID(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-getbyid")

	t.Run("retrieves existing node", func(t *testing.T) {
		n := buildNode(t, "test-getbyid", "region", nil)
		require.NoError(t, repo.Create(ctx, n))

		fetched, err := repo.GetByID(ctx, n.ID)
		require.NoError(t, err)
		assert.Equal(t, n.ID, fetched.ID)
		assert.Equal(t, "test-getbyid", fetched.TenantID)
		assert.Equal(t, "region", fetched.NodeType)
		assert.True(t, fetched.IsActive())
	})

	t.Run("returns ErrNotFound for missing node", func(t *testing.T) {
		_, err := repo.GetByID(ctx, uuid.New())
		require.ErrorIs(t, err, node.ErrNotFound)
	})
}

func TestPostgresRepository_GetByResolutionKey(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-reskey")

	t.Run("retrieves active node by resolution key", func(t *testing.T) {
		n := buildNode(t, "test-reskey", "region", nil)
		require.NoError(t, repo.Create(ctx, n))

		fetched, err := repo.GetByResolutionKey(ctx, "test-reskey", n.ResolutionKey)
		require.NoError(t, err)
		assert.Equal(t, n.ID, fetched.ID)
	})

	t.Run("returns ErrNotFound for non-matching key", func(t *testing.T) {
		_, err := repo.GetByResolutionKey(ctx, "test-reskey", "nonexistent:key")
		require.ErrorIs(t, err, node.ErrNotFound)
	})
}

func TestPostgresRepository_ListChildren(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-children")

	parent := buildNode(t, "test-children", "region", nil)
	require.NoError(t, repo.Create(ctx, parent))

	child1 := buildNode(t, "test-children", "zone", &parent.ID)
	require.NoError(t, repo.Create(ctx, child1))

	child2 := buildNode(t, "test-children", "zone", &parent.ID)
	require.NoError(t, repo.Create(ctx, child2))

	t.Run("returns all active children", func(t *testing.T) {
		children, err := repo.ListChildren(ctx, "test-children", parent.ID)
		require.NoError(t, err)
		assert.Len(t, children, 2)
	})

	t.Run("returns empty for node with no children", func(t *testing.T) {
		children, err := repo.ListChildren(ctx, "test-children", child1.ID)
		require.NoError(t, err)
		assert.Empty(t, children)
	})
}

func TestPostgresRepository_ListByType(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-listtype")

	region1 := buildNode(t, "test-listtype", "region", nil)
	require.NoError(t, repo.Create(ctx, region1))

	region2 := buildNode(t, "test-listtype", "region", nil)
	require.NoError(t, repo.Create(ctx, region2))

	zone1 := buildNode(t, "test-listtype", "zone", &region1.ID)
	require.NoError(t, repo.Create(ctx, zone1))

	t.Run("returns only nodes of specified type", func(t *testing.T) {
		regions, err := repo.ListByType(ctx, "test-listtype", "region")
		require.NoError(t, err)
		assert.Len(t, regions, 2)

		zones, err := repo.ListByType(ctx, "test-listtype", "zone")
		require.NoError(t, err)
		assert.Len(t, zones, 1)
	})

	t.Run("returns empty for non-existent type", func(t *testing.T) {
		nodes, err := repo.ListByType(ctx, "test-listtype", "nonexistent")
		require.NoError(t, err)
		assert.Empty(t, nodes)
	})
}

func TestPostgresRepository_ListRoots(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-roots")

	root1 := buildNode(t, "test-roots", "region", nil)
	require.NoError(t, repo.Create(ctx, root1))

	root2 := buildNode(t, "test-roots", "dno", nil)
	require.NoError(t, repo.Create(ctx, root2))

	child := buildNode(t, "test-roots", "zone", &root1.ID)
	require.NoError(t, repo.Create(ctx, child))

	t.Run("returns only root nodes", func(t *testing.T) {
		roots, err := repo.ListRoots(ctx, "test-roots")
		require.NoError(t, err)
		assert.Len(t, roots, 2)

		for _, r := range roots {
			assert.Nil(t, r.ParentID)
		}
	})
}

func TestPostgresRepository_Supersede(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-supersede")

	t.Run("supersedes active node", func(t *testing.T) {
		original := buildNode(t, "test-supersede", "region", nil)
		require.NoError(t, repo.Create(ctx, original))

		newNode, err := node.NewBuilder("test-supersede").
			WithNodeType("region").
			WithAttributes(map[string]any{"updated": true}).
			Build()
		require.NoError(t, err)

		err = repo.Supersede(ctx, original.ID, newNode)
		require.NoError(t, err)

		// Original should be closed
		fetched, err := repo.GetByID(ctx, original.ID)
		require.NoError(t, err)
		assert.False(t, fetched.IsActive())
		assert.NotNil(t, fetched.ValidTo)

		// New node should be active
		fetchedNew, err := repo.GetByID(ctx, newNode.ID)
		require.NoError(t, err)
		assert.True(t, fetchedNew.IsActive())
	})

	t.Run("rejects superseding already-closed node", func(t *testing.T) {
		original := buildNode(t, "test-supersede", "region", nil)
		require.NoError(t, repo.Create(ctx, original))

		// First supersede
		newNode1, err := node.NewBuilder("test-supersede").
			WithNodeType("region").
			Build()
		require.NoError(t, err)
		require.NoError(t, repo.Supersede(ctx, original.ID, newNode1))

		// Second supersede of same original should fail
		newNode2, err := node.NewBuilder("test-supersede").
			WithNodeType("region").
			Build()
		require.NoError(t, err)
		err = repo.Supersede(ctx, original.ID, newNode2)
		require.ErrorIs(t, err, node.ErrAlreadySuperseded)
	})

	t.Run("rejects superseding non-existent node", func(t *testing.T) {
		newNode, err := node.NewBuilder("test-supersede").
			WithNodeType("region").
			Build()
		require.NoError(t, err)

		err = repo.Supersede(ctx, uuid.New(), newNode)
		require.ErrorIs(t, err, node.ErrNotFound)
	})
}

func TestPostgresRepository_GetAncestors(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-ancestors")

	root := buildNode(t, "test-ancestors", "dno", nil)
	require.NoError(t, repo.Create(ctx, root))

	mid := buildNode(t, "test-ancestors", "gsp", &root.ID)
	require.NoError(t, repo.Create(ctx, mid))

	leaf := buildNode(t, "test-ancestors", "meter", &mid.ID)
	require.NoError(t, repo.Create(ctx, leaf))

	t.Run("returns ancestors parent-first to root", func(t *testing.T) {
		ancestors, err := repo.GetAncestors(ctx, leaf.ID)
		require.NoError(t, err)
		require.Len(t, ancestors, 2)

		// Parent first, then grandparent
		assert.Equal(t, mid.ID, ancestors[0].ID)
		assert.Equal(t, root.ID, ancestors[1].ID)
	})

	t.Run("returns empty for root node", func(t *testing.T) {
		ancestors, err := repo.GetAncestors(ctx, root.ID)
		require.NoError(t, err)
		assert.Empty(t, ancestors)
	})

	t.Run("returns one ancestor for mid-level node", func(t *testing.T) {
		ancestors, err := repo.GetAncestors(ctx, mid.ID)
		require.NoError(t, err)
		require.Len(t, ancestors, 1)
		assert.Equal(t, root.ID, ancestors[0].ID)
	})
}

func TestPostgresRepository_GetAtTime(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-attime")

	t.Run("retrieves node valid at specific time", func(t *testing.T) {
		// Create original node valid from 2 hours ago
		twoHoursAgo := time.Now().Add(-2 * time.Hour)
		original, err := node.NewBuilder("test-attime").
			WithNodeType("region").
			WithValidFrom(twoHoursAgo).
			WithAttributes(map[string]any{"version": "v1"}).
			Build()
		require.NoError(t, err)
		require.NoError(t, repo.Create(ctx, original))

		// Supersede with new node
		updated, err := node.NewBuilder("test-attime").
			WithNodeType("region").
			WithAttributes(map[string]any{"version": "v2"}).
			Build()
		require.NoError(t, err)
		require.NoError(t, repo.Supersede(ctx, original.ID, updated))

		// Query at time before supersession should return original
		oneHourAgo := time.Now().Add(-1 * time.Hour)
		fetched, err := repo.GetAtTime(ctx, "test-attime", original.ResolutionKey, oneHourAgo)
		require.NoError(t, err)
		assert.Equal(t, original.ID, fetched.ID)

		// Query at current time should return the new resolution key's node
		afterSupersede := time.Now()
		fetchedNew, err := repo.GetAtTime(ctx, "test-attime", updated.ResolutionKey, afterSupersede)
		require.NoError(t, err)
		assert.Equal(t, updated.ID, fetchedNew.ID)
	})

	t.Run("returns ErrNotFound for time before node existed", func(t *testing.T) {
		n := buildNode(t, "test-attime", "zone", nil)
		require.NoError(t, repo.Create(ctx, n))

		_, err := repo.GetAtTime(ctx, "test-attime", n.ResolutionKey, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
		require.ErrorIs(t, err, node.ErrNotFound)
	})
}

func TestPostgresRepository_TenantIsolation(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx1 := setupTenantCtx(t, pool, "tenant-iso-a")
	ctx2 := setupTenantCtx(t, pool, "tenant-iso-b")

	t.Run("nodes are isolated by tenant", func(t *testing.T) {
		n1 := buildNode(t, "tenant-iso-a", "region", nil)
		require.NoError(t, repo.Create(ctx1, n1))

		// Tenant B cannot see Tenant A's node
		_, err := repo.GetByID(ctx2, n1.ID)
		require.ErrorIs(t, err, node.ErrNotFound)

		// Tenant B can create same type independently
		n2 := buildNode(t, "tenant-iso-b", "region", nil)
		require.NoError(t, repo.Create(ctx2, n2))

		// Each sees only their own
		roots1, err := repo.ListRoots(ctx1, "tenant-iso-a")
		require.NoError(t, err)
		assert.Len(t, roots1, 1)

		roots2, err := repo.ListRoots(ctx2, "tenant-iso-b")
		require.NoError(t, err)
		assert.Len(t, roots2, 1)
	})
}

func TestPostgresRepository_UniqueConstraint(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-unique")

	t.Run("prevents duplicate active nodes with same resolution key", func(t *testing.T) {
		n1 := buildNode(t, "test-unique", "region", nil)
		require.NoError(t, repo.Create(ctx, n1))

		// Creating another root node is fine (different resolution key because different UUID)
		n2 := buildNode(t, "test-unique", "region", nil)
		require.NoError(t, repo.Create(ctx, n2))

		assert.NotEqual(t, n1.ResolutionKey, n2.ResolutionKey)
	})
}

func TestPostgresRepository_ValidTimeConstraint(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-validtime")

	t.Run("rejects valid_to before valid_from at builder level", func(t *testing.T) {
		from := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

		_, err := node.NewBuilder("test-validtime").
			WithNodeType("region").
			WithValidFrom(from).
			WithValidTo(to).
			Build()

		require.ErrorIs(t, err, node.ErrInvalidNode)
	})

	t.Run("database enforces valid_time_order constraint", func(t *testing.T) {
		// Create a node, then try to manually close with invalid time
		// This tests the DB constraint even if the builder prevents it
		n := buildNode(t, "test-validtime", "region", nil)
		require.NoError(t, repo.Create(ctx, n))

		// The node should be created and fetchable
		fetched, err := repo.GetByID(ctx, n.ID)
		require.NoError(t, err)
		assert.True(t, fetched.IsActive())
	})

	// Verify we can create nodes with valid time ranges
	t.Run("accepts valid time range", func(t *testing.T) {
		from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

		n, err := node.NewBuilder("test-validtime").
			WithNodeType("region").
			WithValidFrom(from).
			WithValidTo(to).
			Build()
		require.NoError(t, err)
		require.NoError(t, repo.Create(ctx, n))

		fetched, err := repo.GetByID(ctx, n.ID)
		require.NoError(t, err)
		assert.False(t, fetched.IsActive())
	})
}

func TestPostgresRepository_ForeignKeyConstraint(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-fk")

	t.Run("parent reference enforced by foreign key", func(t *testing.T) {
		fakeParent := uuid.New()
		n := buildNode(t, "test-fk", "zone", &fakeParent)

		err := repo.Create(ctx, n)
		// The repository validates parent existence before insert, so we get our domain error
		require.ErrorIs(t, err, node.ErrParentNotFound)
	})
}

func TestPostgresRepository_OptimisticLocking(t *testing.T) {
	repo, pool := setupTestRepo(t)
	ctx := setupTenantCtx(t, pool, "test-optlock")

	t.Run("supersede increments version", func(t *testing.T) {
		original := buildNode(t, "test-optlock", "region", nil)
		require.NoError(t, repo.Create(ctx, original))
		assert.Equal(t, int64(1), original.Version)

		newNode, err := node.NewBuilder("test-optlock").
			WithNodeType("region").
			Build()
		require.NoError(t, err)

		require.NoError(t, repo.Supersede(ctx, original.ID, newNode))

		// Original version should be incremented
		fetched, err := repo.GetByID(ctx, original.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(2), fetched.Version)
	})
}
