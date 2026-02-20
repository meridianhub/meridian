package saga_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/services/reference-data/saga"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Fake registry ---

type fakeRegistry struct {
	sagas map[string]*saga.Definition
}

func (f *fakeRegistry) GetByID(_ context.Context, id uuid.UUID) (*saga.Definition, error) {
	for _, d := range f.sagas {
		if d.ID == id {
			return d, nil
		}
	}
	return nil, saga.ErrNotFound
}

func (f *fakeRegistry) GetDefinition(_ context.Context, name string, _ int) (*saga.Definition, error) {
	if d, ok := f.sagas[name]; ok {
		return d, nil
	}
	return nil, saga.ErrNotFound
}

func (f *fakeRegistry) GetActive(_ context.Context, name string) (*saga.Definition, error) {
	if d, ok := f.sagas[name]; ok {
		return d, nil
	}
	return nil, saga.ErrNotFound
}

func (f *fakeRegistry) ListByStatus(_ context.Context, _ saga.Status) ([]*saga.Definition, error) {
	return nil, nil
}

func (f *fakeRegistry) CreateDraft(_ context.Context, _ *saga.Definition) error {
	return nil
}

func (f *fakeRegistry) UpdateDefinition(_ context.Context, _ uuid.UUID, _ *saga.Definition) error {
	return nil
}

