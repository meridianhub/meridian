// Package node provides the domain model and repository for hierarchical
// reference data nodes with bi-temporal support and tenant isolation.
package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Error types for reference data nodes.
var (
	ErrNotFound          = errors.New("reference data node not found")
	ErrAlreadyExists     = errors.New("active node with this resolution key already exists")
	ErrInvalidNode       = errors.New("invalid reference data node")
	ErrParentNotFound    = errors.New("parent node not found")
	ErrParentNotActive   = errors.New("parent node is not active")
	ErrCrossTenantParent = errors.New("parent node belongs to a different tenant")
	ErrImmutableIdentity = errors.New("cannot change identity fields on active node")
	ErrOptimisticLock    = errors.New("optimistic lock failure: node was modified")
	ErrCircularHierarchy = errors.New("circular hierarchy detected")
	ErrMaxDepthExceeded  = errors.New("maximum hierarchy depth exceeded")
	ErrInvalidTimeRange  = errors.New("valid_to must be after valid_from")
	ErrAlreadySuperseded = errors.New("node has already been superseded")
)

// MaxHierarchyDepth is the maximum allowed depth of the node tree.
// This prevents unbounded recursive lookups for resolution key computation.
const MaxHierarchyDepth = 20

// Node represents a hierarchical reference data node with bi-temporal validity.
type Node struct {
	ID            uuid.UUID
	TenantID      string
	NodeType      string
	ParentID      *uuid.UUID
	Attributes    map[string]any
	ResolutionKey string
	ValidFrom     time.Time
	ValidTo       *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Version       int64
}

// IsActive returns true if the node has no valid_to (still active).
func (n *Node) IsActive() bool {
	return n.ValidTo == nil
}

// Builder constructs a Node with validation.
type Builder struct {
	node Node
	errs []string
}

// NewBuilder creates a new Node builder for the given tenant.
func NewBuilder(tenantID string) *Builder {
	return &Builder{
		node: Node{
			TenantID:   tenantID,
			Attributes: make(map[string]any),
			Version:    1,
		},
	}
}

// WithID sets the node ID. If not called, a new UUID is generated on Build.
func (b *Builder) WithID(id uuid.UUID) *Builder {
	b.node.ID = id
	return b
}

// WithNodeType sets the node type (e.g., "region", "zone", "meter").
func (b *Builder) WithNodeType(nodeType string) *Builder {
	b.node.NodeType = nodeType
	return b
}

// WithParentID sets the parent node ID.
func (b *Builder) WithParentID(parentID uuid.UUID) *Builder {
	b.node.ParentID = &parentID
	return b
}

// WithAttributes sets the flexible metadata attributes.
func (b *Builder) WithAttributes(attrs map[string]any) *Builder {
	b.node.Attributes = attrs
	return b
}

// WithValidFrom sets the effective start time. Defaults to now if not set.
func (b *Builder) WithValidFrom(t time.Time) *Builder {
	b.node.ValidFrom = t
	return b
}

// WithValidTo sets the effective end time.
func (b *Builder) WithValidTo(t time.Time) *Builder {
	b.node.ValidTo = &t
	return b
}

// Build validates and returns the constructed Node.
func (b *Builder) Build() (*Node, error) {
	if b.node.TenantID == "" {
		b.errs = append(b.errs, "tenant_id is required")
	}
	if b.node.NodeType == "" {
		b.errs = append(b.errs, "node_type is required")
	}
	if b.node.ID == uuid.Nil {
		b.node.ID = uuid.New()
	}
	if b.node.ValidFrom.IsZero() {
		b.node.ValidFrom = time.Now()
	}
	if b.node.ValidTo != nil && !b.node.ValidTo.After(b.node.ValidFrom) {
		b.errs = append(b.errs, "valid_to must be after valid_from")
	}
	if b.node.Attributes == nil {
		b.node.Attributes = make(map[string]any)
	}

	now := time.Now()
	b.node.CreatedAt = now
	b.node.UpdatedAt = now

	if len(b.errs) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrInvalidNode, strings.Join(b.errs, "; "))
	}

	return &b.node, nil
}

