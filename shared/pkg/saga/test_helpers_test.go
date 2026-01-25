package saga

import (
	"testing"

	"github.com/meridianhub/meridian/shared/platform/testdb"
	"gorm.io/gorm"
)

// setupTestPostgres creates a CockroachDB testcontainer for integration testing.
// Named setupTestPostgres for historical compatibility - CockroachDB is wire-compatible
// with PostgreSQL and is our production database.
func setupTestPostgres(t *testing.T) (*gorm.DB, func()) {
	return testdb.SetupCockroachDB(t, nil)
}
