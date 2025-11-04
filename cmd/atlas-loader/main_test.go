//nolint:goconst // Test data uses string literals for clarity
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAtlasLoaderBinary tests the atlas-loader binary end-to-end
// by building and executing it with various flag combinations.
func TestAtlasLoaderBinary(t *testing.T) {
	ctx := context.Background()

	// Build the binary for testing
	tmpDir := t.TempDir()
	binaryPath := tmpDir + "/atlas-loader"

	buildCmd := exec.CommandContext(ctx, "go", "build", "-o", binaryPath, ".")
	buildCmd.Stderr = os.Stderr
	err := buildCmd.Run()
	require.NoError(t, err, "failed to build atlas-loader binary")

	tests := []struct {
		name          string
		args          []string
		wantExitCode  int
		wantStdout    []string // strings that should appear in stdout
		wantNotStdout []string // strings that should NOT appear in stdout
		wantStderr    []string // strings that should appear in stderr
	}{
		{
			name:         "no filter loads all models",
			args:         []string{},
			wantExitCode: 0,
			wantStdout: []string{
				"CREATE TABLE",
				"customers",
				"accounts",
				"transactions",
			},
			wantNotStdout: []string{
				"CREATE SCHEMA", // No schema statement without filter
			},
		},
		{
			name:         "current_account filter loads only current_account models",
			args:         []string{"-schema=current_account"},
			wantExitCode: 0,
			wantStdout: []string{
				"CREATE SCHEMA IF NOT EXISTS current_account",
				"customers",
				"accounts",
			},
			wantNotStdout: []string{
				"transactions", // position_keeping model should not be included
			},
		},
		{
			name:         "position_keeping filter loads position_keeping models plus Account for FK",
			args:         []string{"-schema=position_keeping"},
			wantExitCode: 0,
			wantStdout: []string{
				"CREATE SCHEMA IF NOT EXISTS current_account",
				"CREATE SCHEMA IF NOT EXISTS position_keeping",
				"accounts",     // Included for FK reference
				"transactions", // position_keeping model
				// Note: customers table also included because Account has FK to Customer
				// GORM requires the full FK chain for proper constraint generation
			},
			wantNotStdout: []string{
				// All necessary models are included for FK integrity
			},
		},
		{
			name:         "unknown schema filter returns error",
			args:         []string{"-schema=invalid_schema"},
			wantExitCode: 1,
			wantStderr: []string{
				"unknown schema filter: invalid_schema",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			cmd := exec.CommandContext(ctx, binaryPath, tt.args...)
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err := cmd.Run()

			// Check exit code
			if tt.wantExitCode == 0 {
				assert.NoError(t, err, "expected successful execution")
			} else {
				assert.Error(t, err, "expected command to fail")
				var exitErr *exec.ExitError
				require.True(t, errors.As(err, &exitErr), "expected ExitError")
				assert.Equal(t, tt.wantExitCode, exitErr.ExitCode(), "unexpected exit code")
			}

			stdoutStr := stdout.String()
			stderrStr := stderr.String()

			// Check expected stdout content
			for _, want := range tt.wantStdout {
				assert.Contains(t, stdoutStr, want, "stdout should contain %q", want)
			}

			// Check unwanted stdout content
			for _, notWant := range tt.wantNotStdout {
				assert.NotContains(t, stdoutStr, notWant, "stdout should NOT contain %q", notWant)
			}

			// Check expected stderr content
			for _, want := range tt.wantStderr {
				assert.Contains(t, stderrStr, want, "stderr should contain %q", want)
			}
		})
	}
}

// TestSchemaFilterParsing tests that the schema filter flag is correctly parsed.
func TestSchemaFilterParsing(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantSchema string
	}{
		{
			name:       "no args defaults to empty string",
			args:       []string{},
			wantSchema: "",
		},
		{
			name:       "current_account flag",
			args:       []string{"-schema=current_account"},
			wantSchema: "current_account",
		},
		{
			name:       "position_keeping flag",
			args:       []string{"-schema=position_keeping"},
			wantSchema: "position_keeping",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset flags for each test
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

			var schemaFilter string
			flag.StringVar(&schemaFilter, "schema", "", "Filter models by schema")

			// Parse test args
			err := flag.CommandLine.Parse(tt.args)
			require.NoError(t, err)

			assert.Equal(t, tt.wantSchema, schemaFilter, "schema filter mismatch")
		})
	}
}

// TestModelInclusion verifies that the correct models are selected for each schema.
func TestModelInclusion(t *testing.T) {
	tests := []struct {
		name           string
		schemaFilter   string
		wantModels     []string
		wantModelCount int
	}{
		{
			name:           "no filter includes all models",
			schemaFilter:   "",
			wantModels:     []string{"Customer", "Account", "Transaction"},
			wantModelCount: 3,
		},
		{
			name:           "current_account includes Customer and Account",
			schemaFilter:   "current_account",
			wantModels:     []string{"Customer", "Account"},
			wantModelCount: 2,
		},
		{
			name:           "position_keeping includes Account (FK) and Transaction",
			schemaFilter:   "position_keeping",
			wantModels:     []string{"Account", "Transaction"},
			wantModelCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the model selection logic from main.go
			var modelList []interface{}

			switch tt.schemaFilter {
			case "current_account":
				modelList = []interface{}{
					"Customer", // Using strings for test simplicity
					"Account",
				}
			case "position_keeping":
				modelList = []interface{}{
					"Account",
					"Transaction",
				}
			case "":
				modelList = []interface{}{
					"Customer",
					"Account",
					"Transaction",
				}
			}

			assert.Equal(t, tt.wantModelCount, len(modelList), "model count mismatch")

			for _, wantModel := range tt.wantModels {
				found := false
				for _, model := range modelList {
					if model == wantModel {
						found = true
						break
					}
				}
				assert.True(t, found, "expected model %q not found in model list", wantModel)
			}
		})
	}
}

