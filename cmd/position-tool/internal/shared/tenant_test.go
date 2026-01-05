package shared

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

func TestNewTenantHelper(t *testing.T) {
	t.Run("returns error for nil pool", func(t *testing.T) {
		helper, err := NewTenantHelper(nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilPool)
		assert.Nil(t, helper)
	})
}

func TestTenantHelper_ValidateTenant(t *testing.T) {
	// Create a helper that we can use for validation (nil pool is ok for validation)
	helper := &TenantHelper{pool: nil}

	tests := []struct {
		name     string
		tenantID string
		wantErr  bool
	}{
		{
			name:     "valid alphanumeric",
			tenantID: "acme_bank",
			wantErr:  false,
		},
		{
			name:     "valid with numbers",
			tenantID: "tenant123",
			wantErr:  false,
		},
		{
			name:     "valid uppercase",
			tenantID: "ACME_BANK",
			wantErr:  false,
		},
		{
			name:     "valid mixed case",
			tenantID: "Acme_Bank_123",
			wantErr:  false,
		},
		{
			name:     "empty string",
			tenantID: "",
			wantErr:  true,
		},
		{
			name:     "contains space",
			tenantID: "acme bank",
			wantErr:  true,
		},
		{
			name:     "contains hyphen",
			tenantID: "acme-bank",
			wantErr:  true,
		},
		{
			name:     "contains special char",
			tenantID: "acme@bank",
			wantErr:  true,
		},
		{
			name:     "too long (over 50 chars)",
			tenantID: "abcdefghijklmnopqrstuvwxyz_abcdefghijklmnopqrstuvwxyz",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := helper.ValidateTenant(tt.tenantID)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTenantHelper_SchemaNameForTenant(t *testing.T) {
	helper := &TenantHelper{pool: nil}

	tests := []struct {
		name           string
		tenantID       string
		expectedSchema string
		wantErr        bool
	}{
		{
			name:           "lowercase tenant",
			tenantID:       "acme_bank",
			expectedSchema: "org_acme_bank",
			wantErr:        false,
		},
		{
			name:           "uppercase tenant normalized",
			tenantID:       "ACME_BANK",
			expectedSchema: "org_acme_bank",
			wantErr:        false,
		},
		{
			name:           "mixed case normalized",
			tenantID:       "AcMe_BaNk",
			expectedSchema: "org_acme_bank",
			wantErr:        false,
		},
		{
			name:           "invalid tenant",
			tenantID:       "invalid-tenant",
			expectedSchema: "",
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema, err := helper.SchemaNameForTenant(tt.tenantID)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Empty(t, schema)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedSchema, schema)
			}
		})
	}
}

func TestTenantHelper_GetTenantInfo(t *testing.T) {
	helper := &TenantHelper{pool: nil}

	t.Run("valid tenant", func(t *testing.T) {
		info, err := helper.GetTenantInfo("acme_bank")
		require.NoError(t, err)
		require.NotNil(t, info)
		assert.Equal(t, "acme_bank", info.TenantID)
		assert.Equal(t, "org_acme_bank", info.SchemaName)
	})

	t.Run("invalid tenant", func(t *testing.T) {
		info, err := helper.GetTenantInfo("invalid-tenant")
		require.Error(t, err)
		assert.Nil(t, info)
	})
}

func TestTenantHelper_WithTenantContext(t *testing.T) {
	helper := &TenantHelper{pool: nil}

	t.Run("valid tenant adds to context", func(t *testing.T) {
		ctx := context.Background()
		newCtx, err := helper.WithTenantContext(ctx, "acme_bank")
		require.NoError(t, err)

		// Verify tenant is in context
		tenantID, ok := tenant.FromContext(newCtx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("acme_bank"), tenantID)
	})

	t.Run("invalid tenant returns error", func(t *testing.T) {
		ctx := context.Background()
		newCtx, err := helper.WithTenantContext(ctx, "invalid-tenant")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid tenant ID")

		// Context should be unchanged (original context returned)
		_, ok := tenant.FromContext(newCtx)
		assert.False(t, ok)
	})
}

func TestTenantHelper_GetTenantFromContext(t *testing.T) {
	helper := &TenantHelper{pool: nil}

	t.Run("tenant present in context", func(t *testing.T) {
		ctx := tenant.WithTenant(context.Background(), tenant.MustNewTenantID("acme_bank"))
		tenantID, err := helper.GetTenantFromContext(ctx)
		require.NoError(t, err)
		assert.Equal(t, tenant.TenantID("acme_bank"), tenantID)
	})

	t.Run("tenant missing from context", func(t *testing.T) {
		ctx := context.Background()
		tenantID, err := helper.GetTenantFromContext(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingTenant)
		assert.Empty(t, tenantID)
	})
}

func TestTenantInfo(t *testing.T) {
	info := TenantInfo{
		TenantID:   "test_tenant",
		SchemaName: "org_test_tenant",
	}

	assert.Equal(t, "test_tenant", info.TenantID)
	assert.Equal(t, "org_test_tenant", info.SchemaName)
}

// Note: Integration tests for BeginTenantScopedTx and ExecuteInTenantScope
// require a real database connection and are in tenant_integration_test.go

func TestTenantHelper_SchemaNameFormat(t *testing.T) {
	helper := &TenantHelper{pool: nil}

	// Verify the schema name always follows "org_" + lowercase(tenantID) format
	testCases := []struct {
		tenantID       string
		expectedPrefix string
	}{
		{"abc", "org_abc"},
		{"ABC", "org_abc"},
		{"Ab_Cd", "org_ab_cd"},
		{"tenant_123", "org_tenant_123"},
	}

	for _, tc := range testCases {
		t.Run(tc.tenantID, func(t *testing.T) {
			schema, err := helper.SchemaNameForTenant(tc.tenantID)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedPrefix, schema)
			assert.Contains(t, schema, "org_")
		})
	}
}

func BenchmarkTenantHelper_ValidateTenant(b *testing.B) {
	helper := &TenantHelper{pool: nil}

	b.ResetTimer()
	for b.Loop() {
		_ = helper.ValidateTenant("acme_bank")
	}
}

func BenchmarkTenantHelper_SchemaNameForTenant(b *testing.B) {
	helper := &TenantHelper{pool: nil}

	b.ResetTimer()
	for b.Loop() {
		_, _ = helper.SchemaNameForTenant("acme_bank")
	}
}
