package persistence

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ========== JSONB Value/Scan ==========

func TestJSONB_Value_NilMap(t *testing.T) {
	var j JSONB
	v, err := j.Value()
	require.NoError(t, err)
	assert.Equal(t, "{}", v)
}

func TestJSONB_Value_NonNilMap(t *testing.T) {
	j := JSONB{"key": "value"}
	v, err := j.Value()
	require.NoError(t, err)
	assert.Contains(t, v, "key")
}

func TestJSONB_Scan_Nil(t *testing.T) {
	var j JSONB
	require.NoError(t, j.Scan(nil))
	assert.Nil(t, j)
}

func TestJSONB_Scan_Bytes(t *testing.T) {
	var j JSONB
	require.NoError(t, j.Scan([]byte(`{"a":"b"}`)))
	assert.Equal(t, "b", j["a"])
}

func TestJSONB_Scan_String(t *testing.T) {
	var j JSONB
	require.NoError(t, j.Scan(`{"x":1}`))
	assert.Equal(t, float64(1), j["x"])
}

func TestJSONB_Scan_UnsupportedType(t *testing.T) {
	var j JSONB
	err := j.Scan(42)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidJSONScan)
}

// ========== JSONBString Value/Scan ==========

func TestJSONBString_Value_NilMap(t *testing.T) {
	var j JSONBString
	v, err := j.Value()
	require.NoError(t, err)
	assert.Nil(t, v)
}

func TestJSONBString_Value_NonNilMap(t *testing.T) {
	j := JSONBString{"key": "value"}
	v, err := j.Value()
	require.NoError(t, err)
	assert.Contains(t, v, "key")
}

func TestJSONBString_Scan_Nil(t *testing.T) {
	var j JSONBString
	require.NoError(t, j.Scan(nil))
	assert.Nil(t, j)
}

func TestJSONBString_Scan_Bytes(t *testing.T) {
	var j JSONBString
	require.NoError(t, j.Scan([]byte(`{"a":"b"}`)))
	assert.Equal(t, "b", j["a"])
}

func TestJSONBString_Scan_String(t *testing.T) {
	var j JSONBString
	require.NoError(t, j.Scan(`{"x":"y"}`))
	assert.Equal(t, "y", j["x"])
}

func TestJSONBString_Scan_UnsupportedType(t *testing.T) {
	var j JSONBString
	err := j.Scan(42)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidJSONScan)
}

// ========== priorityToInt / intToPriority ==========

func TestPriorityToInt_AllValues(t *testing.T) {
	tests := []struct {
		priority string
		expected int16
	}{
		{"LOW", 1},
		{"NORMAL", 2},
		{"HIGH", 3},
		{"CRITICAL", 4},
		{"BOGUS", 2}, // default to NORMAL
	}

	for _, tt := range tests {
		t.Run(tt.priority, func(t *testing.T) {
			assert.Equal(t, tt.expected, priorityToInt(tt.priority))
		})
	}
}

func TestIntToPriority_AllValues(t *testing.T) {
	tests := []struct {
		input    int16
		expected string
	}{
		{1, "LOW"},
		{2, "NORMAL"},
		{3, "HIGH"},
		{4, "CRITICAL"},
		{99, "NORMAL"}, // default to NORMAL
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, intToPriority(tt.input))
		})
	}
}

// ========== nullableString / derefString ==========

func TestNullableString_Empty(t *testing.T) {
	assert.Nil(t, nullableString(""))
}

func TestNullableString_NonEmpty(t *testing.T) {
	p := nullableString("hello")
	require.NotNil(t, p)
	assert.Equal(t, "hello", *p)
}

func TestDerefString_Nil(t *testing.T) {
	assert.Equal(t, "", derefString(nil))
}

func TestDerefString_NonNil(t *testing.T) {
	s := "hello"
	assert.Equal(t, "hello", derefString(&s))
}

// ========== TableName ==========

func TestInstructionEntity_TableName(t *testing.T) {
	assert.Equal(t, "instructions", InstructionEntity{}.TableName())
}

