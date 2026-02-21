package mapping_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/reference-data/mapping"
	sharedcel "github.com/meridianhub/meridian/shared/pkg/cel"
)

func newTestValidator(t *testing.T) *mapping.Validator {
	t.Helper()
	compiler, err := sharedcel.NewCompiler()
	require.NoError(t, err)
	v, err := mapping.NewValidator(compiler)
	require.NoError(t, err)
	return v
}

func TestValidator_ValidDef(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "amount"},
		},
	}
	assert.NoError(t, v.Validate(def))
}

func TestValidator_InvalidExternalSchema(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		ExternalSchema: `not valid json schema`,
	}
	err := v.Validate(def)
	assert.Error(t, err)
	assert.ErrorIs(t, err, mapping.ErrInvalidJSONSchema)
}

func TestValidator_ValidExternalSchema(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		ExternalSchema: `{"type":"object","properties":{"amount":{"type":"string"}}}`,
	}
	assert.NoError(t, v.Validate(def))
}

func TestValidator_DuplicateExternalPath(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "amount"},
			{ExternalPath: "amount", InternalPath: "other_field"},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrDuplicateExternalPath)
}

func TestValidator_DuplicateInternalPath(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{ExternalPath: "a", InternalPath: "amount"},
			{ExternalPath: "b", InternalPath: "amount"},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrDuplicateInternalPath)
}

func TestValidator_InvalidGjsonPath(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{ExternalPath: "[invalid", InternalPath: "amount"},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrInvalidGjsonPath)
}

func TestValidator_IsBatch_MissingBatchTargetPath(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		IsBatch:         true,
		BatchTargetPath: "",
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrBatchTargetPathRequired)
}

func TestValidator_IsBatch_WithBatchTargetPath(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		IsBatch:         true,
		BatchTargetPath: "events",
	}
	assert.NoError(t, v.Validate(def))
}

func TestValidator_InvalidCEL_InboundValidation(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		InboundValidationCEL: "this is not valid CEL !!!",
	}
	err := v.Validate(def)
	assert.Error(t, err)
	assert.ErrorIs(t, err, mapping.ErrInvalidCEL)
}

func TestValidator_ValidCEL_InboundValidation(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		InboundValidationCEL: "attributes.size() > 0",
	}
	assert.NoError(t, v.Validate(def))
}

func TestValidator_InvalidCEL_ComputedField(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		InboundComputed: []mapping.ComputedField{
			{TargetPath: "created_at", CELExpression: "not valid !!!"},
		},
	}
	err := v.Validate(def)
	assert.Error(t, err)
	assert.ErrorIs(t, err, mapping.ErrInvalidCEL)
}

func TestValidator_IdempotencyConfig_MissingSourceSelector(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Idempotency: &mapping.IdempotencyConfig{
			UseContentHash: false,
			SourceSelector: "",
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrIdempotencyConfig)
}

func TestValidator_IdempotencyConfig_ContentHash_MissingFields(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Idempotency: &mapping.IdempotencyConfig{
			UseContentHash:    true,
			ContentHashFields: nil,
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrIdempotencyConfig)
}

func TestValidator_IdempotencyConfig_Valid(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Idempotency: &mapping.IdempotencyConfig{
			SourceSelector: "header.idempotency_key",
		},
	}
	assert.NoError(t, v.Validate(def))
}

func TestValidator_CelTransform_Valid(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{
				ExternalPath: "amount",
				InternalPath: "amount",
				Transform: &mapping.FieldTransform{
					CEL: &mapping.CelTransform{
						InboundCEL: "attributes.size() > 0",
					},
				},
			},
		},
	}
	assert.NoError(t, v.Validate(def))
}

func TestValidator_EnumMapping_Valid(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "status",
				Transform: &mapping.FieldTransform{
					EnumMapping: &mapping.EnumMapping{
						Values: map[string]string{
							"ACTIVE":   "STATUS_ACTIVE",
							"INACTIVE": "STATUS_INACTIVE",
						},
					},
				},
			},
		},
	}
	assert.NoError(t, v.Validate(def))
}

func TestValidator_MultipleTransformVariants_Rejected(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{
				ExternalPath: "status",
				InternalPath: "status",
				Transform: &mapping.FieldTransform{
					DateFormat:   "2006-01-02",
					DefaultValue: "fallback",
				},
			},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrTransformVariantConflict)
}
