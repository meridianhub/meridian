package schema

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseRetryPolicy_HappyPath verifies that a retry block under a handler
// parses into the typed RetryPolicy struct.
func TestParseRetryPolicy_HappyPath(t *testing.T) {
	yaml := `
service: test
version: "1.0"
handlers:
  external.flaky_call:
    description: "External call with custom retry"
    compensate: external.cancel_call
    proto_ref:
      proto_rpc: "test.v1.Service/Method"
    retry:
      base_delay: 2s
      max_delay: 30s
`

	schema, err := Parse([]byte(yaml))
	require.NoError(t, err)

	h := schema.Handlers["external.flaky_call"]
	require.NotNil(t, h)
	require.NotNil(t, h.Retry, "retry block should parse into typed struct")
	assert.Equal(t, 2*time.Second, h.Retry.BaseDelay)
	assert.Equal(t, 30*time.Second, h.Retry.MaxDelay)
}

// TestParseRetryPolicy_Omitted verifies that handlers without a retry block
// produce a nil RetryPolicy so callers fall back to global defaults.
func TestParseRetryPolicy_Omitted(t *testing.T) {
	yaml := `
service: test
version: "1.0"
handlers:
  internal.handler:
    description: "Handler with no retry override"
    compensate: internal.undo
    proto_ref:
      proto_rpc: "test.v1.Service/Method"
`

	schema, err := Parse([]byte(yaml))
	require.NoError(t, err)
	require.NotNil(t, schema.Handlers["internal.handler"])
	assert.Nil(t, schema.Handlers["internal.handler"].Retry, "missing retry block should be nil")
}

// TestParseRetryPolicy_RejectsNegativeBaseDelay enforces the validator: a negative
// base_delay would produce nonsense delays at runtime.
func TestParseRetryPolicy_RejectsNegativeBaseDelay(t *testing.T) {
	yaml := `
service: test
version: "1.0"
handlers:
  bad.handler:
    description: "Bad retry config"
    compensate: bad.undo
    proto_ref:
      proto_rpc: "test.v1.Service/Method"
    retry:
      base_delay: -1s
      max_delay: 30s
`

	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidRetryPolicy)
}

// TestParseRetryPolicy_RejectsBaseDelayAboveMaxDelay catches the inversion error.
func TestParseRetryPolicy_RejectsBaseDelayAboveMaxDelay(t *testing.T) {
	yaml := `
service: test
version: "1.0"
handlers:
  bad.handler:
    description: "Inverted retry config"
    compensate: bad.undo
    proto_ref:
      proto_rpc: "test.v1.Service/Method"
    retry:
      base_delay: 60s
      max_delay: 10s
`

	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidRetryPolicy)
}
