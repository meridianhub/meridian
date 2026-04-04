// Package testdb provides utilities for setting up test databases.
package testdb

import (
	"context"
	"fmt"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"gorm.io/gorm"
)

// TenantTestContext holds the database, context, and cleanup function for tenant-scoped tests.
type TenantTestContext struct {
	DB      *gorm.DB
	Ctx     context.Context
	Cleanup func()
	Tenant  tenant.TenantID
}

// SetupTenantSchema creates a tenant schema and returns a test context with:
// - The tenant schema created in the database
// - search_path set to the tenant schema
// - A context.Context with the tenant ID injected
//
// Usage:
//
//	func TestMyRepository(t *testing.T) {
//	    db, cleanup := testdb.SetupPostgres(t, []interface{}{&MyEntity{}})
//	    defer cleanup()
//
//	    tc := testdb.SetupTenantSchema(t, db, "test_tenant")
//	    // tc.Ctx has tenant context, tc.DB has search_path set
//	}
//
// For tests that need custom table DDL (not using AutoMigrate), use CreateTable after this.
func SetupTenantSchema(t *testing.T, db *gorm.DB, tenantID string) *TenantTestContext {
	t.Helper()

	tid := tenant.TenantID(tenantID)
	schemaName := tid.SchemaName()

	// Create the tenant schema
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	if err != nil {
		t.Fatalf("Failed to create tenant schema %s: %v", schemaName, err)
	}

	// Set search_path to tenant schema so subsequent operations use it
	err = db.Exec(fmt.Sprintf("SET search_path TO %q", schemaName)).Error
	if err != nil {
		t.Fatalf("Failed to set search_path to %s: %v", schemaName, err)
	}

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return &TenantTestContext{
		DB:     db,
		Ctx:    ctx,
		Tenant: tid,
		Cleanup: func() {
			// Drop schema on cleanup (optional, container cleanup handles this too)
			_ = db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schemaName))
		},
	}
}

// CreateTable executes DDL to create a table in the current tenant schema.
// The ddl parameter should be a CREATE TABLE statement without schema qualification.
// The table will be created in the tenant schema set by SetupTenantSchema.
//
// Usage:
//
//	tc := testdb.SetupTenantSchema(t, db, "test_tenant")
//	testdb.CreateTable(t, tc.DB, tc.Tenant, `
//	    CREATE TABLE IF NOT EXISTS %s.liens (
//	        id UUID PRIMARY KEY,
//	        account_id UUID NOT NULL,
//	        ...
//	    )
//	`)
//
// Note: The ddl must contain exactly one %s placeholder for the schema name.
func CreateTable(t *testing.T, db *gorm.DB, tid tenant.TenantID, ddl string) {
	t.Helper()

	schemaName := tid.SchemaName()
	sql := fmt.Sprintf(ddl, schemaName)
	err := db.Exec(sql).Error
	if err != nil {
		t.Fatalf("Failed to create table in schema %s: %v\nDDL: %s", schemaName, err, sql)
	}
}

// LienTableDDL is the standard DDL for creating the liens table in tests.
// Use with CreateTable:
//
//	testdb.CreateTable(t, tc.DB, tc.Tenant, testdb.LienTableDDL)
const LienTableDDL = `CREATE TABLE IF NOT EXISTS %s.liens (
	id UUID PRIMARY KEY,
	account_id UUID NOT NULL,
	amount_cents BIGINT NOT NULL,
	currency VARCHAR(3) NOT NULL,
	status VARCHAR(20) NOT NULL,
	payment_order_reference VARCHAR(255) NOT NULL UNIQUE,
	termination_reason TEXT,
	expires_at TIMESTAMP WITH TIME ZONE,
	created_at TIMESTAMP WITH TIME ZONE NOT NULL,
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
	version INT NOT NULL DEFAULT 1
)`

// AccountTableDDL is the standard DDL for creating the accounts table in tests.
// Use with CreateTable:
//
//	testdb.CreateTable(t, tc.DB, tc.Tenant, testdb.AccountTableDDL)
const AccountTableDDL = `CREATE TABLE IF NOT EXISTS %s.accounts (
	id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	account_id VARCHAR(100) NOT NULL UNIQUE,
	account_identification VARCHAR(34) NOT NULL UNIQUE,
	account_type VARCHAR(50) NOT NULL DEFAULT 'current',
	currency CHAR(3) NOT NULL DEFAULT 'GBP',
	status VARCHAR(20) NOT NULL DEFAULT 'active',
	party_id UUID NOT NULL,
	balance BIGINT NOT NULL DEFAULT 0,
	available_balance BIGINT NOT NULL DEFAULT 0,
	overdraft_limit BIGINT NOT NULL DEFAULT 0,
	overdraft_rate NUMERIC(5,4) NOT NULL DEFAULT 0,
	balance_updated_at TIMESTAMP WITH TIME ZONE,
	opened_at TIMESTAMP WITH TIME ZONE,
	closed_at TIMESTAMP WITH TIME ZONE,
	freeze_reason VARCHAR(1000),
	status_history JSONB NOT NULL DEFAULT '[]'::jsonb,
	version BIGINT NOT NULL DEFAULT 1,
	created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
	created_by VARCHAR(100) NOT NULL DEFAULT 'test',
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
	updated_by VARCHAR(100) NOT NULL DEFAULT 'test',
	deleted_at TIMESTAMP WITH TIME ZONE
)`
