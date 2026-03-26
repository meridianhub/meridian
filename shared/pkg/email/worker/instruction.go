package worker

import (
	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/dispatch"
	"github.com/meridianhub/meridian/shared/pkg/email"
)

// Verify OutboxInstruction satisfies DispatchableInstruction at compile time.
var _ dispatch.DispatchableInstruction = (*OutboxInstruction)(nil)

// OutboxInstruction adapts an email.OutboxEntry to the dispatch.DispatchableInstruction
// interface so it can be processed by the generic dispatch.Worker poll loop.
type OutboxInstruction struct {
	Entry email.OutboxEntry
}

func (o *OutboxInstruction) GetID() string            { return o.Entry.ID.String() }
func (o *OutboxInstruction) GetTenantID() string       { return o.Entry.TenantID }
func (o *OutboxInstruction) GetInstructionType() string { return o.Entry.TemplateName }
func (o *OutboxInstruction) GetConnectionID() string    { return "email" }
func (o *OutboxInstruction) GetStatus() dispatch.InstructionStatus {
	return outboxToDispatchStatus(o.Entry.Status)
}
func (o *OutboxInstruction) GetAttemptCount() int { return o.Entry.Attempts }
func (o *OutboxInstruction) GetMaxAttempts() int  { return o.Entry.MaxAttempts }
func (o *OutboxInstruction) CanRetry() bool       { return o.Entry.Attempts < o.Entry.MaxAttempts }

func outboxToDispatchStatus(s email.OutboxStatus) dispatch.InstructionStatus {
	switch s {
	case email.StatusPending:
		return dispatch.InstructionStatusPending
	case email.StatusSending:
		return dispatch.InstructionStatusDispatching
	case email.StatusSent:
		return dispatch.InstructionStatusDelivered
	case email.StatusFailed:
		return dispatch.InstructionStatusRetrying
	case email.StatusDeadLetter:
		return dispatch.InstructionStatusFailed
	case email.StatusCancelled:
		return dispatch.InstructionStatusCancelled
	default:
		return dispatch.InstructionStatusPending
	}
}

// OutboxEntryID is a helper to parse the UUID from an OutboxInstruction.
func OutboxEntryID(instr *OutboxInstruction) uuid.UUID {
	return instr.Entry.ID
}
