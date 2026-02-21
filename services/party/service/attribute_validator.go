package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	sharedcel "github.com/meridianhub/meridian/shared/pkg/cel"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// celCacheSize is the maximum number of compiled CEL programs retained in the LRU cache.
const celCacheSize = 256

// Attribute validator errors.
var (
	// ErrAttributeValidatorRepoNil is returned when creating an AttributeValidator with a nil repository.
	ErrAttributeValidatorRepoNil = errors.New("party type definition repository cannot be nil")

	// ErrAttributeValidatorCompilerNil is returned when creating an AttributeValidator with a nil compiler.
	ErrAttributeValidatorCompilerNil = errors.New("CEL compiler cannot be nil")

	// ErrAttributeValidationFailed is returned when attribute validation fails against the schema or CEL rule.
	ErrAttributeValidationFailed = errors.New("attribute validation failed")

	// ErrCELNotBoolean is returned when a CEL validation expression does not return a boolean.
	ErrCELNotBoolean = errors.New("validation CEL expression did not return boolean")

	// ErrCELValidationFailed is returned when a CEL validation expression evaluates to false.
	ErrCELValidationFailed = errors.New("validation CEL expression returned false")
)

// AttributeValidator validates party attributes against a tenant's PartyTypeDefinition.
// It performs JSON Schema validation and optional CEL-based cross-field validation.
// Compiled CEL programs are cached in an LRU cache to avoid recompilation per request.
type AttributeValidator struct {
	repo        PartyTypeDefinitionRepository
	celCompiler *sharedcel.Compiler
	// cache keys are "<tenantID>:<partyType>" → compiled CEL program
	cache *lru.Cache[string, cel.Program]
}

// NewAttributeValidator creates a new AttributeValidator.
// Returns an error if either dependency is nil.
func NewAttributeValidator(repo PartyTypeDefinitionRepository, compiler *sharedcel.Compiler) (*AttributeValidator, error) {
	if repo == nil {
		return nil, ErrAttributeValidatorRepoNil
	}
	if compiler == nil {
		return nil, ErrAttributeValidatorCompilerNil
	}

	cache, err := lru.New[string, cel.Program](celCacheSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL program cache: %w", err)
	}

	return &AttributeValidator{
		repo:        repo,
		celCompiler: compiler,
		cache:       cache,
	}, nil
}

// CacheLen returns the number of compiled CEL programs currently held in the cache.
// Intended for testing and observability.
func (v *AttributeValidator) CacheLen() int {
	return v.cache.Len()
}

// ValidateAttributes validates party attributes against the tenant's PartyTypeDefinition
// for the given party type. If no definition exists for the tenant+type combination,
// validation is skipped (definitions are optional). Returns ErrAttributeValidationFailed
// (wrapping details) when validation fails.
func (v *AttributeValidator) ValidateAttributes(ctx context.Context, tenantID, partyType string, party *domain.Party) error {
	// Look up the party type definition
	def, err := v.repo.GetByTenantAndType(ctx, tenantID, partyType)
	if err != nil {
		if errors.Is(err, persistence.ErrPartyTypeDefinitionNotFound) {
			// No definition registered - validation is optional, skip
			return nil
		}
		return fmt.Errorf("failed to look up party type definition: %w", err)
	}

	// Build attribute map from party's attributes
	attrMap := attributeEntriesToMap(party.Attributes())

	// 1. JSON Schema validation
	if err := validateAttributesAgainstSchema(def.AttributeSchema, attrMap); err != nil {
		return fmt.Errorf("%w: %w", ErrAttributeValidationFailed, err)
	}

	// 2. CEL cross-field validation (only when a validation expression is configured)
	if def.ValidationCEL != "" {
		if err := v.evaluateValidationCEL(def, attrMap); err != nil {
			return fmt.Errorf("%w: %w", ErrAttributeValidationFailed, err)
		}
	}

	return nil
}

// evaluateValidationCEL compiles (or retrieves from cache) and evaluates the validation
// CEL expression for the given party type definition.
func (v *AttributeValidator) evaluateValidationCEL(def *persistence.PartyTypeDefinitionEntity, attrMap map[string]string) error {
	cacheKey := def.TenantID + ":" + def.PartyType

	prg, ok := v.cache.Get(cacheKey)
	if !ok {
		compiled, err := v.celCompiler.CompileValidation(def.ValidationCEL)
		if err != nil {
			return fmt.Errorf("failed to compile validation CEL: %w", err)
		}
		v.cache.Add(cacheKey, compiled)
		prg = compiled
	}

	// Evaluate CEL with the attribute map.
	// The validation environment expects: attributes (map<string,string>), amount (string),
	// valid_from (timestamp), valid_to (timestamp), source (string).
	// For party attribute validation we only populate 'attributes'; other vars use zero values.
	zero := time.Time{}
	out, _, err := prg.Eval(map[string]any{
		"attributes": attrMap,
		"amount":     "",
		"valid_from": zero,
		"valid_to":   zero,
		"source":     "",
	})
	if err != nil {
		return fmt.Errorf("CEL evaluation error: %w", err)
	}

	result, ok := out.Value().(bool)
	if !ok {
		return ErrCELNotBoolean
	}

	if !result {
		return ErrCELValidationFailed
	}

	return nil
}

// validateAttributesAgainstSchema validates the attribute map against a JSON Schema.
// The schema is compiled inline; for production-scale use the schema would also be cached.
func validateAttributesAgainstSchema(schemaJSON string, attrMap map[string]string) error {
	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft7

	// Add the schema as an in-memory resource
	schemaURL := "mem:///party_attributes_schema.json"
	if err := compiler.AddResource(schemaURL, strings.NewReader(schemaJSON)); err != nil {
		return fmt.Errorf("invalid attribute schema: %w", err)
	}

	sch, err := compiler.Compile(schemaURL)
	if err != nil {
		return fmt.Errorf("failed to compile attribute schema: %w", err)
	}

	// Convert map[string]string to map[string]interface{} for schema validation
	doc := make(map[string]interface{}, len(attrMap))
	for k, val := range attrMap {
		doc[k] = val
	}

	if err := sch.Validate(doc); err != nil {
		return fmt.Errorf("attributes do not match schema: %w", err)
	}

	return nil
}

// attributeEntriesToMap converts a slice of AttributeEntry to a map[string]string.
func attributeEntriesToMap(entries []domain.AttributeEntry) map[string]string {
	result := make(map[string]string, len(entries))
	for _, e := range entries {
		result[e.Key] = e.Value
	}
	return result
}
