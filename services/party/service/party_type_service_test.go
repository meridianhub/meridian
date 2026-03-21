package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// testTenantID is already defined in grpc_service_integration_test.go
	validAttributeSchema = `{"type":"object","properties":{"annual_income":{"type":"string"}}}`
)

// mockPartyTypeDefinitionRepository is a simple in-memory mock for testing.
type mockPartyTypeDefinitionRepository struct {
	entities  map[uuid.UUID]*persistence.PartyTypeDefinitionEntity
	createErr error
	getErr    error
	listErr   error
	updateErr error
}

func newMockPartyTypeRepo() *mockPartyTypeDefinitionRepository {
	return &mockPartyTypeDefinitionRepository{
		entities: make(map[uuid.UUID]*persistence.PartyTypeDefinitionEntity),
	}
}

func (m *mockPartyTypeDefinitionRepository) Create(_ context.Context, entity *persistence.PartyTypeDefinitionEntity) error {
	if m.createErr != nil {
		return m.createErr
	}
	// Check duplicate
	for _, e := range m.entities {
		if e.TenantID == entity.TenantID && e.PartyType == entity.PartyType {
			return persistence.ErrPartyTypeDefinitionExists
		}
	}
	cp := *entity
	m.entities[entity.ID] = &cp
	return nil
}

func (m *mockPartyTypeDefinitionRepository) GetByID(_ context.Context, id uuid.UUID) (*persistence.PartyTypeDefinitionEntity, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	e, ok := m.entities[id]
	if !ok {
		return nil, persistence.ErrPartyTypeDefinitionNotFound
	}
	cp := *e
	return &cp, nil
}

func (m *mockPartyTypeDefinitionRepository) GetByTenantAndType(_ context.Context, tenantID, partyType string) (*persistence.PartyTypeDefinitionEntity, error) {
	for _, e := range m.entities {
		if e.TenantID == tenantID && e.PartyType == partyType {
			cp := *e
			return &cp, nil
		}
	}
	return nil, persistence.ErrPartyTypeDefinitionNotFound
}

func (m *mockPartyTypeDefinitionRepository) ListByTenant(_ context.Context, tenantID string, partyType string) ([]*persistence.PartyTypeDefinitionEntity, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var result []*persistence.PartyTypeDefinitionEntity
	for _, e := range m.entities {
		if e.TenantID == tenantID {
			if partyType == "" || e.PartyType == partyType {
				cp := *e
				result = append(result, &cp)
			}
		}
	}
	return result, nil
}

func (m *mockPartyTypeDefinitionRepository) Update(_ context.Context, entity *persistence.PartyTypeDefinitionEntity) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	existing, ok := m.entities[entity.ID]
	if !ok {
		return persistence.ErrPartyTypeDefinitionNotFound
	}
	if existing.Version != entity.Version-1 {
		return persistence.ErrPartyTypeVersionConflict
	}
	cp := *entity
	m.entities[entity.ID] = &cp
	return nil
}

func newTestPartyTypeService(t *testing.T) (*PartyTypeDefinitionService, *mockPartyTypeDefinitionRepository) {
	t.Helper()
	repo := newMockPartyTypeRepo()
	svc, err := NewPartyTypeDefinitionService(repo)
	require.NoError(t, err)
	return svc, repo
}

func testCtx() context.Context {
	return tenant.WithTenant(context.Background(), tenant.TenantID(testTenantID))
}

func strPtr(s string) *string { return &s }

// --- Constructor tests ---

func TestNewPartyTypeDefinitionService_NilRepoReturnsError(t *testing.T) {
	_, err := NewPartyTypeDefinitionService(nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPartyTypeRepoNil))
}

func TestNewPartyTypeDefinitionService_Success(t *testing.T) {
	svc, _ := newTestPartyTypeService(t)
	assert.NotNil(t, svc)
}

// --- Register tests ---

func TestRegister_Success(t *testing.T) {
	svc, _ := newTestPartyTypeService(t)
	ctx := testCtx()

	entity, err := svc.Register(ctx, RegisterPartyTypeInput{
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
	})

	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, entity.ID)
	assert.Equal(t, testTenantID, entity.TenantID)
	assert.Equal(t, "PERSON", entity.PartyType)
	assert.Equal(t, validAttributeSchema, entity.AttributeSchema)
	assert.Equal(t, int64(1), entity.Version)
}

func TestRegister_EmptySchemaReturnsError(t *testing.T) {
	svc, _ := newTestPartyTypeService(t)

	_, err := svc.Register(testCtx(), RegisterPartyTypeInput{
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: "",
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAttributeSchemaEmpty))
}

func TestRegister_SchemaTooBigReturnsError(t *testing.T) {
	svc, _ := newTestPartyTypeService(t)

	bigSchema := `{"type":"object","properties":{"k":{"type":"string","description":"` +
		strings.Repeat("a", MaxAttributeSchemaSize) + `"}}}`

	_, err := svc.Register(testCtx(), RegisterPartyTypeInput{
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: bigSchema,
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAttributeSchemaTooBig))
}

func TestRegister_InvalidJSONSchemaReturnsError(t *testing.T) {
	svc, _ := newTestPartyTypeService(t)

	_, err := svc.Register(testCtx(), RegisterPartyTypeInput{
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: "not valid json",
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAttributeSchemaInvalidJSON))
}

