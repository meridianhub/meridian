// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
)

// SagaDefinition represents an immutable, versioned saga definition pinned at the moment
// a saga instance is started. Pinning prevents the resume path from picking up a different
// script if the underlying definition is updated while an instance is still in-flight.
//
// Each row is immutable once written. New (name, version) combinations create new rows.
// Re-applying the same (name, version, script) is idempotent: FindOrCreate returns the
// existing row when the script hash matches.
//
//nolint:revive // SagaDefinition naming is intentional for GORM entity clarity
type SagaDefinition struct {
	// ID is the immutable identifier referenced by SagaInstance.SagaDefinitionID.
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Name is the saga name (e.g., "apply_manifest", "current_account_deposit").
	Name string `gorm:"column:name;type:varchar(64);not null"`

	// Version is the saga version (integer; increments on script change).
	Version int `gorm:"column:version;not null"`

	// Script is the Starlark source code for this definition.
	Script string `gorm:"column:script;type:text;not null"`

	// ParamsSchema captures the parameter schema for callers/UI (optional).
	ParamsSchema JSONB `gorm:"column:params_schema;type:jsonb"`

	// ScriptHash is the SHA-256 hex digest of the script content. Used for content
	// addressing in FindOrCreate (same name+version+hash = reuse; mismatched hash for
	// the same name+version is rejected as a tampering/immutability violation).
	ScriptHash string `gorm:"column:script_hash;type:varchar(64);not null"`

	// CreatedAt records when this definition was first persisted.
	CreatedAt time.Time `gorm:"column:created_at;not null;default:now()"`
}

// TableName returns the table name for the SagaDefinition entity.
func (SagaDefinition) TableName() string {
	return "saga_definitions"
}

// ComputeSagaDefinitionScriptHash returns the SHA-256 hex digest of a script.
// This is the canonical hash used for content addressing of saga definitions.
func ComputeSagaDefinitionScriptHash(script string) string {
	sum := sha256.Sum256([]byte(script))
	return hex.EncodeToString(sum[:])
}

// SagaDefinitionRepository persists and retrieves pinned saga definitions.
//
// FindByID is used by the saga resume path: an in-flight instance carries the
// SagaDefinitionID it was started with, and FindByID returns the exact script
// that was pinned at start time. Resumed sagas must NEVER re-resolve the
// definition from the live manifest or reference-data registry.
//
// FindOrCreate is used by the saga start path: callers resolve the active script
// (from manifest applier or reference-data) and ask the repository to either
// reuse an existing pinned row or insert a new one. Pinning rules:
//   - Same (name, version, script hash): returns the existing row.
//   - Same (name, version) but DIFFERENT script hash: returns
//     ErrSagaDefinitionHashMismatch (immutable versions invariant).
//   - New (name, version): inserts a new row.
//
//nolint:revive // SagaDefinitionRepository naming is intentional for clarity
type SagaDefinitionRepository interface {
	// FindByID retrieves a saga definition by its immutable ID.
	// Returns ErrSagaDefinitionNotFound if no row exists.
	FindByID(ctx context.Context, id uuid.UUID) (*SagaDefinition, error)

	// FindOrCreate returns an existing row matching (name, version, script hash),
	// or inserts a new row if no (name, version) entry exists.
	// Returns ErrSagaDefinitionHashMismatch when (name, version) exists but the
	// stored script hash differs from ComputeSagaDefinitionScriptHash(script).
	FindOrCreate(ctx context.Context, name string, version int, script string, paramsSchema JSONB) (*SagaDefinition, error)
}

// ErrSagaDefinitionNotFound is returned when a saga definition cannot be located.
var ErrSagaDefinitionNotFound = errors.New("saga definition not found")

// ErrSagaDefinitionHashMismatch is returned when a caller attempts to register a
// different script under an already-pinned (name, version). Saga definitions are
// immutable per version; the caller must bump the version to publish a new script.
var ErrSagaDefinitionHashMismatch = errors.New("saga definition script hash mismatch for existing (name, version)")
