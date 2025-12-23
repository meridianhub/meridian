package gateway

import (
	"context"

	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/mock"
)

// Compile-time interface checks to ensure mocks implement the required interfaces
var (
	_ slugCache        = (*MockSlugCache)(nil)
	_ tenantRepository = (*MockTenantRepository)(nil)
)

// MockSlugCache is a mock implementation of slugCache for testing.
type MockSlugCache struct {
	mock.Mock
}

func (m *MockSlugCache) Get(ctx context.Context, slug string) (tenant.TenantID, error) {
	args := m.Called(ctx, slug)
	return args.Get(0).(tenant.TenantID), args.Error(1)
}

func (m *MockSlugCache) Set(ctx context.Context, slug string, tenantID tenant.TenantID) error {
	args := m.Called(ctx, slug, tenantID)
	return args.Error(0)
}

// MockTenantRepository is a mock implementation of tenantRepository for testing.
type MockTenantRepository struct {
	mock.Mock
}

func (m *MockTenantRepository) GetBySlug(ctx context.Context, slug string) (*domain.Tenant, error) {
	args := m.Called(ctx, slug)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Tenant), args.Error(1)
}
