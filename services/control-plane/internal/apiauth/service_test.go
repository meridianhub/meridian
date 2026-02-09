package apiauth_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/apiauth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testTenantID = "test_tenant"

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	t.Cleanup(cleanup)

	schemaName := tenant.MustNewTenantID(testTenantID).SchemaName()
	q := fmt.Sprintf("%q", schemaName)

	ddls := []string{
		fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", q),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s."staff_user" (
			"id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			"email" VARCHAR(255) NOT NULL,
			"name" VARCHAR(255),
			"role" VARCHAR(50) NOT NULL DEFAULT 'operator',
			"status" VARCHAR(20) NOT NULL DEFAULT 'invited',
			"auth_provider_id" VARCHAR(255),
			"created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			"updated_at" TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`, q),
		fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS "idx_staff_user_email_%s" ON %s."staff_user" ("email")`, testTenantID, q),
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s."api_key" (
			"id" UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			"staff_user_id" UUID NOT NULL,
			"key_prefix" VARCHAR(100) NOT NULL,
			"key_hash" BYTEA NOT NULL,
			"name" VARCHAR(255),
			"scopes" TEXT[],
			"rate_limit_rps" INTEGER NOT NULL DEFAULT 100,
			"last_used_at" TIMESTAMPTZ,
			"expires_at" TIMESTAMPTZ,
			"created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			"revoked_at" TIMESTAMPTZ
		)`, q),
	}

	for _, ddl := range ddls {
		require.NoError(t, db.Exec(ddl).Error, "DDL failed: %s", ddl)
	}

	return db
}

func insertTestStaffAndKey(t *testing.T, db *gorm.DB, staffID uuid.UUID, email, name, status, keyPrefix, plaintextKey string, scopes []string, rateLimitRPS int) {
	t.Helper()
	schemaName := tenant.MustNewTenantID(testTenantID).SchemaName()
	q := fmt.Sprintf("%q", schemaName)

	require.NoError(t, db.Exec(
		fmt.Sprintf(`INSERT INTO %s."staff_user" (id, email, name, role, status) VALUES (?, ?, ?, 'operator', ?)`, q),
		staffID, email, name, status,
	).Error)

	hash := sha256.Sum256([]byte(plaintextKey))
	keyID := uuid.New()

	if scopes == nil {
		require.NoError(t, db.Exec(
			fmt.Sprintf(`INSERT INTO %s."api_key" (id, staff_user_id, key_prefix, key_hash, rate_limit_rps) VALUES (?, ?, ?, ?, ?)`, q),
			keyID, staffID, keyPrefix, hash[:], rateLimitRPS,
		).Error)
	} else {
		scopeArray := "{" + joinScopes(scopes) + "}"
		require.NoError(t, db.Exec(
			fmt.Sprintf(`INSERT INTO %s."api_key" (id, staff_user_id, key_prefix, key_hash, scopes, rate_limit_rps) VALUES (?, ?, ?, ?, ?::text[], ?)`, q),
			keyID, staffID, keyPrefix, hash[:], scopeArray, rateLimitRPS,
		).Error)
	}
}

func joinScopes(scopes []string) string {
	result := ""
	for i, s := range scopes {
		if i > 0 {
			result += ","
		}
		result += fmt.Sprintf(`"%s"`, s)
	}
	return result
}

func ctxWithTenant() context.Context {
	tenantID := tenant.MustNewTenantID(testTenantID)
	return tenant.WithTenant(context.Background(), tenantID)
}

func TestValidateAPIKey_ValidKey(t *testing.T) {
	db := setupTestDB(t)
	svc := apiauth.NewService(db, slog.Default())

	staffID := uuid.New()
	keyPrefix := "pk_acme_abc12345"
	plaintextKey := "pk_acme_abc12345_fullentropy"
	insertTestStaffAndKey(t, db, staffID, "alice@acme.com", "Alice", "active", keyPrefix, plaintextKey, []string{"read", "write"}, 50)

	ctx := ctxWithTenant()
	resp, err := svc.ValidateAPIKey(ctx, &controlplanev1.ValidateAPIKeyRequest{
		KeyPrefix:    keyPrefix,
		PlaintextKey: plaintextKey,
	})

	require.NoError(t, err)
	assert.True(t, resp.GetValid())
	assert.Equal(t, testTenantID, resp.GetTenantId())
	assert.Equal(t, "Alice", resp.GetIdentity())
	assert.Equal(t, []string{"read", "write"}, resp.GetScopes())
	assert.Equal(t, int32(50), resp.GetRateLimitRps())
}

