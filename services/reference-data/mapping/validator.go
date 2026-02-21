package mapping

import (
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/tidwall/gjson"

	sharedcel "github.com/meridianhub/meridian/shared/pkg/cel"
)

// Validator validates a MappingDefinition's content before persistence.
type Validator struct {
	celCompiler *sharedcel.Compiler
}

// NewValidator creates a Validator using the shared CEL compiler.
func NewValidator(compiler *sharedcel.Compiler) (*Validator, error) {
	if compiler == nil {
		return nil, ErrCELCompilerNil
	}
	return &Validator{celCompiler: compiler}, nil
}

// Validate performs all semantic validations on a mapping definition.
func (v *Validator) Validate(def *Definition) error {
	if def == nil {
		return fmt.Errorf("%w: definition cannot be nil", ErrRequiredField)
	}
	var errs []error

	if err := v.validateExternalSchema(def.ExternalSchema); err != nil {
		errs = append(errs, err)
	}

	if err := v.validateFields(def.Fields); err != nil {
		errs = append(errs, err)
	}

	if err := v.validateComputedFields(def.InboundComputed, "inbound_computed_fields"); err != nil {
		errs = append(errs, err)
	}

	if err := v.validateComputedFields(def.OutboundComputed, "outbound_computed_fields"); err != nil {
		errs = append(errs, err)
	}

	if err := v.validateCELExpression(def.InboundValidationCEL, "inbound_validation_cel"); err != nil {
		errs = append(errs, err)
	}

	if err := v.validateCELExpression(def.OutboundValidationCEL, "outbound_validation_cel"); err != nil {
		errs = append(errs, err)
	}

	if def.IsBatch && def.BatchTargetPath == "" {
		errs = append(errs, fmt.Errorf("%w: batch_target_path is required when is_batch is true", ErrBatchTargetPathRequired))
	}

	if def.BatchTargetPath != "" && !isValidGjsonPath(def.BatchTargetPath) {
		errs = append(errs, fmt.Errorf("%w: batch_target_path %q", ErrInvalidGjsonPath, def.BatchTargetPath))
	}

	if def.Idempotency != nil {
		if err := v.validateIdempotency(def.Idempotency); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (v *Validator) validateExternalSchema(schema string) error {
	if schema == "" {
		return nil
	}

	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", strings.NewReader(schema)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidJSONSchema, err)
	}
	if _, err := c.Compile("schema.json"); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidJSONSchema, err)
	}
	return nil
}

