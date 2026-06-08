package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/infra"
)

// Schema command flags.
var (
	schemaDataset string
	schemaFormat  string
)

// schemaCmd represents the schema command.
var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Query expected CSV format from DataSetService",
	Long: `Query the expected CSV format for a dataset from the DataSetService.

The schema command retrieves the dataset definition and displays:

  - Required CSV columns
  - Attribute schema (from dataset's attribute_schema)
  - Resolution key expression (for observation grouping)
  - Validation expression (CEL expression for value validation)

This helps ensure your CSV file matches the expected format before import.

Output Formats:
  - text (default): Human-readable format
  - json: JSON output for programmatic use

Examples:
  # Display expected CSV format for a dataset
  market-data-tool schema --tenant=acme_corp --dataset=USD_EUR_FX

  # Output as JSON
  market-data-tool schema --tenant=acme_corp --dataset=USD_EUR_FX --format=json`,
	RunE:          runSchemaWrapper,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(schemaCmd)

	schemaCmd.Flags().StringVar(&schemaDataset, "dataset", "",
		"Dataset code to query (required)")
	schemaCmd.Flags().StringVar(&schemaFormat, "format", "text",
		"Output format: text or json (default: text)")

	_ = schemaCmd.MarkFlagRequired("dataset")
}

// runSchemaWrapper handles exit codes for the schema command.
func runSchemaWrapper(cmd *cobra.Command, args []string) error {
	err := runSchema(cmd, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return nil
}

func runSchema(_ *cobra.Command, _ []string) error {
	// Validate required flags
	if err := validateCommonFlags(); err != nil {
		return err
	}

	if schemaDataset == "" {
		return ErrDatasetRequired
	}

	// Set up graceful shutdown context
	ctx, cancel := ShutdownContext()
	defer cancel()

	logger := slog.Default()
	logger.Debug("fetching dataset schema",
		"tenant", tenantID,
		"dataset", schemaDataset,
		"grpc_endpoint", grpcEndpoint,
	)

	// Create gRPC client
	grpcClient, cleanup, err := infra.NewGRPCClient(ctx, infra.GRPCClientConfig{
		Endpoint: grpcEndpoint,
		TenantID: tenantID,
	})
	if err != nil {
		return fmt.Errorf("failed to create gRPC client: %w", err)
	}
	defer cleanup()

	// Fetch dataset definition
	dataset, err := grpcClient.GetDataSet(ctx, schemaDataset, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch dataset definition: %w", err)
	}

	// Output schema
	switch schemaFormat {
	case "json":
		return printSchemaJSON(dataset)
	default:
		printSchemaText(dataset)
		return nil
	}
}

// schemaOutput represents the JSON output format for schema command.
type schemaOutput struct {
	DatasetCode             string          `json:"dataset_code"`
	Version                 int32           `json:"version"`
	Status                  string          `json:"status"`
	Category                string          `json:"category"`
	Unit                    string          `json:"unit"`
	DisplayName             string          `json:"display_name,omitempty"`
	Description             string          `json:"description,omitempty"`
	RequiredColumns         []string        `json:"required_columns"`
	OptionalColumns         []string        `json:"optional_columns"`
	AttributeSchema         json.RawMessage `json:"attribute_schema,omitempty"`
	ResolutionKeyExpression string          `json:"resolution_key_expression,omitempty"`
	ValidationExpression    string          `json:"validation_expression,omitempty"`
	ErrorMessageExpression  string          `json:"error_message_expression,omitempty"`
}

// printSchemaJSON outputs the schema in JSON format.
func printSchemaJSON(dataset *infra.DataSetDefinition) error {
	var attrSchema json.RawMessage
	if dataset.AttributeSchemaJSON != "" {
		attrSchema = json.RawMessage(dataset.AttributeSchemaJSON)
	}

	output := schemaOutput{
		DatasetCode:             dataset.Code,
		Version:                 dataset.Version,
		Status:                  dataset.Status,
		Category:                dataset.Category,
		Unit:                    dataset.Unit,
		DisplayName:             dataset.DisplayName,
		Description:             dataset.Description,
		RequiredColumns:         []string{"observed_at", "quality_level", "value"},
		OptionalColumns:         []string{"valid_from", "valid_to"},
		AttributeSchema:         attrSchema,
		ResolutionKeyExpression: dataset.ResolutionKeyExpression,
		ValidationExpression:    dataset.ValidationExpression,
		ErrorMessageExpression:  dataset.ErrorMessageExpression,
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

// printSchemaText outputs the schema in human-readable format.
func printSchemaText(dataset *infra.DataSetDefinition) {
	fmt.Println()
	fmt.Println("+---------------------------------------------------------------------------+")
	fmt.Println("|                         DATASET SCHEMA                                    |")
	fmt.Println("+---------------------------------------------------------------------------+")
	fmt.Println()

	fmt.Printf("  Dataset:           %s (version %d)\n", dataset.Code, dataset.Version)
	fmt.Printf("  Status:            %s\n", dataset.Status)
	fmt.Printf("  Category:          %s\n", dataset.Category)
	fmt.Printf("  Unit:              %s\n", dataset.Unit)

	if dataset.DisplayName != "" {
		fmt.Printf("  Display Name:      %s\n", dataset.DisplayName)
	}
	if dataset.Description != "" {
		fmt.Printf("  Description:       %s\n", dataset.Description)
	}

	fmt.Println()
	fmt.Println("  Required CSV Columns:")
	fmt.Println("    - observed_at      RFC3339 timestamp when observation was made")
	fmt.Println("    - quality_level    ESTIMATE, PROVISIONAL, ACTUAL, or VERIFIED (REVISED accepted as legacy)")
	fmt.Println("    - value            Decimal string (max 64 characters)")
	fmt.Println()

	fmt.Println("  Optional CSV Columns:")
	fmt.Println("    - valid_from       RFC3339 timestamp when value becomes valid")
	fmt.Println("    - valid_to         RFC3339 timestamp when value expires")
	fmt.Println()

	if dataset.AttributeSchemaJSON != "" {
		fmt.Println("  Attribute Schema (additional columns from JSON Schema):")
		// Pretty print JSON schema
		var prettyJSON map[string]interface{}
		if err := json.Unmarshal([]byte(dataset.AttributeSchemaJSON), &prettyJSON); err == nil {
			printAttributeSchema(prettyJSON, "    ")
		} else {
			fmt.Printf("    %s\n", dataset.AttributeSchemaJSON)
		}
		fmt.Println()
	}

	if dataset.ResolutionKeyExpression != "" {
		fmt.Println("  Resolution Key Expression (CEL):")
		fmt.Printf("    %s\n", dataset.ResolutionKeyExpression)
		fmt.Println()
	}

	if dataset.ValidationExpression != "" {
		fmt.Println("  Validation Expression (CEL):")
		fmt.Printf("    %s\n", dataset.ValidationExpression)
		fmt.Println()
	}

	if dataset.ErrorMessageExpression != "" {
		fmt.Println("  Error Message Expression (CEL):")
		fmt.Printf("    %s\n", dataset.ErrorMessageExpression)
		fmt.Println()
	}

	fmt.Println("+---------------------------------------------------------------------------+")
}

// printAttributeSchema prints JSON Schema properties in a readable format.
func printAttributeSchema(schema map[string]interface{}, indent string) {
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return
	}

	required := make(map[string]bool)
	if reqList, ok := schema["required"].([]interface{}); ok {
		for _, r := range reqList {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}

	for name, prop := range props {
		propMap, ok := prop.(map[string]interface{})
		if !ok {
			continue
		}

		propType := "any"
		if t, ok := propMap["type"].(string); ok {
			propType = t
		}

		reqMarker := ""
		if required[name] {
			reqMarker = " (required)"
		}

		desc := ""
		if d, ok := propMap["description"].(string); ok {
			desc = fmt.Sprintf(" - %s", d)
		}

		fmt.Printf("%s- %-16s %s%s%s\n", indent, name, propType, reqMarker, desc)
	}
}
