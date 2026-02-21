package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	sharedcel "github.com/meridianhub/meridian/shared/pkg/cel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestAttributeValidator creates an AttributeValidator with a mock repo and real CEL compiler.
func newTestAttributeValidator(t *testing.T, repo PartyTypeDefinitionRepository) *AttributeValidator {
	t.Helper()
	compiler, err := sharedcel.NewCompiler()
	require.NoError(t, err)
	v, err := NewAttributeValidator(repo, compiler)
	require.NoError(t, err)
	return v
}

// makePartyTypeDefinition creates a test entity with given parameters.
func makePartyTypeDefinition(tenantID, partyType, schema, validationCEL string) *persistence.PartyTypeDefinitionEntity {
	return &persistence.PartyTypeDefinitionEntity{
		ID:              uuid.New(),
		TenantID:        tenantID,
		PartyType:       partyType,
		AttributeSchema: schema,
		ValidationCEL:   validationCEL,
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
}

// makeParty creates a domain Party with the given attributes.
func makeParty(partyType domain.PartyType, attrs []domain.AttributeEntry) *domain.Party {
	p, err := domain.NewParty(partyType, "Test Party")
	if err != nil {
		panic(err)
	}
	if attrs != nil {
		p.SetAttributes(attrs)
	}
	return p
}

// TestNewAttributeValidator_NilRepoReturnsError verifies that a nil repository is rejected.
func TestNewAttributeValidator_NilRepoReturnsError(t *testing.T) {
	compiler, err := sharedcel.NewCompiler()
	require.NoError(t, err)

	_, err = NewAttributeValidator(nil, compiler)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAttributeValidatorRepoNil)
}

// TestNewAttributeValidator_NilCompilerReturnsError verifies that a nil compiler is rejected.
func TestNewAttributeValidator_NilCompilerReturnsError(t *testing.T) {
	repo := newMockPartyTypeRepo()

	_, err := NewAttributeValidator(repo, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAttributeValidatorCompilerNil)
}

// TestValidateAttributes_NoDefinitionSkipsValidation verifies that when no PartyTypeDefinition
// is registered for a tenant+type, validation is skipped (not an error).
func TestValidateAttributes_NoDefinitionSkipsValidation(t *testing.T) {
	repo := newMockPartyTypeRepo()
	v := newTestAttributeValidator(t, repo)

	party := makeParty(domain.PartyTypePerson, []domain.AttributeEntry{
		{Key: "foo", Value: "bar"},
	})

	err := v.ValidateAttributes(context.Background(), testTenantID, "PERSON", party)
	require.NoError(t, err)
}

// TestValidateAttributes_EmptyAttributesPassValidation verifies that empty attributes
// pass validation when there is no required schema constraint.
func TestValidateAttributes_EmptyAttributesPassValidation(t *testing.T) {
	repo := newMockPartyTypeRepo()
	schema := `{"type":"object","properties":{"name":{"type":"string"}}}`
	repo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, "")

	v := newTestAttributeValidator(t, repo)
	party := makeParty(domain.PartyTypePerson, nil)

	err := v.ValidateAttributes(context.Background(), testTenantID, "PERSON", party)
	require.NoError(t, err)
}

// TestValidateAttributes_ValidAttributesPassJSONSchema verifies that attributes matching
// the JSON Schema pass validation.
func TestValidateAttributes_ValidAttributesPassJSONSchema(t *testing.T) {
	repo := newMockPartyTypeRepo()
	schema := `{
		"type": "object",
		"properties": {
			"annual_income": {"type": "string"},
			"employment_status": {"type": "string", "enum": ["EMPLOYED", "SELF_EMPLOYED", "UNEMPLOYED"]}
		},
		"required": ["annual_income"]
	}`
	repo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, "")

	v := newTestAttributeValidator(t, repo)
	party := makeParty(domain.PartyTypePerson, []domain.AttributeEntry{
		{Key: "annual_income", Value: "50000"},
		{Key: "employment_status", Value: "EMPLOYED"},
	})

	err := v.ValidateAttributes(context.Background(), testTenantID, "PERSON", party)
	require.NoError(t, err)
}

// TestValidateAttributes_MissingRequiredFieldFailsJSONSchema verifies that missing required
// attributes are rejected.
func TestValidateAttributes_MissingRequiredFieldFailsJSONSchema(t *testing.T) {
	repo := newMockPartyTypeRepo()
	schema := `{
		"type": "object",
		"properties": {
			"annual_income": {"type": "string"}
		},
		"required": ["annual_income"]
	}`
	repo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, "")

	v := newTestAttributeValidator(t, repo)
	party := makeParty(domain.PartyTypePerson, []domain.AttributeEntry{
		{Key: "other_field", Value: "value"},
	})

	err := v.ValidateAttributes(context.Background(), testTenantID, "PERSON", party)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAttributeValidationFailed)
}

// TestValidateAttributes_AdditionalPropertiesFailsWhenDisallowed verifies that extra
// attributes are rejected when additionalProperties is false.
func TestValidateAttributes_AdditionalPropertiesFailsWhenDisallowed(t *testing.T) {
	repo := newMockPartyTypeRepo()
	schema := `{
		"type": "object",
		"properties": {
			"name": {"type": "string"}
		},
		"additionalProperties": false
	}`
	repo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, "")

	v := newTestAttributeValidator(t, repo)
	party := makeParty(domain.PartyTypePerson, []domain.AttributeEntry{
		{Key: "name", Value: "Alice"},
		{Key: "unknown_field", Value: "value"},
	})

	err := v.ValidateAttributes(context.Background(), testTenantID, "PERSON", party)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAttributeValidationFailed)
}

