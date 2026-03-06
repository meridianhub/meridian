package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateMarkdown_EmptyRegistry(t *testing.T) {
	registry := schema.NewRegistry()
	var buf bytes.Buffer

	err := GenerateMarkdown(registry, &buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "# Saga Service Catalog")
	assert.Contains(t, output, "No handlers registered")
}

func TestGenerateMarkdown_SingleHandler(t *testing.T) {
	registry := schema.NewRegistry()
	schemaYAML := `
service: test_service
version: "1.0"
handlers:
  test.handler:
    description: "Test handler for documentation"
    params:
      input_param:
        type: string
        required: true
        description: "Input parameter description"
    returns:
      output_value:
        type: int32
        description: "Output value description"
    compensate: test.compensate_handler
`
	err := registry.LoadFromYAML([]byte(schemaYAML))
	require.NoError(t, err)

	var buf bytes.Buffer
	err = GenerateMarkdown(registry, &buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "# Saga Service Catalog")
	assert.Contains(t, output, "## test.handler")
	assert.Contains(t, output, "Test handler for documentation")
	assert.Contains(t, output, "input_param")
	assert.Contains(t, output, "string")
	assert.Contains(t, output, "output_value")
	assert.Contains(t, output, "int32")
	assert.Contains(t, output, "**Compensation Handler:** `test.compensate_handler`")
}

func TestGenerateMarkdown_ComplexTypes(t *testing.T) {
	registry := schema.NewRegistry()
	schemaYAML := `
service: complex_service
version: "1.0"
handlers:
  complex.handler:
    description: "Handler with complex types"
    compensation_strategy: none
    params:
      status:
        type: enum
        values: [ACTIVE, INACTIVE, PENDING]
        required: true
        description: "Status enum"
      items:
        type: array
        item_type: string
        required: false
        description: "Array of items"
      metadata:
        type: map
        key_type: string
        value_type: string
        required: false
        description: "Metadata map"
    returns:
      result:
        type: bool
        description: "Success status"
`
	err := registry.LoadFromYAML([]byte(schemaYAML))
	require.NoError(t, err)

	var buf bytes.Buffer
	err = GenerateMarkdown(registry, &buf)
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "enum")
	assert.Contains(t, output, "ACTIVE, INACTIVE, PENDING")
	assert.Contains(t, output, "array[string]")
	assert.Contains(t, output, "map[string]string")
}

func TestGenerateJSONSchema_EmptyRegistry(t *testing.T) {
	registry := schema.NewRegistry()
	var buf bytes.Buffer

	err := GenerateJSONSchema(registry, &buf)
	require.NoError(t, err)

	var jsonSchema map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &jsonSchema)
	require.NoError(t, err)

	assert.Equal(t, "http://json-schema.org/draft-07/schema#", jsonSchema["$schema"])
	assert.Equal(t, "Saga Handler Schemas", jsonSchema["title"])
	assert.NotNil(t, jsonSchema["definitions"])
}

func TestGenerateJSONSchema_SingleHandler(t *testing.T) {
	registry := schema.NewRegistry()
	schemaYAML := `
service: test_service
version: "1.0"
handlers:
  test.handler:
    description: "Test handler"
    compensation_strategy: none
    params:
      name:
        type: string
        required: true
        description: "Name parameter"
      age:
        type: int32
        required: false
        description: "Age parameter"
    returns:
      result:
        type: bool
        description: "Success flag"
`
	err := registry.LoadFromYAML([]byte(schemaYAML))
	require.NoError(t, err)

	var buf bytes.Buffer
	err = GenerateJSONSchema(registry, &buf)
	require.NoError(t, err)

	var jsonSchema map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &jsonSchema)
	require.NoError(t, err)

	defs := jsonSchema["definitions"].(map[string]interface{})
	assert.Contains(t, defs, "test.handler")

	handlerDef := defs["test.handler"].(map[string]interface{})
	assert.Equal(t, "Test handler", handlerDef["description"])

	params := handlerDef["properties"].(map[string]interface{})["params"].(map[string]interface{})
	paramProps := params["properties"].(map[string]interface{})
	assert.Contains(t, paramProps, "name")
	assert.Contains(t, paramProps, "age")

	nameParam := paramProps["name"].(map[string]interface{})
	assert.Equal(t, "string", nameParam["type"])

	required := params["required"].([]interface{})
	assert.Contains(t, required, "name")
	assert.NotContains(t, required, "age")
}

