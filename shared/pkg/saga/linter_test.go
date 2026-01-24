package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSemanticLinter_DecimalArithmetic(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantIssues int
		wantType   LintIssueType
	}{
		{
			name: "Decimal multiplication triggers warning",
			script: `
qty = Decimal("10")
rate = Decimal("0.05")
result = qty * rate
`,
			wantIssues: 1,
			wantType:   LintIssueTypeDecimalArithmetic,
		},
		{
			name: "Decimal addition triggers warning",
			script: `
a = Decimal("100.00")
b = Decimal("50.00")
total = a + b
`,
			wantIssues: 1,
			wantType:   LintIssueTypeDecimalArithmetic,
		},
		{
			name: "Decimal division triggers warning",
			script: `
amount = Decimal("100")
shares = Decimal("4")
each = amount / shares
`,
			wantIssues: 1,
			wantType:   LintIssueTypeDecimalArithmetic,
		},
		{
			name: "Loop counter increment is exempt",
			script: `
i = 0
for item in items:
    i = i + 1
`,
			wantIssues: 0,
		},
		{
			name: "List indexing with offset is exempt",
			script: `
items = [1, 2, 3]
offset = 1
x = items[i + offset]
`,
			wantIssues: 0,
		},
		{
			name: "Integer arithmetic is exempt",
			script: `
count = 5
total = count + 10
`,
			wantIssues: 0,
		},
		{
			name: "Valuation engine result usage is allowed",
			script: `
def process(instrument_code):
    result = valuate(instrument=instrument_code, quantity=Decimal("100"))
    # Using the pre-validated rate from Valuation Engine is OK
    # No Decimal arithmetic needed - result is already calculated
    return result
`,
			wantIssues: 0,
		},
		{
			name: "String concatenation is exempt",
			script: `
prefix = "account_"
suffix = "001"
name = prefix + suffix
`,
			wantIssues: 0,
		},
		{
			name: "Multiple Decimal operations trigger multiple warnings",
			script: `
a = Decimal("10")
b = Decimal("20")
c = Decimal("30")
sum1 = a + b
sum2 = sum1 + c
`,
			wantIssues: 2,
			wantType:   LintIssueTypeDecimalArithmetic,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			linter := NewSemanticLinter()
			issues, err := linter.Analyze(tt.script)
			require.NoError(t, err)

			if tt.wantIssues == 0 {
				assert.Empty(t, issues, "expected no lint issues")
			} else {
				assert.Len(t, issues, tt.wantIssues, "unexpected number of lint issues")
				if len(issues) > 0 && tt.wantType != "" {
					assert.Equal(t, tt.wantType, issues[0].Type)
				}
			}
		})
	}
}

func TestSemanticLinter_MagicNumbers(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantIssues int
	}{
		{
			name: "Magic decimal literal triggers warning",
			script: `
rate = 0.05
amount = calculate(rate)
`,
			wantIssues: 1,
		},
		{
			name: "Small integers (0, 1, -1) are exempt",
			script: `
x = 0
y = 1
z = -1
`,
			wantIssues: 0,
		},
		{
			name: "Named constant pattern is OK",
			script: `
TAX_RATE = Decimal("0.15")
amount = valuate(rate=TAX_RATE)
`,
			wantIssues: 0,
		},
		{
			name: "Range boundaries are exempt",
			script: `
for i in range(10):
    process(i)
`,
			wantIssues: 0,
		},
		{
			name: "List index is exempt",
			script: `
items = [1, 2, 3]
first = items[0]
second = items[1]
`,
			wantIssues: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			linter := NewSemanticLinter()
			issues, err := linter.Analyze(tt.script)
			require.NoError(t, err)

			magicNumIssues := filterByType(issues, LintIssueTypeMagicNumber)
			assert.Len(t, magicNumIssues, tt.wantIssues)
		})
	}
}

