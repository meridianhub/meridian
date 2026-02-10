// Package admin provides administrative services for the control plane,
// including the CFO Glass Box causation tree visualizer.
package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

var (
	// ErrPositionNotFound is returned when a financial position log cannot be found.
	ErrPositionNotFound = errors.New("financial position log not found")
	// ErrTransactionNotFound is returned when a transaction cannot be found.
	ErrTransactionNotFound = errors.New("transaction not found")
	// ErrEventNotFound is returned when an event cannot be found.
	ErrEventNotFound = errors.New("event not found")
	// ErrNoSagaFound is returned when no saga can be traced for the given entity.
	ErrNoSagaFound = errors.New("no saga found for the given entity")
)

// PositionInfo contains metadata about a traced position.
type PositionInfo struct {
	PositionID uuid.UUID
	AccountID  string
}

// CausationVisualizer traces financial positions, transactions, and events
// back to their originating saga instances and retrieves the full causation tree.
//
// This enables the "Glass Box" pattern: click a balance sheet line item,
// see every saga step that produced it.
type CausationVisualizer struct {
	db       *gorm.DB
	treeRepo *saga.CausationTreeRepository
	logger   *slog.Logger
}

// NewCausationVisualizer creates a new CausationVisualizer.
func NewCausationVisualizer(db *gorm.DB, treeRepo *saga.CausationTreeRepository, logger *slog.Logger) *CausationVisualizer {
	if logger == nil {
		logger = slog.Default()
	}
	return &CausationVisualizer{
		db:       db,
		treeRepo: treeRepo,
		logger:   logger,
	}
}

// CausationTreeResult contains the causation tree along with tracing metadata.
type CausationTreeResult struct {
	Tree   *saga.CausationTreeNode
	Depth  int
	SagaID uuid.UUID
}

// GetCausationTreeForPosition traces a financial position log back to the
// saga that created it, then returns the full causation tree.
//
// Tracing path: position_log -> transaction_log_entry -> idempotency_key -> saga_instance
func (v *CausationVisualizer) GetCausationTreeForPosition(ctx context.Context, positionID uuid.UUID) (*CausationTreeResult, *PositionInfo, error) {
	v.logger.Debug("tracing causation tree for position",
		"position_id", positionID,
	)

	// Find the saga associated with this position by querying saga_step_results
	// whose idempotency_key references a position-keeping operation.
	// Saga step idempotency keys follow the pattern: saga_{instance_id}_step_{index}
	sagaID, accountID, err := v.findSagaForPosition(ctx, positionID)
	if err != nil {
		return nil, nil, err
	}

	tree, depth, err := v.getCausationTree(ctx, sagaID)
	if err != nil {
		return nil, nil, err
	}

	return &CausationTreeResult{
			Tree:   tree,
			Depth:  depth,
			SagaID: sagaID,
		}, &PositionInfo{
			PositionID: positionID,
			AccountID:  accountID,
		}, nil
}

// GetCausationTreeForTransaction traces a transaction back to the saga that
// created it, then returns the full causation tree.
//
// Tracing path: transaction_id -> saga_step_results (via correlation_id) -> saga_instance
func (v *CausationVisualizer) GetCausationTreeForTransaction(ctx context.Context, transactionID uuid.UUID) (*CausationTreeResult, error) {
	v.logger.Debug("tracing causation tree for transaction",
		"transaction_id", transactionID,
	)

	sagaID, err := v.findSagaForTransaction(ctx, transactionID)
	if err != nil {
		return nil, err
	}

	tree, depth, err := v.getCausationTree(ctx, sagaID)
	if err != nil {
		return nil, err
	}

	return &CausationTreeResult{
		Tree:   tree,
		Depth:  depth,
		SagaID: sagaID,
	}, nil
}

// GetCausationTreeForEvent traces an event back through the saga execution
// chain using its causation_id, then returns the full causation tree.
//
// Tracing path: event_id -> event_outbox (causation_id) -> saga_step_results -> saga_instance
func (v *CausationVisualizer) GetCausationTreeForEvent(ctx context.Context, eventID uuid.UUID) (*CausationTreeResult, error) {
	v.logger.Debug("tracing causation tree for event",
		"event_id", eventID,
	)

	sagaID, err := v.findSagaForEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}

	tree, depth, err := v.getCausationTree(ctx, sagaID)
	if err != nil {
		return nil, err
	}

	return &CausationTreeResult{
		Tree:   tree,
		Depth:  depth,
		SagaID: sagaID,
	}, nil
}

