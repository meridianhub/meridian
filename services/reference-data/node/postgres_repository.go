package node

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

const nodeColumns = `id, tenant_id, node_type, parent_id, attributes, resolution_key,
	valid_from, valid_to, created_at, updated_at, version`

// PostgresRepository implements Repository using PostgreSQL/CockroachDB.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository creates a new PostgreSQL-backed node repository.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) setSearchPath(ctx context.Context, tx pgx.Tx) error {
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return tenant.ErrMissingTenantContext
	}
	schemaName := pq.QuoteIdentifier(tenantID.SchemaName())
	query := fmt.Sprintf("SET LOCAL search_path TO %s", schemaName)
	_, err := tx.Exec(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to set tenant schema scope: %w", err)
	}
	return nil
}

func (r *PostgresRepository) withReadTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit read transaction: %w", err)
	}
	return nil
}

func (r *PostgresRepository) withWriteTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := r.setSearchPath(ctx, tx); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return nil
}

// Create inserts a new reference data node, computing its resolution key from ancestors.
func (r *PostgresRepository) Create(ctx context.Context, n *Node) error {
	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		if err := r.validateParent(ctx, tx, n); err != nil {
			return err
		}

		ancestors, err := r.getAncestorsTx(ctx, tx, n.ParentID)
		if err != nil {
			return err
		}
		n.ResolutionKey = ComputeResolutionKey(n.NodeType, n.ID, ancestors)

		return r.insertNode(ctx, tx, n)
	})
}

// Update modifies non-key attributes of a node with optimistic locking.
func (r *PostgresRepository) Update(ctx context.Context, n *Node) error {
	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		attrsJSON, err := json.Marshal(n.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		now := time.Now()
		query := `
			UPDATE reference_data_node
			SET attributes = $1, valid_to = $2, updated_at = $3, version = version + 1
			WHERE id = $4 AND version = $5`

		result, err := tx.Exec(ctx, query, attrsJSON, n.ValidTo, now, n.ID, n.Version)
		if err != nil {
			return fmt.Errorf("failed to update node: %w", err)
		}
		if result.RowsAffected() == 0 {
			// Distinguish between not found and version conflict
			_, lookupErr := r.getByIDTx(ctx, tx, n.ID)
			if lookupErr != nil {
				if errors.Is(lookupErr, ErrNotFound) {
					return ErrNotFound
				}
				return lookupErr
			}
			return ErrOptimisticLock
		}

		n.UpdatedAt = now
		n.Version++
		return nil
	})
}

// validateParent checks that the parent node exists, is active, and belongs to the same tenant.
func (r *PostgresRepository) validateParent(ctx context.Context, tx pgx.Tx, n *Node) error {
	if n.ParentID == nil {
		return nil
	}

	parent, err := r.getByIDTx(ctx, tx, *n.ParentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrParentNotFound
		}
		return err
	}
	if !parent.IsActive() {
		return ErrParentNotActive
	}
	if parent.TenantID != n.TenantID {
		return ErrCrossTenantParent
	}
	return nil
}

// insertNode persists a node to the database.
func (r *PostgresRepository) insertNode(ctx context.Context, tx pgx.Tx, n *Node) error {
	attrsJSON, err := json.Marshal(n.Attributes)
	if err != nil {
		return fmt.Errorf("failed to marshal attributes: %w", err)
	}

	query := `
		INSERT INTO reference_data_node (
			id, tenant_id, node_type, parent_id, attributes, resolution_key,
			valid_from, valid_to, created_at, updated_at, version
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11
		)`

	_, err = tx.Exec(ctx, query,
		n.ID, n.TenantID, n.NodeType, n.ParentID, attrsJSON, n.ResolutionKey,
		n.ValidFrom, n.ValidTo, n.CreatedAt, n.UpdatedAt, n.Version,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrAlreadyExists
		}
		return fmt.Errorf("failed to insert reference data node: %w", err)
	}
	return nil
}

// GetByID retrieves a node by its UUID.
func (r *PostgresRepository) GetByID(ctx context.Context, id uuid.UUID) (*Node, error) {
	var result *Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		n, err := r.getByIDTx(ctx, tx, id)
		if err != nil {
			return err
		}
		result = n
		return nil
	})
	return result, err
}

