// Package mapping provides a bidirectional JSON transformation engine that applies
// MappingDefinition proto transforms between external and internal formats.
package mapping

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/ext"
	lru "github.com/hashicorp/golang-lru/v2"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ErrFieldExtraction indicates a failure extracting or setting a field value.
var ErrFieldExtraction = errors.New("field extraction failed")

// ErrTransform indicates a general transform failure.
var ErrTransform = errors.New("transform failed")

// ErrValidation indicates a validation CEL expression returned false or failed.
var ErrValidation = errors.New("validation failed")

// ErrCELCompilation indicates a CEL expression failed to compile.
var ErrCELCompilation = errors.New("CEL compilation failed")

// ErrCELEvaluation indicates a CEL expression failed during evaluation.
var ErrCELEvaluation = errors.New("CEL evaluation failed")

// ErrIdempotencyKey indicates a failure deriving the idempotency key.
var ErrIdempotencyKey = errors.New("idempotency key derivation failed")

// ErrInvalidJSON indicates the input was not valid JSON.
var ErrInvalidJSON = errors.New("invalid JSON input")

// ErrEnumNotMapped indicates an enum value had no mapping and no fallback.
var ErrEnumNotMapped = errors.New("enum value not mapped")

// ErrDateParse indicates a date/time string could not be parsed with the given layout.
var ErrDateParse = errors.New("date parse failed")

// ErrAttributeFlatten indicates a failure during attribute flatten/unflatten.
var ErrAttributeFlatten = errors.New("attribute flatten failed")

const defaultCacheSize = 256

// Engine applies MappingDefinition transforms in both inbound and outbound directions.
type Engine struct {
	celCache *lru.Cache[string, cel.Program]
	env      *cel.Env
}

// NewEngine creates a new mapping Engine with a CEL program cache.
func NewEngine() (*Engine, error) {
	return NewEngineWithCacheSize(defaultCacheSize)
}

// NewEngineWithCacheSize creates a new Engine with the specified CEL cache capacity.
func NewEngineWithCacheSize(cacheSize int) (*Engine, error) {
	cache, err := lru.New[string, cel.Program](cacheSize)
	if err != nil {
		return nil, fmt.Errorf("creating CEL cache: %w", err)
	}

	env, err := createMappingCELEnv()
	if err != nil {
		return nil, fmt.Errorf("creating CEL environment: %w", err)
	}

	return &Engine{
		celCache: cache,
		env:      env,
	}, nil
}

