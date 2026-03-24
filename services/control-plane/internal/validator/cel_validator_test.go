package validator

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractRef(t *testing.T) {
	tests := []struct {
		name  string
		ref   string
		want  string
	}{
		{"empty", "", ""},
		{"simple", "#/components/schemas/Foo", "Foo"},
		{"message ref", "#/components/messages/BarMessage", "BarMessage"},
		{"no slash", "Foo", "Foo"},
		{"single slash", "/Foo", "Foo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractRef(tc.ref)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseAsyncAPIFile_ValidDocument(t *testing.T) {
	yaml := `
asyncapi: "3.0.0"
channels:
  market_data_received:
    messages:
      PriceUpdate:
        $ref: "#/components/messages/PriceUpdateMessage"
components:
  messages:
    PriceUpdateMessage:
      payload:
        $ref: "#/components/schemas/PriceUpdatePayload"
  schemas:
    PriceUpdatePayload:
      properties:
        amount: {}
        currency: {}
        timestamp: {}
`
	schemas := make(map[string]map[string]bool)
	parseAsyncAPIFile([]byte(yaml), schemas)

	channelFields, ok := schemas["market_data_received"]
	require.True(t, ok, "expected channel to be parsed")
	assert.True(t, channelFields["amount"])
	assert.True(t, channelFields["currency"])
	assert.True(t, channelFields["timestamp"])
}

func TestParseAsyncAPIFile_InvalidYAML(t *testing.T) {
	schemas := make(map[string]map[string]bool)
	parseAsyncAPIFile([]byte("not: valid: yaml: {{{"), schemas)
	assert.Empty(t, schemas)
}

func TestParseAsyncAPIFile_EmptyDocument(t *testing.T) {
	schemas := make(map[string]map[string]bool)
	parseAsyncAPIFile([]byte("{}"), schemas)
	assert.Empty(t, schemas)
}

func TestParseAsyncAPIFile_MissingMessageRef(t *testing.T) {
	yaml := `
channels:
  my_channel:
    messages:
      MyMsg:
        $ref: "#/components/messages/DoesNotExist"
components:
  messages: {}
  schemas: {}
`
	schemas := make(map[string]map[string]bool)
	parseAsyncAPIFile([]byte(yaml), schemas)
	assert.Empty(t, schemas)
}

func TestValidateMappingCELExpression_Valid(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	result := &ValidationResult{Valid: true}
	v.validateMappingCELExpression("payload.amount > 0", "mappings[0].inbound_validation_cel", result)
	assert.Empty(t, result.Errors)
}

func TestValidateMappingCELExpression_UndeclaredReference(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	result := &ValidationResult{Valid: true}
	v.validateMappingCELExpression("paylod.amount > 0", "mappings[0].inbound_validation_cel", result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "CEL_UNDECLARED_REFERENCE", result.Errors[0].Code)
	assert.Contains(t, result.Errors[0].Suggestion, "payload")
}

func TestValidateMappingCELExpression_TooLong(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	result := &ValidationResult{Valid: true}
	longExpr := strings.Repeat("a", 2049)
	v.validateMappingCELExpression(longExpr, "mappings[0].inbound_validation_cel", result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "CEL_EXPRESSION_TOO_LONG", result.Errors[0].Code)
}

func TestValidateMappingCELExpression_SyntaxError(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	result := &ValidationResult{Valid: true}
	v.validateMappingCELExpression("payload.amount >>>", "mappings[0].inbound_validation_cel", result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "CEL_COMPILATION_ERROR", result.Errors[0].Code)
}

func TestExtractCELFieldRefs_BasicSelect(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	fields := extractCELFieldRefs("event.amount > 0 && event.currency == 'GBP'", v.eventFilterEnv)
	assert.Contains(t, fields, "amount")
	assert.Contains(t, fields, "currency")
}

func TestExtractCELFieldRefs_IndexAccess(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	// event["amount"] style access
	fields := extractCELFieldRefs(`event["amount"] > 0`, v.eventFilterEnv)
	assert.Contains(t, fields, "amount")
}

func TestExtractCELFieldRefs_InvalidExpression(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	fields := extractCELFieldRefs("not valid >>> cel", v.eventFilterEnv)
	assert.Nil(t, fields)
}

func TestValidateEventFilterCEL_Valid(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	result := &ValidationResult{Valid: true}
	v.validateEventFilterCEL("event.amount > 0", "sagas[0].filter", result)
	assert.Empty(t, result.Errors)
}

func TestValidateEventFilterCEL_TooLong(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	result := &ValidationResult{Valid: true}
	longExpr := strings.Repeat("a", 4097)
	v.validateEventFilterCEL(longExpr, "sagas[0].filter", result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "CEL_EXPRESSION_TOO_LONG", result.Errors[0].Code)
}

func TestValidateEventFilterCEL_UndeclaredReference(t *testing.T) {
	v, err := New()
	require.NoError(t, err)

	result := &ValidationResult{Valid: true}
	v.validateEventFilterCEL("evnt.amount > 0", "sagas[0].filter", result)

	require.Len(t, result.Errors, 1)
	assert.Equal(t, "CEL_UNDECLARED_REFERENCE", result.Errors[0].Code)
	assert.Contains(t, result.Errors[0].Suggestion, "event")
}
