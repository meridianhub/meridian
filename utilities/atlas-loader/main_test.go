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
				"customer",               // singular table name for search_path routing
				"account",                // singular table name for search_path routing
				"financial_position_log", // singular table name for search_path routing
				"transaction_log_entry",  // singular table name for search_path routing
			},
			wantNotStdout: []string{
				"CREATE SCHEMA", // No schema statements - tenant provisioner creates schemas
			},
		},
		{
			name:         "current_account filter loads only current_account models",
			args:         []string{"-schema=current_account"},
			wantExitCode: 0,
			wantStdout: []string{
				"CREATE TABLE",
				"customer", // singular table name for search_path routing
				"account",  // singular table name for search_path routing
				"lien",     // singular table name for search_path routing
			},
			wantNotStdout: []string{
				"CREATE SCHEMA",          // No schema statements - tenant provisioner creates schemas
				"financial_position_log", // position_keeping model should not be included
				"transaction_log_entry",  // position_keeping model should not be included
			},
		},
		{
			name:         "position_keeping filter loads position_keeping models plus Account for FK",
			args:         []string{"-schema=position_keeping"},
			wantExitCode: 0,
			wantStdout: []string{
				"CREATE TABLE",
				"account",                // singular table name for search_path routing
				"financial_position_log", // singular table name for search_path routing
				"transaction_log_entry",  // singular table name for search_path routing
				"transaction_lineage",    // singular table name for search_path routing
				"audit_trail_entry",      // singular table name for search_path routing
				// Note: customer table also included because Account has FK to Customer
				// GORM requires the full FK chain for proper constraint generation
			},
			wantNotStdout: []string{
				"CREATE SCHEMA", // No schema statements - tenant provisioner creates schemas
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

		// Verify SQL statement format - output contains only table DDL, no schema creation
		assert.True(t, strings.HasPrefix(output, "CREATE TABLE"), "output should start with CREATE TABLE")
		assert.NotContains(t, output, "CREATE SCHEMA", "output should not contain CREATE SCHEMA - tenant provisioner handles schema creation")
		assert.Contains(t, output, "PRIMARY KEY", "output should contain PRIMARY KEY")

		// Verify unqualified singular table names for search_path routing
		// All entities now use singular, unqualified names for database-per-service architecture
		assert.Contains(t, output, `CREATE TABLE "customer"`, "customer table should be singular and unqualified")
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
