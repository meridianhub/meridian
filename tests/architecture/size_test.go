package architecture_test

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

const (
	maxFileLines     = 800
	maxFunctionLines = 60

	// baselineOversizedFunctions is the number of functions exceeding maxFunctionLines
	// at the time this test was introduced. The test fails if this count increases,
	// preventing new violations while allowing gradual cleanup.
	// Last measured: 2026-03-31 (wireCurrentAccount +12 lines, run +2 lines for per-service email workers)
	baselineOversizedFunctions = 182
)

// knownOversizedFiles tracks files that currently exceed the size limit.
// Each entry must be removed when the file is split to comply.
// Do NOT add new entries — split large files instead.
var knownOversizedFiles = map[string]bool{}

// TestFileSize validates that no non-test, non-generated Go file exceeds maxFileLines.
// Known violations are tracked in knownOversizedFiles — the test fails only on NEW violations.
// See docs/guides/service-file-conventions.md for guidance on splitting large files.
func TestFileSize(t *testing.T) {
	root := findRepoRoot(t)

	walkGoFiles(t, root, func(path string, relPath string) {
		lines := countLines(t, path)
		if lines > maxFileLines {
			if knownOversizedFiles[relPath] {
				t.Logf("KNOWN: %s: %d lines (max %d)", relPath, lines, maxFileLines)
				return
			}
			t.Errorf("%s: %d lines (max %d). See docs/guides/service-file-conventions.md",
				relPath, lines, maxFileLines)
		}
	})
}

// TestFileSizeAllowlistAccuracy checks that every entry in knownOversizedFiles still exists
// and is still over the limit. Stale entries should be removed.
func TestFileSizeAllowlistAccuracy(t *testing.T) {
	root := findRepoRoot(t)

	for relPath := range knownOversizedFiles {
		absPath := root + "/" + relPath
		info, err := os.Stat(absPath)
		if os.IsNotExist(err) {
			t.Errorf("allowlist entry %q: file no longer exists — remove from knownOversizedFiles", relPath)
			continue
		}
		if err != nil || info.IsDir() {
			t.Errorf("allowlist entry %q: not a regular file — remove from knownOversizedFiles", relPath)
			continue
		}
		lines := countLines(t, absPath)
		if lines <= maxFileLines {
			t.Errorf("allowlist entry %q: now %d lines (limit %d) — remove from knownOversizedFiles", relPath, lines, maxFileLines)
		}
	}
}

// TestFunctionSize uses a ratchet approach: it counts the total number of functions
// exceeding maxFunctionLines and fails if the count exceeds the baseline.
// This prevents new violations while allowing gradual cleanup without maintaining
// an individual allowlist for 400+ existing violations.
//
// To reduce the baseline: refactor oversized functions, then lower baselineOversizedFunctions.
// See docs/guides/service-file-conventions.md for guidance.
func TestFunctionSize(t *testing.T) {
	root := findRepoRoot(t)
	fset := token.NewFileSet()

	type violation struct {
		relPath string
		line    int
		name    string
		lines   int
	}
	var violations []violation

	walkGoFiles(t, root, func(path string, relPath string) {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Logf("WARNING: could not parse %s: %v", relPath, err)
			return
		}

		ast.Inspect(file, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}

			start := fset.Position(fd.Pos())
			end := fset.Position(fd.End())
			lines := end.Line - start.Line + 1

			if lines > maxFunctionLines {
				name := fd.Name.Name
				if fd.Recv != nil && len(fd.Recv.List) > 0 {
					name = fmt.Sprintf("(%s).%s", exprName(fd.Recv.List[0].Type), fd.Name.Name)
				}
				violations = append(violations, violation{relPath, start.Line, name, lines})
			}
			return true
		})
	})

	t.Logf("Functions exceeding %d lines: %d (baseline: %d)", maxFunctionLines, len(violations), baselineOversizedFunctions)

	if len(violations) > baselineOversizedFunctions {
		t.Errorf("Function size violations increased from %d to %d. New oversized functions:",
			baselineOversizedFunctions, len(violations))
		// Log all violations to help identify the new ones.
		for _, v := range violations {
			t.Logf("  %s:%d: %s (%d lines)", v.relPath, v.line, v.name, v.lines)
		}
	}

	if len(violations) < baselineOversizedFunctions-5 {
		t.Logf("HINT: Baseline can be reduced from %d to %d — update baselineOversizedFunctions in size_test.go",
			baselineOversizedFunctions, len(violations))
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open %s: %v", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("failed to scan %s: %v", path, err)
	}
	return count
}

// exprName returns a human-readable name for a receiver type expression.
func exprName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + exprName(e.X)
	case *ast.IndexExpr:
		return exprName(e.X)
	case *ast.IndexListExpr:
		return exprName(e.X)
	default:
		return "?"
	}
}
