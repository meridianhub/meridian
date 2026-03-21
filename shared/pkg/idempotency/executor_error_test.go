package idempotency

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExecutorError_Error(t *testing.T) {
	key := Key{
		Namespace: "test-ns",
		Operation: "deposit",
		EntityID:  "acct-123",
	}
	execErr := &ExecutorError{
		Op:  "check",
		Key: key,
		Err: ErrResultNotFound,
	}

	msg := execErr.Error()
	assert.Contains(t, msg, "check")
	assert.Contains(t, msg, "test-ns")
	assert.Contains(t, msg, "result not found")
}

func TestExecutorError_UnwrapChain(t *testing.T) {
	inner := ErrOperationAlreadyProcessed
	execErr := &ExecutorError{
		Op:  "store",
		Key: Key{Namespace: "ns", Operation: "op", EntityID: "id"},
		Err: inner,
	}

	assert.ErrorIs(t, execErr, ErrOperationAlreadyProcessed)
	assert.Contains(t, execErr.Error(), "store")
	assert.Contains(t, execErr.Error(), "ns")
}
