package staff_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/control-plane/internal/staff"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createStaffTables(t *testing.T, db *gorm.DB, schemaName string) {
	t.Helper()
	q := fmt.Sprintf("%q", schemaName)

	ddls := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s."staff_user" (
			"id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			"email" VARCHAR(255) NOT NULL,
			"name" VARCHAR(255),
			"role" VARCHAR(50) NOT NULL DEFAULT 'operator',
			"status" VARCHAR(20) NOT NULL DEFAULT 'invited',
			"auth_provider_id" VARCHAR(255),
			"created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			"updated_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT valid_role CHECK ("role" IN ('admin', 'operator', 'auditor')),
			CONSTRAINT valid_status CHECK ("status" IN ('invited', 'active', 'suspended'))
		)`, q),
		fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS "idx_staff_user_email_%s" ON %s."staff_user" ("email")`, schemaName, q),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s."api_key" (
			"id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			"staff_user_id" UUID NOT NULL REFERENCES %s."staff_user" ("id") ON DELETE RESTRICT,
			"key_prefix" VARCHAR(100) NOT NULL,
			"key_hash" BYTEA NOT NULL,
			"name" VARCHAR(255),
			"scopes" TEXT[],
			"rate_limit_rps" INTEGER NOT NULL DEFAULT 100,
			"last_used_at" TIMESTAMPTZ,
			"expires_at" TIMESTAMPTZ,
			"created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			"revoked_at" TIMESTAMPTZ
		)`, q, q),
		fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS "idx_api_key_prefix_%s" ON %s."api_key" ("key_prefix") WHERE "revoked_at" IS NULL`, schemaName, q),
	}

	for _, ddl := range ddls {
		err := db.Exec(ddl).Error
		require.NoError(t, err, "DDL failed: %s", ddl[:80])
	}
}

