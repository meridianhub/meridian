// Package ports defines the interfaces (ports) for the operational-gateway service.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
)

// Instruction repository errors.
var (
	ErrInstructionNotFound  = errors.New("instruction not found")
	ErrInstructionConflict  = errors.New("version conflict: instruction was modified by another transaction")
	ErrDuplicateIdempotency = errors.New("instruction with this idempotency key already exists")
)

// Connection repository errors.
var (
	ErrConnectionNotFound = errors.New("provider connection not found")
)

// FetchDispatchableParams holds the parameters for fetching dispatchable instructions.
type FetchDispatchableParams struct {
	// Limit is the maximum number of instructions to fetch in one batch.
	Limit int
	// AsOf is the reference time for evaluating scheduled_at / next_retry_at.
	// Instructions with scheduled_at <= AsOf are eligible.
	// Defaults to time.Now() when zero.
	AsOf time.Time
}

// ListInstructionsParams holds the parameters for listing instructions.
type ListInstructionsParams struct {
	// TenantID scopes the query to a specific tenant.
	TenantID string
	// InstructionType filters by instruction type. Empty means all types.
	InstructionType string
	// Statuses filters by instruction status. Empty means all statuses.
	Statuses []domain.InstructionStatus
	// ProviderConnectionID filters by provider connection. Empty means all connections.
	ProviderConnectionID string
	// CreatedAfter filters instructions created at or after this time. Zero means no lower bound.
	CreatedAfter time.Time
	// CreatedBefore filters instructions created at or before this time. Zero means no upper bound.
	CreatedBefore time.Time
	// Limit is the maximum number of instructions to return.
	Limit int
	// Offset is the number of instructions to skip (for cursor-based pagination).
	Offset int
}

// InstructionRepository defines persistence operations for instructions.
type InstructionRepository interface {
	// Save creates or updates an instruction with optimistic locking via the version field.
	// Returns ErrInstructionConflict if the version in the database does not match.
	// Returns ErrDuplicateIdempotency on idempotency key collision.
	Save(ctx context.Context, instruction *domain.Instruction, idempotencyKey string) error

	// FindByID retrieves an instruction by its UUID.
	// Returns ErrInstructionNotFound if no matching instruction exists.
	FindByID(ctx context.Context, id uuid.UUID) (*domain.Instruction, error)

	// ListByTenant retrieves instructions for a tenant with optional filtering and pagination.
	// Results are ordered by created_at DESC, id DESC for stable cursor-based pagination.
	ListByTenant(ctx context.Context, params ListInstructionsParams) ([]*domain.Instruction, int64, error)

	// FetchDispatchable atomically fetches a batch of PENDING or RETRYING instructions
	// that are ready for dispatch and locks them using SELECT FOR UPDATE SKIP LOCKED.
	// Instructions are returned ordered by priority DESC (CRITICAL first), then scheduled_at ASC.
	// The caller must call Save to update instruction state after dispatch.
	FetchDispatchable(ctx context.Context, params FetchDispatchableParams) ([]*domain.Instruction, error)
}

// Route repository errors.
var (
	ErrRouteNotFound = errors.New("instruction route not found")
)

// RouteRepository defines persistence operations for instruction routes.
type RouteRepository interface {
	// Upsert creates or fully replaces an instruction route configuration.
	// Uses INSERT ... ON CONFLICT (tenant_id, instruction_type) DO UPDATE for idempotency.
	Upsert(ctx context.Context, route *domain.Route) error

	// FindByInstructionType retrieves an instruction route by tenant and instruction type.
	// Returns ErrRouteNotFound if no matching route exists.
	FindByInstructionType(ctx context.Context, tenantID string, instructionType string) (*domain.Route, error)

	// ListByTenant retrieves all instruction routes for a tenant.
	ListByTenant(ctx context.Context, tenantID string) ([]*domain.Route, error)
}

// ConnectionRepository defines persistence operations for provider connections.
type ConnectionRepository interface {
	// Upsert creates or replaces a provider connection.
	// Uses INSERT ... ON CONFLICT (tenant_id, connection_id) DO UPDATE to handle idempotent
	// configuration updates.
	Upsert(ctx context.Context, conn *domain.ProviderConnection) error

	// FindByID retrieves a provider connection by tenant and connection ID.
	// Returns ErrConnectionNotFound if no matching connection exists.
	FindByID(ctx context.Context, tenantID string, connectionID string) (*domain.ProviderConnection, error)

	// ListByTenant retrieves all provider connections for a tenant.
	ListByTenant(ctx context.Context, tenantID string) ([]*domain.ProviderConnection, error)

	// UpdateHealth persists updated health and circuit breaker state.
	// This is a targeted update to avoid clobbering concurrent configuration changes.
	UpdateHealth(ctx context.Context, conn *domain.ProviderConnection) error
}
