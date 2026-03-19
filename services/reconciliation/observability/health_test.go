package observability

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNewDatabaseChecker_NilPanics(t *testing.T) {
	require.Panics(t, func() {
		NewDatabaseChecker(nil)
	})
}

func TestDatabaseChecker_Name(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	checker := NewDatabaseChecker(db)
	assert.Equal(t, "database", checker.Name())
}

func TestDatabaseChecker_Healthy(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	checker := NewDatabaseChecker(db)
	result := checker.Check(context.Background())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "successful")
	assert.NoError(t, result.Error)
	assert.Equal(t, "database", result.Name)
}

func TestDatabaseChecker_Unhealthy(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Close the underlying connection to simulate failure
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	checker := NewDatabaseChecker(db)
	result := checker.Check(context.Background())
	assert.Equal(t, health.StatusUnhealthy, result.Status)
	assert.Contains(t, result.Message, "ping failed")
	assert.Error(t, result.Error)
}
