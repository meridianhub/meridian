package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"
)

// parseAndExtractFull parses a full Starlark file and returns extracted references.
func parseAndExtractFull(t *testing.T, script string) ([]Reference, error) {
	t.Helper()

	opts := &syntax.FileOptions{}
	f, err := opts.Parse("test.star", script, 0)
	if err != nil {
		return nil, err
	}

	e := &referenceExtractor{}
	for _, stmt := range f.Stmts {
		e.walkStmt(stmt)
	}
	return e.references, nil
}

// ---------------------------------------------------------------------------
// resolve_instrument
// ---------------------------------------------------------------------------

func TestReferenceExtractor_InstrumentRef(t *testing.T) {
	refs, err := parseAndExtractFull(t, `x = resolve_instrument("GBP")`)
	require.NoError(t, err)

	require.Len(t, refs, 1)
	assert.Equal(t, ReferenceTypeInstrument, refs[0].Type)
	assert.Equal(t, "GBP", refs[0].Key)
}

func TestReferenceExtractor_InstrumentRef_SingleQuotes(t *testing.T) {
	refs, err := parseAndExtractFull(t, `x = resolve_instrument('USD')`)
	require.NoError(t, err)

	require.Len(t, refs, 1)
	assert.Equal(t, ReferenceTypeInstrument, refs[0].Type)
	assert.Equal(t, "USD", refs[0].Key)
}

func TestReferenceExtractor_InstrumentRef_NoArgs(t *testing.T) {
	// Should not produce a reference if no literal argument.
	refs, err := parseAndExtractFull(t, `x = resolve_instrument()`)
	require.NoError(t, err)
	assert.Empty(t, refs)
}

// ---------------------------------------------------------------------------
// resolve_account
// ---------------------------------------------------------------------------

func TestReferenceExtractor_AccountRef(t *testing.T) {
	refs, err := parseAndExtractFull(t, `acc = resolve_account("clearing_account")`)
	require.NoError(t, err)

	require.Len(t, refs, 1)
	assert.Equal(t, ReferenceTypeAccount, refs[0].Type)
	assert.Equal(t, "clearing_account", refs[0].Key)
}

// ---------------------------------------------------------------------------
// invoke_saga
// ---------------------------------------------------------------------------

func TestReferenceExtractor_InvokeSaga_PositionalArg(t *testing.T) {
	refs, err := parseAndExtractFull(t, `invoke_saga("payment.process", params={})`)
	require.NoError(t, err)

	require.Len(t, refs, 1)
	assert.Equal(t, ReferenceTypeSaga, refs[0].Type)
	assert.Equal(t, "payment.process", refs[0].Key)
}

func TestReferenceExtractor_InvokeSaga_KeywordArg(t *testing.T) {
	refs, err := parseAndExtractFull(t, `invoke_saga(saga_name="fee.calculate", params={})`)
	require.NoError(t, err)

	require.Len(t, refs, 1)
	assert.Equal(t, ReferenceTypeSaga, refs[0].Type)
	assert.Equal(t, "fee.calculate", refs[0].Key)
}

// ---------------------------------------------------------------------------
// step handler
// ---------------------------------------------------------------------------

func TestReferenceExtractor_StepHandler(t *testing.T) {
	script := `step(action="position_keeping.initiate_log", params={"amount": "100"})`
	refs, err := parseAndExtractFull(t, script)
	require.NoError(t, err)

	require.Len(t, refs, 1)
	assert.Equal(t, ReferenceTypeStepHandler, refs[0].Type)
	assert.Equal(t, "position_keeping.initiate_log", refs[0].Key)
	assert.True(t, refs[0].ParamsKnown)
	assert.Contains(t, refs[0].Params, "amount")
}

func TestReferenceExtractor_StepHandler_NoAction(t *testing.T) {
	// step() without action kwarg produces no reference.
	refs, err := parseAndExtractFull(t, `step(params={"amount": "100"})`)
	require.NoError(t, err)
	assert.Empty(t, refs)
}

