package saga

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock Registry ---

type mockSagaRegistry struct {
	definitions  map[uuid.UUID]*Definition
	getByIDErr   error
	getDefErr    error
	getActiveErr error
	listErr      error
	createErr    error
	updateErr    error
	activateErr  error
	deprecateErr error
}

func newMockSagaRegistry() *mockSagaRegistry {
	return &mockSagaRegistry{
		definitions: make(map[uuid.UUID]*Definition),
	}
}

func (m *mockSagaRegistry) GetByID(_ context.Context, id uuid.UUID) (*Definition, error) {
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	def, ok := m.definitions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return def, nil
}

func (m *mockSagaRegistry) GetDefinition(_ context.Context, name string, version int) (*Definition, error) {
	if m.getDefErr != nil {
		return nil, m.getDefErr
	}
	for _, def := range m.definitions {
		if def.Name == name && def.Version == version {
			return def, nil
		}
	}
	return nil, ErrNotFound
}

func (m *mockSagaRegistry) GetActive(_ context.Context, name string) (*Definition, error) {
	if m.getActiveErr != nil {
		return nil, m.getActiveErr
	}
	for _, def := range m.definitions {
		if def.Name == name && def.Status == StatusActive {
			return def, nil
		}
	}
	return nil, ErrNotFound
}

func (m *mockSagaRegistry) ListByStatus(_ context.Context, status Status) ([]*Definition, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var result []*Definition
	for _, def := range m.definitions {
		if def.Status == status {
			result = append(result, def)
		}
	}
	return result, nil
}

func (m *mockSagaRegistry) CreateDraft(_ context.Context, def *Definition) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.definitions[def.ID] = def
	return nil
}

func (m *mockSagaRegistry) UpdateDefinition(_ context.Context, id uuid.UUID, updates *Definition) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	def, ok := m.definitions[id]
	if !ok {
		return ErrNotFound
	}
	if updates.DisplayName != "" {
		def.DisplayName = updates.DisplayName
	}
	def.UpdatedAt = time.Now()
	return nil
}

func (m *mockSagaRegistry) ActivateSaga(_ context.Context, id uuid.UUID) error {
	if m.activateErr != nil {
		return m.activateErr
	}
	def, ok := m.definitions[id]
	if !ok {
		return ErrNotFound
	}
	def.Status = StatusActive
	now := time.Now()
	def.ActivatedAt = &now
	return nil
}

func (m *mockSagaRegistry) DeprecateSaga(_ context.Context, id uuid.UUID, successorID *uuid.UUID) error {
	if m.deprecateErr != nil {
		return m.deprecateErr
	}
	def, ok := m.definitions[id]
	if !ok {
		return ErrNotFound
	}
	def.Status = StatusDeprecated
	now := time.Now()
	def.DeprecatedAt = &now
	def.SuccessorID = successorID
	return nil
}

// --- Helpers ---

func setupCachedRegistry(t *testing.T) (*CachedRegistry, *mockSagaRegistry, *Cache) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cache, err := NewCache(client, WithCacheTTL(time.Hour, 0))
	require.NoError(t, err)
	reg := newMockSagaRegistry()
	return NewCachedRegistry(reg, cache), reg, cache
}

func cachedCtx() context.Context {
	return tenant.WithTenant(context.Background(), "test-tenant")
}

