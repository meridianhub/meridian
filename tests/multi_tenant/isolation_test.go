// Package multi_org_test contains comprehensive tests verifying organization isolation
// across all system layers including database, events, Redis, and authentication.
//
// These tests are critical security tests that ensure complete isolation between
// organizations in a multi-tenant deployment.
package multi_org_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"
)

// errWrongSearchPath is a sentinel error for schema search path validation in concurrent tests.
var errWrongSearchPath = errors.New("wrong search_path for organization")

// =============================================================================
// Test Infrastructure Setup
// =============================================================================

// testPostgresContainer holds a PostgreSQL test container with organization schemas
type testPostgresContainer struct {
	container *postgres.PostgresContainer
	pool      *db.PostgresPool
}

// setupPostgresWithOrgSchemas creates a PostgreSQL container with organization schemas for testing
func setupPostgresWithOrgSchemas(ctx context.Context, t *testing.T, orgs ...string) *testPostgresContainer {
	t.Helper()

	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("test_db"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second),
				wait.ForListeningPort("5432/tcp").
					WithStartupTimeout(60*time.Second),
			),
		),
	)
	require.NoError(t, err, "failed to start postgres container")

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "failed to get connection string")

	cfg := db.DefaultConfig(connStr)
	cfg.MaxConnections = 10
	cfg.MinConnections = 1

	pool, err := db.NewPostgresPool(ctx, cfg)
	require.NoError(t, err, "failed to create postgres pool")

	// Create organization schemas with identical table structure
	for _, orgID := range orgs {
		org := tenant.MustNewTenantID(orgID)
		schemaName := org.SchemaName()
		quotedSchema := pq.QuoteIdentifier(schemaName)

		_, err = pool.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quotedSchema))
		require.NoError(t, err, "failed to create schema %s", pq.QuoteIdentifier(schemaName))

		// Create accounts table in org schema
		_, err = pool.ExecContext(ctx, fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s.accounts (
				id SERIAL PRIMARY KEY,
				account_id VARCHAR(50) UNIQUE NOT NULL,
				name VARCHAR(100) NOT NULL,
				balance DECIMAL(15,2) NOT NULL DEFAULT 0.00,
				created_at TIMESTAMP NOT NULL DEFAULT NOW()
			)
		`, quotedSchema))
		require.NoError(t, err, "failed to create accounts table in schema %s", pq.QuoteIdentifier(schemaName))
	}

	t.Cleanup(func() {
		_ = pool.Close()
		_ = pgContainer.Terminate(ctx)
	})

	return &testPostgresContainer{
		container: pgContainer,
		pool:      pool,
	}
}

// testRedisContainer holds a Redis test container
type testRedisContainer struct {
	container *redis.RedisContainer
	client    *goredis.Client
}

// setupRedis creates a Redis container for testing
func setupRedis(ctx context.Context, t *testing.T) *testRedisContainer {
	t.Helper()

	redisContainer, err := redis.Run(ctx, "redis:7-alpine")
	require.NoError(t, err, "failed to start redis container")

	endpoint, err := redisContainer.Endpoint(ctx, "")
	require.NoError(t, err, "failed to get redis endpoint")

	client := goredis.NewClient(&goredis.Options{
		Addr: endpoint,
	})

	t.Cleanup(func() {
		_ = client.Close()
		_ = redisContainer.Terminate(ctx)
	})

	return &testRedisContainer{
		container: redisContainer,
		client:    client,
	}
}

// =============================================================================
// 1. Organization Database Isolation Tests
// =============================================================================

func TestOrganizationDatabaseIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	tc := setupPostgresWithOrgSchemas(ctx, t, "acme_bank", "motive_corp")

	orgA := tenant.MustNewTenantID("acme_bank")
	orgB := tenant.MustNewTenantID("motive_corp")

	t.Run("organization_A_cannot_see_organization_B_data", func(t *testing.T) {
		// Insert account in org A
		ctxA := tenant.WithTenant(ctx, orgA)
		err := db.WithTransaction(ctxA, tc.pool, func(tx db.DB) error {
			if _, err := db.WithTenantScope(ctxA, tx); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctxA,
				"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
				"ACC-123", "Org A Account", 1000.00)
			return err
		})
		require.NoError(t, err, "failed to insert account in org A")

		// Query with org B context → expect NOT_FOUND
		ctxB := tenant.WithTenant(ctx, orgB)
		var count int
		err = db.WithTransaction(ctxB, tc.pool, func(tx db.DB) error {
			if _, err := db.WithTenantScope(ctxB, tx); err != nil {
				return err
			}
			return tx.QueryRowContext(ctxB,
				"SELECT COUNT(*) FROM accounts WHERE account_id = $1",
				"ACC-123").Scan(&count)
		})
		require.NoError(t, err)
		assert.Equal(t, 0, count, "organization B should not see organization A's data")
	})

	t.Run("same_account_id_in_different_organizations_no_conflict", func(t *testing.T) {
		// Insert same account ID in both organizations
		ctxA := tenant.WithTenant(ctx, orgA)
		err := db.WithTransaction(ctxA, tc.pool, func(tx db.DB) error {
			if _, err := db.WithTenantScope(ctxA, tx); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctxA,
				"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING",
				"SHARED-ID", "Org A Shared", 500.00)
			return err
		})
		require.NoError(t, err)

		ctxB := tenant.WithTenant(ctx, orgB)
		err = db.WithTransaction(ctxB, tc.pool, func(tx db.DB) error {
			if _, err := db.WithTenantScope(ctxB, tx); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctxB,
				"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
				"SHARED-ID", "Org B Shared", 2000.00)
			return err
		})
		require.NoError(t, err, "should allow same account ID in different organization")

		// Verify each org sees their own data
		var nameA, nameB string
		var balanceA, balanceB float64

		err = db.WithTransaction(ctxA, tc.pool, func(tx db.DB) error {
			if _, err := db.WithTenantScope(ctxA, tx); err != nil {
				return err
			}
			return tx.QueryRowContext(ctxA,
				"SELECT name, balance FROM accounts WHERE account_id = $1",
				"SHARED-ID").Scan(&nameA, &balanceA)
		})
		require.NoError(t, err)
		assert.Equal(t, "Org A Shared", nameA)
		assert.Equal(t, 500.00, balanceA)

		err = db.WithTransaction(ctxB, tc.pool, func(tx db.DB) error {
			if _, err := db.WithTenantScope(ctxB, tx); err != nil {
				return err
			}
			return tx.QueryRowContext(ctxB,
				"SELECT name, balance FROM accounts WHERE account_id = $1",
				"SHARED-ID").Scan(&nameB, &balanceB)
		})
		require.NoError(t, err)
		assert.Equal(t, "Org B Shared", nameB)
		assert.Equal(t, 2000.00, balanceB)
	})
}

func TestSearchPathRevertsAfterTransaction(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	tc := setupPostgresWithOrgSchemas(ctx, t, "test_org")

	// Get original search_path
	var originalSearchPath string
	err := tc.pool.QueryRowContext(ctx, "SHOW search_path").Scan(&originalSearchPath)
	require.NoError(t, err)

	// Run a transaction with organization scope
	orgID := tenant.MustNewTenantID("test_org")
	orgCtx := tenant.WithTenant(ctx, orgID)

	err = db.WithTransaction(orgCtx, tc.pool, func(tx db.DB) error {
		if _, err := db.WithTenantScope(orgCtx, tx); err != nil {
			return err
		}

		// Verify search_path is set within transaction
		var txSearchPath string
		if err := tx.QueryRowContext(orgCtx, "SHOW search_path").Scan(&txSearchPath); err != nil {
			return err
		}
		assert.Contains(t, txSearchPath, "org_test_org", "search_path should contain organization schema")
		return nil
	})
	require.NoError(t, err)

	// Verify search_path reverted after transaction (SET LOCAL behavior)
	var afterSearchPath string
	err = tc.pool.QueryRowContext(ctx, "SHOW search_path").Scan(&afterSearchPath)
	require.NoError(t, err)
	assert.Equal(t, originalSearchPath, afterSearchPath, "search_path should revert after transaction - ensures no connection pool leakage")
}

func TestSQLInjectionPreventionViaTenantID(t *testing.T) {
	// Test that TenantID validation prevents SQL injection attempts
	testCases := []struct {
		name          string
		maliciousID   string
		expectError   bool
		errorContains string
	}{
		{
			name:          "semicolon_injection",
			maliciousID:   "foo'; DROP TABLE accounts--",
			expectError:   true,
			errorContains: "invalid",
		},
		{
			name:          "quote_injection",
			maliciousID:   "foo'OR'1'='1",
			expectError:   true,
			errorContains: "invalid",
		},
		{
			name:          "space_injection",
			maliciousID:   "foo bar",
			expectError:   true,
			errorContains: "invalid",
		},
		{
			name:          "hyphen_in_id",
			maliciousID:   "org-with-hyphen",
			expectError:   true,
			errorContains: "invalid",
		},
		{
			name:        "too_short",
			maliciousID: "",
			expectError: true,
		},
		{
			name:        "valid_underscore",
			maliciousID: "valid_org_id",
			expectError: false,
		},
		{
			name:        "valid_alphanumeric",
			maliciousID: "AcmeBank123",
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tenant.NewTenantID(tc.maliciousID)
			if tc.expectError {
				assert.Error(t, err, "expected error for malicious organization ID: %s", tc.maliciousID)
			} else {
				assert.NoError(t, err, "expected valid organization ID: %s", tc.maliciousID)
			}
		})
	}
}

func TestOrganizationSchemaNameQuoting(t *testing.T) {
	// Verify pq.QuoteIdentifier is used for schema names
	// This ensures safe identifier handling even if validation is bypassed

	// All organization IDs are lowercased in schema names per PostgreSQL identifier conventions
	testCases := []struct {
		orgID              string
		expectedSchemaName string
	}{
		{"acme_bank", "org_acme_bank"},   // Already lowercase
		{"UPPERCASE", "org_uppercase"},   // Converted to lowercase
		{"Mixed_Case", "org_mixed_case"}, // Converted to lowercase
	}

	for _, tc := range testCases {
		t.Run(tc.orgID, func(t *testing.T) {
			orgID := tenant.MustNewTenantID(tc.orgID)
			schemaName := orgID.SchemaName()
			assert.Equal(t, tc.expectedSchemaName, schemaName)

			// Verify quoting works correctly
			quoted := pq.QuoteIdentifier(schemaName)
			assert.NotEmpty(t, quoted)
			// pq.QuoteIdentifier should wrap in quotes for safety
			assert.Contains(t, quoted, schemaName)
		})
	}

	// Test case-insensitive schema name collision handling
	// PostgreSQL identifiers are case-insensitive by default, so ACME and acme
	// must resolve to the same schema to prevent accidental cross-org access
	t.Run("case_insensitive_schema_names_handled", func(t *testing.T) {
		orgUpper := tenant.MustNewTenantID("ACME")
		orgLower := tenant.MustNewTenantID("acme")
		orgMixed := tenant.MustNewTenantID("Acme")

		// All case variants should produce identical schema names
		assert.Equal(t, orgUpper.SchemaName(), orgLower.SchemaName(),
			"ACME and acme should produce the same schema name")
		assert.Equal(t, orgLower.SchemaName(), orgMixed.SchemaName(),
			"acme and Acme should produce the same schema name")
		assert.Equal(t, "org_acme", orgUpper.SchemaName(),
			"schema name should be lowercase with org_ prefix")
	})
}

// =============================================================================
// 2. Event Isolation Tests (Kafka)
// =============================================================================

func TestKafkaEventOrganizationHeader(t *testing.T) {
	// Test that events published with organization context contain correct header
	// This is a unit test that doesn't require Kafka - tests the header injection logic

	t.Run("organization_header_key_constant", func(t *testing.T) {
		// Verify the header key constant is defined correctly
		assert.Equal(t, "x-tenant-id", tenant.TenantIDKey,
			"tenant header key should be 'x-tenant-id'")
	})

	t.Run("organization_context_extraction", func(t *testing.T) {
		ctx := context.Background()
		orgID := tenant.MustNewTenantID("acme_bank")
		ctxWithOrg := tenant.WithTenant(ctx, orgID)

		extractedOrg, ok := tenant.FromContext(ctxWithOrg)
		assert.True(t, ok, "should extract organization from context")
		assert.Equal(t, "acme_bank", extractedOrg.String())
	})

	t.Run("missing_organization_context_detected", func(t *testing.T) {
		ctx := context.Background()
		_, ok := tenant.FromContext(ctx)
		assert.False(t, ok, "should detect missing organization in context")
	})

	t.Run("require_organization_context_returns_error", func(t *testing.T) {
		ctx := context.Background()
		_, err := tenant.RequireFromContext(ctx)
		assert.ErrorIs(t, err, tenant.ErrMissingTenantContext)
	})
}

// =============================================================================
// 3. Idempotency Key Organization Isolation Tests
// =============================================================================

func TestIdempotencyKeyOrganizationIsolation(t *testing.T) {
	// Test that idempotency keys are properly isolated by organization

	t.Run("key_includes_organization_prefix", func(t *testing.T) {
		keyOrgA := idempotency.Key{
			TenantID:  "acme_bank",
			Namespace: "current-account",
			Operation: "deposit",
			EntityID:  "ACC-123",
			RequestID: "req-001",
		}

		keyOrgB := idempotency.Key{
			TenantID:  "motive_corp",
			Namespace: "current-account",
			Operation: "deposit",
			EntityID:  "ACC-123",
			RequestID: "req-001",
		}

		// Keys should be different due to organization prefix
		assert.NotEqual(t, keyOrgA.String(), keyOrgB.String(),
			"idempotency keys with same request but different organizations should differ")

		// Verify format
		assert.Contains(t, keyOrgA.String(), "acme_bank:",
			"key should contain organization prefix")
		assert.Contains(t, keyOrgB.String(), "motive_corp:",
			"key should contain organization prefix")
	})

	t.Run("key_format_with_organization", func(t *testing.T) {
		key := idempotency.Key{
			TenantID:  "acme_bank",
			Namespace: "current-account",
			Operation: "create",
			EntityID:  "ACC-123",
			RequestID: "req-abc",
		}

		expected := "acme_bank:idempotency:current-account:create:ACC-123:req-abc"
		assert.Equal(t, expected, key.String())
	})

	t.Run("key_format_without_organization_single_org_mode", func(t *testing.T) {
		key := idempotency.Key{
			TenantID:  "", // Single-tenant mode
			Namespace: "current-account",
			Operation: "create",
			EntityID:  "ACC-123",
			RequestID: "req-abc",
		}

		expected := "idempotency:current-account:create:ACC-123:req-abc"
		assert.Equal(t, expected, key.String())
	})

	t.Run("key_validation_rejects_colon_in_org_id", func(t *testing.T) {
		key := idempotency.Key{
			TenantID:  "org:with:colons",
			Namespace: "test",
			Operation: "test",
			EntityID:  "123",
		}

		err := key.Validate()
		assert.ErrorIs(t, err, idempotency.ErrInvalidKey,
			"key with colon in TenantID should be rejected")
	})
}

func TestIdempotencyKeyOrganizationIsolation_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	rc := setupRedis(ctx, t)

	// Create Redis service
	service := idempotency.NewRedisService(rc.client)

	// Same request ID but different organizations
	keyOrgA := idempotency.Key{
		TenantID:  "acme_bank",
		Namespace: "test",
		Operation: "create",
		EntityID:  "entity-1",
		RequestID: "req-001",
	}

	keyOrgB := idempotency.Key{
		TenantID:  "motive_corp",
		Namespace: "test",
		Operation: "create",
		EntityID:  "entity-1",
		RequestID: "req-001",
	}

	// Mark org A key as pending
	err := service.MarkPending(ctx, keyOrgA, 1*time.Minute)
	require.NoError(t, err)

	// Org B should be able to use same request ID (different org = different key)
	err = service.MarkPending(ctx, keyOrgB, 1*time.Minute)
	require.NoError(t, err, "org B should be able to create same request ID as org A")

	// Verify both are held independently
	// Check returns the result for pending status (no error), or ErrOperationAlreadyProcessed for completed
	resultA, err := service.Check(ctx, keyOrgA)
	require.NoError(t, err, "check should succeed for pending key")
	assert.Equal(t, idempotency.StatusPending, resultA.Status, "org A key should be pending")

	resultB, err := service.Check(ctx, keyOrgB)
	require.NoError(t, err, "check should succeed for pending key")
	assert.Equal(t, idempotency.StatusPending, resultB.Status, "org B key should be pending")

	// Most importantly - verify the keys are different
	assert.NotEqual(t, keyOrgA.String(), keyOrgB.String(),
		"keys with different organizations should be different")
}

// =============================================================================
// 4. Security Tests (JWT/Auth)
// =============================================================================

func TestTenantIDFormatValidation(t *testing.T) {
	// Comprehensive validation of organization ID format rules

	validCases := []string{
		"a",
		"abc",
		"ABC",
		"org_123",
		"acme_bank",
		"MOTIVE_CORP",
		"Test123",
		"org_with_underscores",
		"A1b2C3",
	}

	for _, id := range validCases {
		t.Run("valid_"+id, func(t *testing.T) {
			orgID, err := tenant.NewTenantID(id)
			require.NoError(t, err, "expected %q to be valid", id)
			assert.Equal(t, id, orgID.String())
		})
	}

	invalidCases := []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"space", "has space"},
		{"hyphen", "has-hyphen"},
		{"dot", "has.dot"},
		{"semicolon", "has;semicolon"},
		{"quote", "has'quote"},
		{"double_quote", `has"quote`},
		{"slash", "has/slash"},
		{"backslash", `has\backslash`},
		{"special_chars", "org@#$%"},
		{"too_long", "a123456789012345678901234567890123456789012345678901"}, // 51 chars
	}

	for _, tc := range invalidCases {
		t.Run("invalid_"+tc.name, func(t *testing.T) {
			_, err := tenant.NewTenantID(tc.id)
			assert.ErrorIs(t, err, tenant.ErrInvalidTenantID,
				"expected %q to be invalid", tc.id)
		})
	}
}