// createMappingCELEnv creates a CEL environment for mapping expressions.
// Variables available: value (dynamic), input (map), mapped (map), payload (map).
func createMappingCELEnv() (*cel.Env, error) {
	return cel.NewEnv(
		cel.Variable("value", cel.DynType),
		cel.Variable("input", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("mapped", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("payload", cel.MapType(cel.StringType, cel.DynType)),
		ext.Strings(),
		ext.Encoders(),
		ext.Math(),
	)
}

// InboundResult holds the result of an inbound transformation.
type InboundResult struct {
	ProtoJSON      []byte
	IdempotencyKey string
}

// TransformInbound maps an external JSON payload to internal proto JSON using the mapping definition.
func (e *Engine) TransformInbound(mapping *mappingv1.MappingDefinition, externalJSON []byte) (*InboundResult, error) {
	if !gjson.ValidBytes(externalJSON) {
		return nil, ErrInvalidJSON
	}

	if err := e.runValidationCEL(mapping.GetInboundValidationCel(), externalJSON); err != nil {
		return nil, fmt.Errorf("%w: inbound validation: %w", ErrValidation, err)
	}

	output, err := e.applyInboundFields(mapping.GetFields(), externalJSON)
	if err != nil {
		return nil, err
	}

	output, err = e.applyComputedFields(mapping.GetInboundComputedFields(), externalJSON, output)
	if err != nil {
		return nil, err
	}

	idempotencyKey, err := e.deriveIdempotencyKey(mapping.GetIdempotency(), externalJSON)
	if err != nil {
		return nil, err
	}

	return &InboundResult{
		ProtoJSON:      []byte(output),
		IdempotencyKey: idempotencyKey,
	}, nil
}

// TransformOutbound maps an internal proto JSON payload to external JSON using the mapping definition.
func (e *Engine) TransformOutbound(mapping *mappingv1.MappingDefinition, protoJSON []byte) ([]byte, error) {
	if !gjson.ValidBytes(protoJSON) {
		return nil, ErrInvalidJSON
	}

	if err := e.runValidationCEL(mapping.GetOutboundValidationCel(), protoJSON); err != nil {
		return nil, fmt.Errorf("%w: outbound validation: %w", ErrValidation, err)
	}

	output, err := e.applyOutboundFields(mapping.GetFields(), protoJSON)
	if err != nil {
		return nil, err
	}

	output, err = e.applyComputedFields(mapping.GetOutboundComputedFields(), protoJSON, output)
	if err != nil {
		return nil, err
	}

	return []byte(output), nil
}

// runValidationCEL evaluates a validation CEL expression against the source JSON.
// Returns nil if the expression is empty or evaluates to true.
func (e *Engine) runValidationCEL(expr string, sourceJSON []byte) error {
	if expr == "" {
		return nil
	}

	inputMap := gjsonToMap(gjson.ParseBytes(sourceJSON))
	pass, err := e.evalBoolCEL(expr, map[string]any{
		"payload": inputMap,
		"input":   inputMap,
	})
	if err != nil {
		return err
	}
	if !pass {
		return fmt.Errorf("%w: validation expression returned false", ErrValidation)
	}
	return nil
}

// applyInboundFields applies field correspondences for inbound transformation.
func (e *Engine) applyInboundFields(fields []*mappingv1.FieldCorrespondence, externalJSON []byte) (string, error) {
	output := "{}"
	for _, field := range fields {
		val := gjson.GetBytes(externalJSON, field.GetExternalPath())

		// Attribute flatten derives from multiple source keys; run regardless of ExternalPath existence.
		if ft := field.GetTransform(); ft != nil && ft.GetAttributeFlatten() != nil {
			transformed, err := e.applyInboundTransform(ft, val, externalJSON)
			if err != nil {
				return "", fmt.Errorf("field %s -> %s: %w", field.GetExternalPath(), field.GetInternalPath(), err)
			}
			output, err = sjson.Set(output, field.GetInternalPath(), transformed)
			if err != nil {
				return "", fmt.Errorf("%w: setting %s: %w", ErrFieldExtraction, field.GetInternalPath(), err)
			}
			continue
		}

		if !val.Exists() {
			var err error
			output, err = e.applyDefaultIfPresent(field, output)
			if err != nil {
				return "", err
			}
			continue
		}

		transformed, err := e.applyInboundTransform(field.GetTransform(), val, externalJSON)
		if err != nil {
			return "", fmt.Errorf("field %s -> %s: %w", field.GetExternalPath(), field.GetInternalPath(), err)
		}

		output, err = sjson.Set(output, field.GetInternalPath(), transformed)
		if err != nil {
			return "", fmt.Errorf("%w: setting %s: %w", ErrFieldExtraction, field.GetInternalPath(), err)
		}
	}
	return output, nil
}

// applyDefaultIfPresent sets a default value if the field transform specifies one.
func (e *Engine) applyDefaultIfPresent(field *mappingv1.FieldCorrespondence, output string) (string, error) {
	ft := field.GetTransform()
	if ft == nil {
		return output, nil
	}
	dv := ft.GetDefaultValue()
	if dv == "" {
		return output, nil
	}
	result, err := sjson.Set(output, field.GetInternalPath(), dv)
	if err != nil {
		return "", fmt.Errorf("%w: setting default for %s: %w", ErrFieldExtraction, field.GetInternalPath(), err)
	}
	return result, nil
}

// applyOutboundFields applies field correspondences for outbound transformation.
// After mapping each field to its external path, the original internal path is removed
// from the output to prevent leaking internal field names to external consumers.
func (e *Engine) applyOutboundFields(fields []*mappingv1.FieldCorrespondence, protoJSON []byte) (string, error) {
	output := string(protoJSON)
	for _, field := range fields {
		val := gjson.GetBytes(protoJSON, field.GetInternalPath())
		if !val.Exists() {
			continue
		}

		transformed, err := e.applyOutboundTransform(field.GetTransform(), val, protoJSON)
		if err != nil {
			return "", fmt.Errorf("field %s -> %s: %w", field.GetInternalPath(), field.GetExternalPath(), err)
		}

		output, err = sjson.Set(output, field.GetExternalPath(), transformed)
		if err != nil {
			return "", fmt.Errorf("%w: setting %s: %w", ErrFieldExtraction, field.GetExternalPath(), err)
		}

		// Remove the original internal field to avoid leaking internal names.
		if field.GetInternalPath() != field.GetExternalPath() {
			output, _ = sjson.Delete(output, field.GetInternalPath())
		}
	}
	return output, nil
}

// applyComputedFields evaluates computed CEL fields and adds them to the output.
func (e *Engine) applyComputedFields(fields []*mappingv1.ComputedField, sourceJSON []byte, output string) (string, error) {
	if len(fields) == 0 {
		return output, nil
	}

	inputMap := gjsonToMap(gjson.ParseBytes(sourceJSON))
	mappedMap := gjsonToMap(gjson.Parse(output))

	for _, cf := range fields {
		result, err := e.evalCEL(cf.GetCelExpression(), map[string]any{
			"input":  inputMap,
			"mapped": mappedMap,
		})
		if err != nil {
			return "", fmt.Errorf("%w: computed field %s: %w", ErrCELEvaluation, cf.GetTargetPath(), err)
		}

		output, err = sjson.Set(output, cf.GetTargetPath(), celValToInterface(result))
		if err != nil {
			return "", fmt.Errorf("%w: setting computed field %s: %w", ErrFieldExtraction, cf.GetTargetPath(), err)
		}
		mappedMap = gjsonToMap(gjson.Parse(output))
	}
	return output, nil
}

// applyInboundTransform applies the field transform for inbound direction.
func (e *Engine) applyInboundTransform(ft *mappingv1.FieldTransform, val gjson.Result, sourceJSON []byte) (any, error) {
	if ft == nil {
		return gjsonToInterface(val), nil
	}

	switch {
	case ft.GetEnumMapping() != nil:
		return e.applyEnumInbound(ft.GetEnumMapping(), val)
	case ft.GetDateFormat() != "":
		return e.applyDateInbound(ft.GetDateFormat(), val)
	case ft.GetCel() != nil:
		return e.applyCELInbound(ft.GetCel(), val, sourceJSON)
	case ft.GetAttributeFlatten() != nil:
		return e.applyAttributeFlattenInbound(ft.GetAttributeFlatten(), sourceJSON)
	default:
		return gjsonToInterface(val), nil
	}
}

// applyOutboundTransform applies the field transform for outbound (reverse) direction.
func (e *Engine) applyOutboundTransform(ft *mappingv1.FieldTransform, val gjson.Result, sourceJSON []byte) (any, error) {
	if ft == nil {
		return gjsonToInterface(val), nil
	}

	switch {
	case ft.GetEnumMapping() != nil:
		return e.applyEnumOutbound(ft.GetEnumMapping(), val)
	case ft.GetDateFormat() != "":
		return e.applyDateOutbound(ft.GetDateFormat(), val)
	case ft.GetCel() != nil:
		return e.applyCELOutbound(ft.GetCel(), val, sourceJSON)
	case ft.GetAttributeFlatten() != nil:
		return e.applyAttributeFlattenOutbound(val)
	default:
		return gjsonToInterface(val), nil
	}
}

func (e *Engine) applyEnumInbound(em *mappingv1.EnumMapping, val gjson.Result) (any, error) {
	extVal := val.String()
	if mapped, ok := em.GetValues()[extVal]; ok {
		return mapped, nil
	}
	if fb := em.GetFallback(); fb != "" {
		return fb, nil
	}
	return nil, fmt.Errorf("%w: no mapping for external value %q", ErrEnumNotMapped, extVal)
}

func (e *Engine) applyEnumOutbound(em *mappingv1.EnumMapping, val gjson.Result) (any, error) {
	intVal := val.String()
	for extKey, intKey := range em.GetValues() {
		if intKey == intVal {
			return extKey, nil
		}
	}
	if fb := em.GetOutboundFallback(); fb != "" {
		return fb, nil
	}
	return nil, fmt.Errorf("%w: no mapping for internal value %q", ErrEnumNotMapped, intVal)
}

func (e *Engine) applyDateInbound(layout string, val gjson.Result) (any, error) {
	t, err := time.Parse(layout, val.String())
	if err != nil {
		return nil, fmt.Errorf("%w: parsing %q with layout %q: %w", ErrDateParse, val.String(), layout, err)
	}
	return t.Format(time.RFC3339), nil
}

func (e *Engine) applyDateOutbound(layout string, val gjson.Result) (any, error) {
	t, err := time.Parse(time.RFC3339, val.String())
	if err != nil {
		return nil, fmt.Errorf("%w: parsing RFC3339 %q: %w", ErrDateParse, val.String(), err)
	}
	return t.Format(layout), nil
}

func (e *Engine) applyCELInbound(ct *mappingv1.CelTransform, val gjson.Result, sourceJSON []byte) (any, error) {
	expr := ct.GetInboundCel()
	if expr == "" {
		return gjsonToInterface(val), nil
	}

	result, err := e.evalCEL(expr, map[string]any{
		"value": gjsonToInterface(val),
		"input": gjsonToMap(gjson.ParseBytes(sourceJSON)),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: inbound CEL %q: %w", ErrCELEvaluation, expr, err)
	}
	return celValToInterface(result), nil
}

func (e *Engine) applyCELOutbound(ct *mappingv1.CelTransform, val gjson.Result, sourceJSON []byte) (any, error) {
	expr := ct.GetOutboundCel()
	if expr == "" {
		return gjsonToInterface(val), nil
	}

	result, err := e.evalCEL(expr, map[string]any{
		"value": gjsonToInterface(val),
		"input": gjsonToMap(gjson.ParseBytes(sourceJSON)),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: outbound CEL %q: %w", ErrCELEvaluation, expr, err)
	}
	return celValToInterface(result), nil
}

func (e *Engine) applyAttributeFlattenInbound(af *mappingv1.AttributeFlatten, sourceJSON []byte) (any, error) {
	var entries []map[string]string
	for _, key := range af.GetSourceKeys() {
		val := gjson.GetBytes(sourceJSON, key)
		if val.Exists() {
			entries = append(entries, map[string]string{
				"key":   key,
				"value": val.String(),
			})
		}
	}
	return entries, nil
}

func (e *Engine) applyAttributeFlattenOutbound(val gjson.Result) (any, error) {
	result := map[string]string{}
	val.ForEach(func(_, entry gjson.Result) bool {
		k := entry.Get("key").String()
		v := entry.Get("value").String()
		if k != "" {
			result[k] = v
		}
		return true
	})
	return result, nil
}

func (e *Engine) deriveIdempotencyKey(config *mappingv1.IdempotencyConfig, externalJSON []byte) (string, error) {
	if config == nil {
		return "", nil
	}

	if config.GetUseContentHash() {
		return e.deriveContentHash(config.GetContentHashFields(), externalJSON)
	}

	return e.deriveFromSelector(config.GetSourceSelector(), externalJSON)
}

func (e *Engine) deriveContentHash(fields []string, externalJSON []byte) (string, error) {
	if len(fields) == 0 {
		return "", fmt.Errorf("%w: content hash mode requires at least one field", ErrIdempotencyKey)
	}

	hasher := sha256.New()
	sortedFields := make([]string, len(fields))
	copy(sortedFields, fields)
	sort.Strings(sortedFields)

	for _, path := range sortedFields {
		val := gjson.GetBytes(externalJSON, path)
		if !val.Exists() {
			return "", fmt.Errorf("%w: content hash field %q not found in payload", ErrIdempotencyKey, path)
		}
		hasher.Write([]byte(path))
		hasher.Write([]byte("="))
		hasher.Write([]byte(val.String()))
		hasher.Write([]byte(";"))
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (e *Engine) deriveFromSelector(selector string, externalJSON []byte) (string, error) {
	if selector == "" {
		return "", fmt.Errorf("%w: source_selector is required when use_content_hash is false", ErrIdempotencyKey)
	}

	val := gjson.GetBytes(externalJSON, selector)
	if !val.Exists() {
		return "", fmt.Errorf("%w: source_selector %q not found in payload", ErrIdempotencyKey, selector)
	}

	return val.String(), nil
}

// CEL evaluation helpers.

func (e *Engine) compileCEL(expression string) (cel.Program, error) {
	key := celCacheKey(expression)
	if prg, ok := e.celCache.Get(key); ok {
		return prg, nil
	}

	ast, issues := e.env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("%w: %w", ErrCELCompilation, issues.Err())
	}

	prg, err := e.env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCELCompilation, err)
	}

	e.celCache.Add(key, prg)
	return prg, nil
}

func (e *Engine) evalCEL(expression string, vars map[string]any) (ref.Val, error) {
	prg, err := e.compileCEL(expression)
	if err != nil {
		return nil, err
	}

	out, _, err := prg.Eval(vars)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCELEvaluation, err)
	}
	return out, nil
}

func (e *Engine) evalBoolCEL(expression string, vars map[string]any) (bool, error) {
	result, err := e.evalCEL(expression, vars)
	if err != nil {
		return false, err
	}

	b, ok := result.Value().(bool)
	if !ok {
		return false, fmt.Errorf("%w: expected bool, got %T", ErrCELEvaluation, result.Value())
	}
	return b, nil
}

func celCacheKey(expression string) string {
	h := sha256.Sum256([]byte(expression))
	return hex.EncodeToString(h[:])
}

// JSON helpers.

func gjsonToInterface(r gjson.Result) any {
	switch r.Type {
	case gjson.Null:
		return nil
	case gjson.False:
		return false
	case gjson.True:
		return true
	case gjson.Number:
		if r.Num == float64(int64(r.Num)) {
			return int64(r.Num)
		}
		return r.Num
	case gjson.String:
		return r.Str
	case gjson.JSON:
		if r.IsArray() {
			var arr []any
			r.ForEach(func(_, v gjson.Result) bool {
				arr = append(arr, gjsonToInterface(v))
				return true
			})
			return arr
		}
		if r.IsObject() {
			return gjsonToMap(r)
		}
		return r.Raw
	default:
		return r.Raw
	}
}

func gjsonToMap(r gjson.Result) map[string]any {
	m := make(map[string]any)
	r.ForEach(func(key, value gjson.Result) bool {
		m[key.Str] = gjsonToInterface(value)
		return true
	})
	return m
}

func celValToInterface(v ref.Val) any {
	if v == nil {
		return nil
	}
	switch v.Type() {
	case types.BoolType, types.IntType, types.DoubleType, types.StringType:
		return v.Value()
	case types.TimestampType:
		if t, ok := v.Value().(time.Time); ok {
			return t.Format(time.RFC3339)
		}
		return v.Value()
	default:
		if l, ok := v.(traits.Lister); ok {
			it := l.Iterator()
			var out []any
			for it.HasNext() == types.True {
				out = append(out, celValToInterface(it.Next()))
			}
			return out
		}
		if m, ok := v.(traits.Mapper); ok {
			it := m.Iterator()
			out := map[string]any{}
			for it.HasNext() == types.True {
				k := it.Next()
				out[fmt.Sprint(k.Value())] = celValToInterface(m.Get(k))
			}
			return out
		}
		return v.Value()
	}
}

// CacheLen returns the number of cached CEL programs (for testing).
func (e *Engine) CacheLen() int {
	return e.celCache.Len()
}

// CacheContains checks if a CEL expression is cached (for testing).
func (e *Engine) CacheContains(expression string) bool {
	return e.celCache.Contains(celCacheKey(expression))
}

// EscapeJSONPath escapes special characters in a JSON path for sjson.
func EscapeJSONPath(path string) string {
	return strings.ReplaceAll(path, ".", "\\.")
}
