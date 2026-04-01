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