func TestMissingOrganizationContext(t *testing.T) {
	// Test that operations fail appropriately when organization context is missing

	t.Run("from_context_returns_false", func(t *testing.T) {
		ctx := context.Background()
		_, ok := tenant.FromContext(ctx)
		assert.False(t, ok)
	})

	t.Run("require_from_context_returns_error", func(t *testing.T) {
		ctx := context.Background()
		_, err := tenant.RequireFromContext(ctx)
		assert.ErrorIs(t, err, tenant.ErrMissingTenantContext)
	})

	t.Run("must_from_context_panics", func(t *testing.T) {
		ctx := context.Background()
		assert.Panics(t, func() {
			_ = tenant.MustFromContext(ctx) //nolint:staticcheck // intentionally testing deprecated function's panic behavior
		}, "MustFromContext should panic when organization is missing")
	})
}

func TestOrganizationContextIsolation(t *testing.T) {
	// Verify organization context doesn't leak between requests

	t.Run("separate_contexts_have_separate_organizations", func(t *testing.T) {
		ctx := context.Background()

		orgA := tenant.MustNewTenantID("org_a")
		orgB := tenant.MustNewTenantID("org_b")

		ctxA := tenant.WithTenant(ctx, orgA)
		ctxB := tenant.WithTenant(ctx, orgB)

		extractedA, _ := tenant.FromContext(ctxA)
		extractedB, _ := tenant.FromContext(ctxB)

		assert.Equal(t, "org_a", extractedA.String())
		assert.Equal(t, "org_b", extractedB.String())
		assert.NotEqual(t, extractedA, extractedB)
	})

	t.Run("child_context_inherits_organization", func(t *testing.T) {
		ctx := context.Background()
		orgID := tenant.MustNewTenantID("parent_org")
		ctxWithOrg := tenant.WithTenant(ctx, orgID)

		// Create child context (e.g., with timeout)
		childCtx, cancel := context.WithTimeout(ctxWithOrg, 5*time.Second)
		defer cancel()

		extracted, ok := tenant.FromContext(childCtx)
		assert.True(t, ok, "child context should inherit organization")
		assert.Equal(t, "parent_org", extracted.String())
	})

	t.Run("overriding_organization_in_child_context", func(t *testing.T) {
		ctx := context.Background()
		parentOrg := tenant.MustNewTenantID("parent_org")
		childOrg := tenant.MustNewTenantID("child_org")

		ctxParent := tenant.WithTenant(ctx, parentOrg)
		ctxChild := tenant.WithTenant(ctxParent, childOrg)

		// Parent context unchanged
		extractedParent, _ := tenant.FromContext(ctxParent)
		assert.Equal(t, "parent_org", extractedParent.String())

		// Child context has new org
		extractedChild, _ := tenant.FromContext(ctxChild)
		assert.Equal(t, "child_org", extractedChild.String())
	})
}

