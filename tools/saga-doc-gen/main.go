package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

const (
	defaultSchemaDir = "shared/pkg/saga/schema"
	defaultDocsDir   = "docs"
)

// ErrNoHandlers indicates no handlers were found in the schema directory.
var ErrNoHandlers = errors.New("no handlers found in schema directory")

func main() {
	var (
		schemaDir  string
		outputDir  string
		markdown   bool
		jsonSchema bool
	)

	flag.StringVar(&schemaDir, "schema-dir", defaultSchemaDir, "Directory containing handler schema YAML files")
	flag.StringVar(&outputDir, "output-dir", defaultDocsDir, "Output directory for generated documentation")
	flag.BoolVar(&markdown, "markdown", true, "Generate Markdown service catalog")
	flag.BoolVar(&jsonSchema, "json-schema", true, "Generate JSON Schema document")
	flag.Parse()

	if err := run(schemaDir, outputDir, markdown, jsonSchema); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(schemaDir, outputDir string, generateMarkdown, generateJSONSchema bool) error {
	// Load all handler schemas
	registry := schema.NewRegistry()
	if err := registry.LoadFromDirectory(schemaDir); err != nil {
		return fmt.Errorf("failed to load schemas from %s: %w", schemaDir, err)
	}

	handlers := registry.ListHandlers()
	if len(handlers) == 0 {
		return fmt.Errorf("%w: %s", ErrNoHandlers, schemaDir)
	}

	fmt.Printf("Loaded %d handlers from %s\n", len(handlers), schemaDir)

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	// Generate Markdown service catalog
	if generateMarkdown {
		mdPath := filepath.Join(outputDir, "saga-service-catalog.md")
		if err := generateMarkdownCatalog(registry, mdPath); err != nil {
			return fmt.Errorf("failed to generate Markdown: %w", err)
		}
		fmt.Printf("✓ Generated Markdown service catalog: %s\n", mdPath)
	}

	// Generate JSON Schema document
	if generateJSONSchema {
		jsonPath := filepath.Join(outputDir, "saga-handlers.schema.json")
		if err := generateJSONSchemaDocument(registry, jsonPath); err != nil {
			return fmt.Errorf("failed to generate JSON Schema: %w", err)
		}
		fmt.Printf("✓ Generated JSON Schema document: %s\n", jsonPath)
	}

	return nil
}

func generateMarkdownCatalog(registry *schema.Registry, outputPath string) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", outputPath, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close %s: %v\n", outputPath, closeErr)
		}
	}()

	if err := GenerateMarkdown(registry, file); err != nil {
		return fmt.Errorf("failed to generate markdown: %w", err)
	}

	return nil
}

func generateJSONSchemaDocument(registry *schema.Registry, outputPath string) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", outputPath, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close %s: %v\n", outputPath, closeErr)
		}
	}()

	if err := GenerateJSONSchema(registry, file); err != nil {
		return fmt.Errorf("failed to generate JSON schema: %w", err)
	}

	return nil
}
