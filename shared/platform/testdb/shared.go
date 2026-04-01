// Package testdb provides utilities for setting up test databases.
package testdb

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartSharedPostgres starts a single PostgreSQL testcontainer for sharing
// across all tests in a package via TestMain. Returns the connection string
// and a cleanup function that terminates the container.
//
// Usage in TestMain:
//
//	var sharedConnStr string
//
//	func TestMain(m *testing.M) {
//	    connStr, cleanup := testdb.StartSharedPostgres()
//	    sharedConnStr = connStr
//	    code := m.Run()
//	    cleanup()
//	    os.Exit(code)
//	}
//
// Then in each test setup, open a new GORM connection to sharedConnStr
// and create a unique schema for test isolation.
func StartSharedPostgres() (string, func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

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
	if err != nil {
		panic(fmt.Sprintf("testdb: failed to start shared PostgreSQL container: %v", err))
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic(fmt.Sprintf("testdb: failed to get connection string: %v", err))
	}

	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = pgContainer.Terminate(cleanupCtx)
	}

	return connStr, cleanup
}

// StartSharedCockroachDB starts a single CockroachDB testcontainer for sharing
// across all tests in a package via TestMain. Returns the connection string
// and a cleanup function that terminates the container.
//
// Usage in TestMain:
//
//	var sharedCockroachDSN string
//
//	func TestMain(m *testing.M) {
//	    dsn, cleanup := testdb.StartSharedCockroachDB()
//	    sharedCockroachDSN = dsn
//	    code := m.Run()
//	    cleanup()
//	    os.Exit(code)
//	}
//
// Then in each test, create a unique database for isolation:
//
//	dbName := "test_" + strings.ReplaceAll(t.Name(), "/", "_")
//	pool.Exec(ctx, "CREATE DATABASE "+dbName)
func StartSharedCockroachDB() (string, func()) {
	ctx, cancel := context.WithTimeout(context.Background(), cockroachStartupTimeout)
	defer cancel()

	container, err := cockroachdb.Run(ctx,
		CockroachDBImage,
		cockroachdb.WithDatabase("defaultdb"),
		cockroachdb.WithUser("root"),
		cockroachdb.WithInsecure(),
	)
	if err != nil {
		panic(fmt.Sprintf("testdb: failed to start shared CockroachDB container: %v", err))
	}

	connConfig, err := container.ConnectionConfig(ctx)
	if err != nil {
		panic(fmt.Sprintf("testdb: failed to get CockroachDB connection config: %v", err))
	}

	dsn := connConfig.ConnString()

	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = container.Terminate(cleanupCtx)
	}

	return dsn, cleanup
}
