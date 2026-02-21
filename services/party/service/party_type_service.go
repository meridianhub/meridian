package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	sharedcel "github.com/meridianhub/meridian/shared/pkg/cel"
)

// MaxAttributeSchemaSize is the maximum allowed size of an attribute schema in bytes (16KB).
const MaxAttributeSchemaSize = 16 * 1024

// Party type definition service errors
var (
	ErrPartyTypeRepoNil           = errors.New("party type definition repository cannot be nil")
	ErrAttributeSchemaEmpty       = errors.New("attribute_schema must not be empty")
	ErrAttributeSchemaTooBig      = errors.New("attribute_schema exceeds maximum size of 16KB")
	ErrAttributeSchemaInvalidJSON = errors.New("attribute_schema must be a valid JSON Schema object")
	ErrValidationCELInvalid       = errors.New("validation_cel expression is invalid")
	ErrEligibilityCELInvalid      = errors.New("eligibility_cel expression is invalid")
	ErrErrorMessageCELInvalid     = errors.New("error_message_cel expression is invalid")
)

// PartyTypeDefinitionRepository defines the interface for party type definition persistence.
type PartyTypeDefinitionRepository interface {
	Create(ctx context.Context, entity *persistence.PartyTypeDefinitionEntity) error
	GetByID(ctx context.Context, id uuid.UUID) (*persistence.PartyTypeDefinitionEntity, error)
	GetByTenantAndType(ctx context.Context, tenantID, partyType string) (*persistence.PartyTypeDefinitionEntity, error)
	ListByTenant(ctx context.Context, tenantID string, partyType string) ([]*persistence.PartyTypeDefinitionEntity, error)
	Update(ctx context.Context, entity *persistence.PartyTypeDefinitionEntity) error
}

// PartyTypeDefinitionService provides business logic for managing party type definitions.
type PartyTypeDefinitionService struct {
	repo        PartyTypeDefinitionRepository
	celCompiler *sharedcel.Compiler
}

// NewPartyTypeDefinitionService creates a new party type definition service.
func NewPartyTypeDefinitionService(repo PartyTypeDefinitionRepository) (*PartyTypeDefinitionService, error) {
	if repo == nil {
		return nil, ErrPartyTypeRepoNil
	}

	compiler, err := sharedcel.NewCompiler()
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL compiler: %w", err)
	}

	return &PartyTypeDefinitionService{
		repo:        repo,
		celCompiler: compiler,
	}, nil
}

// RegisterPartyTypeInput holds the input for registering a new party type definition.
type RegisterPartyTypeInput struct {
	TenantID        string
	PartyType       string
	AttributeSchema string
	ValidationCEL   string
	EligibilityCEL  string
	ErrorMessageCEL string
}

// UpdatePartyTypeInput holds the input for updating a party type definition.
// Pointer fields distinguish "not provided" (nil = preserve existing) from "set to empty" (ptr to "" = clear).
type UpdatePartyTypeInput struct {
	AttributeSchema *string
	ValidationCEL   *string
	EligibilityCEL  *string
	ErrorMessageCEL *string
	Version         int64
}