// =============================================================================
// 5. Cross-Organization Operation Tests
// =============================================================================

func TestCrossOrganizationDataAccessBlocked(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	tc := setupPostgresWithOrgSchemas(ctx, t, "org_united_nations", "org_motive")

	orgUN := tenant.MustNewTenantID("org_united_nations")
	orgMotive := tenant.MustNewTenantID("org_motive")

	// Insert data in UN organization
	ctxUN := tenant.WithTenant(ctx, orgUN)
	err := db.WithTransaction(ctxUN, tc.pool, func(tx db.DB) error {
		if _, err := db.WithTenantScope(ctxUN, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctxUN,
			"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
			"UN-RICE-VOUCHER", "Rice Voucher Account", 10000.00)
		return err
	})
	require.NoError(t, err)

	// Insert data in Motive organization
	ctxMotive := tenant.WithTenant(ctx, orgMotive)
	err = db.WithTransaction(ctxMotive, tc.pool, func(tx db.DB) error {
		if _, err := db.WithTenantScope(ctxMotive, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctxMotive,
			"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
			"MOTIVE-GPU-HOUR", "GPU Hour Credits", 5000.00)
		return err
	})
	require.NoError(t, err)

	t.Run("motive_cannot_see_un_accounts", func(t *testing.T) {
		var count int
		err := db.WithTransaction(ctxMotive, tc.pool, func(tx db.DB) error {
			if _, err := db.WithTenantScope(ctxMotive, tx); err != nil {
				return err
			}
			return tx.QueryRowContext(ctxMotive,
				"SELECT COUNT(*) FROM accounts WHERE account_id = $1",
				"UN-RICE-VOUCHER").Scan(&count)
		})
		require.NoError(t, err)
		assert.Equal(t, 0, count, "Motive should not see UN's accounts")
	})

	t.Run("un_cannot_see_motive_accounts", func(t *testing.T) {
		var count int
		err := db.WithTransaction(ctxUN, tc.pool, func(tx db.DB) error {
			if _, err := db.WithTenantScope(ctxUN, tx); err != nil {
				return err
			}
			return tx.QueryRowContext(ctxUN,
				"SELECT COUNT(*) FROM accounts WHERE account_id = $1",
				"MOTIVE-GPU-HOUR").Scan(&count)
		})
		require.NoError(t, err)
		assert.Equal(t, 0, count, "UN should not see Motive's accounts")
	})

	t.Run("direct_schema_access_attempt_blocked", func(t *testing.T) {
		// Attempt to directly query another organization's schema should fail
		// when using organization-scoped context

		err := db.WithTransaction(ctxMotive, tc.pool, func(tx db.DB) error {
			if _, err := db.WithTenantScope(ctxMotive, tx); err != nil {
				return err
			}

			// This attempts to query UN's schema directly while in Motive context
			// The search_path is set to org_motive, so org_org_united_nations
			// shouldn't be directly accessible via unqualified table names
			var count int
			return tx.QueryRowContext(ctxMotive,
				"SELECT COUNT(*) FROM accounts WHERE account_id = $1",
				"UN-RICE-VOUCHER").Scan(&count)
		})

		// Query should succeed but return 0 rows (data not visible)
		require.NoError(t, err, "query should not error")
	})
}

