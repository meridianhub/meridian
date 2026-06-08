package generator_test

import (
	"testing"

	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
	"github.com/stretchr/testify/assert"
)

// --- renameKwargs (direct unit tests of the kwarg-renaming walker) ---

func TestRenameKwargs_Table(t *testing.T) {
	reverse := map[string]string{
		"amount":    "quantity",
		"currency":  "instrument_code",
		"direction": "side",
	}

	tests := []struct {
		name     string
		callBody string
		want     string
	}{
		{
			name:     "renames all top-level kwargs",
			callBody: "amount=100, currency='GBP', direction='CREDIT')",
			want:     "quantity=100, instrument_code='GBP', side='CREDIT')",
		},
		{
			name:     "leaves unmapped kwarg untouched",
			callBody: "amount=1, keep_me=2)",
			want:     "quantity=1, keep_me=2)",
		},
		{
			name:     "does not rename inside nested call (depth>0)",
			callBody: "amount=fn(currency='x'))",
			want:     "quantity=fn(currency='x'))",
		},
		{
			name:     "does not treat == comparison as a kwarg",
			callBody: "amount == 5, direction='CREDIT')",
			want:     "amount == 5, side='CREDIT')",
		},
		{
			name:     "skips kwarg name inside string literal",
			callBody: `note="amount=99", amount=1)`,
			want:     `note="amount=99", quantity=1)`,
		},
		{
			name:     "skips kwarg name inside line comment",
			callBody: "amount=1, # currency='x'\n direction='DEBIT')",
			want:     "quantity=1, # currency='x'\n side='DEBIT')",
		},
		{
			name:     "no mapped kwargs leaves body unchanged",
			callBody: "foo=1, bar=2)",
			want:     "foo=1, bar=2)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generator.RenameKwargs(tt.callBody, reverse)
			assert.Equal(t, tt.want, got)
		})
	}
}

// --- splitAtMatchingParen: comment and triple-quote arms ---

func TestSplitAtMatchingParen_Table(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantBody string
		wantRest string
	}{
		{
			name:     "simple body",
			input:    "a=1, b=2) tail",
			wantBody: "a=1, b=2)",
			wantRest: " tail",
		},
		{
			name:     "nested parens",
			input:    "a=fn(1, 2), b=3) rest",
			wantBody: "a=fn(1, 2), b=3)",
			wantRest: " rest",
		},
		{
			name:     "close paren inside string is ignored",
			input:    `msg="a)b") rest`,
			wantBody: `msg="a)b")`,
			wantRest: " rest",
		},
		{
			name:     "close paren inside line comment is ignored",
			input:    "a=1, # )\nb=2) rest",
			wantBody: "a=1, # )\nb=2)",
			wantRest: " rest",
		},
		{
			name:     "unbalanced returns whole string and empty rest",
			input:    "a=1, b=2",
			wantBody: "a=1, b=2",
			wantRest: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, rest := generator.SplitAtMatchingParen(tt.input)
			assert.Equal(t, tt.wantBody, body)
			assert.Equal(t, tt.wantRest, rest)
		})
	}
}

// --- advancePastString: unterminated literals run to end of input ---

func TestAdvancePastString_Table(t *testing.T) {
	tests := []struct {
		name  string
		input string
		start int
		want  int
	}{
		{name: "double quoted terminated", input: `"abc" rest`, start: 0, want: 5},
		{name: "single quoted terminated", input: `'abc' rest`, start: 0, want: 5},
		{name: "escaped quote inside string", input: `"a\"b" x`, start: 0, want: 6},
		{name: "unterminated double quote runs to end", input: `"abc`, start: 0, want: 4},
		{name: "triple quoted terminated", input: `"""ab""" rest`, start: 0, want: 8},
		{name: "unterminated triple quote runs to end", input: `"""abc`, start: 0, want: 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, generator.AdvancePastString(tt.input, tt.start))
		})
	}
}

// --- findUnquotedComment: skip-over-string and not-found arms ---

func TestFindUnquotedComment_Table(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{name: "plain comment", input: "a=1 # note", want: 4},
		{name: "hash inside string is ignored", input: `msg="a#b" # real`, want: 10},
		{name: "no comment", input: "a=1, b=2", want: -1},
		{name: "only hash inside string yields none", input: `msg="#x"`, want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, generator.FindUnquotedComment(tt.input))
		})
	}
}

// --- injectMissingDefaults: multi-arg present set + nested-call paren ---

func TestInjectMissingDefaults_PreservesArgsWithNestedCall(t *testing.T) {
	// A nested call's own kwargs must not be treated as present at the top
	// level, so the missing top-level default is still injected.
	callBody := "amount=fn(direction='x'), keep=1)"
	defaults := map[string]string{"direction": `"CREDIT"`}
	result := generator.InjectMissingDefaults(callBody, defaults)

	assert.Contains(t, result, `direction="CREDIT"`)
	assert.Contains(t, result, "keep=1")
	assert.Contains(t, result, "amount=fn(direction='x')")
}

// TestInjectMissingDefaults_SpaceBeforeEquals exercises the whitespace-skip
// loop in collectTopLevelKwargNames where a kwarg has a space between the name
// and '=' ("amount =100"). The kwarg must still be detected as present.
func TestInjectMissingDefaults_SpaceBeforeEquals(t *testing.T) {
	callBody := "amount =100, direction ='DEBIT')"
	defaults := map[string]string{"direction": `"CREDIT"`}
	result := generator.InjectMissingDefaults(callBody, defaults)

	// direction is already present (with a space before '='), so the default
	// must not be injected.
	assert.NotContains(t, result, `direction="CREDIT"`)
	assert.Contains(t, result, "direction ='DEBIT'")
}

// TestInjectMissingDefaults_CommentInBody exercises the line-comment skip in
// collectTopLevelKwargNames (a '#' token inside the call body that is not part
// of any kwarg name).
func TestInjectMissingDefaults_CommentInBody(t *testing.T) {
	callBody := "amount=100, # direction goes here\n)"
	defaults := map[string]string{"direction": `"CREDIT"`}
	result := generator.InjectMissingDefaults(callBody, defaults)

	// "direction" appears only inside the comment, so it is not "present" and
	// the default is injected.
	assert.Contains(t, result, `direction="CREDIT"`)
	assert.Contains(t, result, "amount=100")
}
