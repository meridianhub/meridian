package service

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// sharedPGConnStr holds the connection string for the shared PostgreSQL
// testcontainer started in TestMain. All tests in this package reuse this
// single container instead of starting ~140 individual ones.
var sharedPGConnStr string

func TestMain(m *testing.M) {
	connStr, cleanup := testdb.StartSharedPostgres()
	sharedPGConnStr = connStr

	code := m.Run()

	cleanup()
	os.Exit(code)
}

// tenantSeq is an atomic counter for generating unique tenant IDs per test.
var tenantSeq uint64

// uniqueTenantID returns a unique tenant.TenantID for test isolation.
// Each call returns a different ID (t1, t2, …) so tests using the shared
// container get non-overlapping schemas via tenant.TenantID.SchemaName().
func uniqueTenantID() tenant.TenantID {
	n := atomic.AddUint64(&tenantSeq, 1)
	return tenant.TenantID(fmt.Sprintf("t%d", n))
}

// openSharedDB opens a new GORM connection to the shared testcontainer.
// Each call returns an independent database session.
func openSharedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormpg.Open(sharedPGConnStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("Failed to connect to shared database: %v", err)
	}
	return db
}
