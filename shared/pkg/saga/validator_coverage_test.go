package saga

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
)

// parseScriptForTest parses a Starlark script into a *syntax.File for direct
// visitor exercising. While-loop parsing is enabled so walkWhileStmt — which is
// unreachable through ValidateSagaScript (FileOptions{}.While defaults to false)
// — can still be covered directly.
func parseScriptForTest(t *testing.T, script string, allowWhile bool) *syntax.File {
	t.Helper()
	opts := &syntax.FileOptions{While: allowWhile}
	file, err := opts.Parse("test.star", script, 0)
	require.NoError(t, err)
	return file
}

// newVisitor returns a fresh validationVisitor for direct method coverage.
func newVisitor() *validationVisitor {
	return &validationVisitor{loopDepth: 0, maxDepth: 0}
}

// TestValidationResult_Summary_WithErrors covers the fatal-error loop of
// Summary, which the lint-only and empty cases in linter_test.go do not reach.
func TestValidationResult_Summary_WithErrors(t *testing.T) {
	r := &ValidationResult{
		Errors: []error{errors.New("boom")},
		LintIssues: []LintIssue{
			{Severity: LintSeverityError, LineNumber: 7, Message: "bad decimal"},
			{Severity: LintSeverityWarning, LineNumber: 3, Message: "watch out"},
		},
	}
	summary := r.Summary()
	assert.Contains(t, summary, "ERROR: boom")
	assert.Contains(t, summary, "ERROR [line 7]: bad decimal")
	assert.Contains(t, summary, "WARNING [line 3]: watch out")
}

// TestValidationResult_StatePredicates exercises HasErrors / HasBlockingLintIssues
// / IsValid across the combinations that gate activation.
func TestValidationResult_StatePredicates(t *testing.T) {
	tests := []struct {
		name         string
		result       ValidationResult
		wantHasErr   bool
		wantBlocking bool
		wantValid    bool
	}{
		{
			name:      "clean",
			result:    ValidationResult{},
			wantValid: true,
		},
		{
			name:       "fatal error",
			result:     ValidationResult{Errors: []error{errors.New("x")}},
			wantHasErr: true,
		},
		{
			name:         "blocking lint",
			result:       ValidationResult{LintIssues: []LintIssue{{Severity: LintSeverityError}}},
			wantBlocking: true,
		},
		{
			name:      "warning-only lint stays valid",
			result:    ValidationResult{LintIssues: []LintIssue{{Severity: LintSeverityWarning}}},
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantHasErr, tt.result.HasErrors())
			assert.Equal(t, tt.wantBlocking, tt.result.HasBlockingLintIssues())
			assert.Equal(t, tt.wantValid, tt.result.IsValid())
		})
	}
}

// TestWalkWhileStmt covers walkWhileStmt directly. It is dead code via
// ValidateSagaScript because while loops are syntactically rejected there
// (FileOptions{}.While == false), so the only way to cover it is a direct call
// with While parsing enabled.
func TestWalkWhileStmt(t *testing.T) {
	t.Run("counts loop depth", func(t *testing.T) {
		file := parseScriptForTest(t, "while True:\n    x = 1\n", true)
		v := newVisitor()
		require.NoError(t, v.walkFile(file))
		assert.Equal(t, 1, v.maxDepth)
	})

	t.Run("rejects blocked call in condition", func(t *testing.T) {
		file := parseScriptForTest(t, "while exec(\"x\"):\n    y = 1\n", true)
		v := newVisitor()
		err := v.walkFile(file)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrBlockedFunction)
	})

	t.Run("rejects blocked call in body", func(t *testing.T) {
		file := parseScriptForTest(t, "while True:\n    z = open(\"f\")\n", true)
		v := newVisitor()
		err := v.walkFile(file)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrBlockedFunction)
	})
}