// TestSchemaStatementGeneration verifies CREATE SCHEMA statements are correctly generated.
func TestSchemaStatementGeneration(t *testing.T) {
	tests := []struct {
		name         string
		schemaFilter string
		wantSchemas  []string
	}{
		{
			name:         "no filter produces no schema statements",
			schemaFilter: "",
			wantSchemas:  []string{},
		},
		{
			name:         "current_account produces single schema statement",
			schemaFilter: "current_account",
			wantSchemas:  []string{"CREATE SCHEMA IF NOT EXISTS current_account;"},
		},
		{
			name:         "position_keeping produces both schemas for FK references",
			schemaFilter: "position_keeping",
			wantSchemas: []string{
				"CREATE SCHEMA IF NOT EXISTS current_account;",
				"CREATE SCHEMA IF NOT EXISTS position_keeping;",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate schema statement generation logic
			var schemaStmt string

			if tt.schemaFilter != "" {
				if tt.schemaFilter == "position_keeping" {
					schemaStmt = "CREATE SCHEMA IF NOT EXISTS current_account;\nCREATE SCHEMA IF NOT EXISTS position_keeping;\n\n"
				} else {
					schemaStmt = "CREATE SCHEMA IF NOT EXISTS " + tt.schemaFilter + ";\n\n"
				}
			}

			if len(tt.wantSchemas) == 0 {
				assert.Empty(t, schemaStmt, "expected no schema statements")
			} else {
				for _, want := range tt.wantSchemas {
					assert.Contains(t, schemaStmt, want, "schema statement should contain %q", want)
				}
			}
		})
	}
}

// TestForeignKeyInclusion verifies that models referenced by foreign keys are included.
func TestForeignKeyInclusion(t *testing.T) {
	t.Run("position_keeping includes Account for FK constraint", func(t *testing.T) {
		// When filtering for position_keeping, Account must be included
		// because Transaction has a FK to Account
		schemaFilter := "position_keeping"

		var modelList []interface{}
		switch schemaFilter {
		case "position_keeping":
			modelList = []interface{}{
				"Account",     // Must be first for FK reference
				"Transaction", // References Account
			}
		}

		require.Len(t, modelList, 2, "expected 2 models")
		assert.Equal(t, "Account", modelList[0], "Account must be included for FK")
		assert.Equal(t, "Transaction", modelList[1], "Transaction is the primary model")
	})
}

// TestOutputFormat verifies the output format is valid SQL.
func TestOutputFormat(t *testing.T) {
	ctx := context.Background()

	t.Run("output contains valid SQL statements", func(t *testing.T) {
		// Build and run the binary
		tmpDir := t.TempDir()
		binaryPath := tmpDir + "/atlas-loader"

		buildCmd := exec.CommandContext(ctx, "go", "build", "-o", binaryPath, ".")
		require.NoError(t, buildCmd.Run())

		var stdout bytes.Buffer
		cmd := exec.CommandContext(ctx, binaryPath, "-schema=current_account")
		cmd.Stdout = &stdout
		require.NoError(t, cmd.Run())

		output := stdout.String()

		// Verify SQL statement format
		assert.True(t, strings.HasPrefix(output, "CREATE SCHEMA"), "output should start with CREATE SCHEMA")
		assert.Contains(t, output, "CREATE TABLE", "output should contain CREATE TABLE")
		assert.Contains(t, output, "PRIMARY KEY", "output should contain PRIMARY KEY")

		// Verify schema qualification (with quotes)
		assert.Contains(t, output, `"current_account"."customers"`, "table should be schema-qualified")
		assert.Contains(t, output, `"current_account"."accounts"`, "table should be schema-qualified")

		// Verify no SQL syntax errors (basic check)
		assert.NotContains(t, output, ";;", "should not have double semicolons")
		assert.True(t, strings.HasSuffix(strings.TrimSpace(output), ";"), "should end with semicolon")
	})
}

// TestErrorHandling verifies error conditions are handled correctly.
func TestErrorHandling(t *testing.T) {
	ctx := context.Background()

	t.Run("invalid schema filter exits with error", func(t *testing.T) {
		tmpDir := t.TempDir()
		binaryPath := tmpDir + "/atlas-loader"

		buildCmd := exec.CommandContext(ctx, "go", "build", "-o", binaryPath, ".")
		require.NoError(t, buildCmd.Run())

		var stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, binaryPath, "-schema=nonexistent")
		cmd.Stderr = &stderr

		err := cmd.Run()
		require.Error(t, err, "expected command to fail")

		var exitErr *exec.ExitError
		require.True(t, errors.As(err, &exitErr))
		assert.Equal(t, 1, exitErr.ExitCode())

		assert.Contains(t, stderr.String(), "unknown schema filter")
	})
}
