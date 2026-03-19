package testdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gorm.io/gorm/logger"
)

// --- adaptCockroachDDLForPostgres tests ---

func TestAdaptCockroachDDLForPostgres_NoChanges(t *testing.T) {
	input := `CREATE TABLE foo (id UUID PRIMARY KEY);`
	result := adaptCockroachDDLForPostgres(input)
	assert.Equal(t, input, result)
}

func TestAdaptCockroachDDLForPostgres_DropIndexCascade(t *testing.T) {
	input := `DROP INDEX IF EXISTS "public"."uq_platform_saga_definition_name" CASCADE;`
	result := adaptCockroachDDLForPostgres(input)
	assert.Contains(t, result, `ALTER TABLE "public"."platform_saga_definition" DROP CONSTRAINT IF EXISTS "uq_platform_saga_definition_name"`)
	assert.NotContains(t, result, "DROP INDEX")
}

func TestAdaptCockroachDDLForPostgres_AddConstraintCheck(t *testing.T) {
	input := `ALTER TABLE public.my_table ADD CONSTRAINT chk_status CHECK (status IN ('a','b'));`
	result := adaptCockroachDDLForPostgres(input)
	assert.Contains(t, result, "DO $compat$ BEGIN")
	assert.Contains(t, result, "EXCEPTION WHEN duplicate_object THEN NULL")
	assert.Contains(t, result, "END $compat$;")
	assert.Contains(t, result, "ADD CONSTRAINT chk_status CHECK")
}

func TestAdaptCockroachDDLForPostgres_AddConstraintIfNotExists(t *testing.T) {
	// CockroachDB extension form: ADD CONSTRAINT IF NOT EXISTS
	input := `ALTER TABLE public.my_table ADD CONSTRAINT IF NOT EXISTS chk_val CHECK (val > 0);`
	result := adaptCockroachDDLForPostgres(input)
	assert.Contains(t, result, "DO $compat$ BEGIN")
	// "IF NOT EXISTS" should be stripped
	assert.NotContains(t, result, "IF NOT EXISTS")
	assert.Contains(t, result, "ADD CONSTRAINT chk_val CHECK")
}

func TestAdaptCockroachDDLForPostgres_BothTransformations(t *testing.T) {
	input := `DROP INDEX IF EXISTS "public"."uq_platform_saga_definition_name" CASCADE;
ALTER TABLE public.my_table ADD CONSTRAINT chk_foo CHECK (foo > 0);`
	result := adaptCockroachDDLForPostgres(input)
	assert.Contains(t, result, "DROP CONSTRAINT IF EXISTS")
	assert.Contains(t, result, "DO $compat$ BEGIN")
}

func TestAdaptCockroachDDLForPostgres_MultipleConstraints(t *testing.T) {
	input := `ALTER TABLE public.t1 ADD CONSTRAINT chk_a CHECK (a > 0);
ALTER TABLE public.t2 ADD CONSTRAINT chk_b CHECK (b > 0);`
	result := adaptCockroachDDLForPostgres(input)
	// Both should be wrapped
	assert.Equal(t, 2, countOccurrences(result, "DO $compat$ BEGIN"))
}

func countOccurrences(s, sub string) int {
	count := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			count++
		}
	}
	return count
}

// --- extractSchemasFromModels tests ---

type modelWithSchema struct{}

func (m *modelWithSchema) TableName() string { return "myschema.my_table" }

type modelWithoutSchema struct{}

func (m *modelWithoutSchema) TableName() string { return "my_table" }

type modelNoTableName struct {
	ID int
}

func TestExtractSchemasFromModels_WithSchema(t *testing.T) {
	models := []interface{}{&modelWithSchema{}}
	schemas := extractSchemasFromModels(models)
	assert.True(t, schemas["myschema"])
	assert.Len(t, schemas, 1)
}

func TestExtractSchemasFromModels_WithoutSchema(t *testing.T) {
	models := []interface{}{&modelWithoutSchema{}}
	schemas := extractSchemasFromModels(models)
	assert.Len(t, schemas, 0)
}

func TestExtractSchemasFromModels_NoTableNameMethod(t *testing.T) {
	models := []interface{}{&modelNoTableName{}}
	schemas := extractSchemasFromModels(models)
	assert.Len(t, schemas, 0)
}

func TestExtractSchemasFromModels_Mixed(t *testing.T) {
	models := []interface{}{&modelWithSchema{}, &modelWithoutSchema{}, &modelNoTableName{}}
	schemas := extractSchemasFromModels(models)
	assert.True(t, schemas["myschema"])
	assert.Len(t, schemas, 1)
}

func TestExtractSchemasFromModels_Empty(t *testing.T) {
	schemas := extractSchemasFromModels(nil)
	assert.Len(t, schemas, 0)
}

func TestExtractSchemasFromModels_DuplicateSchemas(t *testing.T) {
	models := []interface{}{&modelWithSchema{}, &modelWithSchema{}}
	schemas := extractSchemasFromModels(models)
	assert.Len(t, schemas, 1)
}

// --- Option function tests ---

func TestWithMigrations(t *testing.T) {
	cfg := &poolConfig{}
	opt := WithMigrations("reference-data")
	opt(cfg)
	assert.Equal(t, "reference-data", cfg.migrations)
}

func TestWithLogLevel(t *testing.T) {
	cfg := &postgresConfig{}
	opt := WithLogLevel(logger.Info)
	opt(cfg)
	assert.Equal(t, logger.Info, cfg.logLevel)
}

func TestWithModels(t *testing.T) {
	cfg := &setupConfig{}
	m := &modelWithSchema{}
	opt := WithModels(m)
	opt(cfg)
	assert.Len(t, cfg.models, 1)

	// Append more
	opt2 := WithModels(&modelNoTableName{})
	opt2(cfg)
	assert.Len(t, cfg.models, 2)
}

func TestWithTenant(t *testing.T) {
	cfg := &setupConfig{}
	opt := WithTenant("acme_bank")
	opt(cfg)
	assert.Equal(t, "acme_bank", cfg.tenantID)
}

func TestWithAuditTables(t *testing.T) {
	cfg := &setupConfig{}
	opt := WithAuditTables()
	opt(cfg)
	assert.True(t, cfg.auditTables)
}

func TestWithSetupLogLevel(t *testing.T) {
	cfg := &setupConfig{}
	opt := WithSetupLogLevel(logger.Warn)
	opt(cfg)
	assert.Equal(t, logger.Warn, cfg.logLevel)
}
