// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MaxCausationTreeDepth is the defensive limit to prevent runaway recursive queries.
// If a saga hierarchy exceeds this depth, the query stops to avoid performance issues.
const MaxCausationTreeDepth = 10

// ErrSagaNotFound is returned when a saga instance cannot be found.
var ErrSagaNotFound = errors.New("saga instance not found")

// CausationTreeNode represents a node in the causation tree hierarchy.
// It contains the saga instance details along with its executed steps and child sagas.
type CausationTreeNode struct {
	SagaID      uuid.UUID   `json:"saga_id"`
	SagaName    string      `json:"saga_name"`
	Status      string      `json:"status"`
	Steps       []StepNode  `json:"steps"`
	EffectiveAt *time.Time  `json:"effective_at,omitempty"`
	KnowledgeAt *time.Time  `json:"knowledge_at,omitempty"`
	FailedStep  *FailedStep `json:"failed_step,omitempty"`
}

// StepNode represents a step in the saga execution.
type StepNode struct {
	Index      int                  `json:"index"`
	Name       string               `json:"name"`
	Status     string               `json:"status"`
	ExecutedAt *time.Time           `json:"executed_at,omitempty"`
	Error      *string              `json:"error,omitempty"`
	ChildSagas []*CausationTreeNode `json:"child_sagas,omitempty"`
}

// FailedStep contains information about the failed step in a saga.
type FailedStep struct {
	Index         int    `json:"index"`
	Error         string `json:"error"`
	ErrorCategory string `json:"error_category,omitempty"`
}

// CausationTreeRepository provides methods for querying causation trees.
type CausationTreeRepository struct {
	db *gorm.DB
}

// NewCausationTreeRepository creates a new CausationTreeRepository.
func NewCausationTreeRepository(db *gorm.DB) *CausationTreeRepository {
	return &CausationTreeRepository{db: db}
}

// causationTreeRow represents a row from the causation tree query result.
type causationTreeRow struct {
	// Saga instance fields
	SagaID          uuid.UUID
	SagaName        sql.NullString
	Status          string
	EffectiveAt     sql.NullTime
	KnowledgeAt     sql.NullTime
	ParentSagaID    *uuid.UUID
	ParentStepIndex sql.NullInt32
	Depth           int
	// Error fields
	ErrorMessage  sql.NullString
	ErrorCategory sql.NullString
	FailedStepIdx sql.NullInt32
	// Step result fields (may be null if no step results)
	StepIndex     sql.NullInt32
	StepName      sql.NullString
	StepStatus    sql.NullString
	StepError     sql.NullString
	StepErrorCat  sql.NullString
	StepCreatedAt sql.NullTime
}