func (r *PostgresRepository) getByIDTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Node, error) {
	query := `
		SELECT ` + nodeColumns + `
		FROM reference_data_node
		WHERE id = $1`

	row := tx.QueryRow(ctx, query, id)
	n, err := scanNode(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query node by id: %w", err)
	}
	return n, nil
}

// GetAsAt retrieves the version of a node valid at the given time.
// It first looks up the node by ID to get its resolution key lineage,
// then finds the version that was valid at asAt.
func (r *PostgresRepository) GetAsAt(ctx context.Context, tenantID string, id uuid.UUID, asAt time.Time) (*Node, error) {
	var result *Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		// First get the node to determine its resolution key prefix
		// We need to find all versions that share the same position in the tree.
		// Nodes sharing the same resolution key prefix (minus the UUID part) are versions.
		// The simplest approach: find all nodes with the same (tenant_id, node_type, parent_id)
		// where one of them has this ID, then filter by time.
		query := `
			WITH target AS (
				SELECT tenant_id, node_type, parent_id
				FROM reference_data_node
				WHERE id = $1 AND tenant_id = $2
			)
			SELECT n.id, n.tenant_id, n.node_type, n.parent_id, n.attributes, n.resolution_key,
				n.valid_from, n.valid_to, n.created_at, n.updated_at, n.version
			FROM reference_data_node n
			JOIN target t ON n.tenant_id = t.tenant_id
				AND n.node_type = t.node_type
				AND (n.parent_id = t.parent_id OR (n.parent_id IS NULL AND t.parent_id IS NULL))
			WHERE n.valid_from <= $3
				AND (n.valid_to IS NULL OR n.valid_to > $3)
			ORDER BY n.valid_from DESC
			LIMIT 1`

		row := tx.QueryRow(ctx, query, id, tenantID, asAt)
		n, err := scanNode(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query node as-at: %w", err)
		}
		result = n
		return nil
	})
	return result, err
}

// GetHistory returns all temporal versions of a node, ordered by valid_from descending.
func (r *PostgresRepository) GetHistory(ctx context.Context, tenantID string, id uuid.UUID) ([]*Node, error) {
	var result []*Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		// Find the node to get its position identity (type + parent)
		// then get all versions sharing that position
		query := `
			WITH target AS (
				SELECT tenant_id, node_type, parent_id
				FROM reference_data_node
				WHERE id = $1 AND tenant_id = $2
			)
			SELECT n.id, n.tenant_id, n.node_type, n.parent_id, n.attributes, n.resolution_key,
				n.valid_from, n.valid_to, n.created_at, n.updated_at, n.version
			FROM reference_data_node n
			JOIN target t ON n.tenant_id = t.tenant_id
				AND n.node_type = t.node_type
				AND (n.parent_id = t.parent_id OR (n.parent_id IS NULL AND t.parent_id IS NULL))
			ORDER BY n.valid_from DESC`

		rows, err := tx.Query(ctx, query, id, tenantID)
		if err != nil {
			return fmt.Errorf("failed to query node history: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			n, err := scanNodeFromRows(rows)
			if err != nil {
				return err
			}
			result = append(result, n)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		if len(result) == 0 {
			return ErrNotFound
		}
		return nil
	})
	return result, err
}

// GetByResolutionKey retrieves the node matching the resolution key at the given time.
// If asAt is zero, retrieves the active node (valid_to IS NULL).
func (r *PostgresRepository) GetByResolutionKey(ctx context.Context, tenantID, resolutionKey string, asAt time.Time) (*Node, error) {
	var result *Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		var query string
		var args []any

		if asAt.IsZero() {
			query = `
				SELECT ` + nodeColumns + `
				FROM reference_data_node
				WHERE tenant_id = $1 AND resolution_key = $2 AND valid_to IS NULL`
			args = []any{tenantID, resolutionKey}
		} else {
			query = `
				SELECT ` + nodeColumns + `
				FROM reference_data_node
				WHERE tenant_id = $1
					AND resolution_key = $2
					AND valid_from <= $3
					AND (valid_to IS NULL OR valid_to > $3)
				ORDER BY valid_from DESC
				LIMIT 1`
			args = []any{tenantID, resolutionKey, asAt}
		}

		row := tx.QueryRow(ctx, query, args...)
		n, err := scanNode(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query node by resolution key: %w", err)
		}
		result = n
		return nil
	})
	return result, err
}

