//nolint:goconst // Test data uses string literals for clarity
package main

import (
	"bytes"
	"context"
	"errors"
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
				"accounts",                // shared domain models use GORM default plural naming
				"financial_position_logs", // shared domain models use GORM default plural naming
				"transaction_log_entries", // shared domain models use GORM default plural naming
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
				"account", // service entity uses singular table name
				"lien",    // service entity uses singular table name for balance holds
			},
			wantNotStdout: []string{
				"financial_position_logs", // position_keeping model should not be included
				"transaction_log_entries", // position_keeping model should not be included
			},
		},
		{
			name:         "position_keeping filter loads position_keeping models plus Account for FK",
			args:         []string{"-schema=position_keeping"},
			wantExitCode: 0,
			wantStdout: []string{
				"CREATE SCHEMA IF NOT EXISTS current_account",
				"CREATE SCHEMA IF NOT EXISTS position_keeping",
				"accounts",                // shared Account model uses GORM default plural naming
				"financial_position_logs", // shared domain model uses GORM default plural naming
				"transaction_log_entries", // shared domain model uses GORM default plural naming
				"transaction_lineages",    // shared domain model uses GORM default plural naming
				"audit_trail_entries",     // shared domain model uses GORM default plural naming
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

		// Verify schema qualification for schema-bound tables
		assert.Contains(t, output, `"current_account"."customers"`, "customers table should be schema-qualified")

		// Verify unqualified table names for multi-org tables (account, lien)
		// Service-specific entities use singular names for search_path routing
		assert.Contains(t, output, `CREATE TABLE "account"`, "account table should be singular and unqualified")
		assert.Contains(t, output, `CREATE TABLE "lien"`, "lien table should be singular and unqualified")

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