func TestSemanticLinter_NestedConditionals(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantIssues int
	}{
		{
			name: "3 levels of nesting is OK",
			script: `
if a:
    if b:
        if c:
            do_something()
`,
			wantIssues: 0,
		},
		{
			name: "4 levels of nesting triggers warning",
			script: `
if a:
    if b:
        if c:
            if d:
                do_something()
`,
			wantIssues: 1,
		},
		{
			name: "Flat conditionals are OK",
			script: `
if a:
    do_a()
if b:
    do_b()
if c:
    do_c()
`,
			wantIssues: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			linter := NewSemanticLinter()
			issues, err := linter.Analyze(tt.script)
			require.NoError(t, err)

			nestedIssues := filterByType(issues, LintIssueTypeNestedConditional)
			assert.Len(t, nestedIssues, tt.wantIssues)
		})
	}
}

func TestSemanticLinter_HardcodedInstrumentCodes(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		wantIssues int
	}{
		{
			name: "Hardcoded instrument code in valuate call triggers warning",
			script: `
def process():
    return valuate(instrument="ELEC_NZD")
`,
			wantIssues: 1,
		},
		{
			name: "Parameterized instrument is OK",
			script: `
def process(instrument):
    return valuate(instrument=instrument)
`,
			wantIssues: 0,
		},
		{
			name: "resolve_instrument result is OK",
			script: `
def process(ref):
    instrument = resolve_instrument(reference=ref)
    return valuate(instrument=instrument)
`,
			wantIssues: 0,
		},
		{
			name: "Hardcoded account code in resolve_account triggers warning",
			script: `
def process():
    account = resolve_account(reference="ACC_001")
    return account
`,
			wantIssues: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			linter := NewSemanticLinter()
			issues, err := linter.Analyze(tt.script)
			require.NoError(t, err)

			hardcodedIssues := filterByType(issues, LintIssueTypeHardcodedCode)
			assert.Len(t, hardcodedIssues, tt.wantIssues)
		})
	}
}

func TestSemanticLinter_PreStepCheck(t *testing.T) {
	tests := []struct {
		name       string
		script     string
		handlers   map[string]HandlerMetadata
		wantIssues int
	}{
		{
			name: "External step without pre-check triggers error",
			script: `
def execute(ctx):
    result = step(name="send_payment", handler="payment_gateway.send", params={"amount": Decimal("100")})
    return result
`,
			handlers: map[string]HandlerMetadata{
				"payment_gateway.send": {IsExternal: true, RequiresPreCheck: true},
			},
			wantIssues: 1,
		},
		{
			name: "External step with verify_external_state is OK",
			script: `
def execute(ctx):
    verify_external_state(handler="payment_gateway.send", check_fn=check_payment_status)
    result = step(name="send_payment", handler="payment_gateway.send", params={"amount": Decimal("100")})
    return result
`,
			handlers: map[string]HandlerMetadata{
				"payment_gateway.send": {IsExternal: true, RequiresPreCheck: true},
			},
			wantIssues: 0,
		},
		{
			name: "Internal handler without pre-check is OK",
			script: `
def execute(ctx):
    result = step(name="create_booking", handler="financial_accounting.create_booking", params={})
    return result
`,
			handlers: map[string]HandlerMetadata{
				"financial_accounting.create_booking": {IsExternal: false, RequiresPreCheck: false},
			},
			wantIssues: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			linter := NewSemanticLinter()
			linter.SetHandlerMetadata(tt.handlers)
			issues, err := linter.Analyze(tt.script)
			require.NoError(t, err)

			preCheckIssues := filterByType(issues, LintIssueTypeMissingPreCheck)
			assert.Len(t, preCheckIssues, tt.wantIssues)
		})
	}
}

func TestSemanticLinter_EnforcementLevels(t *testing.T) {
	tests := []struct {
		name          string
		script        string
		level         EnforcementLevel
		wantSeverity  LintSeverity
		expectBlocked bool
	}{
		{
			name: "Warning level allows activation",
			script: `
qty = Decimal("10")
rate = Decimal("0.05")
result = qty * rate
`,
			level:         EnforcementLevelWarning,
			wantSeverity:  LintSeverityWarning,
			expectBlocked: false,
		},
		{
			name: "Error level blocks activation",
			script: `
qty = Decimal("10")
rate = Decimal("0.05")
result = qty * rate
`,
			level:         EnforcementLevelError,
			wantSeverity:  LintSeverityError,
			expectBlocked: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			linter := NewSemanticLinter()
			linter.SetEnforcementLevel(LintIssueTypeDecimalArithmetic, tt.level)
			issues, err := linter.Analyze(tt.script)
			require.NoError(t, err)

			if len(issues) > 0 {
				assert.Equal(t, tt.wantSeverity, issues[0].Severity)
			}

			blocked := linter.HasBlockingIssues(issues)
			assert.Equal(t, tt.expectBlocked, blocked)
		})
	}
}

