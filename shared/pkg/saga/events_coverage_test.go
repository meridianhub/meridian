package saga

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestStepCompletedEvent_GetCorrelationID(t *testing.T) {
	correlationID := uuid.New()
	event := &StepCompletedEvent{
		CorrelationID: correlationID,
	}
	assert.Equal(t, correlationID, event.GetCorrelationID())
}

func TestStepCompletedEvent_SagaID(t *testing.T) {
	sagaID := uuid.New()
	event := &StepCompletedEvent{
		SagaInstanceID: sagaID,
	}
	assert.Equal(t, sagaID, event.SagaID())
}

func TestStepFailedEvent_GetCorrelationID(t *testing.T) {
	correlationID := uuid.New()
	event := &StepFailedEvent{
		CorrelationID: correlationID,
	}
	assert.Equal(t, correlationID, event.GetCorrelationID())
}

func TestStepFailedEvent_SagaID(t *testing.T) {
	sagaID := uuid.New()
	event := &StepFailedEvent{
		SagaInstanceID: sagaID,
	}
	assert.Equal(t, sagaID, event.SagaID())
}

func TestProgressEvent_GetCorrelationID(t *testing.T) {
	correlationID := uuid.New()
	event := &ProgressEvent{
		CorrelationID: correlationID,
	}
	assert.Equal(t, correlationID, event.GetCorrelationID())
}
