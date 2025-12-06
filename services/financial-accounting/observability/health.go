// Package observability provides health checks for the financial-accounting service.
package observability

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/health"
)

// GormDBChecker checks the health of a GORM database connection via the underlying *sql.DB.
type GormDBChecker struct {
	db *sql.DB
}

// NewGormDBChecker creates a new GORM database health checker.
// Pass the *sql.DB obtained from gorm.DB.DB().
func NewGormDBChecker(db *sql.DB) *GormDBChecker {
	if db == nil {
		panic("NewGormDBChecker: db cannot be nil")
	}
	return &GormDBChecker{
		db: db,
	}
}

// Name returns the component name.
func (g *GormDBChecker) Name() string {
	return "database"
}

// Check performs a database health check by pinging the connection.
func (g *GormDBChecker) Check(ctx context.Context) health.ComponentResult {
	start := time.Now()

	err := g.db.PingContext(ctx)
	responseTime := time.Since(start)

	status := health.StatusHealthy
	message := "database connection successful"

	if err != nil {
		status = health.StatusUnhealthy
		message = fmt.Sprintf("database ping failed: %v", err)
	}

	return health.ComponentResult{
		Name:         g.Name(),
		Status:       status,
		Message:      message,
		ResponseTime: responseTime,
		CheckedAt:    start,
		Error:        err,
	}
}
