package migrations

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAdaptCockroachDDLForPostgres_NoChanges(t *testing.T) {
	input := `CREATE TABLE foo (id UUID PRIMARY KEY);`
	assert.Equal(t, input, adaptCockroachDDLForPostgres(input))
}

func TestAdaptCockroachDDLForPostgres_ManifestVersionUniqueConstraint(t *testing.T) {
	input := `DROP INDEX IF EXISTS uq_manifest_version_version CASCADE;`
	result := adaptCockroachDDLForPostgres(input)
	assert.Contains(t, result, "ALTER TABLE manifest_version DROP CONSTRAINT IF EXISTS uq_manifest_version_version")
	assert.NotContains(t, result, "DROP INDEX")
}

func TestAdaptCockroachDDLForPostgres_SagaDefinitionUniqueConstraint(t *testing.T) {
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
	assert.Contains(t, result, "ADD CONSTRAINT chk_status CHECK")
}

func TestAdaptCockroachDDLForPostgres_AddConstraintIfNotExists(t *testing.T) {
	input := `ALTER TABLE public.my_table ADD CONSTRAINT IF NOT EXISTS chk_val CHECK (val > 0);`
	result := adaptCockroachDDLForPostgres(input)
	assert.Contains(t, result, "DO $compat$ BEGIN")
	assert.NotContains(t, result, "IF NOT EXISTS")
	assert.Contains(t, result, "ADD CONSTRAINT chk_val CHECK")
}

func TestAdaptCockroachDDLForPostgres_MultiStatementMigration(t *testing.T) {
	input := `UPDATE manifest_version SET version_new = version::TEXT WHERE version_new IS NULL;
ALTER TABLE manifest_version ALTER COLUMN version_new SET NOT NULL;
DROP INDEX IF EXISTS uq_manifest_version_version CASCADE;
DROP INDEX IF EXISTS idx_manifest_version_version;
ALTER TABLE manifest_version DROP COLUMN version;`

	result := adaptCockroachDDLForPostgres(input)

	// The DROP INDEX CASCADE for the unique constraint should be rewritten
	assert.Contains(t, result, "ALTER TABLE manifest_version DROP CONSTRAINT IF EXISTS uq_manifest_version_version")
	// The regular DROP INDEX (non-unique) should be left unchanged
	assert.Contains(t, result, "DROP INDEX IF EXISTS idx_manifest_version_version")
	// Other statements should be untouched
	assert.Contains(t, result, "UPDATE manifest_version SET version_new")
	assert.Contains(t, result, "ALTER TABLE manifest_version DROP COLUMN version")
}