// ComputeResolutionKey computes the resolution key for a node given its ancestors.
// The resolution key is a path-like string: "parent_key/type:id" where parent_key
// is the resolution key of the parent node. For root nodes: "type:id".
//
// ancestors must be ordered from the immediate parent to the root.
// The node's own ID is always the last segment.
func ComputeResolutionKey(nodeType string, nodeID uuid.UUID, ancestors []*Node) string {
	if len(ancestors) == 0 {
		return fmt.Sprintf("%s:%s", nodeType, nodeID.String())
	}

	// Build from root to this node
	parts := make([]string, 0, len(ancestors)+1)

	// Ancestors are ordered parent-first, so reverse to get root-first
	for i := len(ancestors) - 1; i >= 0; i-- {
		a := ancestors[i]
		parts = append(parts, fmt.Sprintf("%s:%s", a.NodeType, a.ID.String()))
	}

	// Add this node as the final segment
	parts = append(parts, fmt.Sprintf("%s:%s", nodeType, nodeID.String()))

	return strings.Join(parts, "/")
}

// Repository defines the interface for managing reference data nodes.
// All methods extract tenant context from ctx using shared/platform/tenant.
type Repository interface {
	// Create inserts a new reference data node.
	// The resolution key is computed automatically from the node's ancestors.
	// Returns ErrAlreadyExists if an active node with the same resolution key exists.
	// Returns ErrParentNotFound if the parent node does not exist.
	// Returns ErrParentNotActive if the parent node has been superseded.
	// Returns ErrCrossTenantParent if the parent belongs to a different tenant.
	Create(ctx context.Context, node *Node) error

	// Update modifies non-key attributes of a node (attributes, valid_to).
	// Identity fields (tenant_id, node_type, parent_id, resolution_key) are immutable.
	// Uses optimistic locking via version column.
	// Returns ErrNotFound if the node does not exist.
	// Returns ErrOptimisticLock if the version has changed since the node was read.
	Update(ctx context.Context, node *Node) error

	// GetByID retrieves a node by its UUID.
	// Returns ErrNotFound if the node does not exist.
	GetByID(ctx context.Context, id uuid.UUID) (*Node, error)

	// GetAsAt retrieves the version of a node that was valid at the given time.
	// Looks up all versions sharing the same tenant_id and resolution_key lineage
	// where valid_from <= asAt AND (valid_to IS NULL OR valid_to > asAt).
	// Returns ErrNotFound if no version was valid at the given time.
	GetAsAt(ctx context.Context, tenantID string, id uuid.UUID, asAt time.Time) (*Node, error)

	// GetHistory returns all temporal versions of a node (active and superseded),
	// ordered by valid_from descending (newest first).
	GetHistory(ctx context.Context, tenantID string, id uuid.UUID) ([]*Node, error)

	// GetByResolutionKey retrieves the node matching the resolution key at the given time.
	// If asAt is zero, retrieves the active node (valid_to IS NULL).
	// Returns ErrNotFound if no matching node exists.
	GetByResolutionKey(ctx context.Context, tenantID, resolutionKey string, asAt time.Time) (*Node, error)

	// GetChildren returns child nodes of the given parent.
	// If activeOnly is true, only returns active nodes (valid_to IS NULL).
	GetChildren(ctx context.Context, tenantID string, parentID uuid.UUID, activeOnly bool) ([]*Node, error)

	// ListByType returns all active nodes of the given type within a tenant.
	ListByType(ctx context.Context, tenantID, nodeType string) ([]*Node, error)

	// ListRoots returns all active root nodes (no parent) within a tenant.
	ListRoots(ctx context.Context, tenantID string) ([]*Node, error)

	// Supersede closes the current node (sets valid_to) and creates a new version.
	// The new node inherits the same parent and type but can have updated attributes.
	// Returns ErrAlreadySuperseded if the node has already been closed.
	Supersede(ctx context.Context, nodeID uuid.UUID, newNode *Node) error

	// GetAncestors returns the chain of ancestors from the immediate parent to root.
	// Uses a recursive CTE for efficient traversal.
	// Returns empty slice for root nodes.
	// Returns ErrMaxDepthExceeded if the chain exceeds MaxHierarchyDepth.
	GetAncestors(ctx context.Context, nodeID uuid.UUID) ([]*Node, error)

	// GetSubtree returns all descendants of a root node up to maxDepth levels.
	// The root node itself is included at depth 0.
	// Uses a recursive CTE with depth limit for efficient traversal.
	GetSubtree(ctx context.Context, tenantID string, rootID uuid.UUID, maxDepth int) ([]*Node, error)

	// GetAtTime retrieves the node state that was valid at the given effective time.
	// Uses bi-temporal query: valid_from <= asOf < valid_to (or valid_to IS NULL).
	GetAtTime(ctx context.Context, tenantID, resolutionKey string, asOf time.Time) (*Node, error)

	// BulkCreate inserts multiple nodes in a single transaction.
	// Resolution keys are computed automatically for each node.
	// The nodes should be ordered such that parents appear before children.
	BulkCreate(ctx context.Context, nodes []*Node) error
}