// GetChildren returns child nodes of the given parent.
// If activeOnly is true, only active nodes (valid_to IS NULL) are returned.
func (r *PostgresRepository) GetChildren(ctx context.Context, tenantID string, parentID uuid.UUID, activeOnly bool) ([]*Node, error) {
	var result []*Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT ` + nodeColumns + `
			FROM reference_data_node
			WHERE tenant_id = $1 AND parent_id = $2`

		if activeOnly {
			query += ` AND valid_to IS NULL`
		}
		query += ` ORDER BY node_type, resolution_key`

		rows, err := tx.Query(ctx, query, tenantID, parentID)
		if err != nil {
			return fmt.Errorf("failed to query children: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			n, err := scanNodeFromRows(rows)
			if err != nil {
				return err
			}
			result = append(result, n)
		}
		return rows.Err()
	})
	return result, err
}

// ListByType returns all active nodes of the given type within a tenant.
func (r *PostgresRepository) ListByType(ctx context.Context, tenantID, nodeType string) ([]*Node, error) {
	var result []*Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT ` + nodeColumns + `
			FROM reference_data_node
			WHERE tenant_id = $1 AND node_type = $2 AND valid_to IS NULL
			ORDER BY resolution_key`

		rows, err := tx.Query(ctx, query, tenantID, nodeType)
		if err != nil {
			return fmt.Errorf("failed to query nodes by type: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			n, err := scanNodeFromRows(rows)
			if err != nil {
				return err
			}
			result = append(result, n)
		}
		return rows.Err()
	})
	return result, err
}

