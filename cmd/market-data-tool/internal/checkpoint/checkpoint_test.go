package checkpoint

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestCheckpointStatuses(t *testing.T) {
	assert.Equal(t, Status("RUNNING"), StatusRunning)
	assert.Equal(t, Status("COMPLETED"), StatusCompleted)
	assert.Equal(t, Status("FAILED"), StatusFailed)
	assert.Equal(t, Status("CANCELLED"), StatusCancelled)
}

func TestCheckpointErrors(t *testing.T) {
	assert.NotNil(t, ErrNilPool)
	assert.NotNil(t, ErrCheckpointNotFound)
	assert.NotNil(t, ErrDuplicateImport)
	assert.NotNil(t, ErrImportInProgress)
	assert.NotNil(t, ErrFileNotFound)
	assert.NotNil(t, ErrChecksumMismatch)
	assert.NotNil(t, ErrNilCheckpoint)
}

func TestNewManager_NilPool(t *testing.T) {
	m, err := NewManager(nil)
	assert.Nil(t, m)
	assert.True(t, errors.Is(err, ErrNilPool))
}

func TestCheckpoint_IncrementSuccess(t *testing.T) {
	cp := &Checkpoint{
		ManifestID:    uuid.New(),
		ProcessedRows: 0,
		SuccessCount:  0,
	}

	cp.IncrementSuccess(5)
	assert.Equal(t, 5, cp.SuccessCount)
	assert.Equal(t, 5, cp.ProcessedRows)
	assert.Equal(t, 5, cp.LastProcessedLine)

	cp.IncrementSuccess(3)
	assert.Equal(t, 8, cp.SuccessCount)
	assert.Equal(t, 8, cp.ProcessedRows)
	assert.Equal(t, 8, cp.LastProcessedLine)
}

func TestCheckpoint_IncrementFailure(t *testing.T) {
	cp := &Checkpoint{
		ManifestID:   uuid.New(),
		FailureCount: 0,
	}

	cp.IncrementFailure(2)
	assert.Equal(t, 2, cp.FailureCount)
	assert.Equal(t, 2, cp.ProcessedRows)
	assert.Equal(t, 2, cp.LastProcessedLine)
}

func TestCheckpoint_SetTotalRows(t *testing.T) {
	cp := &Checkpoint{}
	cp.SetTotalRows(1000)
	assert.Equal(t, 1000, cp.TotalRows)
}

func TestCheckpoint_Progress(t *testing.T) {
	t.Run("zero total rows returns 0", func(t *testing.T) {
		cp := &Checkpoint{TotalRows: 0, ProcessedRows: 5}
		assert.Equal(t, 0.0, cp.Progress())
	})

	t.Run("half processed returns 50", func(t *testing.T) {
		cp := &Checkpoint{TotalRows: 100, ProcessedRows: 50}
		assert.InDelta(t, 50.0, cp.Progress(), 0.001)
	})

	t.Run("fully processed returns 100", func(t *testing.T) {
		cp := &Checkpoint{TotalRows: 200, ProcessedRows: 200}
		assert.InDelta(t, 100.0, cp.Progress(), 0.001)
	})
}

func TestCheckpoint_IsResumable(t *testing.T) {
	tests := []struct {
		status     Status
		resumable  bool
	}{
		{StatusRunning, true},
		{StatusCancelled, true},
		{StatusFailed, true},
		{StatusCompleted, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			cp := &Checkpoint{Status: tt.status}
			assert.Equal(t, tt.resumable, cp.IsResumable())
		})
	}
}

func TestCheckpoint_IncrementSuccessAndFailure_Combined(t *testing.T) {
	cp := &Checkpoint{}

	cp.IncrementSuccess(10)
	cp.IncrementFailure(5)

	assert.Equal(t, 10, cp.SuccessCount)
	assert.Equal(t, 5, cp.FailureCount)
	assert.Equal(t, 15, cp.ProcessedRows)
	assert.Equal(t, 15, cp.LastProcessedLine)
}
