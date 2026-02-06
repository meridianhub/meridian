package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Tests for previously uncovered metric recording functions.
// These verify the functions don't panic and exercise the Prometheus counter paths.

func TestRecordSuspend(t *testing.T) {
	assert.NotPanics(t, func() {
		RecordSuspend()
	})
}

func TestRecordResume(t *testing.T) {
	assert.NotPanics(t, func() {
		RecordResume()
	})
}

func TestRecordResumeIdempotent(t *testing.T) {
	assert.NotPanics(t, func() {
		RecordResumeIdempotent()
	})
}

func TestRecordOrphanScanError(t *testing.T) {
	assert.NotPanics(t, func() {
		RecordOrphanScanError()
	})
}

func TestRecordStepFailure_Placeholder(t *testing.T) {
	// RecordStepFailure is a placeholder that currently does nothing.
	// Verify it doesn't panic.
	assert.NotPanics(t, func() {
		RecordStepFailure("step_name", "FATAL")
	})
}