// ListRoots returns all active root nodes (no parent) within a tenant.
func (r *PostgresRepository) ListRoots(ctx context.Context, tenantID string) ([]*Node, error) {
	var result []*Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT ` + nodeColumns + `
			FROM reference_data_node
			WHERE tenant_id = $1 AND parent_id IS NULL AND valid_to IS NULL
			ORDER BY node_type, resolution_key`

		rows, err := tx.Query(ctx, query, tenantID)
		if err != nil {
			return fmt.Errorf("failed to query root nodes: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			n, err := scanNodeFromRows(rows)
			if err != nil {
				return err
			}
			result = append(result, n)
		}
		return rows.Err()
	})
	return result, err
}

// Supersede closes the current node and creates a new version.
func (r *PostgresRepository) Supersede(ctx context.Context, nodeID uuid.UUID, newNode *Node) error {
	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		existing, err := r.getByIDTx(ctx, tx, nodeID)
		if err != nil {
			return err
		}
		if !existing.IsActive() {
			return ErrAlreadySuperseded
		}

		now := time.Now()
		closeQuery := `
			UPDATE reference_data_node
			SET valid_to = $1, updated_at = $1, version = version + 1
			WHERE id = $2 AND version = $3`

		result, err := tx.Exec(ctx, closeQuery, now, nodeID, existing.Version)
		if err != nil {
			return fmt.Errorf("failed to close existing node: %w", err)
		}
		if result.RowsAffected() == 0 {
			return ErrOptimisticLock
		}

		ancestors, err := r.getAncestorsTx(ctx, tx, newNode.ParentID)
		if err != nil {
			return err
		}
		newNode.ResolutionKey = ComputeResolutionKey(newNode.NodeType, newNode.ID, ancestors)

		return r.insertNode(ctx, tx, newNode)
	})
}

// GetAncestors returns the chain of ancestors from the immediate parent to root
// using a recursive CTE.
func (r *PostgresRepository) GetAncestors(ctx context.Context, nodeID uuid.UUID) ([]*Node, error) {
	var result []*Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		n, err := r.getByIDTx(ctx, tx, nodeID)
		if err != nil {
			return err
		}

		ancestors, err := r.getAncestorsTx(ctx, tx, n.ParentID)
		if err != nil {
			return err
		}
		result = ancestors
		return nil
	})
	return result, err
}

// getAncestorsTx traverses the parent chain using a recursive CTE.
func (r *PostgresRepository) getAncestorsTx(ctx context.Context, tx pgx.Tx, parentID *uuid.UUID) ([]*Node, error) {
	if parentID == nil {
		return nil, nil
	}

	query := `
		WITH RECURSIVE ancestors AS (
			SELECT id, tenant_id, node_type, parent_id, attributes, resolution_key,
				valid_from, valid_to, created_at, updated_at, version, 1 AS depth
			FROM reference_data_node
			WHERE id = $1
			UNION ALL
			SELECT n.id, n.tenant_id, n.node_type, n.parent_id, n.attributes, n.resolution_key,
				n.valid_from, n.valid_to, n.created_at, n.updated_at, n.version, a.depth + 1
			FROM reference_data_node n
			JOIN ancestors a ON n.id = a.parent_id
			WHERE a.depth < $2
		)
		SELECT id, tenant_id, node_type, parent_id, attributes, resolution_key,
			valid_from, valid_to, created_at, updated_at, version
		FROM ancestors
		ORDER BY depth ASC`

	rows, err := tx.Query(ctx, query, parentID, MaxHierarchyDepth)
	if err != nil {
		return nil, fmt.Errorf("failed to query ancestors: %w", err)
	}
	defer rows.Close()

	var ancestors []*Node
	for rows.Next() {
		n, err := scanNodeFromRows(rows)
		if err != nil {
			return nil, err
		}
		ancestors = append(ancestors, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Check if the last ancestor still has a parent (chain exceeds max depth)
	if len(ancestors) > 0 && ancestors[len(ancestors)-1].ParentID != nil {
		if len(ancestors) >= MaxHierarchyDepth {
			return nil, ErrMaxDepthExceeded
		}
	}

	return ancestors, nil
}

// GetSubtree returns all descendants of a root node up to maxDepth levels.
// The root node itself is included at depth 0.
func (r *PostgresRepository) GetSubtree(ctx context.Context, tenantID string, rootID uuid.UUID, maxDepth int) ([]*Node, error) {
	var result []*Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		query := `
			WITH RECURSIVE subtree AS (
				SELECT id, tenant_id, node_type, parent_id, attributes, resolution_key,
					valid_from, valid_to, created_at, updated_at, version, 0 AS depth
				FROM reference_data_node
				WHERE id = $1 AND tenant_id = $2 AND valid_to IS NULL
				UNION ALL
				SELECT n.id, n.tenant_id, n.node_type, n.parent_id, n.attributes, n.resolution_key,
					n.valid_from, n.valid_to, n.created_at, n.updated_at, n.version, s.depth + 1
				FROM reference_data_node n
				JOIN subtree s ON n.parent_id = s.id
				WHERE n.tenant_id = $2 AND n.valid_to IS NULL AND s.depth < $3
			)
			SELECT id, tenant_id, node_type, parent_id, attributes, resolution_key,
				valid_from, valid_to, created_at, updated_at, version
			FROM subtree
			ORDER BY depth, node_type, resolution_key`

		rows, err := tx.Query(ctx, query, rootID, tenantID, maxDepth)
		if err != nil {
			return fmt.Errorf("failed to query subtree: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			n, err := scanNodeFromRows(rows)
			if err != nil {
				return err
			}
			result = append(result, n)
		}
		return rows.Err()
	})
	return result, err
}

// GetAtTime retrieves the node that was valid at the given effective time.
func (r *PostgresRepository) GetAtTime(ctx context.Context, tenantID, resolutionKey string, asOf time.Time) (*Node, error) {
	var result *Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT ` + nodeColumns + `
			FROM reference_data_node
			WHERE tenant_id = $1
				AND resolution_key = $2
				AND valid_from <= $3
				AND (valid_to IS NULL OR valid_to > $3)
			ORDER BY valid_from DESC
			LIMIT 1`

		row := tx.QueryRow(ctx, query, tenantID, resolutionKey, asOf)
		n, err := scanNode(row)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("failed to query node at time: %w", err)
		}
		result = n
		return nil
	})
	return result, err
}