func (v *Validator) validateFields(fields []FieldCorrespondence) error {
	externalPaths := make(map[string]bool)
	internalPaths := make(map[string]bool)

	for i, f := range fields {
		if !isValidGjsonPath(f.ExternalPath) {
			return fmt.Errorf("%w: fields[%d].external_path %q", ErrInvalidGjsonPath, i, f.ExternalPath)
		}
		if strings.TrimSpace(f.InternalPath) == "" {
			return fmt.Errorf("%w: fields[%d].internal_path is required", ErrInvalidGjsonPath, i)
		}
		if !isValidGjsonPath(f.InternalPath) {
			return fmt.Errorf("%w: fields[%d].internal_path %q", ErrInvalidGjsonPath, i, f.InternalPath)
		}
		if externalPaths[f.ExternalPath] {
			return fmt.Errorf("%w: %q", ErrDuplicateExternalPath, f.ExternalPath)
		}
		externalPaths[f.ExternalPath] = true

		if internalPaths[f.InternalPath] {
			return fmt.Errorf("%w: %q", ErrDuplicateInternalPath, f.InternalPath)
		}
		internalPaths[f.InternalPath] = true

		if f.Transform != nil {
			if err := v.validateFieldTransform(f.Transform, i); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *Validator) validateFieldTransform(t *FieldTransform, idx int) error {
	// Ensure exactly one transform variant is set
	variants := 0
	if t.CEL != nil {
		variants++
		if err := v.validateCELTransform(t.CEL, idx); err != nil {
			return err
		}
	}
	if t.EnumMapping != nil {
		variants++
	}
	if t.DateFormat != "" {
		variants++
	}
	if t.DefaultValue != "" {
		variants++
	}
	if t.AttributeFlatten != nil {
		variants++
		if err := v.validateAttributeFlatten(t.AttributeFlatten, idx); err != nil {
			return err
		}
	}

	if variants == 0 {
		return fmt.Errorf("fields[%d].transform: %w", idx, ErrTransformVariantRequired)
	}
	if variants > 1 {
		return fmt.Errorf("fields[%d].transform: %w", idx, ErrTransformVariantConflict)
	}
	return nil
}

func (v *Validator) validateCELTransform(t *CelTransform, idx int) error {
	if t.InboundCEL != "" {
		if err := v.validateCELExpressionRaw(t.InboundCEL); err != nil {
			return fmt.Errorf("fields[%d].transform.cel.inbound_cel: %w", idx, err)
		}
	}
	if t.OutboundCEL != "" {
		if err := v.validateCELExpressionRaw(t.OutboundCEL); err != nil {
			return fmt.Errorf("fields[%d].transform.cel.outbound_cel: %w", idx, err)
		}
	}
	return nil
}

func (v *Validator) validateAttributeFlatten(af *AttributeFlatten, idx int) error {
	if strings.TrimSpace(af.TargetField) == "" {
		return fmt.Errorf("%w: fields[%d].transform.attribute_flatten.target_field", ErrRequiredField, idx)
	}
	for _, key := range af.SourceKeys {
		if !isValidGjsonPath(key) {
			return fmt.Errorf("%w: fields[%d].transform.attribute_flatten.source_keys %q",
				ErrInvalidGjsonPath, idx, key)
		}
	}
	return nil
}

func (v *Validator) validateComputedFields(fields []ComputedField, fieldName string) error {
	for i, f := range fields {
		if strings.TrimSpace(f.TargetPath) == "" {
			return fmt.Errorf("%w: %s[%d].target_path", ErrRequiredField, fieldName, i)
		}
		if !isValidGjsonPath(f.TargetPath) {
			return fmt.Errorf("%w: %s[%d].target_path %q", ErrInvalidGjsonPath, fieldName, i, f.TargetPath)
		}
		if err := v.validateCELExpressionRaw(f.CELExpression); err != nil {
			return fmt.Errorf("%s[%d].cel_expression: %w: %w", fieldName, i, ErrInvalidCEL, err)
		}
	}
	return nil
}

func (v *Validator) validateCELExpression(expr, fieldName string) error {
	if expr == "" {
		return nil
	}
	if err := v.validateCELExpressionRaw(expr); err != nil {
		return fmt.Errorf("%s: %w: %w", fieldName, ErrInvalidCEL, err)
	}
	return nil
}

// validateCELExpressionRaw uses the shared CEL compiler to validate an expression.
// We re-use the validation environment as a general-purpose CEL check.
func (v *Validator) validateCELExpressionRaw(expr string) error {
	return v.celCompiler.ValidateValidationCEL(expr)
}

func (v *Validator) validateIdempotency(cfg *IdempotencyConfig) error {
	if !cfg.UseContentHash && cfg.SourceSelector == "" {
		return fmt.Errorf("%w: source_selector is required when use_content_hash is false", ErrIdempotencyConfig)
	}
	if cfg.UseContentHash && len(cfg.ContentHashFields) == 0 {
		return fmt.Errorf("%w: content_hash_fields must have at least one entry when use_content_hash is true", ErrIdempotencyConfig)
	}
	if cfg.SourceSelector != "" && !isValidGjsonPath(cfg.SourceSelector) {
		return fmt.Errorf("%w: idempotency.source_selector %q", ErrInvalidGjsonPath, cfg.SourceSelector)
	}
	for _, f := range cfg.ContentHashFields {
		if !isValidGjsonPath(f) {
			return fmt.Errorf("%w: idempotency.content_hash_fields %q", ErrInvalidGjsonPath, f)
		}
	}
	return nil
}

// isValidGjsonPath checks that the given string is a syntactically valid gjson path.
// gjson.Get with an empty JSON string returns a Result; any non-panic is considered valid syntax.
// We use gjson.ParseMany to check path syntax without requiring actual data.
func isValidGjsonPath(path string) bool {
	if path == "" {
		return false
	}
	// gjson paths are validated by attempting to parse them.
	// A path with unbalanced brackets/braces is invalid.
	result := gjson.Get("{}", path)
	// If result type is JSON and the raw value is populated it means gjson parsed the path OK.
	// Even when the key is absent the result exists without error.
	_ = result
	return isGjsonPathSyntaxValid(path)
}

// isGjsonPathSyntaxValid checks for balanced and correctly matched brackets.
// Uses a stack to enforce that each closing bracket matches its opener,
// catching mismatched pairs like "[}" which a simple depth counter misses.
func isGjsonPathSyntaxValid(path string) bool {
	stack := make([]rune, 0, 8)
	for _, ch := range path {
		switch ch {
		case '[', '{':
			stack = append(stack, ch)
		case ']', '}':
			if len(stack) == 0 {
				return false
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if (ch == ']' && open != '[') || (ch == '}' && open != '{') {
				return false
			}
		}
	}
	return len(stack) == 0
}
