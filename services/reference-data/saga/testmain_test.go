package saga

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// sharedPgPool is a shared PostgreSQL connection pool for integration tests.
// Started once in TestMain, reused across all tests via per-test schema isolation.
var sharedPgPool *pgxpool.Pool

// SharedPgConnStr is the connection string for the shared PostgreSQL container.
// Exported so that saga_test (external test package) can access it.
var SharedPgConnStr string

// sharedCrdbDSN is a shared CockroachDB DSN for platform sync integration tests.
// Started once in TestMain, tests create per-test databases for isolation.
var sharedCrdbDSN string

func TestMain(m *testing.M) {
	// Start shared PostgreSQL container (used by grpc_handler_test.go)
	pgConnStr, pgCleanup := testdb.StartSharedPostgres()

	pool, err := pgxpool.New(context.Background(), pgConnStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testdb: failed to create shared pgx pool: %v\n", err)
		pgCleanup()
		os.Exit(1)
	}
	sharedPgPool = pool
	SharedPgConnStr = pgConnStr

	// Start shared CockroachDB container (used by platform_sync, seeder, e2e tests)
	crdbDSN, crdbCleanup := testdb.StartSharedCockroachDB()
	sharedCrdbDSN = crdbDSN

	code := m.Run()

	pool.Close()
	pgCleanup()
	crdbCleanup()
	os.Exit(code)
}
