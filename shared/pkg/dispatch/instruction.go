package dispatch

// InstructionStatus represents the lifecycle state of a dispatchable instruction.
type InstructionStatus string

// Instruction status constants define the lifecycle states.
const (
	InstructionStatusPending      InstructionStatus = "PENDING"
	InstructionStatusDispatching  InstructionStatus = "DISPATCHING"
	InstructionStatusDelivered    InstructionStatus = "DELIVERED"
	InstructionStatusAcknowledged InstructionStatus = "ACKNOWLEDGED"
	InstructionStatusRetrying     InstructionStatus = "RETRYING"
	InstructionStatusFailed       InstructionStatus = "FAILED"
	InstructionStatusExpired      InstructionStatus = "EXPIRED"
	InstructionStatusCancelled    InstructionStatus = "CANCELLED"
)

// IsTerminal returns true if the status represents a terminal state where no further
// transitions are possible.
func (s InstructionStatus) IsTerminal() bool {
	switch s {
	case InstructionStatusAcknowledged,
		InstructionStatusFailed,
		InstructionStatusExpired,
		InstructionStatusCancelled:
		return true
	case InstructionStatusPending,
		InstructionStatusDispatching,
		InstructionStatusDelivered,
		InstructionStatusRetrying:
		return false
	}
	return false
}

// DispatchableInstruction is the interface that any instruction type must implement
// to be processed by the generic dispatch worker. It provides the minimum information
// needed for routing, dispatching, and retry management.
type DispatchableInstruction interface {
	// GetID returns the unique identifier for this instruction.
	GetID() string
	// GetTenantID returns the tenant identifier that owns this instruction.
	GetTenantID() string
	// GetInstructionType returns the type of instruction (used for route resolution).
	GetInstructionType() string
	// GetConnectionID returns the target provider connection identifier.
	GetConnectionID() string
	// GetStatus returns the current lifecycle status.
	GetStatus() InstructionStatus
	// GetAttemptCount returns the number of dispatch attempts made so far.
	GetAttemptCount() int
	// GetMaxAttempts returns the maximum number of dispatch attempts allowed.
	GetMaxAttempts() int
	// CanRetry returns true if the instruction has remaining retry attempts.
	CanRetry() bool
}