func TestRegister_NonObjectJSONSchemaReturnsError(t *testing.T) {
	svc, _ := newTestPartyTypeService(t)

	// Valid JSON but not an object (array)
	_, err := svc.Register(testCtx(), RegisterPartyTypeInput{
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: `["not", "an", "object"]`,
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAttributeSchemaInvalidJSON))
}

func TestRegister_InvalidValidationCELReturnsError(t *testing.T) {
	svc, _ := newTestPartyTypeService(t)

	_, err := svc.Register(testCtx(), RegisterPartyTypeInput{
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
		ValidationCEL:   "this is not valid CEL !!@#$",
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrValidationCELInvalid))
}

func TestRegister_InvalidEligibilityCELReturnsError(t *testing.T) {
	svc, _ := newTestPartyTypeService(t)

	_, err := svc.Register(testCtx(), RegisterPartyTypeInput{
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
		EligibilityCEL:  "invalid !! CEL",
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrEligibilityCELInvalid))
}

func TestRegister_ValidCELExpressions(t *testing.T) {
	svc, _ := newTestPartyTypeService(t)

	entity, err := svc.Register(testCtx(), RegisterPartyTypeInput{
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
		ValidationCEL:   `attributes["annual_income"] != ""`,
		EligibilityCEL:  `party["type"] == "PERSON"`,
	})

	require.NoError(t, err)
	assert.Equal(t, `attributes["annual_income"] != ""`, entity.ValidationCEL)
}

func TestRegister_DuplicateReturnsError(t *testing.T) {
	svc, _ := newTestPartyTypeService(t)

	input := RegisterPartyTypeInput{
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
	}

	_, err := svc.Register(testCtx(), input)
	require.NoError(t, err)

	_, err = svc.Register(testCtx(), input)
	require.Error(t, err)
	assert.True(t, errors.Is(err, persistence.ErrPartyTypeDefinitionExists))
}

// --- GetByID tests ---

func TestGetByID_Success(t *testing.T) {
	svc, repo := newTestPartyTypeService(t)
	ctx := testCtx()

	id := uuid.New()
	repo.entities[id] = &persistence.PartyTypeDefinitionEntity{
		ID:              id,
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	entity, err := svc.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, id, entity.ID)
}

func TestGetByID_NotFound(t *testing.T) {
	svc, _ := newTestPartyTypeService(t)

	_, err := svc.GetByID(testCtx(), uuid.New())
	require.Error(t, err)
	assert.True(t, errors.Is(err, persistence.ErrPartyTypeDefinitionNotFound))
}

// --- Update tests ---

func TestUpdate_Success(t *testing.T) {
	svc, repo := newTestPartyTypeService(t)
	ctx := testCtx()

	id := uuid.New()
	repo.entities[id] = &persistence.PartyTypeDefinitionEntity{
		ID:              id,
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
		ValidationCEL:   "",
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	newSchema := `{"type":"object","properties":{"income":{"type":"number"}}}`
	updated, err := svc.Update(ctx, id, UpdatePartyTypeInput{
		AttributeSchema: strPtr(newSchema),
		Version:         1,
	})

	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Version)
	assert.Equal(t, newSchema, updated.AttributeSchema)
}

func TestUpdate_VersionConflict(t *testing.T) {
	svc, repo := newTestPartyTypeService(t)
	ctx := testCtx()

	id := uuid.New()
	repo.entities[id] = &persistence.PartyTypeDefinitionEntity{
		ID:              id,
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
		Version:         2, // DB is at version 2
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Client thinks version is 1, but DB has 2
	_, err := svc.Update(ctx, id, UpdatePartyTypeInput{
		AttributeSchema: strPtr(validAttributeSchema),
		Version:         1,
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, persistence.ErrPartyTypeVersionConflict))
}

func TestUpdate_NotFound(t *testing.T) {
	svc, _ := newTestPartyTypeService(t)

	_, err := svc.Update(testCtx(), uuid.New(), UpdatePartyTypeInput{
		AttributeSchema: strPtr(validAttributeSchema),
		Version:         1,
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, persistence.ErrPartyTypeDefinitionNotFound))
}

func TestUpdate_InvalidSchemaReturnsError(t *testing.T) {
	svc, repo := newTestPartyTypeService(t)
	ctx := testCtx()

	id := uuid.New()
	repo.entities[id] = &persistence.PartyTypeDefinitionEntity{
		ID:              id,
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	_, err := svc.Update(ctx, id, UpdatePartyTypeInput{
		AttributeSchema: strPtr("not valid json"),
		Version:         1,
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAttributeSchemaInvalidJSON))
}

func TestUpdate_PreservesExistingCELWhenNotProvided(t *testing.T) {
	svc, repo := newTestPartyTypeService(t)
	ctx := testCtx()

	id := uuid.New()
	repo.entities[id] = &persistence.PartyTypeDefinitionEntity{
		ID:              id,
		TenantID:        testTenantID,
		PartyType:       "PERSON",
		AttributeSchema: validAttributeSchema,
		ValidationCEL:   `attributes["annual_income"] != ""`,
		Version:         1,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Update only AttributeSchema, not ValidationCEL (nil ValidationCEL means preserve existing)
	updated, err := svc.Update(ctx, id, UpdatePartyTypeInput{
		AttributeSchema: strPtr(`{"type":"object","properties":{"income":{"type":"number"}}}`),
		Version:         1,
	})

	require.NoError(t, err)
	assert.Equal(t, `attributes["annual_income"] != ""`, updated.ValidationCEL)
}