// TestWalkExpr_NodeTypes covers walkExpr branches that the existing scripts do
// not exercise: ternary (CondExpr), slice, lambda, unary, index, tuple, paren,
// and dot expressions — each carrying a blocked call so propagation is verified.
func TestWalkExpr_NodeTypes(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		wantErr bool
	}{
		{
			name:    "ternary clean",
			script:  "x = 1 if True else 2\n",
			wantErr: false,
		},
		{
			name:    "ternary blocked in cond",
			script:  "x = 1 if exec(\"e\") else 2\n",
			wantErr: true,
		},
		{
			name:    "ternary blocked in true branch",
			script:  "x = open(\"f\") if True else 2\n",
			wantErr: true,
		},
		{
			name:    "ternary blocked in false branch",
			script:  "x = 1 if True else compile(\"c\")\n",
			wantErr: true,
		},
		{
			name:    "slice clean",
			script:  "x = [1, 2, 3][0:2:1]\n",
			wantErr: false,
		},
		{
			name:    "slice blocked in lo",
			script:  "x = [1, 2, 3][open(\"f\"):2]\n",
			wantErr: true,
		},
		{
			name:    "lambda clean",
			script:  "f = lambda a: a + 1\n",
			wantErr: false,
		},
		{
			name:    "lambda blocked body",
			script:  "f = lambda a: exec(a)\n",
			wantErr: true,
		},
		{
			name:    "unary clean",
			script:  "x = -5\n",
			wantErr: false,
		},
		{
			name:    "unary blocked operand",
			script:  "x = -open(\"f\")\n",
			wantErr: true,
		},
		{
			name:    "index blocked in index",
			script:  "x = [1, 2][exec(\"e\")]\n",
			wantErr: true,
		},
		{
			name:    "tuple blocked element",
			script:  "x = (1, open(\"f\"), 3)\n",
			wantErr: true,
		},
		{
			name:    "paren blocked",
			script:  "x = (exec(\"e\"))\n",
			wantErr: true,
		},
		{
			name:    "dot expr blocked receiver",
			script:  "x = exec(\"e\").attr\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSagaScript(tt.script)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrBlockedFunction)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestWalk_ErrorPropagation covers the error-return branches of the statement
// walkers (assign LHS/RHS, def body, if cond/true/false, for X/body, dict
// key/value, exprList) so a blocked call nested inside each surfaces.
func TestWalk_ErrorPropagation(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{name: "assign LHS via subscript", script: "d = {}\nd[exec(\"e\")] = 1\n"},
		{name: "assign RHS", script: "x = open(\"f\")\n"},
		{name: "def body", script: "def f():\n    return exec(\"e\")\n"},
		{name: "if cond", script: "if exec(\"e\"):\n    x = 1\n"},
		{name: "if true branch", script: "if True:\n    x = open(\"f\")\n"},
		{name: "if false branch", script: "if True:\n    x = 1\nelse:\n    y = compile(\"c\")\n"},
		{name: "for iterable", script: "for i in [open(\"f\")]:\n    x = i\n"},
		{name: "for body", script: "for i in range(3):\n    x = exec(\"e\")\n"},
		{name: "dict key", script: "d = {open(\"f\"): 1}\n"},
		{name: "dict value", script: "d = {\"k\": exec(\"e\")}\n"},
		{name: "list element", script: "x = [1, open(\"f\"), 3]\n"},
		{name: "call argument", script: "y = str(exec(\"e\"))\n"},
		{name: "binary operand", script: "x = 1 + open(\"f\")\n"},
		{name: "return expr", script: "def f():\n    return open(\"f\")\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSagaScript(tt.script)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrBlockedFunction)
		})
	}
}

// TestWalkComprehension_BlockedClauses covers the error branches inside
// walkComprehension: blocked call in the for-clause iterable, in an if-clause
// filter, and in the body expression.
func TestWalkComprehension_BlockedClauses(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{name: "for clause iterable", script: "x = [i for i in open(\"f\")]\n"},
		{name: "if clause filter", script: "x = [i for i in range(3) if exec(str(i))]\n"},
		{name: "body expr", script: "x = [open(str(i)) for i in range(3)]\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSagaScript(tt.script)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrBlockedFunction)
		})
	}
}

// TestWalkStmt_NilBranches covers safe statement nodes that return nil without
// nested expressions: branch statements (break/continue/pass) and a bare return.
func TestWalkStmt_NilBranches(t *testing.T) {
	script := `
def f():
    for i in range(3):
        if i == 1:
            continue
        if i == 2:
            break
        pass
    return
`
	require.NoError(t, ValidateSagaScript(script))
}

// TestValidateWithLinter_NilLinter covers ValidateWithLinter's nil-linter
// branch: basic validation still runs but semantic linting is skipped.
func TestValidateWithLinter_NilLinter(t *testing.T) {
	t.Run("nil linter skips lint analysis", func(t *testing.T) {
		result, err := ValidateWithLinter("x = 1 + 2\n", nil)
		require.NoError(t, err)
		assert.False(t, result.HasErrors())
		assert.Empty(t, result.LintIssues)
	})

	t.Run("basic validation failure short-circuits linting", func(t *testing.T) {
		result, err := ValidateWithLinter("exec(\"e\")\n", NewSemanticLinter())
		require.NoError(t, err)
		require.True(t, result.HasErrors())
		// Lint analysis is skipped when basic validation produced errors.
		assert.Empty(t, result.LintIssues)
	})
}
