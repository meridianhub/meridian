// Package main implements the saga documentation generator tool.
// It generates Markdown service catalogs and JSON Schema documents from handler YAML schemas.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/template"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

const (
	jsonTypeString  = "string"
	jsonTypeInteger = "integer"
	jsonTypeBoolean = "boolean"
	jsonTypeArray   = "array"
	jsonTypeObject  = "object"
)

var (
	// ErrTemplateParse indicates failure to parse the markdown template.
	ErrTemplateParse = errors.New("failed to parse markdown template")
	// ErrTemplateExecute indicates failure to execute the markdown template.
	ErrTemplateExecute = errors.New("failed to execute markdown template")
	// ErrJSONEncode indicates failure to encode JSON schema.
	ErrJSONEncode = errors.New("failed to encode JSON schema")
)

// MarkdownTemplateData represents the data passed to the Markdown template.
type MarkdownTemplateData struct {
	Handlers []HandlerTemplateData
}

// HandlerTemplateData represents a handler for template rendering.
type HandlerTemplateData struct {
	Name          string
	Description   string
	Params        []FieldTemplateData
	Returns       []FieldTemplateData
	Compensate    string
	HasCompensate bool
}

// FieldTemplateData represents a field for template rendering.
type FieldTemplateData struct {
	Name        string
	Type        string
	Required    bool
	Description string
}

// markdownTemplate is the template for generating the service catalog in Markdown format.
const markdownTemplate = `# Saga Service Catalog

This document provides a reference for all saga handlers available in the Meridian platform.

{{if not .Handlers}}
## No handlers registered

The schema registry is empty. Please ensure handler schemas are loaded.
{{else}}
{{range .Handlers}}
## {{.Name}}

{{.Description}}

### Parameters

{{if not .Params}}
_No parameters_
{{else}}
| Name | Type | Required | Description |
|------|------|----------|-------------|
{{range .Params}}| {{.Name}} | {{.Type}} | {{if .Required}}✓{{else}}-{{end}} | {{.Description}} |
{{end}}
{{end}}

### Returns

{{if not .Returns}}
_No return values_
{{else}}
| Name | Type | Description |
|------|------|-------------|
{{range .Returns}}| {{.Name}} | {{.Type}} | {{.Description}} |
{{end}}
{{end}}

{{if .HasCompensate}}
**Compensation Handler:** ` + "`{{.Compensate}}`" + `
{{end}}

### Example Usage

` + "```" + `starlark
result = {{.Name}}(
{{range $i, $p := .Params}}{{if $i}},
{{end}}    {{$p.Name}}=<value>{{end}}
)
` + "```" + `

---

{{end}}
{{end}}
`

// GenerateMarkdown generates a Markdown service catalog from the schema registry.
func GenerateMarkdown(registry *schema.Registry, writer io.Writer) error {
	tmpl, err := template.New("markdown").Parse(markdownTemplate)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTemplateParse, err)
	}

	data := buildMarkdownData(registry)

	if err := tmpl.Execute(writer, data); err != nil {
		return fmt.Errorf("%w: %w", ErrTemplateExecute, err)
	}

	return nil
}

// buildMarkdownData converts the registry into template data.
func buildMarkdownData(registry *schema.Registry) MarkdownTemplateData {
	handlerNames := registry.ListHandlers()
	handlers := make([]HandlerTemplateData, 0, len(handlerNames))

	for _, name := range handlerNames {
		handler, err := registry.GetHandler(name)
		if err != nil {
			continue // Should not happen for registered handlers
		}

		handlerData := HandlerTemplateData{
			Name:          name,
			Description:   handler.Description,
			Params:        buildFieldList(handler.Params),
			Returns:       buildFieldList(handler.Returns),
			Compensate:    handler.Compensate,
			HasCompensate: handler.Compensate != "",
		}

		handlers = append(handlers, handlerData)
	}

	return MarkdownTemplateData{Handlers: handlers}
}

// buildFieldList converts a map of field definitions to a sorted slice for template rendering.
func buildFieldList(fields map[string]*schema.FieldDef) []FieldTemplateData {
	if len(fields) == 0 {
		return nil
	}

	result := make([]FieldTemplateData, 0, len(fields))
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		field := fields[name]
		result = append(result, FieldTemplateData{
			Name:        name,
			Type:        formatFieldType(field),
			Required:    field.Required,
			Description: field.Description,
		})
	}

	return result
}

// formatFieldType formats a field type for display.
func formatFieldType(field *schema.FieldDef) string {
	switch field.Type {
	case schema.TypeString, schema.TypeInt32, schema.TypeInt64, schema.TypeUint32,
		schema.TypeBool, schema.TypeDecimal, schema.TypeUUID:
		return string(field.Type)
	case schema.TypeEnum:
		if len(field.Values) > 0 {
			return fmt.Sprintf("enum (%s)", strings.Join(field.Values, ", "))
		}
		return "enum"
	case schema.TypeArray:
		if field.ItemType != "" {
			return fmt.Sprintf("array[%s]", field.ItemType)
		}
		return "array"
	case schema.TypeMap:
		if field.KeyType != "" && field.ValueType != "" {
			return fmt.Sprintf("map[%s]%s", field.KeyType, field.ValueType)
		}
		return "map"
	default:
		return string(field.Type)
	}
}

