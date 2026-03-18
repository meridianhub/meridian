// Package testdb provides utilities for setting up test databases.
//
// [SetupCockroachDB] spins up a CockroachDB testcontainer and returns a GORM
// connection ready for integration tests. Using CockroachDB (rather than a
// PostgreSQL substitute) ensures production parity for migration and constraint
// behavior.
//
// The container is started with up to three retries to handle transient Docker
// daemon contention in CI environments.
//
// # Usage
//
//	func TestMyRepository(t *testing.T) {
//	    db, cleanup := testdb.SetupCockroachDB(t, nil)
//	    defer cleanup()
//	    // use db ...
//	}
package testdb