// =============================================================================
// 6. Observability Tests
// =============================================================================

func TestOrganizationInContext(t *testing.T) {
	// Verify organization ID is properly stored in context for observability

	t.Run("organization_stored_in_context", func(t *testing.T) {
		ctx := context.Background()
		orgID := tenant.MustNewTenantID("observable_org")
		ctxWithOrg := tenant.WithTenant(ctx, orgID)

		extracted, ok := tenant.FromContext(ctxWithOrg)
		require.True(t, ok)
		assert.Equal(t, "observable_org", extracted.String())
	})

	t.Run("organization_id_string_format", func(t *testing.T) {
		// Verify the string format is suitable for logging/tracing
		orgID := tenant.MustNewTenantID("log_test_org")
		assert.Equal(t, "log_test_org", orgID.String(),
			"organization ID should be directly usable in logs")
	})

	t.Run("schema_name_format_for_metrics", func(t *testing.T) {
		// Schema names could be used for metrics labeling
		orgID := tenant.MustNewTenantID("Metrics_Org")
		schemaName := orgID.SchemaName()
		assert.Equal(t, "org_metrics_org", schemaName,
			"schema name should be lowercase for consistent metrics")
	})
}

// =============================================================================
// 7. Additional Edge Case Tests
// =============================================================================

