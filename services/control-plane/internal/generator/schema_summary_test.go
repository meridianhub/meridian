package generator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildManifestSchemaSummary_ContainsHeader(t *testing.T) {
	result := BuildManifestSchemaSummary()

	assert.Contains(t, result, "## Manifest Schema Summary")
	assert.Contains(t, result, "Manifest is the atomic configuration snapshot")
}

func TestBuildManifestSchemaSummary_ContainsTopLevelFields(t *testing.T) {
	result := BuildManifestSchemaSummary()

	// Top-level Manifest fields
	assert.Contains(t, result, "`version`")
	assert.Contains(t, result, "`metadata`")
	assert.Contains(t, result, "`instruments`")
	assert.Contains(t, result, "`account_types`")
	assert.Contains(t, result, "`sagas`")
	assert.Contains(t, result, "`valuation_rules`")
}

func TestBuildManifestSchemaSummary_RequiredFieldsMarked(t *testing.T) {
	result := BuildManifestSchemaSummary()

	// version and metadata are required in the proto
	assert.Contains(t, result, "`version`: string *(required)*")
	assert.Contains(t, result, "`metadata`: ManifestMetadata *(required)*")
}

func TestBuildManifestSchemaSummary_NestedFieldsExpanded(t *testing.T) {
	result := BuildManifestSchemaSummary()

	// ManifestMetadata nested fields should be visible (depth 1)
	assert.Contains(t, result, "`name`")
	assert.Contains(t, result, "`industry`")
	assert.Contains(t, result, "`description`")
}

func TestBuildManifestSchemaSummary_RepeatedFieldsMarked(t *testing.T) {
	result := BuildManifestSchemaSummary()

	assert.Contains(t, result, "repeated InstrumentDefinition")
	assert.Contains(t, result, "repeated AccountTypeDefinition")
	assert.Contains(t, result, "repeated SagaDefinition")
}

func TestBuildManifestSchemaSummary_EnumsExpanded(t *testing.T) {
	result := BuildManifestSchemaSummary()

	// InstrumentType enum values should appear
	assert.Contains(t, result, "INSTRUMENT_TYPE_FIAT")
}

func TestBuildManifestSchemaSummary_NonEmpty(t *testing.T) {
	result := BuildManifestSchemaSummary()

	// Should produce a non-trivial output
	assert.Greater(t, len(result), 500, "summary should be substantial")
}
