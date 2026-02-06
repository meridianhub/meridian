package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidSagaStatuses_ReturnsAllStatuses(t *testing.T) {
	statuses := ValidSagaStatuses()
	require.Len(t, statuses, 9)

	expected := []SagaStatus{
		SagaStatusPending,
		SagaStatusRunning,
		SagaStatusWaitingForEvent,
		SagaStatusCompleted,
		SagaStatusCompensating,
		SagaStatusCompensated,
		SagaStatusFailed,
		SagaStatusFailedManualIntervention,
		SagaStatusSuspended,
	}
	assert.Equal(t, expected, statuses)
}

func TestIsValidSagaStatus(t *testing.T) {
	t.Run("valid statuses return true", func(t *testing.T) {
		for _, status := range ValidSagaStatuses() {
			assert.True(t, IsValidSagaStatus(status), "expected %q to be valid", status)
		}
	})

	t.Run("invalid statuses return false", func(t *testing.T) {
		assert.False(t, IsValidSagaStatus(SagaStatus("INVALID")))
		assert.False(t, IsValidSagaStatus(SagaStatus("")))
		assert.False(t, IsValidSagaStatus(SagaStatus("running"))) // lowercase
	})
}

func TestJSONB_Value_NilReturnsEmptyObject(t *testing.T) {
	var j JSONB
	val, err := j.Value()
	require.NoError(t, err)
	assert.Equal(t, []byte("{}"), val)
}

func TestJSONB_Value_NonNilMarshals(t *testing.T) {
	j := JSONB{"key": "value", "num": float64(42)}
	val, err := j.Value()
	require.NoError(t, err)
	assert.NotNil(t, val)
}

func TestJSONB_Scan_NilSetsNil(t *testing.T) {
	j := JSONB{"existing": "data"}
	err := j.Scan(nil)
	require.NoError(t, err)
	assert.Nil(t, j)
}

func TestJSONB_Scan_Bytes(t *testing.T) {
	var j JSONB
	err := j.Scan([]byte(`{"key":"value"}`))
	require.NoError(t, err)
	assert.Equal(t, "value", j["key"])
}

func TestJSONB_Scan_String(t *testing.T) {
	var j JSONB
	err := j.Scan(`{"key":"value"}`)
	require.NoError(t, err)
	assert.Equal(t, "value", j["key"])
}

func TestJSONB_Scan_UnsupportedType(t *testing.T) {
	var j JSONB
	err := j.Scan(42)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedJSONBType)
}

func TestSagaInstance_TableName(t *testing.T) {
	assert.Equal(t, "saga_instances", SagaInstance{}.TableName())
}

func TestSagaStepResult_TableName(t *testing.T) {
	assert.Equal(t, "saga_step_results", SagaStepResult{}.TableName())
}
