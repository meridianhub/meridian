package mapping_test

import (
	"errors"
	"testing"

	"github.com/meridianhub/meridian/services/reference-data/mapping"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Error sentinel existence
// ---------------------------------------------------------------------------

func TestMappingErrors_AllSentinelsNonNil(t *testing.T) {
	sentinels := []error{
		mapping.ErrNotFound,
		mapping.ErrNotDraft,
		mapping.ErrNotActive,
		mapping.ErrAlreadyExists,
		mapping.ErrInvalidCEL,
		mapping.ErrInvalidJSON,
		mapping.ErrInvalidJSONSchema,
		mapping.ErrInvalidGjsonPath,
		mapping.ErrDuplicateExternalPath,
		mapping.ErrDuplicateInternalPath,
		mapping.ErrBatchTargetPathRequired,
		mapping.ErrIdempotencyConfig,
		mapping.ErrOptimisticLock,
		mapping.ErrInvalidStatusTransition,
		mapping.ErrTransformVariantRequired,
		mapping.ErrTransformVariantConflict,
		mapping.ErrCELCompilerNil,
		mapping.ErrRequiredField,
	}

	for _, e := range sentinels {
		assert.NotNil(t, e)
	}
}

// ---------------------------------------------------------------------------
// Error sentinel identity (errors.Is)
// ---------------------------------------------------------------------------

func TestMappingErrors_SentinelsMatchThemselves(t *testing.T) {
	assert.True(t, errors.Is(mapping.ErrNotFound, mapping.ErrNotFound))
	assert.True(t, errors.Is(mapping.ErrNotDraft, mapping.ErrNotDraft))
	assert.True(t, errors.Is(mapping.ErrNotActive, mapping.ErrNotActive))
	assert.True(t, errors.Is(mapping.ErrAlreadyExists, mapping.ErrAlreadyExists))
	assert.True(t, errors.Is(mapping.ErrInvalidCEL, mapping.ErrInvalidCEL))
	assert.True(t, errors.Is(mapping.ErrInvalidJSON, mapping.ErrInvalidJSON))
	assert.True(t, errors.Is(mapping.ErrCELCompilerNil, mapping.ErrCELCompilerNil))
	assert.True(t, errors.Is(mapping.ErrRequiredField, mapping.ErrRequiredField))
}

// ---------------------------------------------------------------------------
// Error sentinel distinctness
// ---------------------------------------------------------------------------

func TestMappingErrors_SentinelsAreDistinct(t *testing.T) {
	assert.NotEqual(t, mapping.ErrNotFound, mapping.ErrNotDraft)
	assert.NotEqual(t, mapping.ErrNotDraft, mapping.ErrNotActive)
	assert.NotEqual(t, mapping.ErrAlreadyExists, mapping.ErrNotFound)
	assert.NotEqual(t, mapping.ErrInvalidCEL, mapping.ErrInvalidJSON)
	assert.NotEqual(t, mapping.ErrInvalidJSONSchema, mapping.ErrInvalidGjsonPath)
	assert.NotEqual(t, mapping.ErrDuplicateExternalPath, mapping.ErrDuplicateInternalPath)
	assert.NotEqual(t, mapping.ErrTransformVariantRequired, mapping.ErrTransformVariantConflict)
	assert.NotEqual(t, mapping.ErrCELCompilerNil, mapping.ErrRequiredField)
}

// ---------------------------------------------------------------------------
// Wrapping with errors.Is
// ---------------------------------------------------------------------------

func TestMappingErrors_IsWrappable(t *testing.T) {
	wrapped := errors.Join(mapping.ErrNotFound, errors.New("additional context"))
	assert.True(t, errors.Is(wrapped, mapping.ErrNotFound))
}