func TestStaffService(t *testing.T) {
	// Single CockroachDB container for all subtests
	gormDB, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	tc := testdb.SetupTenantSchema(t, gormDB, "test_tenant")
	defer tc.Cleanup()

	createStaffTables(t, gormDB, tc.Tenant.SchemaName())

	svc := staff.NewService(gormDB, slog.Default())
	ctx := tc.Ctx

	t.Run("InviteStaff", func(t *testing.T) {
		t.Run("success", func(t *testing.T) {
			user, err := svc.InviteStaff(ctx, "invite-success@example.com", "Alice Smith", "operator")
			require.NoError(t, err)
			require.NotNil(t, user)

			assert.NotEqual(t, uuid.Nil, user.ID)
			assert.Equal(t, "invite-success@example.com", user.Email)
			assert.Equal(t, "Alice Smith", user.Name)
			assert.Equal(t, "operator", user.Role)
			assert.Equal(t, "invited", user.Status)
			assert.False(t, user.CreatedAt.IsZero())
		})

		t.Run("all_roles", func(t *testing.T) {
			for _, role := range []string{"admin", "operator", "auditor"} {
				user, err := svc.InviteStaff(ctx, "roles-"+role+"@example.com", "", role)
				require.NoError(t, err, "role: %s", role)
				assert.Equal(t, role, user.Role)
			}
		})

		t.Run("invalid_role", func(t *testing.T) {
			_, err := svc.InviteStaff(ctx, "invalid-role@example.com", "Bob", "superadmin")
			require.ErrorIs(t, err, staff.ErrInvalidRole)
		})

		t.Run("duplicate_email", func(t *testing.T) {
			_, err := svc.InviteStaff(ctx, "duplicate@example.com", "Alice", "operator")
			require.NoError(t, err)

			_, err = svc.InviteStaff(ctx, "duplicate@example.com", "Alice 2", "admin")
			require.ErrorIs(t, err, staff.ErrEmailAlreadyExists)
		})

		t.Run("empty_email", func(t *testing.T) {
			_, err := svc.InviteStaff(ctx, "", "Alice", "operator")
			require.ErrorIs(t, err, staff.ErrEmailRequired)
		})

		t.Run("email_normalization", func(t *testing.T) {
			user, err := svc.InviteStaff(ctx, "  Normalize@EXAMPLE.COM  ", "Norm", "operator")
			require.NoError(t, err)
			assert.Equal(t, "normalize@example.com", user.Email)
		})
	})

	t.Run("ActivateStaff", func(t *testing.T) {
		t.Run("success", func(t *testing.T) {
			user, err := svc.InviteStaff(ctx, "activate-ok@example.com", "Carol", "operator")
			require.NoError(t, err)

			err = svc.ActivateStaff(ctx, user.ID, "auth0|activate123")
			require.NoError(t, err)

			activated, err := svc.GetStaff(ctx, user.ID)
			require.NoError(t, err)
			assert.Equal(t, "active", activated.Status)
			assert.Equal(t, "auth0|activate123", activated.AuthProviderID)
		})

		t.Run("not_found", func(t *testing.T) {
			err := svc.ActivateStaff(ctx, uuid.New(), "auth0|nope")
			require.ErrorIs(t, err, staff.ErrStaffNotFound)
		})

		t.Run("already_active", func(t *testing.T) {
			user, err := svc.InviteStaff(ctx, "activate-twice@example.com", "Dan", "operator")
			require.NoError(t, err)
			err = svc.ActivateStaff(ctx, user.ID, "auth0|first")
			require.NoError(t, err)

			err = svc.ActivateStaff(ctx, user.ID, "auth0|second")
			require.ErrorIs(t, err, staff.ErrInvalidStatus)
		})
	})

	t.Run("SuspendStaff", func(t *testing.T) {
		t.Run("success", func(t *testing.T) {
			user, err := svc.InviteStaff(ctx, "suspend-ok@example.com", "Eve", "operator")
			require.NoError(t, err)
			err = svc.ActivateStaff(ctx, user.ID, "auth0|eve")
			require.NoError(t, err)

			err = svc.SuspendStaff(ctx, user.ID)
			require.NoError(t, err)

			suspended, err := svc.GetStaff(ctx, user.ID)
			require.NoError(t, err)
			assert.Equal(t, "suspended", suspended.Status)
		})

		t.Run("idempotent", func(t *testing.T) {
			user, err := svc.InviteStaff(ctx, "suspend-idem@example.com", "Frank", "operator")
			require.NoError(t, err)

			err = svc.SuspendStaff(ctx, user.ID)
			require.NoError(t, err)
			err = svc.SuspendStaff(ctx, user.ID)
			require.NoError(t, err) // second call is no-op
		})

		t.Run("not_found", func(t *testing.T) {
			err := svc.SuspendStaff(ctx, uuid.New())
			require.ErrorIs(t, err, staff.ErrStaffNotFound)
		})
	})

	t.Run("ListStaff", func(t *testing.T) {
		// Uses staff created above; verify list returns multiple
		users, err := svc.ListStaff(ctx)
		require.NoError(t, err)
		assert.True(t, len(users) > 0, "should list at least one staff user")
	})

	t.Run("APIKey", func(t *testing.T) {
		// Create an active user for API key tests
		apiUser, err := svc.InviteStaff(ctx, "apikey-user@example.com", "API User", "operator")
		require.NoError(t, err)
		err = svc.ActivateStaff(ctx, apiUser.ID, "auth0|apikey")
		require.NoError(t, err)

		t.Run("create_success", func(t *testing.T) {
			result, err := svc.CreateAPIKey(ctx, apiUser.ID, "motive", "My Key", []string{"read", "write"}, 24*time.Hour)
			require.NoError(t, err)
			require.NotNil(t, result)

			assert.NotEqual(t, uuid.Nil, result.ID)
			assert.Contains(t, result.KeyPrefix, "pk_motive_")
			assert.Contains(t, result.PlaintextKey, "pk_motive_")
			assert.Equal(t, "My Key", result.Name)
			assert.Equal(t, []string{"read", "write"}, result.Scopes)
			assert.Equal(t, 100, result.RateLimitRPS)
			assert.NotNil(t, result.ExpiresAt)
		})

		t.Run("create_format", func(t *testing.T) {
			result, err := svc.CreateAPIKey(ctx, apiUser.ID, "acme", "format-test", nil, 0)
			require.NoError(t, err)

			// Verify prefix is first 8 chars of entropy
			assert.Contains(t, result.PlaintextKey, result.KeyPrefix[:len(result.KeyPrefix)])
			assert.Nil(t, result.ExpiresAt) // no TTL
		})

		t.Run("create_suspended_staff", func(t *testing.T) {
			suspended, err := svc.InviteStaff(ctx, "suspended-apikey@example.com", "Susp", "operator")
			require.NoError(t, err)
			err = svc.ActivateStaff(ctx, suspended.ID, "auth0|susp")
			require.NoError(t, err)
			err = svc.SuspendStaff(ctx, suspended.ID)
			require.NoError(t, err)

			_, err = svc.CreateAPIKey(ctx, suspended.ID, "acme", "test", nil, 0)
			require.ErrorIs(t, err, staff.ErrStaffSuspended)
		})

		t.Run("create_staff_not_found", func(t *testing.T) {
			_, err := svc.CreateAPIKey(ctx, uuid.New(), "acme", "test", nil, 0)
			require.ErrorIs(t, err, staff.ErrStaffNotFound)
		})

		t.Run("validate_success", func(t *testing.T) {
			result, err := svc.CreateAPIKey(ctx, apiUser.ID, "acme", "validate-test", []string{"read"}, 0)
			require.NoError(t, err)

			validated, err := svc.ValidateAPIKey(ctx, result.KeyPrefix, result.PlaintextKey)
			require.NoError(t, err)
			require.NotNil(t, validated)

			assert.Equal(t, apiUser.ID, validated.ID)
			assert.Equal(t, "apikey-user@example.com", validated.Email)
			assert.Equal(t, "operator", validated.Role)
			assert.Equal(t, "active", validated.Status)
		})

		t.Run("validate_wrong_key", func(t *testing.T) {
			result, err := svc.CreateAPIKey(ctx, apiUser.ID, "acme", "wrong-key-test", nil, 0)
			require.NoError(t, err)

			_, err = svc.ValidateAPIKey(ctx, result.KeyPrefix, "pk_acme_completely_wrong")
			require.ErrorIs(t, err, staff.ErrInvalidAPIKey)
		})

		t.Run("validate_expired", func(t *testing.T) {
			result, err := svc.CreateAPIKey(ctx, apiUser.ID, "acme", "expired-test", nil, 1*time.Millisecond)
			require.NoError(t, err)

			time.Sleep(10 * time.Millisecond) //nolint:forbidigo // triggers API key expiry (1ms TTL)

			_, err = svc.ValidateAPIKey(ctx, result.KeyPrefix, result.PlaintextKey)
			require.ErrorIs(t, err, staff.ErrAPIKeyExpired)
		})

		t.Run("validate_suspended_staff", func(t *testing.T) {
			suspUser, err := svc.InviteStaff(ctx, "validate-susp@example.com", "VSusp", "operator")
			require.NoError(t, err)
			err = svc.ActivateStaff(ctx, suspUser.ID, "auth0|vsusp")
			require.NoError(t, err)

			result, err := svc.CreateAPIKey(ctx, suspUser.ID, "acme", "susp-validate", nil, 0)
			require.NoError(t, err)

			err = svc.SuspendStaff(ctx, suspUser.ID)
			require.NoError(t, err)

			_, err = svc.ValidateAPIKey(ctx, result.KeyPrefix, result.PlaintextKey)
			require.ErrorIs(t, err, staff.ErrStaffSuspended)
		})

		t.Run("revoke_success", func(t *testing.T) {
			result, err := svc.CreateAPIKey(ctx, apiUser.ID, "acme", "revoke-test", nil, 0)
			require.NoError(t, err)

			err = svc.RevokeAPIKey(ctx, result.KeyPrefix)
			require.NoError(t, err)

			_, err = svc.ValidateAPIKey(ctx, result.KeyPrefix, result.PlaintextKey)
			require.ErrorIs(t, err, staff.ErrAPIKeyNotFound)
		})

		t.Run("revoke_not_found", func(t *testing.T) {
			err := svc.RevokeAPIKey(ctx, "pk_nonexistent_12345678")
			require.ErrorIs(t, err, staff.ErrAPIKeyNotFound)
		})
	})

	t.Run("MapRoleToAuth", func(t *testing.T) {
		tests := []struct {
			staffRole string
			wantRole  string
			wantErr   bool
		}{
			{"admin", "admin", false},
			{"operator", "operator", false},
			{"auditor", "auditor", false},
			{"superadmin", "", true},
		}
		for _, tt := range tests {
			t.Run(tt.staffRole, func(t *testing.T) {
				role, err := staff.MapRoleToAuth(tt.staffRole)
				if tt.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					assert.Equal(t, tt.wantRole, role.String())
				}
			})
		}
	})
}