func TestGenerateJSONSchema_EnumType(t *testing.T) {
	registry := schema.NewRegistry()
	schemaYAML := `
service: enum_service
version: "1.0"
handlers:
  enum.handler:
    description: "Handler with enum"
    compensation_strategy: none
    params:
      status:
        type: enum
        values: [PENDING, ACTIVE, CLOSED]
        required: true
        description: "Status value"
    returns:
      result:
        type: string
        description: "Result"
`
	err := registry.LoadFromYAML([]byte(schemaYAML))
	require.NoError(t, err)

	var buf bytes.Buffer
	err = GenerateJSONSchema(registry, &buf)
	require.NoError(t, err)

	var jsonSchema map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &jsonSchema)
	require.NoError(t, err)

	defs := jsonSchema["definitions"].(map[string]interface{})
	handlerDef := defs["enum.handler"].(map[string]interface{})
	params := handlerDef["properties"].(map[string]interface{})["params"].(map[string]interface{})
	paramProps := params["properties"].(map[string]interface{})
	statusParam := paramProps["status"].(map[string]interface{})

	assert.Equal(t, "string", statusParam["type"])
	enumValues := statusParam["enum"].([]interface{})
	assert.Contains(t, enumValues, "PENDING")
	assert.Contains(t, enumValues, "ACTIVE")
	assert.Contains(t, enumValues, "CLOSED")
}

func TestGenerateJSONSchema_ArrayAndMapTypes(t *testing.T) {
	registry := schema.NewRegistry()
	schemaYAML := `
service: collection_service
version: "1.0"
handlers:
  collection.handler:
    description: "Handler with collections"
    compensation_strategy: none
    params:
      tags:
        type: array
        item_type: string
        required: true
        description: "Array of tags"
      metadata:
        type: map
        key_type: string
        value_type: int32
        required: true
        description: "Metadata map"
    returns:
      result:
        type: bool
        description: "Success"
`
	err := registry.LoadFromYAML([]byte(schemaYAML))
	require.NoError(t, err)

	var buf bytes.Buffer
	err = GenerateJSONSchema(registry, &buf)
	require.NoError(t, err)

	var jsonSchema map[string]interface{}
	err = json.Unmarshal(buf.Bytes(), &jsonSchema)
	require.NoError(t, err)

	defs := jsonSchema["definitions"].(map[string]interface{})
	handlerDef := defs["collection.handler"].(map[string]interface{})
	params := handlerDef["properties"].(map[string]interface{})["params"].(map[string]interface{})
	paramProps := params["properties"].(map[string]interface{})

	tagsParam := paramProps["tags"].(map[string]interface{})
	assert.Equal(t, "array", tagsParam["type"])
	tagsItems := tagsParam["items"].(map[string]interface{})
	assert.Equal(t, "string", tagsItems["type"])

	metadataParam := paramProps["metadata"].(map[string]interface{})
	assert.Equal(t, "object", metadataParam["type"])
	additionalProps := metadataParam["additionalProperties"].(map[string]interface{})
	assert.Equal(t, "integer", additionalProps["type"])
}

func TestEndToEnd_GenerateBothFormats(t *testing.T) {
	// Create a temporary directory for test output
	tmpDir := t.TempDir()

	// Load the real handlers.yaml schema
	registry := schema.NewRegistry()
	schemaPath := filepath.Join("..", "..", "shared", "pkg", "saga", "schema", "handlers.yaml")

	// Check if file exists before loading (might not exist in test environment)
	if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
		t.Skip("handlers.yaml not found, skipping end-to-end test")
	}

	err := registry.LoadFromFile(schemaPath)
	require.NoError(t, err)

	// Generate Markdown
	mdPath := filepath.Join(tmpDir, "saga-service-catalog.md")
	mdFile, err := os.Create(mdPath)
	require.NoError(t, err)
	defer mdFile.Close()

	err = GenerateMarkdown(registry, mdFile)
	require.NoError(t, err)

	// Verify Markdown file was created and has content
	mdStat, err := os.Stat(mdPath)
	require.NoError(t, err)
	assert.Greater(t, mdStat.Size(), int64(0))

	// Generate JSON Schema
	jsonPath := filepath.Join(tmpDir, "saga-handlers.schema.json")
	jsonFile, err := os.Create(jsonPath)
	require.NoError(t, err)
	defer jsonFile.Close()

	err = GenerateJSONSchema(registry, jsonFile)
	require.NoError(t, err)

	// Verify JSON Schema file was created and is valid JSON
	jsonStat, err := os.Stat(jsonPath)
	require.NoError(t, err)
	assert.Greater(t, jsonStat.Size(), int64(0))

	// Validate JSON Schema structure
	jsonData, err := os.ReadFile(jsonPath)
	require.NoError(t, err)

	var jsonSchema map[string]interface{}
	err = json.Unmarshal(jsonData, &jsonSchema)
	require.NoError(t, err)

	assert.Equal(t, "http://json-schema.org/draft-07/schema#", jsonSchema["$schema"])
	assert.Contains(t, jsonSchema, "definitions")
}
