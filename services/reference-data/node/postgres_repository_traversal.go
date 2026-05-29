package node

// Tree traversal, bulk creation, and row-scanning helpers for PostgresRepository.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

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