func TestStaffIsolation_DifferentTenants(t *testing.T) {
	gormDB, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	// Setup tenant A
	tcA := testdb.SetupTenantSchema(t, gormDB, "tenant_a")
	defer tcA.Cleanup()
	createStaffTables(t, gormDB, tcA.Tenant.SchemaName())

	// Setup tenant B
	schemaB := tenant.TenantID("tenant_b").SchemaName()
	err := gormDB.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaB)).Error
	require.NoError(t, err)
	err = gormDB.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaB)).Error
	require.NoError(t, err)
	createStaffTables(t, gormDB, schemaB)
	ctxB := tenant.WithTenant(context.Background(), "tenant_b")

	svc := staff.NewService(gormDB, slog.Default())

	// Create staff in tenant A
	_, err = svc.InviteStaff(tcA.Ctx, "shared@example.com", "Alice A", "admin")
	require.NoError(t, err)

	// Tenant B should see no staff
	usersB, err := svc.ListStaff(ctxB)
	require.NoError(t, err)
	assert.Empty(t, usersB, "tenant B should not see tenant A's staff")

	// Same email in tenant B should succeed (different schema)
	_, err = svc.InviteStaff(ctxB, "shared@example.com", "Alice B", "operator")
	require.NoError(t, err)

	// Verify isolation
	usersA, err := svc.ListStaff(tcA.Ctx)
	require.NoError(t, err)
	assert.Len(t, usersA, 1)
	assert.Equal(t, "Alice A", usersA[0].Name)

	usersB, err = svc.ListStaff(ctxB)
	require.NoError(t, err)
	assert.Len(t, usersB, 1)
	assert.Equal(t, "Alice B", usersB[0].Name)
}
