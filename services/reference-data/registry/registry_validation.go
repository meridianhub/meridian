package registry

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ValidateAttributes executes the CEL validation expression against the provided attributes.
func (r *PostgresRegistry) ValidateAttributes(ctx context.Context, code string, version int, attrs AttributeBag) (ValidationResult, error) {
	def, err := r.GetDefinition(ctx, code, version)
	if err != nil {
		return ValidationResult{}, err
	}

	if def.ValidationExpression == "" {
		return ValidationResult{Valid: true}, nil
	}

	prg, err := r.getOrCompileValidation(ctx, code, version, def.ValidationExpression)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to get validation program: %w", err)
	}

	input := r.buildCELInput(attrs)
	return r.executeValidation(ctx, prg, input, def, code, version)
}

// buildCELInput constructs the input map for CEL evaluation.
func (r *PostgresRegistry) buildCELInput(attrs AttributeBag) map[string]any {
	// Ensure attributes is never nil to prevent CEL evaluation issues
	attributes := attrs.Attributes
	if attributes == nil {
		attributes = make(map[string]string)
	}

	input := map[string]any{
		"attributes": attributes,
		"amount":     attrs.Amount,
		"source":     attrs.Source,
		"valid_from": time.Time{},
		"valid_to":   time.Time{},
	}

	if attrs.ValidFrom != nil {
		input["valid_from"] = *attrs.ValidFrom
	}
	if attrs.ValidTo != nil {
		input["valid_to"] = *attrs.ValidTo
	}

	return input
}

// executeValidation runs the CEL program and handles the result.
func (r *PostgresRegistry) executeValidation(ctx context.Context, prg cel.Program, input map[string]any, def *InstrumentDefinition, code string, version int) (ValidationResult, error) {
	result, _, err := prg.Eval(input)
	if err != nil {
		return ValidationResult{
			Valid:        false,
			ErrorMessage: fmt.Sprintf("validation expression error: %v", err),
		}, nil
	}

	valid, ok := result.Value().(bool)
	if !ok {
		return ValidationResult{
			Valid:        false,
			ErrorMessage: "validation expression did not return boolean",
		}, nil
	}

	if valid {
		return ValidationResult{Valid: true}, nil
	}

	errorMsg := r.getCustomErrorMessage(ctx, def, code, version, input)
	return ValidationResult{Valid: false, ErrorMessage: errorMsg}, nil
}

const defaultValidationErrorMsg = "validation failed"

// getCustomErrorMessage evaluates the error message expression if defined.
func (r *PostgresRegistry) getCustomErrorMessage(ctx context.Context, def *InstrumentDefinition, code string, version int, input map[string]any) string {
	if def.ErrorMessageExpression == "" {
		return defaultValidationErrorMsg
	}

	errorPrg, err := r.getOrCompileErrorMessage(ctx, code, version, def.ErrorMessageExpression)
	if err != nil {
		return defaultValidationErrorMsg
	}

	errorResult, _, evalErr := errorPrg.Eval(input)
	if evalErr != nil {
		return defaultValidationErrorMsg
	}

	if msg, ok := errorResult.Value().(string); ok {
		return msg
	}

	return defaultValidationErrorMsg
}

// compileCELExpressions validates and compiles all CEL expressions in a definition.
func (r *PostgresRegistry) compileCELExpressions(def *InstrumentDefinition) error {
	if def.ValidationExpression != "" {
		_, err := r.compiler.CompileValidation(def.ValidationExpression)
		if err != nil {
			return errors.Join(ErrInvalidCEL, fmt.Errorf("validation expression: %w", err))
		}
	}

	if def.FungibilityKeyExpression != "" {
		_, err := r.compiler.CompileBucketKey(def.FungibilityKeyExpression)
		if err != nil {
			return errors.Join(ErrInvalidCEL, fmt.Errorf("fungibility key expression: %w", err))
		}
	}

	// ErrorMessageExpression uses the same environment as validation but returns a string.
	if def.ErrorMessageExpression != "" {
		_, err := r.compiler.CompileValueExpression(def.ErrorMessageExpression)
		if err != nil {
			return errors.Join(ErrInvalidCEL, fmt.Errorf("error message expression: %w", err))
		}
	}

	return nil
}

// celCacheKeyPrefix builds the tenant-and-instrument-scoped prefix shared by
// every cache entry for a given (tenant, code, version, exprType) tuple.
// Tenant id is included so that two tenants defining the same instrument
// code/version with different CEL expressions never share a compiled
// program - see DAT-6.
func (r *PostgresRegistry) celCacheKeyPrefix(ctx context.Context, code string, version int, exprType string) string {
	tenantID, _ := tenant.FromContext(ctx)
	return fmt.Sprintf("%s:%s:%d:%s:", tenantID, code, version, exprType)
}

// celCacheKey builds the full cache key, including a hash of the expression
// text so that updated expressions never collide with a stale cached
// program for the same tenant/code/version/type.
func (r *PostgresRegistry) celCacheKey(ctx context.Context, code string, version int, exprType, expr string) string {
	exprHash := sha256.Sum256([]byte(expr))
	return fmt.Sprintf("%s%x", r.celCacheKeyPrefix(ctx, code, version, exprType), exprHash)
}

// getOrCompileValidation gets a cached validation program or compiles one.
func (r *PostgresRegistry) getOrCompileValidation(ctx context.Context, code string, version int, expr string) (cel.Program, error) {
	cacheKey := r.celCacheKey(ctx, code, version, "validation", expr)

	r.programCacheMu.RLock()
	prg, ok := r.programCache[cacheKey]
	r.programCacheMu.RUnlock()

	if ok {
		return prg, nil
	}

	// Compile and cache
	prg, err := r.compiler.CompileValidation(expr)
	if err != nil {
		return nil, err
	}

	r.programCacheMu.Lock()
	r.programCache[cacheKey] = prg
	r.programCacheMu.Unlock()

	return prg, nil
}

// getOrCompileErrorMessage gets a cached error message program or compiles one.
func (r *PostgresRegistry) getOrCompileErrorMessage(ctx context.Context, code string, version int, expr string) (cel.Program, error) {
	cacheKey := r.celCacheKey(ctx, code, version, "error", expr)

	r.programCacheMu.RLock()
	prg, ok := r.programCache[cacheKey]
	r.programCacheMu.RUnlock()

	if ok {
		return prg, nil
	}

	// Compile and cache; error message expressions return strings, not booleans.
	prg, err := r.compiler.CompileValueExpression(expr)
	if err != nil {
		return nil, err
	}

	r.programCacheMu.Lock()
	r.programCache[cacheKey] = prg
	r.programCacheMu.Unlock()

	return prg, nil
}

// invalidateCache removes cached programs for an instrument, scoped to the
// tenant on ctx. Because cache keys include a hash of the expression text,
// entries are removed by tenant:code:version:type prefix rather than by an
// exact key match.
func (r *PostgresRegistry) invalidateCache(ctx context.Context, code string, version int) {
	prefixes := []string{
		r.celCacheKeyPrefix(ctx, code, version, "validation"),
		r.celCacheKeyPrefix(ctx, code, version, "error"),
		r.celCacheKeyPrefix(ctx, code, version, "bucket"),
	}

	r.programCacheMu.Lock()
	defer r.programCacheMu.Unlock()

	for key := range r.programCache {
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				delete(r.programCache, key)
				break
			}
		}
	}
}
