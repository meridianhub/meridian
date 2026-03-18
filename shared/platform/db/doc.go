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
// # Environment Variables
//
//   - DATABASE_URL: full connection string (required)
//   - DB_MAX_OPEN_CONNS: maximum open connections (default: 25)
//   - DB_MAX_IDLE_CONNS: maximum idle connections (default: 5)
//   - DB_CONN_MAX_LIFETIME: connection lifetime (default: 5m)
package db
