package saga

import (
	"testing"

	pkgsaga "github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/stretchr/testify/assert"
)

func TestSimilarity_IdenticalStrings(t *testing.T) {
	score := similarity("hello", "hello")
	assert.Equal(t, 10, score) // len("hello") * 2
}

func TestSimilarity_EmptyStrings(t *testing.T) {
	score := similarity("", "")
	assert.Equal(t, 0, score) // len("") * 2 = 0
}

func TestSimilarity_CommonPrefix(t *testing.T) {
	score := similarity("hello_world", "hello_earth")
	assert.Greater(t, score, 0, "common prefix should contribute to score")
}

func TestSimilarity_CommonSuffix(t *testing.T) {
	score := similarity("foo_handler", "bar_handler")
	assert.Greater(t, score, 0, "common suffix should contribute to score")
}

func TestSimilarity_SubstringMatch(t *testing.T) {
	score := similarity("handler", "step_handler_v2")
	assert.Greater(t, score, 0, "substring match should increase score")
}

func TestSimilarity_CompletelyDifferent(t *testing.T) {
	score := similarity("abc", "xyz")
	assert.Equal(t, 0, score, "no common prefix/suffix/substring should yield 0")
}

func TestFindSimilar_EmptyCandidates(t *testing.T) {
	result := findSimilar("hello", nil)
	assert.Equal(t, "", result)

	result = findSimilar("hello", []string{})
	assert.Equal(t, "", result)
}

func TestFindSimilar_ExactMatch(t *testing.T) {
	result := findSimilar("hello", []string{"world", "hello", "hi"})
	assert.Equal(t, "hello", result)
}

func TestFindSimilar_CaseInsensitive(t *testing.T) {
	result := findSimilar("Hello", []string{"world", "hello", "hi"})
	assert.Equal(t, "hello", result)
}

func TestFindSimilar_NoGoodMatch(t *testing.T) {
	result := findSimilar("xyz", []string{"abcdefgh", "ijklmnop"})
	assert.Equal(t, "", result, "no sufficiently similar candidate should return empty")
}

func TestFindSimilar_BestMatchSelected(t *testing.T) {
	result := findSimilar("position_keeping", []string{
		"position_taking",
		"account_keeping",
		"totally_different",
	})
	// "position_taking" shares more prefix with "position_keeping"
	assert.Equal(t, "position_taking", result)
}

func TestSuggestAttribute_EmptySchema(t *testing.T) {
	v := &ReferenceValidator{}
	result := v.suggestAttribute(map[string]interface{}{}, "key")
	assert.Equal(t, "", result)
}

func TestSuggestAttribute_HasMatch(t *testing.T) {
	v := &ReferenceValidator{}
	schema := map[string]interface{}{
		"status":     "string",
		"balance":    "decimal",
		"created_at": "timestamp",
	}
	result := v.suggestAttribute(schema, "statu") // close to "status"
	assert.Contains(t, result, "status")
}

func TestSuggestHandler_EmptyRegistry(t *testing.T) {
	reg := pkgsaga.NewHandlerRegistry()
	v := &ReferenceValidator{handlerRegistry: reg}
	result := v.suggestHandler("nonexistent_handler")
	assert.Equal(t, "", result)
}

func TestSuggestInstrument_NilChecker(t *testing.T) {
	v := &ReferenceValidator{instrumentChecker: nil}
	result := v.suggestInstrument(nil, "GBX")
	assert.Equal(t, "", result)
}

func TestSuggestSaga_NilChecker(t *testing.T) {
	v := &ReferenceValidator{definitionChecker: nil}
	result := v.suggestSaga(nil, "payment.initiate")
	assert.Equal(t, "", result)
}