// findSagaForPosition finds the root saga instance for a given position log.
// It queries the saga_instances table looking for sagas whose correlation_id
// matches the position's correlation chain.
func (v *CausationVisualizer) findSagaForPosition(ctx context.Context, positionID uuid.UUID) (uuid.UUID, string, error) {
	// Query: find a saga instance that has step results referencing this position.
	// The link is through the saga step's idempotency key or correlation_id that
	// matches data in the position-keeping service's transaction entries.
	//
	// Strategy: Look for saga_instances whose correlation_id appears in the
	// financial position log's transaction entries (via the reference field).
	query := `
		SELECT si.id, COALESCE(fpl.account_id, '')
		FROM saga_instances si
		INNER JOIN saga_step_results ssr ON ssr.saga_instance_id = si.id
		LEFT JOIN financial_position_logs fpl ON fpl.log_id = $1
		WHERE si.correlation_id::text = $1::text
		   OR ssr.idempotency_key LIKE '%' || $1::text || '%'
		LIMIT 1
	`

	var sagaID uuid.UUID
	var accountID string
	err := v.db.WithContext(ctx).Raw(query, positionID).Row().Scan(&sagaID, &accountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Fallback: try to find via the position log's lineage
			return v.findSagaViaPositionLineage(ctx, positionID)
		}
		return uuid.Nil, "", fmt.Errorf("query saga for position failed: %w", err)
	}

	// Walk up to root saga
	rootID, err := v.findRootSaga(ctx, sagaID)
	if err != nil {
		return uuid.Nil, "", err
	}

	return rootID, accountID, nil
}

// findSagaViaPositionLineage attempts to find a saga by querying the position
// log's transaction lineage and matching correlation IDs.
func (v *CausationVisualizer) findSagaViaPositionLineage(ctx context.Context, positionID uuid.UUID) (uuid.UUID, string, error) {
	// Look for saga_instances whose correlation_id matches any transaction_id
	// in the position log's entries, or whose step results reference the position.
	query := `
		SELECT si.id, ''
		FROM saga_instances si
		WHERE si.id = (
			SELECT ssr.saga_instance_id
			FROM saga_step_results ssr
			WHERE ssr.result::text LIKE '%' || $1::text || '%'
			LIMIT 1
		)
		LIMIT 1
	`

	var sagaID uuid.UUID
	var accountID string
	err := v.db.WithContext(ctx).Raw(query, positionID).Row().Scan(&sagaID, &accountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, "", ErrNoSagaFound
		}
		return uuid.Nil, "", fmt.Errorf("query saga via position lineage failed: %w", err)
	}

	rootID, err := v.findRootSaga(ctx, sagaID)
	if err != nil {
		return uuid.Nil, "", err
	}

	return rootID, accountID, nil
}

// findSagaForTransaction finds the root saga instance for a given transaction.
// It looks up saga_instances by matching the transaction_id against saga
// correlation_id or step result data.
func (v *CausationVisualizer) findSagaForTransaction(ctx context.Context, transactionID uuid.UUID) (uuid.UUID, error) {
	query := `
		SELECT si.id
		FROM saga_instances si
		WHERE si.correlation_id = $1
		   OR si.id = (
			   SELECT ssr.saga_instance_id
			   FROM saga_step_results ssr
			   WHERE ssr.result::text LIKE '%' || $1::text || '%'
			   LIMIT 1
		   )
		ORDER BY si.created_at DESC
		LIMIT 1
	`

	var sagaID uuid.UUID
	err := v.db.WithContext(ctx).Raw(query, transactionID).Row().Scan(&sagaID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, ErrNoSagaFound
		}
		return uuid.Nil, fmt.Errorf("query saga for transaction failed: %w", err)
	}

	return v.findRootSaga(ctx, sagaID)
}

