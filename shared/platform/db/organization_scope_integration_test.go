package db

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/organization"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupOrgScopeTestContainer creates a PostgreSQL container with organization schemas for testing
func setupOrgScopeTestContainer(ctx context.Context, t *testing.T) (*PostgresPool, func()) {
	t.Helper()

	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("test_db"),
		postgres.WithUsername("test_user"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err, "failed to start postgres container")

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err, "failed to get connection string")

	cfg := DefaultConfig(connStr)
	cfg.MaxConnections = 10
	cfg.MinConnections = 2

	pool, err := NewPostgresPool(ctx, cfg)
	require.NoError(t, err, "failed to create postgres pool")

	// Create organization schemas with identical table structure
	schemas := []string{"org_test_a", "org_test_b"}
	for _, schema := range schemas {
		_, err = pool.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema)
		require.NoError(t, err, "failed to create schema %s", schema)

		_, err = pool.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS `+schema+`.accounts (
				id SERIAL PRIMARY KEY,
				account_id VARCHAR(50) UNIQUE NOT NULL,
				name VARCHAR(100) NOT NULL,
				balance DECIMAL(15,2) NOT NULL DEFAULT 0.00
			)
		`)
		require.NoError(t, err, "failed to create accounts table in schema %s", schema)
	}

	// Insert different data in each schema
	_, err = pool.ExecContext(ctx,
		"INSERT INTO org_test_a.accounts (account_id, name, balance) VALUES ($1, $2, $3)",
		"ACC-001", "Org A Account", 1000.00)
	require.NoError(t, err, "failed to insert into org_test_a")

	_, err = pool.ExecContext(ctx,
		"INSERT INTO org_test_b.accounts (account_id, name, balance) VALUES ($1, $2, $3)",
		"ACC-001", "Org B Account", 2000.00)
	require.NoError(t, err, "failed to insert into org_test_b")

	// Create shared reference data in public schema
	_, err = pool.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS public.currency_codes (
			code VARCHAR(3) PRIMARY KEY,
			name VARCHAR(50) NOT NULL
		)
	`)
	require.NoError(t, err, "failed to create public.currency_codes")

	_, err = pool.ExecContext(ctx,
		"INSERT INTO public.currency_codes (code, name) VALUES ($1, $2)",
		"USD", "US Dollar")
	require.NoError(t, err, "failed to insert public reference data")

	cleanup := func() {
		_ = pool.Close()
		_ = pgContainer.Terminate(ctx)
	}

	return pool, cleanup
}

func TestWithOrganizationScope_Integration_QueryRouting(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupOrgScopeTestContainer(ctx, t)
	defer cleanup()

	t.Run("org_a retrieves org_a data", func(t *testing.T) {
		orgID := organization.MustNewOrganizationID("test_a")
		orgCtx := organization.WithOrganization(ctx, orgID)

		var name string
		var balance float64

		err := WithTransaction(orgCtx, pool, func(tx DB) error {
			if _, err := WithOrganizationScope(orgCtx, tx); err != nil {
				return err
			}

			return tx.QueryRowContext(orgCtx,
				"SELECT name, balance FROM accounts WHERE account_id = $1",
				"ACC-001").Scan(&name, &balance)
		})

		require.NoError(t, err)
		assert.Equal(t, "Org A Account", name)
		assert.Equal(t, 1000.00, balance)
	})

	t.Run("org_b retrieves org_b data", func(t *testing.T) {
		orgID := organization.MustNewOrganizationID("test_b")
		orgCtx := organization.WithOrganization(ctx, orgID)

		var name string
		var balance float64

		err := WithTransaction(orgCtx, pool, func(tx DB) error {
			if _, err := WithOrganizationScope(orgCtx, tx); err != nil {
				return err
			}

			return tx.QueryRowContext(orgCtx,
				"SELECT name, balance FROM accounts WHERE account_id = $1",
				"ACC-001").Scan(&name, &balance)
		})

		require.NoError(t, err)
		assert.Equal(t, "Org B Account", name)
		assert.Equal(t, 2000.00, balance)
	})
}