// TestValidateAttributes_ValidationCELPasses verifies that a passing CEL expression
// allows the request through.
func TestValidateAttributes_ValidationCELPasses(t *testing.T) {
	repo := newMockPartyTypeRepo()
	schema := `{"type":"object","properties":{"annual_income":{"type":"string"}}}`
	// Use "in" operator to check map key presence (CEL has() works on proto fields, not map keys)
	validationCEL := `"annual_income" in attributes && attributes["annual_income"] != ""`
	repo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, validationCEL)

	v := newTestAttributeValidator(t, repo)
	party := makeParty(domain.PartyTypePerson, []domain.AttributeEntry{
		{Key: "annual_income", Value: "75000"},
	})

	err := v.ValidateAttributes(context.Background(), testTenantID, "PERSON", party)
	require.NoError(t, err)
}

// TestValidateAttributes_ValidationCELFails verifies that a failing CEL expression
// rejects the request.
func TestValidateAttributes_ValidationCELFails(t *testing.T) {
	repo := newMockPartyTypeRepo()
	schema := `{"type":"object","properties":{"annual_income":{"type":"string"}}}`
	// CEL that requires annual_income to be non-empty
	validationCEL := `"annual_income" in attributes && attributes["annual_income"] != ""`
	repo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, validationCEL)

	v := newTestAttributeValidator(t, repo)
	party := makeParty(domain.PartyTypePerson, []domain.AttributeEntry{
		{Key: "annual_income", Value: ""},
	})

	err := v.ValidateAttributes(context.Background(), testTenantID, "PERSON", party)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAttributeValidationFailed)
}

// TestValidateAttributes_CachingReducesCompilation verifies the LRU cache works by
// validating the same tenant+type twice - the second call should use the cached program.
func TestValidateAttributes_CachingReducesCompilation(t *testing.T) {
	repo := newMockPartyTypeRepo()
	schema := `{"type":"object","properties":{"x":{"type":"string"}}}`
	validationCEL := `"x" in attributes`
	repo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, validationCEL)

	v := newTestAttributeValidator(t, repo)
	party := makeParty(domain.PartyTypePerson, []domain.AttributeEntry{
		{Key: "x", Value: "1"},
	})

	// First call
	err := v.ValidateAttributes(context.Background(), testTenantID, "PERSON", party)
	require.NoError(t, err)

	// Second call - should use cached program
	err = v.ValidateAttributes(context.Background(), testTenantID, "PERSON", party)
	require.NoError(t, err)

	// Cache should have one entry
	assert.Equal(t, 1, v.CacheLen())
}

// errRepo is a stub repository that returns errors for GetByTenantAndType.
type errRepo struct {
	mockPartyTypeDefinitionRepository
	getTenantTypeErr error
}

func (r *errRepo) GetByTenantAndType(_ context.Context, _, _ string) (*persistence.PartyTypeDefinitionEntity, error) {
	if r.getTenantTypeErr != nil {
		return nil, r.getTenantTypeErr
	}
	return nil, persistence.ErrPartyTypeDefinitionNotFound
}

// TestValidateAttributes_RepoErrorPropagates verifies that repository errors are propagated.
func TestValidateAttributes_RepoErrorPropagates(t *testing.T) {
	repo := &errRepo{
		mockPartyTypeDefinitionRepository: *newMockPartyTypeRepo(),
		getTenantTypeErr:                  errors.New("database error"),
	}

	v := newTestAttributeValidator(t, repo)
	party := makeParty(domain.PartyTypePerson, nil)

	err := v.ValidateAttributes(context.Background(), testTenantID, "PERSON", party)
	require.Error(t, err)
}

// TestValidateAttributes_DifferentTenantsSeparateDefinitions verifies that tenant isolation
// works: each tenant has its own definition.
func TestValidateAttributes_DifferentTenantsSeparateDefinitions(t *testing.T) {
	repo := newMockPartyTypeRepo()

	// Tenant A: requires annual_income
	schemaA := `{"type":"object","required":["annual_income"],"properties":{"annual_income":{"type":"string"}}}`
	repo.entities[uuid.New()] = makePartyTypeDefinition("tenant_a", "PERSON", schemaA, "")

	// Tenant B: no requirements
	schemaB := `{"type":"object","properties":{"name":{"type":"string"}}}`
	repo.entities[uuid.New()] = makePartyTypeDefinition("tenant_b", "PERSON", schemaB, "")

	v := newTestAttributeValidator(t, repo)

	// Tenant A party without annual_income should fail
	partyA := makeParty(domain.PartyTypePerson, nil)
	err := v.ValidateAttributes(context.Background(), "tenant_a", "PERSON", partyA)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAttributeValidationFailed)

	// Tenant B party without annual_income should pass
	partyB := makeParty(domain.PartyTypePerson, nil)
	err = v.ValidateAttributes(context.Background(), "tenant_b", "PERSON", partyB)
	require.NoError(t, err)
}
