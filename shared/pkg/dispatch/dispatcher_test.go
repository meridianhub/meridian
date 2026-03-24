package dispatch

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResult_zero_value(t *testing.T) {
	var r Result
	assert.Equal(t, 0, r.StatusCode)
	assert.Nil(t, r.ResponseBody)
	assert.Nil(t, r.Outcome)
	assert.Equal(t, time.Duration(0), r.Duration)
	assert.NoError(t, r.Error)
}

func TestResult_with_fields(t *testing.T) {
	r := Result{
		StatusCode:   200,
		ResponseBody: []byte(`{"id":"abc"}`),
		Outcome: &Outcome{
			ExternalID:     "abc",
			ProviderStatus: "ACCEPTED",
			ShouldRetry:    false,
			FailureReason:  "",
		},
		Duration: 150 * time.Millisecond,
		Error:    nil,
	}

	assert.Equal(t, 200, r.StatusCode)
	assert.Equal(t, "abc", r.Outcome.ExternalID)
	assert.Equal(t, "ACCEPTED", r.Outcome.ProviderStatus)
	assert.False(t, r.Outcome.ShouldRetry)
	assert.Empty(t, r.Outcome.FailureReason)
}

func TestResult_with_error(t *testing.T) {
	r := Result{
		StatusCode: 0,
		Error:      errors.New("connection refused"),
	}

	assert.Error(t, r.Error)
	assert.Nil(t, r.Outcome)
}

func TestOutcome_retry(t *testing.T) {
	o := Outcome{
		ProviderStatus: "PENDING",
		ShouldRetry:    true,
	}
	assert.True(t, o.ShouldRetry)
	assert.Empty(t, o.FailureReason)
}

func TestOutcome_permanent_failure(t *testing.T) {
	o := Outcome{
		ProviderStatus: "REJECTED",
		ShouldRetry:    false,
		FailureReason:  "insufficient funds",
	}
	assert.False(t, o.ShouldRetry)
	assert.Equal(t, "insufficient funds", o.FailureReason)
}
