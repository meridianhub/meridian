package dispatch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInstructionStatusConstants(t *testing.T) {
	assert.Equal(t, InstructionStatus("PENDING"), InstructionStatusPending)
	assert.Equal(t, InstructionStatus("DISPATCHING"), InstructionStatusDispatching)
	assert.Equal(t, InstructionStatus("DELIVERED"), InstructionStatusDelivered)
	assert.Equal(t, InstructionStatus("ACKNOWLEDGED"), InstructionStatusAcknowledged)
	assert.Equal(t, InstructionStatus("RETRYING"), InstructionStatusRetrying)
	assert.Equal(t, InstructionStatus("FAILED"), InstructionStatusFailed)
	assert.Equal(t, InstructionStatus("EXPIRED"), InstructionStatusExpired)
	assert.Equal(t, InstructionStatus("CANCELLED"), InstructionStatusCancelled)
}

func TestInstructionStatus_IsTerminal(t *testing.T) {
	terminal := []InstructionStatus{
		InstructionStatusAcknowledged,
		InstructionStatusFailed,
		InstructionStatusExpired,
		InstructionStatusCancelled,
	}
	for _, s := range terminal {
		assert.True(t, s.IsTerminal(), "expected %s to be terminal", s)
	}

	nonTerminal := []InstructionStatus{
		InstructionStatusPending,
		InstructionStatusDispatching,
		InstructionStatusDelivered,
		InstructionStatusRetrying,
	}
	for _, s := range nonTerminal {
		assert.False(t, s.IsTerminal(), "expected %s to be non-terminal", s)
	}
}

// mockInstruction verifies that the DispatchableInstruction interface is implementable.
type mockInstruction struct {
	id              string
	tenantID        string
	instructionType string
	connectionID    string
	status          InstructionStatus
	attemptCount    int
	maxAttempts     int
}

func (m *mockInstruction) GetID() string                { return m.id }
func (m *mockInstruction) GetTenantID() string          { return m.tenantID }
func (m *mockInstruction) GetInstructionType() string   { return m.instructionType }
func (m *mockInstruction) GetConnectionID() string      { return m.connectionID }
func (m *mockInstruction) GetStatus() InstructionStatus { return m.status }
func (m *mockInstruction) GetAttemptCount() int         { return m.attemptCount }
func (m *mockInstruction) GetMaxAttempts() int          { return m.maxAttempts }
func (m *mockInstruction) CanRetry() bool               { return m.attemptCount < m.maxAttempts }

// Compile-time check that mockInstruction implements DispatchableInstruction.
var _ DispatchableInstruction = (*mockInstruction)(nil)

func TestDispatchableInstruction_Interface(t *testing.T) {
	instr := &mockInstruction{
		id:              "instr-1",
		tenantID:        "tenant-a",
		instructionType: "payment.initiate",
		connectionID:    "conn-1",
		status:          InstructionStatusPending,
		attemptCount:    1,
		maxAttempts:     3,
	}

	assert.Equal(t, "instr-1", instr.GetID())
	assert.Equal(t, "tenant-a", instr.GetTenantID())
	assert.Equal(t, "payment.initiate", instr.GetInstructionType())
	assert.Equal(t, "conn-1", instr.GetConnectionID())
	assert.Equal(t, InstructionStatusPending, instr.GetStatus())
	assert.Equal(t, 1, instr.GetAttemptCount())
	assert.Equal(t, 3, instr.GetMaxAttempts())
	assert.True(t, instr.CanRetry())
}

func TestDispatchableInstruction_CanRetryExhausted(t *testing.T) {
	instr := &mockInstruction{
		attemptCount: 3,
		maxAttempts:  3,
	}
	assert.False(t, instr.CanRetry())
}