// JSONSchemaOutput represents the top-level JSON Schema document.
type JSONSchemaOutput struct {
	Schema      string                       `json:"$schema"`
	Title       string                       `json:"title"`
	Description string                       `json:"description"`
	Definitions map[string]JSONSchemaHandler `json:"definitions"`
}

// JSONSchemaHandler represents a handler definition in JSON Schema format.
type JSONSchemaHandler struct {
	Type        string                     `json:"type"`
	Description string                     `json:"description"`
	Properties  map[string]JSONSchemaField `json:"properties"`
	Required    []string                   `json:"required,omitempty"`
}

// JSONSchemaField represents a field in JSON Schema format.
type JSONSchemaField struct {
	Type                 string                     `json:"type,omitempty"`
	Description          string                     `json:"description,omitempty"`
	Enum                 []string                   `json:"enum,omitempty"`
	Items                *JSONSchemaFieldType       `json:"items,omitempty"`
	AdditionalProperties *JSONSchemaFieldType       `json:"additionalProperties,omitempty"`
	Properties           map[string]JSONSchemaField `json:"properties,omitempty"`
	Required             []string                   `json:"required,omitempty"`
}

// JSONSchemaFieldType represents a simple type reference in JSON Schema.
type JSONSchemaFieldType struct {
	Type string `json:"type"`
}

// GenerateJSONSchema generates a JSON Schema document from the schema registry.
func GenerateJSONSchema(registry *schema.Registry, writer io.Writer) error {
	output := buildJSONSchema(registry)

	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		return fmt.Errorf("%w: %w", ErrJSONEncode, err)
	}

	return nil
}

// buildJSONSchema converts the registry into JSON Schema format.
func buildJSONSchema(registry *schema.Registry) JSONSchemaOutput {
	handlerNames := registry.ListHandlers()
	definitions := make(map[string]JSONSchemaHandler, len(handlerNames))

	for _, name := range handlerNames {
		handler, err := registry.GetHandler(name)
		if err != nil {
			continue // Should not happen for registered handlers
		}

		paramsRequired := collectRequiredFields(handler.Params)
		returnsRequired := collectRequiredFields(handler.Returns)

		definitions[name] = JSONSchemaHandler{
			Type:        "object",
			Description: handler.Description,
			Properties: map[string]JSONSchemaField{
				"params": {
					Type:       "object",
					Properties: convertFieldsToJSONSchema(handler.Params),
					Required:   paramsRequired,
				},
				"returns": {
					Type:       "object",
					Properties: convertFieldsToJSONSchema(handler.Returns),
					Required:   returnsRequired,
				},
			},
			Required: []string{"params", "returns"},
		}
	}

	return JSONSchemaOutput{
		Schema:      "http://json-schema.org/draft-07/schema#",
		Title:       "Saga Handler Schemas",
		Description: "JSON Schema definitions for all saga handlers in the Meridian platform",
		Definitions: definitions,
	}
}

// collectRequiredFields extracts the names of required fields.
func collectRequiredFields(fields map[string]*schema.FieldDef) []string {
	var required []string
	for name, field := range fields {
		if field.Required {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	return required
}

// convertFieldsToJSONSchema converts field definitions to JSON Schema format.
func convertFieldsToJSONSchema(fields map[string]*schema.FieldDef) map[string]JSONSchemaField {
	result := make(map[string]JSONSchemaField, len(fields))
	for name, field := range fields {
		result[name] = convertFieldToJSONSchema(field)
	}
	return result
}

// convertFieldToJSONSchema converts a single field to JSON Schema format.
func convertFieldToJSONSchema(field *schema.FieldDef) JSONSchemaField {
	jsonField := JSONSchemaField{
		Description: field.Description,
	}

	switch field.Type {
	case schema.TypeString, schema.TypeUUID:
		jsonField.Type = jsonTypeString
	case schema.TypeInt32, schema.TypeInt64, schema.TypeUint32:
		jsonField.Type = jsonTypeInteger
	case schema.TypeBool:
		jsonField.Type = jsonTypeBoolean
	case schema.TypeDecimal:
		jsonField.Type = jsonTypeString // Decimal is represented as string in JSON
	case schema.TypeEnum:
		jsonField.Type = jsonTypeString
		jsonField.Enum = field.Values
	case schema.TypeArray:
		jsonField.Type = jsonTypeArray
		if field.ItemType != "" {
			itemType := convertSimpleFieldType(field.ItemType)
			jsonField.Items = &JSONSchemaFieldType{Type: itemType}
		}
	case schema.TypeMap:
		jsonField.Type = jsonTypeObject
		if field.ValueType != "" {
			valueType := convertSimpleFieldType(field.ValueType)
			jsonField.AdditionalProperties = &JSONSchemaFieldType{Type: valueType}
		}
	}

	return jsonField
}

// convertSimpleFieldType converts a FieldType to a JSON Schema type string.
func convertSimpleFieldType(fieldType schema.FieldType) string {
	switch fieldType {
	case schema.TypeString, schema.TypeUUID:
		return jsonTypeString
	case schema.TypeInt32, schema.TypeInt64, schema.TypeUint32:
		return jsonTypeInteger
	case schema.TypeBool:
		return jsonTypeBoolean
	case schema.TypeDecimal:
		return jsonTypeString
	case schema.TypeEnum:
		return jsonTypeString
	case schema.TypeArray:
		return jsonTypeArray
	case schema.TypeMap:
		return jsonTypeObject
	default:
		return jsonTypeString
	}
}