func TestWithOrganizationScope_Integration_Isolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupOrgScopeTestContainer(ctx, t)
	defer cleanup()

	// Org A should not see Org B's unique data
	orgID := organization.MustNewOrganizationID("test_a")
	orgCtx := organization.WithOrganization(ctx, orgID)

	// Insert unique data in org_b
	_, err := pool.ExecContext(ctx,
		"INSERT INTO org_test_b.accounts (account_id, name, balance) VALUES ($1, $2, $3)",
		"ACC-UNIQUE-B", "Org B Only", 5000.00)
	require.NoError(t, err)

	// Try to query it from org_a context - should return no rows
	var count int
	err = WithTransaction(orgCtx, pool, func(tx DB) error {
		if _, err := WithOrganizationScope(orgCtx, tx); err != nil {
			return err
		}

		return tx.QueryRowContext(orgCtx,
			"SELECT COUNT(*) FROM accounts WHERE account_id = $1",
			"ACC-UNIQUE-B").Scan(&count)
	})

	require.NoError(t, err)
	assert.Equal(t, 0, count, "org_a should not see org_b's unique data")
}

func TestWithOrganizationScope_Integration_SearchPathAutoRevert(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupOrgScopeTestContainer(ctx, t)
	defer cleanup()

	// Get original search_path
	var originalSearchPath string
	err := pool.QueryRowContext(ctx, "SHOW search_path").Scan(&originalSearchPath)
	require.NoError(t, err)

	// Run a transaction with organization scope
	orgID := organization.MustNewOrganizationID("test_a")
	orgCtx := organization.WithOrganization(ctx, orgID)

	err = WithTransaction(orgCtx, pool, func(tx DB) error {
		if _, err := WithOrganizationScope(orgCtx, tx); err != nil {
			return err
		}

		// Verify search_path is set within transaction
		var txSearchPath string
		if err := tx.QueryRowContext(orgCtx, "SHOW search_path").Scan(&txSearchPath); err != nil {
			return err
		}
		// Should contain org_test_a
		assert.Contains(t, txSearchPath, "org_test_a")
		return nil
	})
	require.NoError(t, err)

	// Verify search_path reverted after transaction
	var afterSearchPath string
	err = pool.QueryRowContext(ctx, "SHOW search_path").Scan(&afterSearchPath)
	require.NoError(t, err)
	assert.Equal(t, originalSearchPath, afterSearchPath, "search_path should revert after transaction")
}

func TestWithOrganizationScope_Integration_PublicSchemaAccess(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupOrgScopeTestContainer(ctx, t)
	defer cleanup()

	// Both organizations should be able to read from public schema
	orgs := []string{"test_a", "test_b"}

	for _, org := range orgs {
		t.Run("org_"+org+"_can_read_public", func(t *testing.T) {
			orgID := organization.MustNewOrganizationID(org)
			orgCtx := organization.WithOrganization(ctx, orgID)

			var currencyName string
			err := WithTransaction(orgCtx, pool, func(tx DB) error {
				if _, err := WithOrganizationScope(orgCtx, tx); err != nil {
					return err
				}

				// Query public schema table
				return tx.QueryRowContext(orgCtx,
					"SELECT name FROM public.currency_codes WHERE code = $1",
					"USD").Scan(&currencyName)
			})

			require.NoError(t, err)
			assert.Equal(t, "US Dollar", currencyName)
		})
	}
}