func TestInstructionAttemptEntity_TableName(t *testing.T) {
	assert.Equal(t, "instruction_attempts", InstructionAttemptEntity{}.TableName())
}

func TestConnectionEntity_TableName(t *testing.T) {
	assert.Equal(t, "provider_connections", ConnectionEntity{}.TableName())
}

// ========== AuthConfigJSON Value/Scan ==========

func TestAuthConfigJSON_Value(t *testing.T) {
	a := AuthConfigJSON{AuthType: "api_key", HeaderName: "X-Key", SecretRef: "ref"}
	v, err := a.Value()
	require.NoError(t, err)
	assert.Contains(t, v, "api_key")
}

func TestAuthConfigJSON_Scan_Nil(t *testing.T) {
	var a AuthConfigJSON
	require.NoError(t, a.Scan(nil))
}

func TestAuthConfigJSON_Scan_Bytes(t *testing.T) {
	var a AuthConfigJSON
	require.NoError(t, a.Scan([]byte(`{"auth_type":"basic","username":"u"}`)))
	assert.Equal(t, "basic", a.AuthType)
	assert.Equal(t, "u", a.Username)
}

func TestAuthConfigJSON_Scan_String(t *testing.T) {
	var a AuthConfigJSON
	require.NoError(t, a.Scan(`{"auth_type":"oauth2","token_url":"https://t"}`))
	assert.Equal(t, "oauth2", a.AuthType)
	assert.Equal(t, "https://t", a.TokenURL)
}

func TestAuthConfigJSON_Scan_UnsupportedType(t *testing.T) {
	var a AuthConfigJSON
	err := a.Scan(42)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidJSONScan)
}

// ========== RetryPolicyJSON Value/Scan ==========

func TestRetryPolicyJSON_Value(t *testing.T) {
	r := RetryPolicyJSON{MaxAttempts: 3, BackoffMultiplier: 2.0}
	v, err := r.Value()
	require.NoError(t, err)
	assert.Contains(t, v, "max_attempts")
}

func TestRetryPolicyJSON_Scan_Nil(t *testing.T) {
	var r RetryPolicyJSON
	require.NoError(t, r.Scan(nil))
}

func TestRetryPolicyJSON_Scan_Bytes(t *testing.T) {
	var r RetryPolicyJSON
	require.NoError(t, r.Scan([]byte(`{"max_attempts":5}`)))
	assert.Equal(t, 5, r.MaxAttempts)
}

func TestRetryPolicyJSON_Scan_String(t *testing.T) {
	var r RetryPolicyJSON
	require.NoError(t, r.Scan(`{"backoff_multiplier":3.0}`))
	assert.Equal(t, 3.0, r.BackoffMultiplier)
}

func TestRetryPolicyJSON_Scan_UnsupportedType(t *testing.T) {
	var r RetryPolicyJSON
	err := r.Scan(42)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidJSONScan)
}

// ========== RateLimitJSON Value/Scan ==========

func TestRateLimitJSON_Value(t *testing.T) {
	r := RateLimitJSON{RequestsPerSecond: 100, BurstSize: 50}
	v, err := r.Value()
	require.NoError(t, err)
	assert.Contains(t, v, "requests_per_second")
}

func TestRateLimitJSON_Scan_Nil(t *testing.T) {
	var r RateLimitJSON
	require.NoError(t, r.Scan(nil))
}

func TestRateLimitJSON_Scan_Bytes(t *testing.T) {
	var r RateLimitJSON
	require.NoError(t, r.Scan([]byte(`{"requests_per_second":100}`)))
	assert.Equal(t, 100.0, r.RequestsPerSecond)
}

func TestRateLimitJSON_Scan_String(t *testing.T) {
	var r RateLimitJSON
	require.NoError(t, r.Scan(`{"burst_size":50}`))
	assert.Equal(t, 50, r.BurstSize)
}

func TestRateLimitJSON_Scan_UnsupportedType(t *testing.T) {
	var r RateLimitJSON
	err := r.Scan(42)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidJSONScan)
}
