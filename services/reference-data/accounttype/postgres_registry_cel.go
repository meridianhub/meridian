package accounttype

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
)

// ValidateTransaction executes the CEL validation expression against the provided attributes.
func (r *PostgresRegistry) ValidateTransaction(ctx context.Context, code string, version int, attrs AttributeBag) (ValidationResult, error) {
	def, err := r.GetDefinition(ctx, code, version)
	if err != nil {
		return ValidationResult{}, err
	}

	if def.ValidationCEL == "" {
		return ValidationResult{Valid: true}, nil
	}

	prg, err := r.getOrCompile(code, version, "validation", def.ValidationCEL, r.compiler.CompileValidation)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to compile validation: %w", err)
	}

	input := r.buildValidationInput(attrs)
	result, _, err := prg.Eval(input)
	if err != nil {
		return ValidationResult{
			Valid:  false,
			Errors: []string{fmt.Sprintf("validation expression error: %v", err)},
		}, nil
	}

	valid, ok := result.Value().(bool)
	if !ok {
		return ValidationResult{
			Valid:  false,
			Errors: []string{"validation expression did not return boolean"},
		}, nil
	}

	if valid {
		return ValidationResult{Valid: true}, nil
	}

	return ValidationResult{
		Valid:  false,
		Errors: []string{"validation failed"},
	}, nil
}

// CheckEligibility executes the CEL eligibility expression against the provided attributes.
func (r *PostgresRegistry) CheckEligibility(ctx context.Context, code string, version int, attrs AttributeBag) (ValidationResult, error) {
	def, err := r.GetDefinition(ctx, code, version)
	if err != nil {
		return ValidationResult{}, err
	}

	if def.EligibilityCEL == "" {
		return ValidationResult{Valid: true}, nil
	}

	prg, err := r.getOrCompile(code, version, "eligibility", def.EligibilityCEL, r.compiler.CompileEligibility)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to compile eligibility: %w", err)
	}

	attributes := attrs.Attributes
	if attributes == nil {
		attributes = make(map[string]string)
	}

	input := map[string]any{
		"party":      attributes,
		"attributes": attributes,
	}

	result, _, err := prg.Eval(input)
	if err != nil {
		return ValidationResult{
			Valid:  false,
			Errors: []string{fmt.Sprintf("eligibility expression error: %v", err)},
		}, nil
	}

	valid, ok := result.Value().(bool)
	if !ok {
		return ValidationResult{
			Valid:  false,
			Errors: []string{"eligibility expression did not return boolean"},
		}, nil
	}

	if valid {
		return ValidationResult{Valid: true}, nil
	}

	return ValidationResult{
		Valid:  false,
		Errors: []string{"eligibility check failed"},
	}, nil
}

// GetProductFeatures returns the attributes (product features) for an account type.
func (r *PostgresRegistry) GetProductFeatures(ctx context.Context, code string, version int) (map[string]any, error) {
	def, err := r.GetDefinition(ctx, code, version)
	if err != nil {
		return nil, err
	}

	if def.Attributes == nil {
		return map[string]any{}, nil
	}

	return def.Attributes, nil
}

func (r *PostgresRegistry) buildValidationInput(attrs AttributeBag) map[string]any {
	attributes := attrs.Attributes
	if attributes == nil {
		attributes = make(map[string]string)
	}

	return map[string]any{
		"attributes": attributes,
		"amount":     attrs.Amount,
		"source":     "",
		"valid_from": time.Time{},
		"valid_to":   time.Time{},
	}
}

func (r *PostgresRegistry) getOrCompile(code string, version int, exprType string, expr string, compileFn func(string) (cel.Program, error)) (cel.Program, error) {
	exprHash := sha256.Sum256([]byte(expr))
	cacheKey := fmt.Sprintf("%s:%d:%s:%x", code, version, exprType, exprHash)

	r.programCacheMu.RLock()
	prg, ok := r.programCache[cacheKey]
	r.programCacheMu.RUnlock()

	if ok {
		return prg, nil
	}

	prg, err := compileFn(expr)
	if err != nil {
		return nil, err
	}

	r.programCacheMu.Lock()
	r.programCache[cacheKey] = prg
	r.programCacheMu.Unlock()

	return prg, nil
}

func (r *PostgresRegistry) invalidateCache(code string, version int) {
	r.programCacheMu.Lock()
	defer r.programCacheMu.Unlock()

	prefix := fmt.Sprintf("%s:%d:", code, version)
	for key := range r.programCache {
		if strings.HasPrefix(key, prefix) {
			delete(r.programCache, key)
		}
	}
}
