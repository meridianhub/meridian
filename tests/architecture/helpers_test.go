package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findRepoRoot walks up from the test directory to find the directory containing go.mod.
func findRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod in any parent directory")
		}
		dir = parent
	}
}

// shouldSkipDir returns true for directories that should be excluded from analysis.
func shouldSkipDir(name string) bool {
	switch name {
	case "vendor", ".git", "node_modules", "gen", "frontend":
		return true
	}
	return false
}

// isGeneratedFile returns true for generated Go files that should be excluded from analysis.
func isGeneratedFile(path string) bool {
	base := filepath.Base(path)
	return strings.HasSuffix(base, ".pb.go") ||
		strings.HasSuffix(base, "_grpc.pb.go") ||
		strings.HasSuffix(base, ".pb.gw.go") ||
		strings.HasSuffix(base, ".connect.go")
}

// isTestFile returns true for Go test files.
func isTestFile(path string) bool {
	return strings.HasSuffix(filepath.Base(path), "_test.go")
}

// walkGoFiles walks the directory tree starting at root, calling fn for each
// non-generated, non-test .go file. Directories matching shouldSkipDir are skipped.
func walkGoFiles(t *testing.T, root string, fn func(path string, relPath string)) {
	t.Helper()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && shouldSkipDir(info.Name()) {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}
		if isGeneratedFile(path) || isTestFile(path) {
			return nil
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// Normalize to forward slashes so allowlist keys work cross-platform.
		relPath = filepath.ToSlash(relPath)
		fn(path, relPath)
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk directory %s: %v", root, err)
	}
}
