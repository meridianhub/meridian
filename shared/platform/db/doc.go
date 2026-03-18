// Package db provides a database abstraction layer optimized for distributed SQL databases.
//
// The package implements a unified DB interface that works with both connection pools and
// transactions, so repository code can be written once and work in either context. It is
// optimized for CockroachDB with serializable isolation, automatic retry logic, and
// per-service database isolation via connection strings.
//
// # Database-per-Service
//
// Each microservice connects to its own isolated database. The database name is part of
// the connection string:
//
//	postgresql://meridian:secret@cockroachdb:26257/meridian_current_account?sslmode=require
//
// # Configuration
//
// Use [DefaultConfig] to build a Config with CockroachDB-tuned defaults:
//
//   - MaxConnections: 50
//   - MinConnections: 5
//   - MaxConnectionLifetime: 1 hour
//   - MaxConnectionIdleTime: 10 minutes
//
// The connection string is typically read from the DATABASE_URL environment variable.
package db
