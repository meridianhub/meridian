package app

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/shared/pkg/health"
)

// PgxPoolChecker checks the health of a pgxpool database connection.
type PgxPoolChecker struct {
	pool *pgxpool.Pool
}

// NewPgxPoolChecker creates a new pgxpool health checker.
func NewPgxPoolChecker(pool *pgxpool.Pool) *PgxPoolChecker {
	if pool == nil {
		panic("NewPgxPoolChecker: pool cannot be nil")
	}
	return &PgxPoolChecker{
		pool: pool,
	}
}

// Name returns the component name.
func (d *PgxPoolChecker) Name() string {
	return "database"
}

// Check performs a database health check by pinging the connection pool.
func (d *PgxPoolChecker) Check(ctx context.Context) health.ComponentResult {
	start := time.Now()

	err := d.pool.Ping(ctx)
	responseTime := time.Since(start)

	status := health.StatusHealthy
	message := "database connection successful"

	if err != nil {
		status = health.StatusUnhealthy
		message = fmt.Sprintf("database ping failed: %v", err)
	}

	return health.ComponentResult{
		Name:         d.Name(),
		Status:       status,
		Message:      message,
		ResponseTime: responseTime,
		CheckedAt:    start,
		Error:        err,
	}
}
