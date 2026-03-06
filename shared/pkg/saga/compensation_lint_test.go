package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Schema validation tests ---

func TestCompensationSchema_RejectsHandlerWithNeitherCompensateNorStrategy(t *testing.T) {
	linter := NewSemanticLinter()
	linter.SetHandlerMetadata(map[string]HandlerMetadata{
		"test.handler": {
			// No CompensationStrategy, no HasAutoCompensation
		},
	})

	script := `
def execute(ctx):
    result = step(name="do_thing", handler="test.handler", params={})
    return result
`
	issues, err := linter.Analyze(script)
	require.NoError(t, err)

	compIssues := filterByType(issues, LintIssueTypeMissingCompensationStrategy)
	assert.Len(t, compIssues, 1, "should detect missing compensation strategy")
	assert.Contains(t, compIssues[0].Message, "test.handler")
	assert.Contains(t, compIssues[0].SuggestedFix, "compensation_strategy")
}

func TestCompensationSchema_AcceptsHandlerWithCompensate(t *testing.T) {
	linter := NewSemanticLinter()
	linter.SetHandlerMetadata(map[string]HandlerMetadata{
		"test.handler": {
			HasAutoCompensation:  true,
			CompensationStrategy: "auto",
		},
	})

	script := `
def execute(ctx):
    result = step(name="do_thing", handler="test.handler", params={})
    return result
`
	issues, err := linter.Analyze(script)
	require.NoError(t, err)

	compIssues := filterByType(issues, LintIssueTypeMissingCompensationStrategy)
	assert.Empty(t, compIssues, "handler with compensate should not trigger warning")
}

func TestCompensationSchema_AcceptsHandlerWithStrategyNone(t *testing.T) {
	linter := NewSemanticLinter()
	linter.SetHandlerMetadata(map[string]HandlerMetadata{
		"test.handler": {
			CompensationStrategy: "none",
		},
	})

	script := `
def execute(ctx):
    result = step(name="do_thing", handler="test.handler", params={})
    return result
`
	issues, err := linter.Analyze(script)
	require.NoError(t, err)

	compIssues := filterByType(issues, LintIssueTypeMissingCompensationStrategy)
	assert.Empty(t, compIssues, "handler with strategy none should not trigger warning")
}

func TestCompensationSchema_AcceptsHandlerWithStrategySagaManaged(t *testing.T) {
	linter := NewSemanticLinter()
	linter.SetHandlerMetadata(map[string]HandlerMetadata{
		"test.handler": {
			CompensationStrategy: "saga_managed",
		},
	})

	script := `
def execute(ctx):
    result = step(name="do_thing", handler="test.handler", params={})
    return result
`
	issues, err := linter.Analyze(script)
	require.NoError(t, err)

	compIssues := filterByType(issues, LintIssueTypeMissingCompensationStrategy)
	assert.Empty(t, compIssues, "handler with strategy saga_managed should not trigger warning")
}

func TestCompensationLint_DefaultSeverityIsWarning(t *testing.T) {
	linter := NewSemanticLinter()
	linter.SetHandlerMetadata(map[string]HandlerMetadata{
		"test.handler": {
			// No compensation strategy
		},
	})

	script := `
def execute(ctx):
    result = step(name="do_thing", handler="test.handler", params={})
    return result
`
	issues, err := linter.Analyze(script)
	require.NoError(t, err)

	compIssues := filterByType(issues, LintIssueTypeMissingCompensationStrategy)
	require.Len(t, compIssues, 1)
	assert.Equal(t, LintSeverityWarning, compIssues[0].Severity,
		"compensation strategy lint should be WARNING by default (draft)")
}

func TestCompensationLint_EscalatesToErrorForActivation(t *testing.T) {
	linter := NewSemanticLinter()
	linter.SetEnforcementLevel(LintIssueTypeMissingCompensationStrategy, EnforcementLevelError)
	linter.SetHandlerMetadata(map[string]HandlerMetadata{
		"test.handler": {
			// No compensation strategy
		},
	})

	script := `
def execute(ctx):
    result = step(name="do_thing", handler="test.handler", params={})
    return result
`
	issues, err := linter.Analyze(script)
	require.NoError(t, err)

	compIssues := filterByType(issues, LintIssueTypeMissingCompensationStrategy)
	require.Len(t, compIssues, 1)
	assert.Equal(t, LintSeverityError, compIssues[0].Severity,
		"compensation strategy lint should escalate to ERROR for activation")

	assert.True(t, linter.HasBlockingIssues(issues), "should block activation")
}

func TestCompensationLint_DoesNotFireForUnknownHandlers(t *testing.T) {
	linter := NewSemanticLinter()
	// No handler metadata configured - handler not in registry

	script := `
def execute(ctx):
    result = step(name="do_thing", handler="unknown.handler", params={})
    return result
`
	issues, err := linter.Analyze(script)
	require.NoError(t, err)

	compIssues := filterByType(issues, LintIssueTypeMissingCompensationStrategy)
	assert.Empty(t, compIssues, "should not fire for handlers not in metadata")
}

func TestCompensationLint_MultipleHandlers(t *testing.T) {
	linter := NewSemanticLinter()
	linter.SetHandlerMetadata(map[string]HandlerMetadata{
		"test.covered": {
			HasAutoCompensation:  true,
			CompensationStrategy: "auto",
		},
		"test.uncovered": {
			// No compensation strategy
		},
		"test.none": {
			CompensationStrategy: "none",
		},
	})

	script := `
def execute(ctx):
    r1 = step(name="step1", handler="test.covered", params={})
    r2 = step(name="step2", handler="test.uncovered", params={})
    r3 = step(name="step3", handler="test.none", params={})
    return r3
`
	issues, err := linter.Analyze(script)
	require.NoError(t, err)

	compIssues := filterByType(issues, LintIssueTypeMissingCompensationStrategy)
	assert.Len(t, compIssues, 1, "only uncovered handler should trigger")
	assert.Contains(t, compIssues[0].Message, "test.uncovered")
}

func TestCompensationLint_PreCheckAndCompensationBothFire(t *testing.T) {
	linter := NewSemanticLinter()
	linter.SetHandlerMetadata(map[string]HandlerMetadata{
		"gateway.send": {
			IsExternal:       true,
			RequiresPreCheck: true,
			// No compensation strategy
		},
	})

	script := `
def execute(ctx):
    result = step(name="send", handler="gateway.send", params={})
    return result
`
	issues, err := linter.Analyze(script)
	require.NoError(t, err)

	preCheckIssues := filterByType(issues, LintIssueTypeMissingPreCheck)
	compIssues := filterByType(issues, LintIssueTypeMissingCompensationStrategy)

	assert.Len(t, preCheckIssues, 1, "should fire pre-check warning")
	assert.Len(t, compIssues, 1, "should fire compensation warning")
}