func TestSemanticLinter_SuggestedFixes(t *testing.T) {
	t.Run("Decimal arithmetic has suggested fix", func(t *testing.T) {
		linter := NewSemanticLinter()
		issues, err := linter.Analyze(`
qty = Decimal("10")
rate = Decimal("0.05")
result = qty * rate
`)
		require.NoError(t, err)
		require.Len(t, issues, 1)
		assert.Contains(t, issues[0].SuggestedFix, "cel_eval")
	})

	t.Run("Missing pre-check has suggested fix", func(t *testing.T) {
		linter := NewSemanticLinter()
		linter.SetHandlerMetadata(map[string]HandlerMetadata{
			"payment_gateway.send": {IsExternal: true, RequiresPreCheck: true},
		})
		issues, err := linter.Analyze(`
def execute(ctx):
    result = step(name="send_payment", handler="payment_gateway.send", params={})
    return result
`)
		require.NoError(t, err)

		preCheckIssues := filterByType(issues, LintIssueTypeMissingPreCheck)
		require.Len(t, preCheckIssues, 1)
		assert.Contains(t, preCheckIssues[0].SuggestedFix, "verify_external_state")
	})
}

func TestSemanticLinter_LineNumbers(t *testing.T) {
	linter := NewSemanticLinter()
	issues, err := linter.Analyze(`
# Line 1: comment
qty = Decimal("10")
rate = Decimal("0.05")
result = qty * rate
`)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, 5, issues[0].LineNumber, "expected issue on line 5")
}

func TestSemanticLinter_EmptyScript(t *testing.T) {
	linter := NewSemanticLinter()
	issues, err := linter.Analyze("")
	require.NoError(t, err)
	assert.Empty(t, issues)
}

func TestSemanticLinter_SyntaxError(t *testing.T) {
	linter := NewSemanticLinter()
	_, err := linter.Analyze("def broken(")
	assert.Error(t, err)
}

// filterByType is a helper to filter lint issues by type.
func filterByType(issues []LintIssue, issueType LintIssueType) []LintIssue {
	var filtered []LintIssue
	for _, issue := range issues {
		if issue.Type == issueType {
			filtered = append(filtered, issue)
		}
	}
	return filtered
}

func TestValidateDraft_IncludesLintWarnings(t *testing.T) {
	script := `
qty = Decimal("10")
rate = Decimal("0.05")
result = qty * rate
`
	result, err := ValidateDraft(script)
	require.NoError(t, err)
	assert.False(t, result.HasErrors())
	assert.Len(t, result.LintIssues, 1)
	assert.Equal(t, LintSeverityWarning, result.LintIssues[0].Severity)
	assert.True(t, result.IsValid(), "draft validation should pass with warnings")
}

func TestValidateActivation_BlocksDecimalArithmetic(t *testing.T) {
	script := `
qty = Decimal("10")
rate = Decimal("0.05")
result = qty * rate
`
	err := ValidateActivation(script)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
}

func TestValidateActivation_AllowsCleanScript(t *testing.T) {
	script := `
def process(input):
    account = input["account"]
    amount = input["amount"]
    return {"account": account, "amount": amount}
`
	err := ValidateActivation(script)
	require.NoError(t, err)
}

func TestValidationResult_Summary(t *testing.T) {
	result := &ValidationResult{
		LintIssues: []LintIssue{
			{
				Type:       LintIssueTypeDecimalArithmetic,
				Severity:   LintSeverityWarning,
				LineNumber: 5,
				Message:    "Financial math detected",
			},
		},
	}

	summary := result.Summary()
	assert.Contains(t, summary, "WARNING")
	assert.Contains(t, summary, "line 5")
	assert.Contains(t, summary, "Financial math detected")
}

func TestValidationResult_NoIssues(t *testing.T) {
	result := &ValidationResult{}
	assert.Equal(t, "No issues found", result.Summary())
	assert.True(t, result.IsValid())
}
