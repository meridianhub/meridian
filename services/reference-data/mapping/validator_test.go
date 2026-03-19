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

func TestValidator_AttributeFlatten_ValidSourceKeys(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{
				ExternalPath: "data",
				InternalPath: "attributes",
				Transform: &mapping.FieldTransform{
					AttributeFlatten: &mapping.AttributeFlatten{
						SourceKeys:  []string{"key1", "nested.key2", "array.0.value"},
						TargetField: "merged_attrs",
					},
				},
			},
		},
	}
	assert.NoError(t, v.Validate(def))
}

func TestValidator_AttributeFlatten_InvalidSourceKey(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{
				ExternalPath: "data",
				InternalPath: "attributes",
				Transform: &mapping.FieldTransform{
					AttributeFlatten: &mapping.AttributeFlatten{
						SourceKeys:  []string{"valid_key", "[unbalanced"},
						TargetField: "merged",
					},
				},
			},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrInvalidGjsonPath)
}

func TestValidator_AttributeFlatten_EmptyTargetField(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{
				ExternalPath: "data",
				InternalPath: "attributes",
				Transform: &mapping.FieldTransform{
					AttributeFlatten: &mapping.AttributeFlatten{
						SourceKeys:  []string{"key1"},
						TargetField: "",
					},
				},
			},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrRequiredField)
}

func TestValidator_AttributeFlatten_EmptySourceKey(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{
				ExternalPath: "data",
				InternalPath: "attributes",
				Transform: &mapping.FieldTransform{
					AttributeFlatten: &mapping.AttributeFlatten{
						SourceKeys:  []string{""},
						TargetField: "merged",
					},
				},
			},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrInvalidGjsonPath)
}

func TestValidator_GjsonPathSyntax(t *testing.T) {
	v := newTestValidator(t)

	tests := []struct {
		name      string
		path      string
		expectErr bool
	}{
		{"simple key", "amount", false},
		{"nested key", "data.amount", false},
		{"array index", "items.0.name", false},
		{"balanced brackets", "items[0].name", false},
		{"unbalanced open bracket", "items[0.name", true},
		{"unbalanced close bracket", "items]0.name", true},
		{"mismatched brackets", "items[0}", true},
		{"mismatched braces", "items{0]", true},
		{"empty string", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := &mapping.Definition{
				Fields: []mapping.FieldCorrespondence{
					{ExternalPath: tt.path, InternalPath: "target"},
				},
			}
			err := v.Validate(def)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidator_NilDefinition(t *testing.T) {
	v := newTestValidator(t)
	err := v.Validate(nil)
	assert.ErrorIs(t, err, mapping.ErrRequiredField)
}

func TestValidator_EmptyTransform_Rejected(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{
				ExternalPath: "amount",
				InternalPath: "amount",
				Transform:    &mapping.FieldTransform{},
			},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrTransformVariantRequired)
}

func TestValidator_NewValidator_NilCompiler(t *testing.T) {
	_, err := mapping.NewValidator(nil)
	assert.ErrorIs(t, err, mapping.ErrCELCompilerNil)
}

func TestValidator_OutboundValidationCEL_Invalid(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		OutboundValidationCEL: "this is not valid CEL !!!",
	}
	err := v.Validate(def)
	assert.Error(t, err)
	assert.ErrorIs(t, err, mapping.ErrInvalidCEL)
}

func TestValidator_OutboundComputedField_Invalid(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		OutboundComputed: []mapping.ComputedField{
			{TargetPath: "out_field", CELExpression: "not valid !!!"},
		},
	}
	err := v.Validate(def)
	assert.Error(t, err)
	assert.ErrorIs(t, err, mapping.ErrInvalidCEL)
}

func TestValidator_BatchTargetPath_InvalidGjson(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		IsBatch:         true,
		BatchTargetPath: "[unbalanced",
	}
	err := v.Validate(def)
	assert.Error(t, err)
}

func TestValidator_IdempotencyConfig_InvalidSourceSelectorGjson(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Idempotency: &mapping.IdempotencyConfig{
			SourceSelector: "[invalid",
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrInvalidGjsonPath)
}

func TestValidator_IdempotencyConfig_InvalidContentHashFieldGjson(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Idempotency: &mapping.IdempotencyConfig{
			UseContentHash:    true,
			ContentHashFields: []string{"valid_key", "[broken"},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrInvalidGjsonPath)
}

func TestValidator_ComputedField_EmptyTargetPath(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		InboundComputed: []mapping.ComputedField{
			{TargetPath: "", CELExpression: "true"},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrRequiredField)
}

func TestValidator_ComputedField_InvalidTargetPathGjson(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		InboundComputed: []mapping.ComputedField{
			{TargetPath: "[unbalanced", CELExpression: "true"},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrInvalidGjsonPath)
}

func TestValidator_InternalPath_Empty(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: ""},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrInvalidGjsonPath)
}

func TestValidator_InternalPath_InvalidGjson(t *testing.T) {
	v := newTestValidator(t)

	def := &mapping.Definition{
		Fields: []mapping.FieldCorrespondence{
			{ExternalPath: "amount", InternalPath: "[broken"},
		},
	}
	err := v.Validate(def)
	assert.ErrorIs(t, err, mapping.ErrInvalidGjsonPath)
}