func makeTestDef(name string, status Status) *Definition {
	now := time.Now()
	return &Definition{
		ID:        uuid.New(),
		Name:      name,
		Version:   1,
		Script:    "def run(): pass",
		Status:    status,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// --- GetByID ---

func TestCachedRegistry_GetByID_CacheMiss_FetchesFromRegistry(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("deposit", StatusActive)
	reg.definitions[def.ID] = def

	result, err := cr.GetByID(ctx, def.ID)
	require.NoError(t, err)
	assert.Equal(t, def.ID, result.ID)
}

func TestCachedRegistry_GetByID_CacheHit(t *testing.T) {
	cr, reg, cache := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("deposit", StatusActive)
	reg.definitions[def.ID] = def

	// First call populates cache
	_, err := cr.GetByID(ctx, def.ID)
	require.NoError(t, err)

	// Remove from underlying registry to prove cache serves it
	delete(reg.definitions, def.ID)

	result, err := cr.GetByID(ctx, def.ID)
	require.NoError(t, err)
	assert.Equal(t, def.ID, result.ID)

	// Verify cache has it
	assert.NotNil(t, cache.GetByID(ctx, def.ID))
}

func TestCachedRegistry_GetByID_RegistryError(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	reg.getByIDErr = errors.New("db connection failed")

	_, err := cr.GetByID(ctx, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db connection failed")
}

// --- GetDefinition ---

func TestCachedRegistry_GetDefinition_CacheMiss(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("withdrawal", StatusDraft)
	reg.definitions[def.ID] = def

	result, err := cr.GetDefinition(ctx, "withdrawal", 1)
	require.NoError(t, err)
	assert.Equal(t, "withdrawal", result.Name)
}

func TestCachedRegistry_GetDefinition_CacheHit(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("withdrawal", StatusDraft)
	reg.definitions[def.ID] = def

	// Populate cache
	_, err := cr.GetDefinition(ctx, "withdrawal", 1)
	require.NoError(t, err)

	// Remove from registry
	delete(reg.definitions, def.ID)

	result, err := cr.GetDefinition(ctx, "withdrawal", 1)
	require.NoError(t, err)
	assert.Equal(t, "withdrawal", result.Name)
}

func TestCachedRegistry_GetDefinition_RegistryError(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	reg.getDefErr = ErrNotFound

	_, err := cr.GetDefinition(ctx, "nonexistent", 1)
	require.ErrorIs(t, err, ErrNotFound)
}

// --- GetActive ---

func TestCachedRegistry_GetActive_CacheMiss(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("transfer", StatusActive)
	reg.definitions[def.ID] = def

	result, err := cr.GetActive(ctx, "transfer")
	require.NoError(t, err)
	assert.Equal(t, "transfer", result.Name)
}

func TestCachedRegistry_GetActive_CacheHit(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("transfer", StatusActive)
	reg.definitions[def.ID] = def

	// Populate cache
	_, err := cr.GetActive(ctx, "transfer")
	require.NoError(t, err)

	// Remove from registry
	delete(reg.definitions, def.ID)

	result, err := cr.GetActive(ctx, "transfer")
	require.NoError(t, err)
	assert.Equal(t, "transfer", result.Name)
}

func TestCachedRegistry_GetActive_RegistryError(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	reg.getActiveErr = ErrNotFound

	_, err := cr.GetActive(ctx, "nonexistent")
	require.ErrorIs(t, err, ErrNotFound)
}

// --- ListByStatus (passthrough, no cache) ---

func TestCachedRegistry_ListByStatus_Delegates(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()

	def1 := makeTestDef("saga-1", StatusActive)
	def2 := makeTestDef("saga-2", StatusActive)
	reg.definitions[def1.ID] = def1
	reg.definitions[def2.ID] = def2

	result, err := cr.ListByStatus(ctx, StatusActive)
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestCachedRegistry_ListByStatus_Error(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	reg.listErr = errors.New("list failed")

	_, err := cr.ListByStatus(ctx, StatusActive)
	require.Error(t, err)
}

// --- CreateDraft (passthrough) ---

func TestCachedRegistry_CreateDraft_Delegates(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("new-saga", StatusDraft)

	err := cr.CreateDraft(ctx, def)
	require.NoError(t, err)
	assert.Contains(t, reg.definitions, def.ID)
}

func TestCachedRegistry_CreateDraft_Error(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	reg.createErr = ErrAlreadyExists

	err := cr.CreateDraft(ctx, makeTestDef("dup", StatusDraft))
	require.ErrorIs(t, err, ErrAlreadyExists)
}

// --- UpdateDefinition (invalidates cache) ---

func TestCachedRegistry_UpdateDefinition_InvalidatesCache(t *testing.T) {
	cr, reg, cache := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("updatable", StatusDraft)
	reg.definitions[def.ID] = def

	// Populate cache
	_, _ = cr.GetByID(ctx, def.ID)
	require.NotNil(t, cache.GetByID(ctx, def.ID))

	err := cr.UpdateDefinition(ctx, def.ID, &Definition{DisplayName: "Updated"})
	require.NoError(t, err)

	// Cache should be invalidated
	assert.Nil(t, cache.GetByID(ctx, def.ID))
}

func TestCachedRegistry_UpdateDefinition_GetByIDError(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	reg.getByIDErr = ErrNotFound

	err := cr.UpdateDefinition(ctx, uuid.New(), &Definition{DisplayName: "X"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestCachedRegistry_UpdateDefinition_RegistryError(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("update-fail", StatusDraft)
	reg.definitions[def.ID] = def
	reg.updateErr = ErrNotDraft

	err := cr.UpdateDefinition(ctx, def.ID, &Definition{DisplayName: "X"})
	require.ErrorIs(t, err, ErrNotDraft)
}

// --- ActivateSaga (invalidates cache) ---

func TestCachedRegistry_ActivateSaga_InvalidatesCache(t *testing.T) {
	cr, reg, cache := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("activatable", StatusDraft)
	reg.definitions[def.ID] = def

	// Populate cache
	_, _ = cr.GetByID(ctx, def.ID)
	require.NotNil(t, cache.GetByID(ctx, def.ID))

	err := cr.ActivateSaga(ctx, def.ID)
	require.NoError(t, err)

	assert.Nil(t, cache.GetByID(ctx, def.ID))
}

func TestCachedRegistry_ActivateSaga_GetByIDError(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	reg.getByIDErr = ErrNotFound

	err := cr.ActivateSaga(ctx, uuid.New())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestCachedRegistry_ActivateSaga_RegistryError(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("activate-fail", StatusActive)
	reg.definitions[def.ID] = def
	reg.activateErr = ErrNotDraft

	err := cr.ActivateSaga(ctx, def.ID)
	require.ErrorIs(t, err, ErrNotDraft)
}

// --- DeprecateSaga (invalidates cache) ---

func TestCachedRegistry_DeprecateSaga_InvalidatesCache(t *testing.T) {
	cr, reg, cache := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("deprecatable", StatusActive)
	reg.definitions[def.ID] = def

	// Populate cache
	_, _ = cr.GetByID(ctx, def.ID)
	require.NotNil(t, cache.GetByID(ctx, def.ID))

	successorID := uuid.New()
	err := cr.DeprecateSaga(ctx, def.ID, &successorID)
	require.NoError(t, err)

	assert.Nil(t, cache.GetByID(ctx, def.ID))
}

func TestCachedRegistry_DeprecateSaga_GetByIDError(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	reg.getByIDErr = ErrNotFound

	err := cr.DeprecateSaga(ctx, uuid.New(), nil)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestCachedRegistry_DeprecateSaga_RegistryError(t *testing.T) {
	cr, reg, _ := setupCachedRegistry(t)
	ctx := cachedCtx()
	def := makeTestDef("deprecate-fail", StatusDraft)
	reg.definitions[def.ID] = def
	reg.deprecateErr = ErrNotActive

	err := cr.DeprecateSaga(ctx, def.ID, nil)
	require.ErrorIs(t, err, ErrNotActive)
}
