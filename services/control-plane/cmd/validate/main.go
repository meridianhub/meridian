// Package main provides a CLI tool for validating Meridian manifest files
// against the protobuf schema, CEL expression type-checking, and Starlark
// compilation. Used by CI pipeline and local development.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"google.golang.org/protobuf/encoding/protojson"

	currentaccountclient "github.com/meridianhub/meridian/services/current-account/client"
	financialaccountingclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	financialgatewayclient "github.com/meridianhub/meridian/services/financial-gateway/client"
	internalaccountclient "github.com/meridianhub/meridian/services/internal-account/client"
	marketinformationclient "github.com/meridianhub/meridian/services/market-information/client"
	operationalgatewayclient "github.com/meridianhub/meridian/services/operational-gateway/client"
	partyclient "github.com/meridianhub/meridian/services/party/client"
	positionkeepingclient "github.com/meridianhub/meridian/services/position-keeping/client"
	reconciliationclient "github.com/meridianhub/meridian/services/reconciliation/client"
	referencedataclient "github.com/meridianhub/meridian/services/reference-data/client"
)

func main() {
	manifestGlob := flag.String("manifest", "", "glob pattern for manifest files (e.g., examples/manifests/*.json)")
	jsonOutput := flag.Bool("json", false, "output results as JSON")
	flag.Parse()

	if *manifestGlob == "" {
		fmt.Fprintf(os.Stderr, "Usage: validate -manifest=<glob>\n")
		fmt.Fprintf(os.Stderr, "  Validates manifest files against the Meridian schema.\n\n")
		fmt.Fprintf(os.Stderr, "Example:\n")
		fmt.Fprintf(os.Stderr, "  validate -manifest='examples/manifests/*.json'\n")
		os.Exit(1)
	}

	files, err := filepath.Glob(*manifestGlob)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid glob pattern: %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no files matched pattern: %s\n", *manifestGlob)
		os.Exit(1)
	}

	// Build handler registry from all service client registrations and derive schema.
	derivedSchema, err := buildDerivedSchema()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to derive handler schema: %v\n", err)
		os.Exit(1)
	}

	v, err := validator.New(validator.WithDerivedSchema(derivedSchema))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create validator: %v\n", err)
		os.Exit(1)
	}

	hasFailures := false
	for _, file := range files {
		result, validErr := validateFile(v, file)
		if validErr != nil {
			if *jsonOutput {
				out, _ := json.MarshalIndent(map[string]any{
					"file":  file,
					"error": validErr.Error(),
				}, "", "  ")
				fmt.Println(string(out))
			} else {
				fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", file, validErr)
			}
			hasFailures = true
			continue
		}

		if *jsonOutput {
			out, _ := json.MarshalIndent(map[string]any{
				"file":   file,
				"result": result,
			}, "", "  ")
			fmt.Println(string(out))
		} else {
			printResult(file, result)
		}

		if !result.Valid {
			hasFailures = true
		}
	}

	if hasFailures {
		os.Exit(1)
	}
}

// buildDerivedSchema registers all service handlers and derives the schema from proto metadata.
func buildDerivedSchema() (*schema.Schema, error) {
	registry := saga.NewHandlerRegistry()

	registrations := []struct {
		name string
		fn   func() error
	}{
		{"current-account", func() error {
			return currentaccountclient.RegisterStarlarkHandlers(registry, &currentaccountclient.Client{})
		}},
		{"financial-accounting", func() error {
			return financialaccountingclient.RegisterStarlarkHandlers(registry, &financialaccountingclient.Client{})
		}},
		{"financial-gateway", func() error {
			return financialgatewayclient.RegisterStarlarkHandlers(registry, &financialgatewayclient.Client{})
		}},
		{"internal-account", func() error {
			return internalaccountclient.RegisterStarlarkHandlers(registry, &internalaccountclient.Client{})
		}},
		{"market-information", func() error {
			return marketinformationclient.RegisterStarlarkHandlers(registry, &marketinformationclient.Client{})
		}},
		{"operational-gateway", func() error {
			return operationalgatewayclient.RegisterStarlarkHandlers(registry, &operationalgatewayclient.Client{})
		}},
		{"party", func() error {
			return partyclient.RegisterStarlarkHandlers(registry, &partyclient.Client{})
		}},
		{"position-keeping", func() error {
			return positionkeepingclient.RegisterStarlarkHandlers(registry, &positionkeepingclient.Client{})
		}},
		{"reconciliation", func() error {
			return reconciliationclient.RegisterStarlarkHandlers(registry, &reconciliationclient.Client{})
		}},
		{"reference-data", func() error {
			return referencedataclient.RegisterStarlarkHandlers(registry, &referencedataclient.Client{})
		}},
	}

	for _, r := range registrations {
		if err := r.fn(); err != nil {
			return nil, fmt.Errorf("register %s handlers: %w", r.name, err)
		}
	}

	return schema.DeriveSchema(registry)
}

func validateFile(v *validator.ManifestValidator, path string) (*validator.ValidationResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	manifest := &controlplanev1.Manifest{}
	if err := protojson.Unmarshal(data, manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest JSON: %w", err)
	}

	result := v.Validate(manifest, nil)
	return result, nil
}

func printResult(file string, result *validator.ValidationResult) {
	if result.Valid {
		fmt.Printf("PASS %s\n", file)
	} else {
		fmt.Printf("FAIL %s\n", file)
	}

	for _, e := range result.Errors {
		fmt.Printf("  ERROR [%s] %s: %s\n", e.Code, e.Path, e.Message)
		if e.Suggestion != "" {
			fmt.Printf("    suggestion: %s\n", e.Suggestion)
		}
	}

	for _, w := range result.Warnings {
		fmt.Printf("  WARN  [%s] %s: %s\n", w.Code, w.Path, w.Message)
	}
}