// Register creates a new party type definition after validating the JSON Schema and CEL expressions.
func (s *PartyTypeDefinitionService) Register(ctx context.Context, input RegisterPartyTypeInput) (*persistence.PartyTypeDefinitionEntity, error) {
	if err := s.validateAttributeSchema(input.AttributeSchema); err != nil {
		return nil, err
	}
	if err := s.validateCELExpressions(input.ValidationCEL, input.EligibilityCEL, input.ErrorMessageCEL); err != nil {
		return nil, err
	}

	now := time.Now()
	entity := &persistence.PartyTypeDefinitionEntity{
		ID:              uuid.New(),
		TenantID:        input.TenantID,
		PartyType:       input.PartyType,
		AttributeSchema: input.AttributeSchema,
		ValidationCEL:   input.ValidationCEL,
		EligibilityCEL:  input.EligibilityCEL,
		ErrorMessageCEL: input.ErrorMessageCEL,
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.repo.Create(ctx, entity); err != nil {
		return nil, err
	}

	return entity, nil
}

// CELCompiler returns the CEL compiler used by this service.
// Allows sharing the compiler with other components such as AttributeValidator.
func (s *PartyTypeDefinitionService) CELCompiler() *sharedcel.Compiler {
	return s.celCompiler
}

// GetByID retrieves a party type definition by ID.
func (s *PartyTypeDefinitionService) GetByID(ctx context.Context, id uuid.UUID) (*persistence.PartyTypeDefinitionEntity, error) {
	return s.repo.GetByID(ctx, id)
}

// GetByTenantAndType retrieves a party type definition by tenant and party type.
func (s *PartyTypeDefinitionService) GetByTenantAndType(ctx context.Context, tenantID, partyType string) (*persistence.PartyTypeDefinitionEntity, error) {
	return s.repo.GetByTenantAndType(ctx, tenantID, partyType)
}

// ListByTenant retrieves all party type definitions for a tenant, optionally filtered by party type.
func (s *PartyTypeDefinitionService) ListByTenant(ctx context.Context, tenantID string, partyType string) ([]*persistence.PartyTypeDefinitionEntity, error) {
	return s.repo.ListByTenant(ctx, tenantID, partyType)
}

// Update applies partial updates to a party type definition with optimistic locking.
func (s *PartyTypeDefinitionService) Update(ctx context.Context, id uuid.UUID, input UpdatePartyTypeInput) (*persistence.PartyTypeDefinitionEntity, error) {
	// Load current definition
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Optimistic locking check: caller must always supply the current version.
	if existing.Version != input.Version {
		return nil, persistence.ErrPartyTypeVersionConflict
	}

	// Apply updates: nil pointer means "not provided" (preserve existing); non-nil means "set to this value" (including empty = clear).
	if input.AttributeSchema != nil {
		if err := s.validateAttributeSchema(*input.AttributeSchema); err != nil {
			return nil, err
		}
		existing.AttributeSchema = *input.AttributeSchema
	}

	updatedValidationCEL := existing.ValidationCEL
	updatedEligibilityCEL := existing.EligibilityCEL
	updatedErrorMessageCEL := existing.ErrorMessageCEL

	if input.ValidationCEL != nil {
		updatedValidationCEL = *input.ValidationCEL
	}
	if input.EligibilityCEL != nil {
		updatedEligibilityCEL = *input.EligibilityCEL
	}
	if input.ErrorMessageCEL != nil {
		updatedErrorMessageCEL = *input.ErrorMessageCEL
	}

	if err := s.validateCELExpressions(updatedValidationCEL, updatedEligibilityCEL, updatedErrorMessageCEL); err != nil {
		return nil, err
	}

	existing.ValidationCEL = updatedValidationCEL
	existing.EligibilityCEL = updatedEligibilityCEL
	existing.ErrorMessageCEL = updatedErrorMessageCEL
	existing.UpdatedAt = time.Now()
	existing.Version++

	if err := s.repo.Update(ctx, existing); err != nil {
		return nil, err
	}

	return existing, nil
}

// validateAttributeSchema validates that the attribute schema is a valid non-empty JSON object
// and within the 16KB size limit.
func (s *PartyTypeDefinitionService) validateAttributeSchema(schema string) error {
	if schema == "" {
		return ErrAttributeSchemaEmpty
	}
	if len(schema) > MaxAttributeSchemaSize {
		return ErrAttributeSchemaTooBig
	}
	// Must be a valid JSON object (JSON Schema is always a JSON object).
	// Unmarshal into interface{} first so that JSON null (valid JSON but not an object)
	// is caught by the type assertion below rather than silently yielding a nil map.
	var parsed interface{}
	if err := json.Unmarshal([]byte(schema), &parsed); err != nil {
		return fmt.Errorf("%w: %w", ErrAttributeSchemaInvalidJSON, err)
	}
	if _, ok := parsed.(map[string]interface{}); !ok {
		return ErrAttributeSchemaInvalidJSON
	}
	return nil
}

// validateCELExpressions compiles each non-empty CEL expression to catch syntax errors early.
func (s *PartyTypeDefinitionService) validateCELExpressions(validationCEL, eligibilityCEL, errorMessageCEL string) error {
	if validationCEL != "" {
		if _, err := s.celCompiler.CompileValidation(validationCEL); err != nil {
			return fmt.Errorf("%w: %w", ErrValidationCELInvalid, err)
		}
	}
	if eligibilityCEL != "" {
		if _, err := s.celCompiler.CompileEligibility(eligibilityCEL); err != nil {
			return fmt.Errorf("%w: %w", ErrEligibilityCELInvalid, err)
		}
	}
	if errorMessageCEL != "" {
		// Error message CEL is a value expression (string output), use CompileValueExpression
		if _, err := s.celCompiler.CompileValueExpression(errorMessageCEL); err != nil {
			return fmt.Errorf("%w: %w", ErrErrorMessageCELInvalid, err)
		}
	}
	return nil
}
