// Package contracts verifies that immutable contract files have not been modified.
// This test MUST run before all other integration tests to ensure contract integrity.
// The "aaa_" prefix ensures alphabetical-first execution in Go test discovery.
package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// contractFile represents a file that is part of the immutable contract.
type contractFile struct {
	path           string // Relative path from project root
	expectedSHA256 string // Expected SHA256 checksum
}

// contractFiles defines the files that constitute the immutable contract for the
// Universal Asset System. These files were locked in Task 1 and must not be modified
// without explicit approval and a corresponding update to the checksums.
//
// If this test fails, it means a contract file was modified. This is intentional:
// - Proto files define the wire format for all services
// - Interface files define the domain contracts
// Modifying these requires careful coordination across all dependent services.
var contractFiles = []contractFile{
	{
		path:           "api/proto/meridian/quantity/v1/quantity.proto",
		expectedSHA256: "e50b694b8781af25bc1b9788880fa7fcd0307644b07809cacd04e7e414cfd781",
	},
	{
		path:           "api/proto/meridian/reference_data/v1/instrument.proto",
		expectedSHA256: "3046fdf5ea38a47e7f20c72d5e727c3e4c74919a3f3618cd863778e84f6465c6",
	},
	{
		path:           "shared/platform/quantity/interfaces.go",
		expectedSHA256: "b570315b92b2301f4220df29084dc0c22eaf10d0529a253407a74ec2de9061f6",
	},
}

// TestAAAContractVerification verifies that immutable contract files have not been modified.
// The "AAA" prefix ensures this test runs first alphabetically, blocking the test suite
// if any contract file has been modified without approval.
//
// CONTRACT VIOLATION means:
// 1. A proto or interface file was modified without updating checksums
// 2. All dependent services may have incompatible expectations
// 3. The modification must be reviewed and approved before proceeding
func TestAAAContractVerification(t *testing.T) {
	projectRoot := findProjectRoot(t)

	var violations []string

	for _, cf := range contractFiles {
		fullPath := filepath.Join(projectRoot, cf.path)

		actualChecksum, err := calculateSHA256(fullPath)
		if err != nil {
			t.Fatalf("Failed to calculate checksum for %s: %v", cf.path, err)
		}

		if actualChecksum != cf.expectedSHA256 {
			violations = append(violations, formatViolation(cf.path, cf.expectedSHA256, actualChecksum))
		}
	}

	if len(violations) > 0 {
		t.Fatalf(`
================================================================================
                         CONTRACT VIOLATION DETECTED
================================================================================

Proto/interface was modified without approval.

The following contract files have been modified since the last approved state:

%s
--------------------------------------------------------------------------------
IMPACT: These files define the wire format and domain contracts. Modifications
affect all services depending on these definitions.

TO RESOLVE:
1. If the change is intentional and approved:
   - Update the expected checksums in tests/contracts/aaa_contract_test.go
   - Document the change in the PR description
   - Ensure all dependent services are updated

2. If the change was accidental:
   - Revert the changes to the contract files
   - Re-run tests to verify

================================================================================
`, formatViolations(violations))
	}
}

// findProjectRoot locates the project root directory by walking up from the test file.
func findProjectRoot(t *testing.T) string {
	t.Helper()

	// Get the directory of this test file
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("Failed to get current file path")
	}

	// Walk up from tests/contracts/ to find project root
	dir := filepath.Dir(filename)
	for {
		// Check for go.mod as indicator of project root
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("Could not find project root (no go.mod found)")
		}
		dir = parent
	}
}

// calculateSHA256 computes the SHA256 checksum of a file.
func calculateSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// formatViolation formats a single contract violation for display.
func formatViolation(path, expected, actual string) string {
	return path + "\n" +
		"  Expected: " + expected + "\n" +
		"  Actual:   " + actual
}

// formatViolations joins multiple violations with newlines.
func formatViolations(violations []string) string {
	result := ""
	for i, v := range violations {
		if i > 0 {
			result += "\n\n"
		}
		result += v
	}
	return result
}
