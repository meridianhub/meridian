package saga

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrScriptHashCorruption is returned when a saga instance's recorded
// ScriptHashAtStart does not match the script_hash on the SagaDefinition row
// referenced by SagaDefinitionID. This indicates the pinned row was tampered
// with (or the wrong instance was loaded) and the saga must be flagged for
// manual intervention rather than resumed.
var ErrScriptHashCorruption = errors.New("saga definition script hash does not match instance.ScriptHashAtStart")

// ErrInstanceMissingDefinitionID is returned by LoadPinnedDefinition when the
// supplied instance has SagaDefinitionID == uuid.Nil. Legacy instances created
// before version pinning was wired in must be backfilled (see the backfill
// migration) before they can resume.
var ErrInstanceMissingDefinitionID = errors.New("saga instance has no saga_definition_id; backfill required before resume")

// ErrNilSagaInstance is returned when LoadPinnedDefinition is called with a
// nil instance pointer.
var ErrNilSagaInstance = errors.New("saga instance is nil")

// ErrNilSagaDefinitionRepository is returned when LoadPinnedDefinition is
// called with a nil repository, preventing a nil-pointer panic on FindByID.
var ErrNilSagaDefinitionRepository = errors.New("saga definition repository is nil")

// LoadPinnedDefinition returns the SagaDefinition that the given instance was
// started with - NOT the current "active" definition for that saga name.
//
// This is the single entry point the resume path MUST use to obtain the script.
// It enforces two invariants:
//
//  1. The instance carries a non-nil SagaDefinitionID (set at start time, kept
//     immutable through the saga's lifetime).
//  2. The script_hash recorded on the pinned definition matches the
//     ScriptHashAtStart recorded on the instance. A mismatch means the pinned
//     row was modified out of band; we refuse to resume.
//
// Re-resolving the definition from the live manifest or the reference-data
// registry is explicitly disallowed: a definition that has since been
// deprecated or replaced must continue to drive existing in-flight instances.
func LoadPinnedDefinition(
	ctx context.Context,
	repo SagaDefinitionRepository,
	instance *SagaInstance,
) (*SagaDefinition, error) {
	if instance == nil {
		return nil, ErrNilSagaInstance
	}
	if repo == nil {
		return nil, ErrNilSagaDefinitionRepository
	}
	if instance.SagaDefinitionID == uuid.Nil {
		return nil, ErrInstanceMissingDefinitionID
	}

	def, err := repo.FindByID(ctx, instance.SagaDefinitionID)
	if err != nil {
		return nil, fmt.Errorf("load pinned saga definition: %w", err)
	}

	// Verify the hash recorded on the instance matches the pinned definition.
	// Empty ScriptHashAtStart is treated as "skip verification" so older instances
	// created before this field was populated remain resumable.
	if instance.ScriptHashAtStart != "" && def.ScriptHash != instance.ScriptHashAtStart {
		return nil, fmt.Errorf("%w: instance=%s definition=%s",
			ErrScriptHashCorruption, instance.ScriptHashAtStart, def.ScriptHash)
	}

	return def, nil
}