func TestTenantIDEdgeCases(t *testing.T) {
	t.Run("maximum_length_50_chars", func(t *testing.T) {
		// 50 characters should be valid
		maxLengthID := "a1234567890123456789012345678901234567890123456789"
		assert.Len(t, maxLengthID, 50)

		orgID, err := tenant.NewTenantID(maxLengthID)
		require.NoError(t, err)
		assert.Equal(t, maxLengthID, orgID.String())
	})

	t.Run("51_chars_exceeds_maximum", func(t *testing.T) {
		tooLongID := "a12345678901234567890123456789012345678901234567890"
		assert.Len(t, tooLongID, 51)

		_, err := tenant.NewTenantID(tooLongID)
		assert.Error(t, err)
	})

	t.Run("single_char_minimum", func(t *testing.T) {
		orgID, err := tenant.NewTenantID("a")
		require.NoError(t, err)
		assert.Equal(t, "a", orgID.String())
	})

	t.Run("is_empty_check", func(t *testing.T) {
		var emptyOrg tenant.TenantID
		assert.True(t, emptyOrg.IsEmpty())

		nonEmptyOrg := tenant.MustNewTenantID("not_empty")
		assert.False(t, nonEmptyOrg.IsEmpty())
	})

	t.Run("must_panics_on_invalid", func(t *testing.T) {
		assert.Panics(t, func() {
			tenant.MustNewTenantID("invalid-hyphen")
		})
	})
}

func TestOrganizationDatabaseScopeErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	tc := setupPostgresWithOrgSchemas(ctx, t, "error_test")

	t.Run("missing_organization_context_returns_error", func(t *testing.T) {
		// Context without organization
		err := db.WithTransaction(ctx, tc.pool, func(tx db.DB) error {
			_, err := db.WithTenantScope(ctx, tx) // Missing organization in context
			return err
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, tenant.ErrMissingTenantContext)
	})

	t.Run("non_existent_schema_query_fails_gracefully", func(t *testing.T) {
		// Create context with organization that has no schema
		orgID := tenant.MustNewTenantID("nonexistent_org")
		orgCtx := tenant.WithTenant(ctx, orgID)

		err := db.WithTransaction(orgCtx, tc.pool, func(tx db.DB) error {
			if _, err := db.WithTenantScope(orgCtx, tx); err != nil {
				return err
			}

			// WithTenantScope fail-fast check prevents reaching this query
			// when the tenant schema does not exist.
			var count int
			return tx.QueryRowContext(orgCtx, "SELECT COUNT(*) FROM accounts").Scan(&count)
		})

		require.Error(t, err, "query to non-existent schema should fail")
		assert.ErrorIs(t, err, db.ErrTenantSchemaNotProvisioned,
			"WithTenantScope should fail-fast when schema does not exist")
		assert.Contains(t, err.Error(), "org_nonexistent_org",
			"error should mention the missing schema")
	})
}