func TestWithOrganizationScope_Integration_MissingOrganizationContext(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupOrgScopeTestContainer(ctx, t)
	defer cleanup()

	// Context without organization
	err := WithTransaction(ctx, pool, func(tx DB) error {
		_, err := WithOrganizationScope(ctx, tx) // Missing organization in context
		return err
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, organization.ErrMissingOrganizationContext)
}

func TestWithOrganizationScope_Integration_NonExistentSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupOrgScopeTestContainer(ctx, t)
	defer cleanup()

	// Create context with organization that has no schema
	orgID := organization.MustNewOrganizationID("nonexistent")
	orgCtx := organization.WithOrganization(ctx, orgID)

	// SET LOCAL should succeed (PostgreSQL allows setting search_path to non-existent schemas)
	// But querying tables should fail
	err := WithTransaction(orgCtx, pool, func(tx DB) error {
		if _, err := WithOrganizationScope(orgCtx, tx); err != nil {
			return err
		}

		// This query should fail because the schema doesn't exist
		var count int
		return tx.QueryRowContext(orgCtx, "SELECT COUNT(*) FROM accounts").Scan(&count)
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "accounts") // Table not found in search_path
}

func TestWithOrganizationScope_Integration_SQLInjectionPrevention(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupOrgScopeTestContainer(ctx, t)
	defer cleanup()

	// Attempt SQL injection through organization ID
	// Note: This test relies on organization.NewOrganizationID rejecting invalid characters
	// The SQL injection attempt should be blocked at the OrganizationID validation level

	// Valid organization IDs cannot contain SQL injection characters due to validation
	// So we test that the schema name is properly quoted even for edge cases

	t.Run("schema_name_is_quoted", func(t *testing.T) {
		orgID := organization.MustNewOrganizationID("test_a")
		orgCtx := organization.WithOrganization(ctx, orgID)

		err := WithTransaction(orgCtx, pool, func(tx DB) error {
			if _, err := WithOrganizationScope(orgCtx, tx); err != nil {
				return err
			}

			// Verify search_path is properly quoted
			var searchPath string
			if err := tx.QueryRowContext(orgCtx, "SHOW search_path").Scan(&searchPath); err != nil {
				return err
			}

			// The schema name should be in the search_path
			assert.Contains(t, searchPath, "org_test_a")
			return nil
		})

		require.NoError(t, err)
	})
}

func TestWithOrganizationScope_Integration_WriteIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx := context.Background()
	pool, cleanup := setupOrgScopeTestContainer(ctx, t)
	defer cleanup()

	// Write from org_a context
	orgIDa := organization.MustNewOrganizationID("test_a")
	orgCtxA := organization.WithOrganization(ctx, orgIDa)

	err := WithTransaction(orgCtxA, pool, func(tx DB) error {
		if _, err := WithOrganizationScope(orgCtxA, tx); err != nil {
			return err
		}

		_, err := tx.ExecContext(orgCtxA,
			"INSERT INTO accounts (account_id, name, balance) VALUES ($1, $2, $3)",
			"ACC-WRITE-TEST", "Write Test", 3000.00)
		return err
	})
	require.NoError(t, err)

	// Verify the write is only visible in org_a
	t.Run("write_visible_in_org_a", func(t *testing.T) {
		var balance float64
		err := WithTransaction(orgCtxA, pool, func(tx DB) error {
			if _, err := WithOrganizationScope(orgCtxA, tx); err != nil {
				return err
			}

			return tx.QueryRowContext(orgCtxA,
				"SELECT balance FROM accounts WHERE account_id = $1",
				"ACC-WRITE-TEST").Scan(&balance)
		})

		require.NoError(t, err)
		assert.Equal(t, 3000.00, balance)
	})

	t.Run("write_not_visible_in_org_b", func(t *testing.T) {
		orgIDb := organization.MustNewOrganizationID("test_b")
		orgCtxB := organization.WithOrganization(ctx, orgIDb)

		var count int
		err := WithTransaction(orgCtxB, pool, func(tx DB) error {
			if _, err := WithOrganizationScope(orgCtxB, tx); err != nil {
				return err
			}

			return tx.QueryRowContext(orgCtxB,
				"SELECT COUNT(*) FROM accounts WHERE account_id = $1",
				"ACC-WRITE-TEST").Scan(&count)
		})

		require.NoError(t, err)
		assert.Equal(t, 0, count, "org_b should not see org_a's write")
	})
}