// GetCausationTree retrieves the full causation tree for a given root saga.
// It uses a recursive CTE to efficiently fetch all descendant sagas and their step results.
func (r *CausationTreeRepository) GetCausationTree(ctx context.Context, rootSagaID uuid.UUID) (*CausationTreeNode, error) {
	// The recursive CTE query fetches:
	// 1. The root saga and all descendant sagas (via parent_saga_id)
	// 2. Step results for each saga
	// 3. Ordered by depth, parent_step_index, step_index for proper tree construction
	query := `
		WITH RECURSIVE causation_tree AS (
			-- Base case: start from root saga
			SELECT
				si.id,
				si.saga_name,
				si.status,
				si.knowledge_at,
				si.parent_saga_id,
				si.parent_step_index,
				si.error_message,
				si.error_category,
				si.failed_step_index,
				1 as depth
			FROM saga_instances si
			WHERE si.id = $1

			UNION ALL

			-- Recursive case: find child sagas
			SELECT
				child.id,
				child.saga_name,
				child.status,
				child.knowledge_at,
				child.parent_saga_id,
				child.parent_step_index,
				child.error_message,
				child.error_category,
				child.failed_step_index,
				ct.depth + 1
			FROM saga_instances child
			INNER JOIN causation_tree ct ON child.parent_saga_id = ct.id
			WHERE ct.depth < $2
		)
		SELECT
			ct.id as saga_id,
			ct.saga_name,
			ct.status,
			ct.knowledge_at,
			ct.parent_saga_id,
			ct.parent_step_index,
			ct.depth,
			ct.error_message,
			ct.error_category,
			ct.failed_step_index,
			ssr.step_index,
			ssr.step_name,
			ssr.status as step_status,
			ssr.error as step_error,
			ssr.error_category as step_error_category,
			ssr.created_at as step_created_at
		FROM causation_tree ct
		LEFT JOIN saga_step_results ssr ON ct.id = ssr.saga_instance_id
		ORDER BY ct.depth, ct.parent_step_index NULLS FIRST, ct.id, ssr.step_index NULLS FIRST
	`

	rows, err := r.db.WithContext(ctx).Raw(query, rootSagaID, MaxCausationTreeDepth).Rows()
	if err != nil {
		return nil, fmt.Errorf("causation tree query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return r.buildTreeFromRows(rows, rootSagaID)
}

// buildTreeFromRows constructs the nested tree structure from the flat query result set.
//
func (r *CausationTreeRepository) buildTreeFromRows(rows *sql.Rows, rootSagaID uuid.UUID) (*CausationTreeNode, error) {
	// Map to store all saga nodes by their ID
	sagaNodes := make(map[uuid.UUID]*CausationTreeNode)
	// Map to track step indices we've seen for each saga (to avoid duplicates)
	seenSteps := make(map[uuid.UUID]map[int]bool)
	// Track parent relationships for building the tree
	parentRelations := make(map[uuid.UUID]struct {
		parentID        uuid.UUID
		parentStepIndex int
	})

	rowCount := 0
	for rows.Next() {
		rowCount++
		var row causationTreeRow

		if err := rows.Scan(
			&row.SagaID,
			&row.SagaName,
			&row.Status,
			&row.KnowledgeAt,
			&row.ParentSagaID,
			&row.ParentStepIndex,
			&row.Depth,
			&row.ErrorMessage,
			&row.ErrorCategory,
			&row.FailedStepIdx,
			&row.StepIndex,
			&row.StepName,
			&row.StepStatus,
			&row.StepError,
			&row.StepErrorCat,
			&row.StepCreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan causation tree row: %w", err)
		}

		// Get or create the saga node
		node, exists := sagaNodes[row.SagaID]
		if !exists {
			node = &CausationTreeNode{
				SagaID: row.SagaID,
				Status: row.Status,
				Steps:  []StepNode{},
			}
			if row.SagaName.Valid {
				node.SagaName = row.SagaName.String
			}
			if row.KnowledgeAt.Valid {
				t := row.KnowledgeAt.Time
				node.KnowledgeAt = &t
			}
			// Add failed step info if present
			if row.FailedStepIdx.Valid {
				node.FailedStep = &FailedStep{
					Index: int(row.FailedStepIdx.Int32),
				}
				if row.ErrorMessage.Valid {
					node.FailedStep.Error = row.ErrorMessage.String
				}
				if row.ErrorCategory.Valid {
					node.FailedStep.ErrorCategory = row.ErrorCategory.String
				}
			}
			sagaNodes[row.SagaID] = node
			seenSteps[row.SagaID] = make(map[int]bool)
		}

		// Track parent relationship
		if row.ParentSagaID != nil && row.ParentStepIndex.Valid {
			parentRelations[row.SagaID] = struct {
				parentID        uuid.UUID
				parentStepIndex int
			}{
				parentID:        *row.ParentSagaID,
				parentStepIndex: int(row.ParentStepIndex.Int32),
			}
		}

		// Add step result if present and not already added
		if row.StepIndex.Valid {
			stepIdx := int(row.StepIndex.Int32)
			if !seenSteps[row.SagaID][stepIdx] {
				seenSteps[row.SagaID][stepIdx] = true
				step := StepNode{
					Index:      stepIdx,
					ChildSagas: []*CausationTreeNode{},
				}
				if row.StepName.Valid {
					step.Name = row.StepName.String
				}
				if row.StepStatus.Valid {
					step.Status = row.StepStatus.String
				}
				if row.StepCreatedAt.Valid {
					t := row.StepCreatedAt.Time
					step.ExecutedAt = &t
				}
				if row.StepError.Valid {
					step.Error = &row.StepError.String
				}
				node.Steps = append(node.Steps, step)
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating causation tree rows: %w", err)
	}

	// If no rows found, the saga doesn't exist
	if rowCount == 0 {
		return nil, ErrSagaNotFound
	}

	// Build the tree by attaching child sagas to their parent steps
	for childID, parent := range parentRelations {
		childNode := sagaNodes[childID]
		parentNode := sagaNodes[parent.parentID]
		if parentNode == nil || childNode == nil {
			continue
		}

		// Find or create the parent step
		found := false
		for i := range parentNode.Steps {
			if parentNode.Steps[i].Index == parent.parentStepIndex {
				parentNode.Steps[i].ChildSagas = append(parentNode.Steps[i].ChildSagas, childNode)
				found = true
				break
			}
		}
		// If parent step doesn't exist in results (no step result yet), create a placeholder
		if !found {
			parentNode.Steps = append(parentNode.Steps, StepNode{
				Index:      parent.parentStepIndex,
				ChildSagas: []*CausationTreeNode{childNode},
			})
		}
	}

	// Return the root node
	rootNode := sagaNodes[rootSagaID]
	if rootNode == nil {
		return nil, ErrSagaNotFound
	}

	return rootNode, nil
}

// GetTreeDepth returns the maximum depth of a causation tree.
// This is useful for observability metrics.
func (r *CausationTreeRepository) GetTreeDepth(ctx context.Context, rootSagaID uuid.UUID) (int, error) {
	query := `
		WITH RECURSIVE causation_tree AS (
			SELECT id, 1 as depth
			FROM saga_instances
			WHERE id = $1

			UNION ALL

			SELECT child.id, ct.depth + 1
			FROM saga_instances child
			INNER JOIN causation_tree ct ON child.parent_saga_id = ct.id
			WHERE ct.depth < $2
		)
		SELECT COALESCE(MAX(depth), 0) FROM causation_tree
	`

	var depth int
	if err := r.db.WithContext(ctx).Raw(query, rootSagaID, MaxCausationTreeDepth).Scan(&depth).Error; err != nil {
		return 0, fmt.Errorf("failed to get tree depth: %w", err)
	}

	return depth, nil
}