// findSagaForEvent finds the root saga instance for a given event.
// It traces the event's causation_id back through the saga step results
// to find the originating saga.
func (v *CausationVisualizer) findSagaForEvent(ctx context.Context, eventID uuid.UUID) (uuid.UUID, error) {
	// Events in the outbox have a causation_id that links to the saga step
	// that produced them. The causation_id is a deterministic UUIDv5 generated
	// from the saga instance ID and step index.
	query := `
		SELECT si.id
		FROM saga_instances si
		WHERE si.id = (
			SELECT ssr.saga_instance_id
			FROM saga_step_results ssr
			WHERE ssr.causation_id = $1
			LIMIT 1
		)
		LIMIT 1
	`

	var sagaID uuid.UUID
	err := v.db.WithContext(ctx).Raw(query, eventID).Row().Scan(&sagaID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Fallback: try matching on the event outbox table
			return v.findSagaViaOutbox(ctx, eventID)
		}
		return uuid.Nil, fmt.Errorf("query saga for event failed: %w", err)
	}

	return v.findRootSaga(ctx, sagaID)
}

// findSagaViaOutbox attempts to find a saga by looking up the event in the
// outbox table and following its causation chain.
// Returns ErrNoSagaFound if the outbox table does not exist or contains no match.
func (v *CausationVisualizer) findSagaViaOutbox(ctx context.Context, eventID uuid.UUID) (uuid.UUID, error) {
	// Check if event_outbox table exists before querying it.
	// This is necessary because the outbox table may not exist in all deployments
	// (e.g., services that don't publish events).
	var tableExists bool
	checkQuery := `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'event_outbox')`
	if err := v.db.WithContext(ctx).Raw(checkQuery).Row().Scan(&tableExists); err != nil {
		return uuid.Nil, ErrNoSagaFound
	}
	if !tableExists {
		return uuid.Nil, ErrNoSagaFound
	}

	query := `
		SELECT si.id
		FROM saga_instances si
		INNER JOIN saga_step_results ssr ON ssr.saga_instance_id = si.id
		WHERE ssr.causation_id::text = (
			SELECT eo.causation_id
			FROM event_outbox eo
			WHERE eo.id = $1
			LIMIT 1
		)
		LIMIT 1
	`

	var sagaID uuid.UUID
	err := v.db.WithContext(ctx).Raw(query, eventID).Row().Scan(&sagaID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, ErrNoSagaFound
		}
		return uuid.Nil, fmt.Errorf("query saga via outbox failed: %w", err)
	}

	return v.findRootSaga(ctx, sagaID)
}

// findRootSaga walks up the parent_saga_id chain to find the root saga.
// It uses a recursive CTE with a depth limit for safety.
func (v *CausationVisualizer) findRootSaga(ctx context.Context, sagaID uuid.UUID) (uuid.UUID, error) {
	query := `
		WITH RECURSIVE saga_chain AS (
			SELECT id, parent_saga_id, 1 as depth
			FROM saga_instances
			WHERE id = $1

			UNION ALL

			SELECT si.id, si.parent_saga_id, sc.depth + 1
			FROM saga_instances si
			INNER JOIN saga_chain sc ON si.id = sc.parent_saga_id
			WHERE sc.depth < $2
		)
		SELECT id
		FROM saga_chain
		WHERE parent_saga_id IS NULL
		ORDER BY depth DESC
		LIMIT 1
	`

	var rootID uuid.UUID
	err := v.db.WithContext(ctx).Raw(query, sagaID, saga.MaxCausationTreeDepth).Row().Scan(&rootID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// If no parent found, the saga itself is the root
			return sagaID, nil
		}
		return uuid.Nil, fmt.Errorf("find root saga failed: %w", err)
	}

	return rootID, nil
}

// getCausationTree retrieves the causation tree and its depth for a saga.
func (v *CausationVisualizer) getCausationTree(ctx context.Context, sagaID uuid.UUID) (*saga.CausationTreeNode, int, error) {
	tree, err := v.treeRepo.GetCausationTree(ctx, sagaID)
	if err != nil {
		if errors.Is(err, saga.ErrSagaNotFound) {
			return nil, 0, ErrNoSagaFound
		}
		v.logger.Error("failed to get causation tree",
			"saga_id", sagaID,
			"error", err,
		)
		return nil, 0, fmt.Errorf("failed to retrieve causation tree: %w", err)
	}

	depth, err := v.treeRepo.GetTreeDepth(ctx, sagaID)
	if err != nil {
		v.logger.Warn("failed to get tree depth",
			"saga_id", sagaID,
			"error", err,
		)
		depth = 0
	}

	v.logger.Info("causation tree retrieved",
		"saga_id", sagaID,
		"depth", depth,
	)

	return tree, depth, nil
}
