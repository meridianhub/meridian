package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/pkg/saga/validation"
	"github.com/spf13/cobra"
)

// Flags for the validate command.
var (
	jsonOutput   bool
	handlersPath string
)

// validateCmd validates a Starlark saga script locally using mock handlers.
var validateCmd = &cobra.Command{
	Use:   "validate <script.star>",
	Short: "Validate a Starlark saga script locally",
	Long: `Validates a Starlark saga script using auto-generated mock handlers.

The script is executed in a sandboxed runtime with mock handlers
generated from the handler schema. This provides fast local feedback
before deployment.

Exit Codes:
  0 - Script is valid
  1 - Validation failed (syntax error, undefined handler, etc.)

Examples:
  meridian-cli saga validate withdrawal.star
  meridian-cli saga validate --json payment.star
  meridian-cli saga validate --handlers /path/to/schema.yaml deposit.star`,
	Args: cobra.ExactArgs(1),
	RunE: runValidate,
}

func init() {
	validateCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output validation report as JSON")
	validateCmd.Flags().StringVar(&handlersPath, "handlers", "", "Path to handler schema YAML file")

	sagaCmd.AddCommand(validateCmd)
}

// runValidate is the Cobra command handler for saga validate.
func runValidate(_ *cobra.Command, args []string) error {
	scriptPath := args[0]

	result, err := runValidateLogic(scriptPath, handlersPath)
	if err != nil {
		return err
	}

	// Load schema for available handler names (for suggestions)
	var availableHandlers []string
	schemaReg, loadErr := loadSchemaRegistry(handlersPath)
	if loadErr == nil {
		availableHandlers = schemaReg.ListHandlers()
	}

	output := formatOutput(result, jsonOutput, availableHandlers)
	fmt.Print(output)

	if !result.Success {
		// Exit with code 1 for failed validation (cobra interprets non-nil error as exit 1)
		os.Exit(1)
	}

	return nil
}

// runValidateLogic contains the core validation logic, separated for testability.
// It reads the script, loads the schema, and runs the dry-run validator.
func runValidateLogic(scriptPath, handlers string) (*validation.ValidationResult, error) {
	// Read script file
	scriptData, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read script: %w", err)
	}

	// Load schema registry
	schemaReg, err := loadSchemaRegistry(handlers)
	if err != nil {
		return nil, fmt.Errorf("failed to load schema: %w", err)
	}

	// Create a quiet logger - CLI only needs to show validation results, not execution logs.
	// Errors are captured in the ValidationResult, so we suppress INFO/WARN logs.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	runtime, err := saga.NewRuntime(logger, saga.WithTimeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("failed to create runtime: %w", err)
	}

	// Create mock registry from schema
	mockRegistry, err := validation.NewMockHandlerRegistry(schemaReg)
	if err != nil {
		return nil, fmt.Errorf("failed to create mock registry: %w", err)
	}

	// Create validator
	validator, err := validation.NewDryRunValidator(validation.DryRunValidatorConfig{
		Runtime:        runtime,
		MockRegistry:   mockRegistry,
		SchemaRegistry: schemaReg,
		Logger:         logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create validator: %w", err)
	}

	// Validate
	ctx := context.Background()
	result, err := validator.Validate(ctx, string(scriptData))
	if err != nil {
		return nil, fmt.Errorf("validation error: %w", err)
	}

	return result, nil
}

// loadSchemaRegistry loads the schema registry from the specified path or default location.
func loadSchemaRegistry(handlers string) (*schema.Registry, error) {
	reg := schema.NewRegistry()

	if handlers != "" {
		if err := reg.LoadFromFile(handlers); err != nil {
			return nil, err
		}
		return reg, nil
	}

	// No handlers path specified - return empty registry (will catch undefined handler errors)
	return reg, nil
}

// formatOutput formats the validation result for CLI output.
func formatOutput(result *validation.ValidationResult, asJSON bool, availableHandlers []string) string {
	if asJSON {
		formatter := &validation.JSONFormatter{
			AvailableHandlers: availableHandlers,
		}
		return formatter.Format(result)
	}

	formatter := &validation.HumanReadableFormatter{
		AvailableHandlers: availableHandlers,
	}
	return formatter.Format(result)
}
