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
	query := fmt.Sprintf("SET LOCAL search_path TO %s, public", schemaName)
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
		SELECT id, tenant_id, node_type, parent_id, attributes, resolution_key,
			valid_from, valid_to, created_at, updated_at, version
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

// GetByResolutionKey retrieves the active node matching the resolution key.
func (r *PostgresRepository) GetByResolutionKey(ctx context.Context, tenantID, resolutionKey string) (*Node, error) {
	var result *Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, tenant_id, node_type, parent_id, attributes, resolution_key,
				valid_from, valid_to, created_at, updated_at, version
			FROM reference_data_node
			WHERE tenant_id = $1 AND resolution_key = $2 AND valid_to IS NULL`

		row := tx.QueryRow(ctx, query, tenantID, resolutionKey)
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

// ListChildren returns all active child nodes of the given parent.
func (r *PostgresRepository) ListChildren(ctx context.Context, tenantID string, parentID uuid.UUID) ([]*Node, error) {
	var result []*Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, tenant_id, node_type, parent_id, attributes, resolution_key,
				valid_from, valid_to, created_at, updated_at, version
			FROM reference_data_node
			WHERE tenant_id = $1 AND parent_id = $2 AND valid_to IS NULL
			ORDER BY node_type, resolution_key`

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
			SELECT id, tenant_id, node_type, parent_id, attributes, resolution_key,
				valid_from, valid_to, created_at, updated_at, version
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
			SELECT id, tenant_id, node_type, parent_id, attributes, resolution_key,
				valid_from, valid_to, created_at, updated_at, version
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
		// Get the existing node
		existing, err := r.getByIDTx(ctx, tx, nodeID)
		if err != nil {
			return err
		}
		if !existing.IsActive() {
			return ErrAlreadySuperseded
		}

		// Close the existing node
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

		// Compute resolution key for the new node
		ancestors, err := r.getAncestorsTx(ctx, tx, newNode.ParentID)
		if err != nil {
			return err
		}
		newNode.ResolutionKey = ComputeResolutionKey(newNode.NodeType, newNode.ID, ancestors)

		return r.insertNode(ctx, tx, newNode)
	})
}

// GetAncestors returns the chain of ancestors from the immediate parent to root.
func (r *PostgresRepository) GetAncestors(ctx context.Context, nodeID uuid.UUID) ([]*Node, error) {
	var result []*Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		// Get the node to find its parent
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

// getAncestorsTx traverses the parent chain iteratively up to MaxHierarchyDepth.
func (r *PostgresRepository) getAncestorsTx(ctx context.Context, tx pgx.Tx, parentID *uuid.UUID) ([]*Node, error) {
	if parentID == nil {
		return nil, nil
	}

	var ancestors []*Node
	currentParentID := parentID

	for i := 0; i < MaxHierarchyDepth; i++ {
		if currentParentID == nil {
			break
		}

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

// GetAtTime retrieves the node that was valid at the given effective time.
func (r *PostgresRepository) GetAtTime(ctx context.Context, tenantID, resolutionKey string, asOf time.Time) (*Node, error) {
	var result *Node
	err := r.withReadTx(ctx, func(tx pgx.Tx) error {
		query := `
			SELECT id, tenant_id, node_type, parent_id, attributes, resolution_key,
				valid_from, valid_to, created_at, updated_at, version
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
