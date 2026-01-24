package saga

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSagaScript_ValidScript(t *testing.T) {
	script := `
def my_saga():
    x = 1 + 2
    return x
`
	err := ValidateSagaScript(script)
	require.NoError(t, err)
}

func TestValidateSagaScript_EmptyScript(t *testing.T) {
	err := ValidateSagaScript("")
	require.NoError(t, err)
}

func TestValidateSagaScript_ScriptTooLarge(t *testing.T) {
	// Create a script larger than MaxScriptSize (64KB)
	script := strings.Repeat("x = 1\n", 15000) // ~90KB
	err := ValidateSagaScript(script)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrScriptTooLarge)
}

func TestValidateSagaScript_BlockedFunctions(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		blocked string
	}{
		{
			name:    "load function",
			script:  `load("module.star", "func")`,
			blocked: "load",
		},
		{
			name:    "exec function",
			script:  `exec("code")`,
			blocked: "exec",
		},
		{
			name:    "compile function",
			script:  `compile("code")`,
			blocked: "compile",
		},
		{
			name:    "open function",
			script:  `open("file.txt")`,
			blocked: "open",
		},
		{
			name:    "eval function",
			script:  `eval("1+1")`,
			blocked: "eval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSagaScript(tt.script)
			require.Error(t, err, "expected error for blocked function %s", tt.blocked)
			assert.ErrorIs(t, err, ErrBlockedFunction)
			assert.Contains(t, err.Error(), tt.blocked)
		})
	}
}

func TestValidateSagaScript_LoopNesting(t *testing.T) {
	tests := []struct {
		name    string
		script  string
		wantErr bool
	}{
		{
			name: "single loop - allowed",
			script: `
for i in range(10):
    x = i
`,
			wantErr: false,
		},
		{
			name: "two nested loops - allowed",
			script: `
for i in range(10):
    for j in range(10):
        x = i + j
`,
			wantErr: false,
		},
		{
			name: "three nested loops - allowed (at limit)",
			script: `
for i in range(10):
    for j in range(10):
        for k in range(10):
            x = i + j + k
`,
			wantErr: false,
		},
		{
			name: "four nested loops - rejected",
			script: `
for i in range(10):
    for j in range(10):
        for k in range(10):
            for l in range(10):
                x = i + j + k + l
`,
			wantErr: true,
		},
		{
			name: "deeply nested with functions - allowed if within limit",
			script: `
def outer():
    for i in range(10):
        for j in range(10):
            pass

def inner():
    for k in range(10):
        pass
`,
			wantErr: false,
		},
		{
			name: "three nested comprehensions - allowed (at limit)",
			script: `
result = [[[z for z in range(2)] for y in range(2)] for x in range(2)]
`,
			wantErr: false,
		},
		{
			name: "four nested comprehensions - rejected",
			script: `
result = [[[[w for w in range(2)] for z in range(2)] for y in range(2)] for x in range(2)]
`,
			wantErr: true,
		},
		{
			name: "comprehension with multiple for clauses - counts depth",
			script: `
result = [x+y+z for x in range(2) for y in range(2) for z in range(2)]
`,
			wantErr: false,
		},
		{
			name: "comprehension with four for clauses - rejected",
			script: `
result = [x+y+z+w for x in range(2) for y in range(2) for z in range(2) for w in range(2)]
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSagaScript(tt.script)
			if tt.wantErr {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrExcessiveLoopNesting)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateSagaScript_SyntaxError(t *testing.T) {
	script := `
def incomplete(
    x = 1
`
	err := ValidateSagaScript(script)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSyntax)
}

func TestValidateSagaScript_ValidComplexScript(t *testing.T) {
	// A realistic saga script
	script := `
def transfer_saga(input):
    source = input["source_account"]
    dest = input["dest_account"]
    amount = Decimal(input["amount"])

    # Step 1: Validate accounts
    source_valid = resolve_account(source)
    dest_valid = resolve_account(dest)

    if not source_valid or not dest_valid:
        fail("Invalid accounts")

    # Step 2: Create posting
    p = posting(source, dest, str(amount))

    log("Transfer completed")
    return {"status": "success", "posting": p}
`
	err := ValidateSagaScript(script)
	require.NoError(t, err)
}

func TestValidateSagaScript_AllowedBuiltins(t *testing.T) {
	// Script using allowed builtins should pass
	script := `
x = len([1, 2, 3])
y = str(42)
z = int("123")
items = list(range(10))
d = Decimal("100.50")
result = min(1, 2, 3)
`
	err := ValidateSagaScript(script)
	require.NoError(t, err)
}

func TestValidationError_Details(t *testing.T) {
	script := `exec("code")`
	err := ValidateSagaScript(script)
	require.Error(t, err)

	// Error should contain the function name
	assert.Contains(t, err.Error(), "exec")

	// Should be the right error type
	assert.ErrorIs(t, err, ErrBlockedFunction)
}
