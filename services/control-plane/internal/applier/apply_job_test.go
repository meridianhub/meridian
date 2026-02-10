package applier

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyJobStatus_Constants(t *testing.T) {
	assert.Equal(t, ApplyJobStatus("PENDING"), ApplyJobStatusPending)
	assert.Equal(t, ApplyJobStatus("APPLYING"), ApplyJobStatusApplying)
	assert.Equal(t, ApplyJobStatus("APPLIED"), ApplyJobStatusApplied)
	assert.Equal(t, ApplyJobStatus("FAILED"), ApplyJobStatusFailed)
}

func TestApplyJob_Fields(t *testing.T) {
	sagaID := uuid.New()
	job := &ApplyJob{
		ID:              uuid.New(),
		ManifestVersion: 42,
		SagaExecutionID: &sagaID,
		Status:          ApplyJobStatusPending,
		Error:           "",
	}

	assert.NotEqual(t, uuid.Nil, job.ID)
	assert.Equal(t, 42, job.ManifestVersion)
	assert.Equal(t, &sagaID, job.SagaExecutionID)
	assert.Equal(t, ApplyJobStatusPending, job.Status)
	assert.Empty(t, job.Error)
}

func TestNewApplyJobRepository(t *testing.T) {
	repo := NewApplyJobRepository(nil)
	require.NotNil(t, repo)
}

func TestApplyJobStatus_StringRepresentation(t *testing.T) {
	tests := []struct {
		status   ApplyJobStatus
		expected string
	}{
		{ApplyJobStatusPending, "PENDING"},
		{ApplyJobStatusApplying, "APPLYING"},
		{ApplyJobStatusApplied, "APPLIED"},
		{ApplyJobStatusFailed, "FAILED"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.status))
		})
	}
}

func TestApplyJob_NilSagaExecutionID(t *testing.T) {
	job := &ApplyJob{
		ID:              uuid.New(),
		ManifestVersion: 1,
		Status:          ApplyJobStatusPending,
	}

	assert.Nil(t, job.SagaExecutionID)
	assert.Nil(t, job.CompletedAt)
}
