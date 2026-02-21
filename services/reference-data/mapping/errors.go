package mapping

import "errors"

var (
	// ErrNotFound is returned when a mapping definition is not found.
	ErrNotFound = errors.New("mapping definition not found")

	// ErrNotDraft is returned when an operation requires DRAFT status.
	ErrNotDraft = errors.New("mapping definition must be in DRAFT status")

	// ErrNotActive is returned when deprecation requires ACTIVE status.
	ErrNotActive = errors.New("mapping definition must be in ACTIVE status")

	// ErrAlreadyExists is returned when a mapping with the same (tenant, name, version) already exists.
	ErrAlreadyExists = errors.New("mapping definition already exists")

	// ErrInvalidCEL is returned when a CEL expression fails to compile.
	ErrInvalidCEL = errors.New("invalid CEL expression")

	// ErrInvalidJSON is returned when provided JSON is malformed.
	ErrInvalidJSON = errors.New("invalid JSON")

	// ErrInvalidJSONSchema is returned when external_schema is not a valid JSON Schema.
	ErrInvalidJSONSchema = errors.New("invalid JSON Schema")

	// ErrInvalidGjsonPath is returned when a gjson path is syntactically invalid.
	ErrInvalidGjsonPath = errors.New("invalid gjson path")

	// ErrDuplicateExternalPath is returned when two fields share an external_path.
	ErrDuplicateExternalPath = errors.New("duplicate external_path in fields")

	// ErrDuplicateInternalPath is returned when two fields share an internal_path.
	ErrDuplicateInternalPath = errors.New("duplicate internal_path in fields")

	// ErrBatchTargetPathRequired is returned when is_batch is true but batch_target_path is empty.
	ErrBatchTargetPathRequired = errors.New("batch_target_path is required when is_batch is true")

	// ErrIdempotencyConfig is returned when an IdempotencyConfig is self-contradictory.
	ErrIdempotencyConfig = errors.New("invalid idempotency configuration")

	// ErrOptimisticLock is returned when an optimistic-lock conflict is detected on update.
	ErrOptimisticLock = errors.New("mapping definition was modified by another transaction")

	// ErrTransformVariantRequired is returned when a FieldTransform has no variant set.
	ErrTransformVariantRequired = errors.New("at least one transform variant must be set")

	// ErrTransformVariantConflict is returned when a FieldTransform has more than one variant set.
	ErrTransformVariantConflict = errors.New("at most one transform variant may be set")

	// ErrCELCompilerNil is returned when a nil CEL compiler is provided.
	ErrCELCompilerNil = errors.New("cel compiler cannot be nil")
)