// BulkCreate inserts multiple nodes in a single transaction.
// Nodes should be ordered so that parents appear before their children.
func (r *PostgresRepository) BulkCreate(ctx context.Context, nodes []*Node) error {
	return r.withWriteTx(ctx, func(tx pgx.Tx) error {
		batchIDs := make(map[uuid.UUID]*Node, len(nodes))
		for _, n := range nodes {
			batchIDs[n.ID] = n
		}

		for _, n := range nodes {
			if err := r.bulkCreateNode(ctx, tx, n, batchIDs); err != nil {
				return err
			}
		}
		return nil
	})
}

// bulkCreateNode validates, computes resolution key, and inserts a single node during bulk create.
func (r *PostgresRepository) bulkCreateNode(ctx context.Context, tx pgx.Tx, n *Node, batchIDs map[uuid.UUID]*Node) error {
	if n.ParentID != nil {
		if parentNode, inBatch := batchIDs[*n.ParentID]; inBatch {
			if parentNode.TenantID != n.TenantID {
				return ErrCrossTenantParent
			}
			if !parentNode.IsActive() {
				return ErrParentNotActive
			}
		} else {
			if err := r.validateParent(ctx, tx, n); err != nil {
				return err
			}
		}
	}

	ancestors, err := r.getAncestorsForBulk(ctx, tx, n, batchIDs)
	if err != nil {
		return err
	}
	n.ResolutionKey = ComputeResolutionKey(n.NodeType, n.ID, ancestors)

	return r.insertNode(ctx, tx, n)
}

// getAncestorsForBulk computes ancestors considering both the batch and the database.
func (r *PostgresRepository) getAncestorsForBulk(ctx context.Context, tx pgx.Tx, n *Node, batch map[uuid.UUID]*Node) ([]*Node, error) {
	if n.ParentID == nil {
		return nil, nil
	}

	var ancestors []*Node
	currentParentID := n.ParentID

	for i := 0; i < MaxHierarchyDepth; i++ {
		if currentParentID == nil {
			break
		}

		// Check if parent is in the current batch (already inserted in this tx)
		if batchNode, ok := batch[*currentParentID]; ok {
			ancestors = append(ancestors, batchNode)
			currentParentID = batchNode.ParentID
			continue
		}

		// Otherwise look up in database
		parent, err := r.getByIDTx(ctx, tx, *currentParentID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, ErrParentNotFound
			}
			return nil, err
		}
		ancestors = append(ancestors, parent)
		currentParentID = parent.ParentID
	}

	if currentParentID != nil {
		return nil, ErrMaxDepthExceeded
	}

	return ancestors, nil
}

// scanNode scans a single row into a Node.
func scanNode(row pgx.Row) (*Node, error) {
	var n Node
	var parentID *uuid.UUID
	var attrsJSON []byte
	var validTo sql.NullTime

	err := row.Scan(
		&n.ID, &n.TenantID, &n.NodeType, &parentID, &attrsJSON, &n.ResolutionKey,
		&n.ValidFrom, &validTo, &n.CreatedAt, &n.UpdatedAt, &n.Version,
	)
	if err != nil {
		return nil, err
	}

	n.ParentID = parentID
	if validTo.Valid {
		n.ValidTo = &validTo.Time
	}

	if err := json.Unmarshal(attrsJSON, &n.Attributes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
	}

	return &n, nil
}

// scanNodeFromRows scans from pgx.Rows into a Node.
func scanNodeFromRows(rows pgx.Rows) (*Node, error) {
	var n Node
	var parentID *uuid.UUID
	var attrsJSON []byte
	var validTo sql.NullTime

	err := rows.Scan(
		&n.ID, &n.TenantID, &n.NodeType, &parentID, &attrsJSON, &n.ResolutionKey,
		&n.ValidFrom, &validTo, &n.CreatedAt, &n.UpdatedAt, &n.Version,
	)
	if err != nil {
		return nil, err
	}

	n.ParentID = parentID
	if validTo.Valid {
		n.ValidTo = &validTo.Time
	}

	if err := json.Unmarshal(attrsJSON, &n.Attributes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
	}

	return &n, nil
}