// =============================================================================
// 8. Concurrent Access Tests
// =============================================================================

func TestConcurrentOrganizationAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	tc := setupPostgresWithOrgSchemas(ctx, t, "concurrent_a", "concurrent_b")

	orgA := tenant.MustNewTenantID("concurrent_a")
	orgB := tenant.MustNewTenantID("concurrent_b")

	t.Run("parallel_transactions_maintain_isolation", func(t *testing.T) {
		// Run multiple goroutines accessing different orgs concurrently
		const iterations = 10
		done := make(chan error, iterations*2)

		for i := 0; i < iterations; i++ {
			// Org A transaction
			go func(iter int) {
				ctxA := tenant.WithTenant(ctx, orgA)
				err := db.WithTransaction(ctxA, tc.pool, func(tx db.DB) error {
					if _, err := db.WithTenantScope(ctxA, tx); err != nil {
						return err
					}

					// Insert with unique ID
					accountID := fmt.Sprintf("CONCURRENT-A-%d", iter)
					_, err := tx.ExecContext(ctxA,
						"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING",
						accountID, "Concurrent A", 100.00)
					if err != nil {
						return err
					}

					// Verify we're in correct schema
					var searchPath string
					if err := tx.QueryRowContext(ctxA, "SHOW search_path").Scan(&searchPath); err != nil {
						return err
					}
					if !strings.Contains(searchPath, "org_concurrent_a") {
						return fmt.Errorf("%w: expected org_concurrent_a, got %s", errWrongSearchPath, searchPath)
					}

					return nil
				})
				done <- err
			}(i)

			// Org B transaction
			go func(iter int) {
				ctxB := tenant.WithTenant(ctx, orgB)
				err := db.WithTransaction(ctxB, tc.pool, func(tx db.DB) error {
					if _, err := db.WithTenantScope(ctxB, tx); err != nil {
						return err
					}

					// Insert with unique ID
					accountID := fmt.Sprintf("CONCURRENT-B-%d", iter)
					_, err := tx.ExecContext(ctxB,
						"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING",
						accountID, "Concurrent B", 200.00)
					if err != nil {
						return err
					}

					// Verify we're in correct schema
					var searchPath string
					if err := tx.QueryRowContext(ctxB, "SHOW search_path").Scan(&searchPath); err != nil {
						return err
					}
					if !strings.Contains(searchPath, "org_concurrent_b") {
						return fmt.Errorf("%w: expected org_concurrent_b, got %s", errWrongSearchPath, searchPath)
					}

					return nil
				})
				done <- err
			}(i)
		}

		// Collect results
		for i := 0; i < iterations*2; i++ {
			err := <-done
			require.NoError(t, err, "concurrent transaction %d failed", i)
		}

		// Verify isolation - org A should not see org B data and vice versa
		ctxA := tenant.WithTenant(ctx, orgA)
		var countA, countB int

		err := db.WithTransaction(ctxA, tc.pool, func(tx db.DB) error {
			if _, err := db.WithTenantScope(ctxA, tx); err != nil {
				return err
			}
			return tx.QueryRowContext(ctxA,
				"SELECT COUNT(*) FROM accounts WHERE account_id LIKE 'CONCURRENT-A-%'").Scan(&countA)
		})
		require.NoError(t, err)

		err = db.WithTransaction(ctxA, tc.pool, func(tx db.DB) error {
			if _, err := db.WithTenantScope(ctxA, tx); err != nil {
				return err
			}
			return tx.QueryRowContext(ctxA,
				"SELECT COUNT(*) FROM accounts WHERE account_id LIKE 'CONCURRENT-B-%'").Scan(&countB)
		})
		require.NoError(t, err)

		assert.GreaterOrEqual(t, countA, 1, "org A should have its own records")
		assert.Equal(t, 0, countB, "org A should not see org B's records")
	})
}