func TestReferenceExtractor_StepHandler_VariableParams(t *testing.T) {
	script := `step(action="my.handler", params=my_dict)`
	refs, err := parseAndExtractFull(t, script)
	require.NoError(t, err)

	require.Len(t, refs, 1)
	assert.Equal(t, ReferenceTypeStepHandler, refs[0].Type)
	assert.False(t, refs[0].ParamsKnown, "params from variable should mark ParamsKnown=false")
}

// ---------------------------------------------------------------------------
// Attribute access
// ---------------------------------------------------------------------------

func TestReferenceExtractor_AttributeAccess(t *testing.T) {
	script := `val = resolve_instrument("GBP").attributes["precision"]`
	refs, err := parseAndExtractFull(t, script)
	require.NoError(t, err)

	// Should produce instrument ref AND attribute ref.
	var attrRefs []Reference
	for _, r := range refs {
		if r.Type == ReferenceTypeAttribute {
			attrRefs = append(attrRefs, r)
		}
	}
	require.Len(t, attrRefs, 1)
	assert.Equal(t, "precision", attrRefs[0].AttributeKey)
	assert.Equal(t, "GBP", attrRefs[0].InstrumentCode)
}

// ---------------------------------------------------------------------------
// Nested / compound statements
// ---------------------------------------------------------------------------

func TestReferenceExtractor_IfStatement(t *testing.T) {
	script := `
if True:
    x = resolve_instrument("GBP")
else:
    x = resolve_instrument("USD")
`
	refs, err := parseAndExtractFull(t, script)
	require.NoError(t, err)

	codes := make([]string, 0, len(refs))
	for _, r := range refs {
		if r.Type == ReferenceTypeInstrument {
			codes = append(codes, r.Key)
		}
	}
	assert.Contains(t, codes, "GBP")
	assert.Contains(t, codes, "USD")
}

func TestReferenceExtractor_ForStatement(t *testing.T) {
	script := `
for item in items:
    x = resolve_instrument("EUR")
`
	refs, err := parseAndExtractFull(t, script)
	require.NoError(t, err)

	require.Len(t, refs, 1)
	assert.Equal(t, "EUR", refs[0].Key)
}

func TestReferenceExtractor_DefStatement(t *testing.T) {
	script := `
def my_func():
    return resolve_instrument("CHF")
`
	refs, err := parseAndExtractFull(t, script)
	require.NoError(t, err)

	require.Len(t, refs, 1)
	assert.Equal(t, "CHF", refs[0].Key)
}

// ---------------------------------------------------------------------------
// Multiple references in one script
// ---------------------------------------------------------------------------

func TestReferenceExtractor_MultipleTypes(t *testing.T) {
	script := `
instrument = resolve_instrument("GBP")
account = resolve_account("clearing")
step(action="position_keeping.initiate_log", params={"amount": "0"})
invoke_saga("fee.calculate", params={})
`
	refs, err := parseAndExtractFull(t, script)
	require.NoError(t, err)

	typeCount := map[ReferenceType]int{}
	for _, r := range refs {
		typeCount[r.Type]++
	}
	assert.Equal(t, 1, typeCount[ReferenceTypeInstrument])
	assert.Equal(t, 1, typeCount[ReferenceTypeAccount])
	assert.Equal(t, 1, typeCount[ReferenceTypeStepHandler])
	assert.Equal(t, 1, typeCount[ReferenceTypeSaga])
}

// ---------------------------------------------------------------------------
// walkExpr edge cases
// ---------------------------------------------------------------------------

func TestReferenceExtractor_NilExpr_DoesNotPanic(t *testing.T) {
	e := &referenceExtractor{}
	// Should not panic.
	assert.NotPanics(t, func() {
		e.walkExpr(nil)
	})
}

func TestReferenceExtractor_EmptyScript_ProducesNoRefs(t *testing.T) {
	refs, err := parseAndExtractFull(t, ``)
	require.NoError(t, err)
	assert.Empty(t, refs)
}