func TestValidateAPIKey_InvalidKey(t *testing.T) {
	db := setupTestDB(t)
	svc := apiauth.NewService(db, slog.Default())

	staffID := uuid.New()
	keyPrefix := "pk_acme_abc12345"
	plaintextKey := "pk_acme_abc12345_fullentropy"
	insertTestStaffAndKey(t, db, staffID, "alice@acme.com", "Alice", "active", keyPrefix, plaintextKey, nil, 100)

	ctx := ctxWithTenant()
	resp, err := svc.ValidateAPIKey(ctx, &controlplanev1.ValidateAPIKeyRequest{
		KeyPrefix:    keyPrefix,
		PlaintextKey: "wrong_key",
	})

	require.NoError(t, err)
	assert.False(t, resp.GetValid())
}

func TestValidateAPIKey_MissingTenantContext(t *testing.T) {
	db := setupTestDB(t)
	svc := apiauth.NewService(db, slog.Default())

	_, err := svc.ValidateAPIKey(context.Background(), &controlplanev1.ValidateAPIKeyRequest{
		KeyPrefix:    "pk_acme_abc12345",
		PlaintextKey: "pk_acme_abc12345_fullentropy",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "tenant context required")
}

func TestValidateAPIKey_EmptyFields(t *testing.T) {
	db := setupTestDB(t)
	svc := apiauth.NewService(db, slog.Default())

	_, err := svc.ValidateAPIKey(ctxWithTenant(), &controlplanev1.ValidateAPIKeyRequest{
		KeyPrefix:    "",
		PlaintextKey: "",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestValidateAPIKey_SuspendedUser(t *testing.T) {
	db := setupTestDB(t)
	svc := apiauth.NewService(db, slog.Default())

	staffID := uuid.New()
	keyPrefix := "pk_acme_def12345"
	plaintextKey := "pk_acme_def12345_fullentropy"
	insertTestStaffAndKey(t, db, staffID, "bob@acme.com", "Bob", "suspended", keyPrefix, plaintextKey, nil, 100)

	ctx := ctxWithTenant()
	resp, err := svc.ValidateAPIKey(ctx, &controlplanev1.ValidateAPIKeyRequest{
		KeyPrefix:    keyPrefix,
		PlaintextKey: plaintextKey,
	})

	require.NoError(t, err)
	assert.False(t, resp.GetValid())
}

func TestValidateAPIKey_ExpiredKey(t *testing.T) {
	db := setupTestDB(t)
	svc := apiauth.NewService(db, slog.Default())

	staffID := uuid.New()
	keyPrefix := "pk_acme_exp12345"
	plaintextKey := "pk_acme_exp12345_fullentropy"

	schemaName := tenant.MustNewTenantID(testTenantID).SchemaName()
	q := fmt.Sprintf("%q", schemaName)

	require.NoError(t, db.Exec(
		fmt.Sprintf(`INSERT INTO %s."staff_user" (id, email, name, role, status) VALUES (?, ?, ?, 'operator', 'active')`, q),
		staffID, "charlie@acme.com", "Charlie",
	).Error)

	hash := sha256.Sum256([]byte(plaintextKey))
	expired := time.Now().Add(-1 * time.Hour)
	require.NoError(t, db.Exec(
		fmt.Sprintf(`INSERT INTO %s."api_key" (id, staff_user_id, key_prefix, key_hash, expires_at) VALUES (?, ?, ?, ?, ?)`, q),
		uuid.New(), staffID, keyPrefix, hash[:], expired,
	).Error)

	ctx := ctxWithTenant()
	resp, err := svc.ValidateAPIKey(ctx, &controlplanev1.ValidateAPIKeyRequest{
		KeyPrefix:    keyPrefix,
		PlaintextKey: plaintextKey,
	})

	require.NoError(t, err)
	assert.False(t, resp.GetValid())
}

func TestValidateAPIKey_FallsBackToEmail(t *testing.T) {
	db := setupTestDB(t)
	svc := apiauth.NewService(db, slog.Default())

	staffID := uuid.New()
	keyPrefix := "pk_acme_noname12"
	plaintextKey := "pk_acme_noname12_fullentropy"

	schemaName := tenant.MustNewTenantID(testTenantID).SchemaName()
	q := fmt.Sprintf("%q", schemaName)

	// Insert staff user without a name
	require.NoError(t, db.Exec(
		fmt.Sprintf(`INSERT INTO %s."staff_user" (id, email, role, status) VALUES (?, ?, 'operator', 'active')`, q),
		staffID, "noname@acme.com",
	).Error)

	hash := sha256.Sum256([]byte(plaintextKey))
	require.NoError(t, db.Exec(
		fmt.Sprintf(`INSERT INTO %s."api_key" (id, staff_user_id, key_prefix, key_hash) VALUES (?, ?, ?, ?)`, q),
		uuid.New(), staffID, keyPrefix, hash[:],
	).Error)

	ctx := ctxWithTenant()
	resp, err := svc.ValidateAPIKey(ctx, &controlplanev1.ValidateAPIKeyRequest{
		KeyPrefix:    keyPrefix,
		PlaintextKey: plaintextKey,
	})

	require.NoError(t, err)
	assert.True(t, resp.GetValid())
	assert.Equal(t, "noname@acme.com", resp.GetIdentity())
}
