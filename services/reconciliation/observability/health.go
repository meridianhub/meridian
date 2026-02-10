// Package observability provides health checks and metrics for the reconciliation service.
package observability

import (
	"context"
	"fmt"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/health"
	"gorm.io/gorm"
)

// DatabaseChecker checks the health of a GORM database connection.
type DatabaseChecker struct {
	db *gorm.DB
}

// NewDatabaseChecker creates a new database health checker.
func NewDatabaseChecker(db *gorm.DB) *DatabaseChecker {
	if db == nil {
		panic("NewDatabaseChecker: db cannot be nil")
	}
	return &DatabaseChecker{db: db}
}

// Name returns the component name.
func (d *DatabaseChecker) Name() string {
	return "database"
}

// Check performs a database health check by pinging the connection.
func (d *DatabaseChecker) Check(ctx context.Context) health.ComponentResult {
	start := time.Now()

	sqlDB, err := d.db.DB()
	if err != nil {
		return health.ComponentResult{
			Name:         d.Name(),
			Status:       health.StatusUnhealthy,
			Message:      fmt.Sprintf("failed to get database instance: %v", err),
			ResponseTime: time.Since(start),
			CheckedAt:    start,
			Error:        err,
		}
	}

	err = sqlDB.PingContext(ctx)
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