func (f *fakeRegistry) ActivateSaga(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (f *fakeRegistry) DeprecateSaga(_ context.Context, _ uuid.UUID, _ *uuid.UUID) error {
	return nil
}

// --- Fake account type loader for cache ---

type fakeAccountTypeLoader struct {
	types map[string]*accounttype.Definition
}

func (f *fakeAccountTypeLoader) LoadAccountType(_ context.Context, code string) (*accounttype.Definition, error) {
	if d, ok := f.types[code]; ok {
		return d, nil
	}
	return nil, accounttype.ErrNotFound
}

func (f *fakeAccountTypeLoader) ListActiveAccountTypes(_ context.Context) ([]*accounttype.Definition, error) {
	list := make([]*accounttype.Definition, 0, len(f.types))
	for _, d := range f.types {
		list = append(list, d)
	}
	return list, nil
}

// buildCache returns a LocalAccountTypeCache with the given loader and no CEL compiler.
func buildCache(loader cache.AccountTypeLoader) *cache.LocalAccountTypeCache {
	return cache.NewLocalAccountTypeCache(loader, nil)
}

// tenantCtx returns a context with a test tenant ID set.
func tenantCtxForResolver(t *testing.T) context.Context {
	t.Helper()
	tid := tenant.TenantID("test-tenant-resolver")
	return tenant.WithTenant(context.Background(), tid)
}

func TestProductTypeSagaResolver_ResolveForProductType(t *testing.T) {
	t.Run("product type with DefaultSagaPrefix resolves prefixed saga name", func(t *testing.T) {
		ctx := tenantCtxForResolver(t)
		tid, _ := tenant.FromContext(ctx)

		registry := &fakeRegistry{
			sagas: map[string]*saga.Definition{
				"SAVINGS.deposit": {
					ID:     uuid.New(),
					Name:   "SAVINGS.deposit",
					Status: saga.StatusActive,
					Script: "def main(): pass",
				},
			},
		}

		loader := &fakeAccountTypeLoader{
			types: map[string]*accounttype.Definition{
				"SAVINGS_ACCOUNT": {
					Code:              "SAVINGS_ACCOUNT",
					DefaultSagaPrefix: "SAVINGS",
					Status:            accounttype.StatusActive,
				},
			},
		}

		resolver := saga.NewProductTypeSagaResolver(registry, buildCache(loader))
		def, err := resolver.ResolveForProductType(ctx, tid, "SAVINGS_ACCOUNT", "deposit")

		require.NoError(t, err)
		assert.Equal(t, "SAVINGS.deposit", def.Name)
	})

	t.Run("prefixed saga not found returns ErrSagaNotFound with no fallback", func(t *testing.T) {
		ctx := tenantCtxForResolver(t)
		tid, _ := tenant.FromContext(ctx)

		registry := &fakeRegistry{
			// "SAVINGS.deposit" does NOT exist - only the generic "deposit"
			sagas: map[string]*saga.Definition{
				"deposit": {
					ID:     uuid.New(),
					Name:   "deposit",
					Status: saga.StatusActive,
					Script: "def main(): pass",
				},
			},
		}

		loader := &fakeAccountTypeLoader{
			types: map[string]*accounttype.Definition{
				"SAVINGS_ACCOUNT": {
					Code:              "SAVINGS_ACCOUNT",
					DefaultSagaPrefix: "SAVINGS",
					Status:            accounttype.StatusActive,
				},
			},
		}

		resolver := saga.NewProductTypeSagaResolver(registry, buildCache(loader))
		_, err := resolver.ResolveForProductType(ctx, tid, "SAVINGS_ACCOUNT", "deposit")

		require.Error(t, err)
		assert.True(t, errors.Is(err, saga.ErrSagaNotFound),
			"expected ErrSagaNotFound, got: %v", err)
	})

	t.Run("product type with empty DefaultSagaPrefix resolves generic operation", func(t *testing.T) {
		ctx := tenantCtxForResolver(t)
		tid, _ := tenant.FromContext(ctx)

		registry := &fakeRegistry{
			sagas: map[string]*saga.Definition{
				"deposit": {
					ID:     uuid.New(),
					Name:   "deposit",
					Status: saga.StatusActive,
					Script: "def main(): pass",
				},
			},
		}

		loader := &fakeAccountTypeLoader{
			types: map[string]*accounttype.Definition{
				"GENERIC_ACCOUNT": {
					Code:              "GENERIC_ACCOUNT",
					DefaultSagaPrefix: "", // no prefix
					Status:            accounttype.StatusActive,
				},
			},
		}

		resolver := saga.NewProductTypeSagaResolver(registry, buildCache(loader))
		def, err := resolver.ResolveForProductType(ctx, tid, "GENERIC_ACCOUNT", "deposit")

		require.NoError(t, err)
		assert.Equal(t, "deposit", def.Name)
	})

	t.Run("product type not found falls back to generic operation", func(t *testing.T) {
		ctx := tenantCtxForResolver(t)
		tid, _ := tenant.FromContext(ctx)

		registry := &fakeRegistry{
			sagas: map[string]*saga.Definition{
				"deposit": {
					ID:     uuid.New(),
					Name:   "deposit",
					Status: saga.StatusActive,
					Script: "def main(): pass",
				},
			},
		}

		loader := &fakeAccountTypeLoader{
			types: map[string]*accounttype.Definition{
				// UNKNOWN_TYPE not registered
			},
		}

		resolver := saga.NewProductTypeSagaResolver(registry, buildCache(loader))
		def, err := resolver.ResolveForProductType(ctx, tid, "UNKNOWN_TYPE", "deposit")

		require.NoError(t, err)
		assert.Equal(t, "deposit", def.Name)
	})

	t.Run("product type not found and generic saga not found returns ErrNotFound", func(t *testing.T) {
		ctx := tenantCtxForResolver(t)
		tid, _ := tenant.FromContext(ctx)

		registry := &fakeRegistry{
			sagas: map[string]*saga.Definition{},
		}

		loader := &fakeAccountTypeLoader{
			types: map[string]*accounttype.Definition{},
		}

		resolver := saga.NewProductTypeSagaResolver(registry, buildCache(loader))
		_, err := resolver.ResolveForProductType(ctx, tid, "UNKNOWN_TYPE", "deposit")

		require.Error(t, err)
		assert.True(t, errors.Is(err, saga.ErrNotFound),
			"expected ErrNotFound, got: %v", err)
	})
}
