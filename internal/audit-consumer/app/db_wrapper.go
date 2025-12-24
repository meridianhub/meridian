package app

import (
	"context"
	"database/sql"
)

// dbWrapper wraps sql.DB to implement the observability.DBPinger interface.
type dbWrapper struct {
	db *sql.DB
}

// Ping implements observability.DBPinger.
func (d *dbWrapper) Ping(ctx context.Context) error {
	return d.db.PingContext(ctx)
}

// Stats returns database connection pool statistics.
func (d *dbWrapper) Stats() sql.DBStats {
	return d.db.Stats()
}
