// Package domain contains the Instruction aggregate root and related domain logic
// for orchestrating instruction delivery workflows in the operational gateway.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Instruction domain errors
var (
	ErrInvalidInstructionTransition = errors.New("invalid instruction status transition")
	ErrInstructionTerminal          = errors.New("instruction is in terminal state")
	ErrInstructionNotCancellable    = errors.New("instruction cannot be cancelled in current state")
	ErrMaxAttemptsExhausted         = errors.New("max dispatch attempts exhausted")
	ErrMissingInstructionType       = errors.New("instruction type is required")
	ErrMissingProviderConnectionID  = errors.New("provider connection ID is required")
	ErrMissingTenantID              = errors.New("tenant ID is required")
	ErrMissingPayload               = errors.New("payload is required")
	ErrMissingFailureReason         = errors.New("failure reason is required")
)

// InstructionStatus represents the lifecycle state of an instruction in the delivery saga.
type InstructionStatus string

// Instruction status constants.
//
// State Machine:
//
//	PENDING -> DISPATCHING -> DELIVERED -> ACKNOWLEDGED (happy path)
//
// Retry transitions (delivery failure):
//
//	DISPATCHING -> RETRYING -> DISPATCHING (retry loop, up to MaxAttempts)
//	DISPATCHING -> FAILED (non-retryable error, or retries exhausted)
//	RETRYING    -> FAILED (retries exhausted)
//
// Expiry (TTL exceeded):
//
//	PENDING  -> EXPIRED
//	RETRYING -> EXPIRED
//
// Cancellation (system or user initiated):
//
//	PENDING  -> CANCELLED
//	RETRYING -> CANCELLED
//
// Terminal states: ACKNOWLEDGED, FAILED, EXPIRED, CANCELLED
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

// Priority represents the dispatch priority of an instruction.
type Priority string

const (
	// PriorityLow is the lowest dispatch priority.
	PriorityLow Priority = "LOW"
	// PriorityNormal is the default dispatch priority.
	PriorityNormal Priority = "NORMAL"
	// PriorityHigh indicates elevated dispatch urgency.
	PriorityHigh Priority = "HIGH"
	// PriorityCritical requires immediate dispatch ahead of all other instructions.
	PriorityCritical Priority = "CRITICAL"
)

// InstructionAttempt records a single dispatch attempt and its outcome.
type InstructionAttempt struct {
	AttemptNumber int
	FailureReason string
	ErrorCode     string
	AttemptedAt   time.Time
}

// Instruction represents the aggregate root for the instruction delivery saga.
// It acts as the saga orchestrator for delivering instructions to external providers,
// managing retries and tracking delivery state.
//
// Note: Fields are exported for persistence layer access. State transitions should only be
// performed via the state machine methods which enforce invariants.
type Instruction struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	InstructionType      string
	ProviderConnectionID string
	CorrelationID        string
	CausationID          string
	Payload              map[string]any
	Metadata             map[string]string
	Priority             Priority
	Status               InstructionStatus
	ScheduledAt          *time.Time
	ExpiresAt            *time.Time
	DispatchedAt         *time.Time
	CompletedAt          *time.Time
	MaxAttempts          int
	AttemptCount         int
	Attempts             []InstructionAttempt
	FailureReason        string
	ErrorCode            string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// InstructionOption is a functional option for configuring an Instruction.
type InstructionOption func(*Instruction)

// WithPriority sets the dispatch priority of the instruction.
func WithPriority(p Priority) InstructionOption {
	return func(i *Instruction) {
		i.Priority = p
	}
}

// WithScheduledAt sets the time at which the instruction should first be dispatched.
func WithScheduledAt(t time.Time) InstructionOption {
	return func(i *Instruction) {
		i.ScheduledAt = &t
	}
}

// WithExpiresAt sets the TTL deadline after which the instruction will be expired.
func WithExpiresAt(t time.Time) InstructionOption {
	return func(i *Instruction) {
		i.ExpiresAt = &t
	}
}

// WithMetadata attaches arbitrary key-value metadata to the instruction.
func WithMetadata(m map[string]string) InstructionOption {
	return func(i *Instruction) {
		i.Metadata = m
	}
}

// WithCorrelationID sets the correlation ID for distributed tracing.
func WithCorrelationID(id string) InstructionOption {
	return func(i *Instruction) {
		i.CorrelationID = id
	}
}

// WithCausationID sets the causation ID linking this instruction to the event that created it.
func WithCausationID(id string) InstructionOption {
	return func(i *Instruction) {
		i.CausationID = id
	}
}

// WithMaxAttempts overrides the default maximum dispatch attempts.
func WithMaxAttempts(n int) InstructionOption {
	return func(i *Instruction) {
		i.MaxAttempts = n
	}
}

const defaultMaxAttempts = 3

// NewInstruction creates a new Instruction in PENDING status.
// Validates required fields and applies functional options.
func NewInstruction(
	tenantID uuid.UUID,
	instructionType string,
	providerConnectionID string,
	payload map[string]any,
	opts ...InstructionOption,
) (*Instruction, error) {
	if tenantID == uuid.Nil {
		return nil, ErrMissingTenantID
	}
	if instructionType == "" {
		return nil, ErrMissingInstructionType
	}
	if providerConnectionID == "" {
		return nil, ErrMissingProviderConnectionID
	}
	if payload == nil {
		return nil, ErrMissingPayload
	}

	now := time.Now()
	i := &Instruction{
		ID:                   uuid.New(),
		TenantID:             tenantID,
		InstructionType:      instructionType,
		ProviderConnectionID: providerConnectionID,
		Payload:              payload,
		Priority:             PriorityNormal,
		Status:               InstructionStatusPending,
		MaxAttempts:          defaultMaxAttempts,
		AttemptCount:         0,
		Attempts:             []InstructionAttempt{},
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	for _, opt := range opts {
		opt(i)
	}

	return i, nil
}

// MarkDispatching transitions the instruction from PENDING or RETRYING to DISPATCHING.
// This is called when the instruction is being actively sent to the provider.
// Increments AttemptCount to record that a new dispatch attempt has started.
// Returns ErrInvalidInstructionTransition if not in PENDING or RETRYING state.
func (i *Instruction) MarkDispatching() error {
	if i.Status != InstructionStatusPending && i.Status != InstructionStatusRetrying {
		return ErrInvalidInstructionTransition
	}

	now := time.Now()
	i.Status = InstructionStatusDispatching
	i.AttemptCount++
	i.DispatchedAt = &now
	i.UpdatedAt = now
	return nil
}

// MarkDelivered transitions the instruction from DISPATCHING to DELIVERED.
// This is called when the provider has confirmed receipt of the instruction.
// Returns ErrInvalidInstructionTransition if not in DISPATCHING state.
func (i *Instruction) MarkDelivered() error {
	if i.Status != InstructionStatusDispatching {
		return ErrInvalidInstructionTransition
	}

	i.Status = InstructionStatusDelivered
	i.UpdatedAt = time.Now()
	return nil
}

// MarkAcknowledged transitions the instruction from DELIVERED to ACKNOWLEDGED.
// This is called when the provider confirms the instruction has been processed.
// Returns ErrInvalidInstructionTransition if not in DELIVERED state.
func (i *Instruction) MarkAcknowledged() error {
	if i.Status != InstructionStatusDelivered {
		return ErrInvalidInstructionTransition
	}

	now := time.Now()
	i.Status = InstructionStatusAcknowledged
	i.CompletedAt = &now
	i.UpdatedAt = now
	return nil
}

// MarkRetrying transitions the instruction from DISPATCHING to RETRYING.
// Records the failed attempt and schedules a retry. AttemptCount is already
// incremented by MarkDispatching, so this method checks whether further
// retries remain before appending the attempt record.
// Returns ErrInvalidInstructionTransition if not in DISPATCHING state.
// Returns ErrMissingFailureReason if reason is empty.
// Returns ErrMaxAttemptsExhausted if no retry attempts remain.
func (i *Instruction) MarkRetrying(reason string, errorCode string) error {
	if i.Status != InstructionStatusDispatching {
		return ErrInvalidInstructionTransition
	}
	if reason == "" {
		return ErrMissingFailureReason
	}
	if i.AttemptCount >= i.MaxAttempts {
		return ErrMaxAttemptsExhausted
	}

	attempt := InstructionAttempt{
		AttemptNumber: i.AttemptCount,
		FailureReason: reason,
		ErrorCode:     errorCode,
		AttemptedAt:   time.Now(),
	}
	i.Attempts = append(i.Attempts, attempt)
	i.Status = InstructionStatusRetrying
	i.UpdatedAt = time.Now()
	return nil
}

// MarkFailed transitions the instruction to FAILED from DISPATCHING or RETRYING.
// This is called when the instruction has permanently failed delivery.
// Idempotent: Returns nil if already failed (preserving original reason).
// Returns ErrInstructionTerminal if in any other terminal state.
// Returns ErrMissingFailureReason if reason is empty.
func (i *Instruction) MarkFailed(reason string, errorCode string) error {
	if reason == "" {
		return ErrMissingFailureReason
	}

	// Idempotent: already failed
	if i.Status == InstructionStatusFailed {
		return nil
	}

	if i.IsTerminal() {
		return ErrInstructionTerminal
	}

	if i.Status != InstructionStatusDispatching && i.Status != InstructionStatusRetrying {
		return ErrInvalidInstructionTransition
	}

	now := time.Now()
	i.Status = InstructionStatusFailed
	i.FailureReason = reason
	i.ErrorCode = errorCode
	i.CompletedAt = &now
	i.UpdatedAt = now
	return nil
}

// MarkExpired transitions the instruction to EXPIRED.
// Valid from: PENDING, DISPATCHING, RETRYING (per proto state machine).
// DELIVERED instructions cannot expire (delivery is complete).
// This is called when the instruction's TTL has elapsed.
// Idempotent: Returns nil if already expired.
// Returns ErrInstructionTerminal if in a terminal state other than EXPIRED.
// Returns ErrInvalidInstructionTransition if in DELIVERED state.
func (i *Instruction) MarkExpired() error {
	// Idempotent: already expired
	if i.Status == InstructionStatusExpired {
		return nil
	}

	if i.IsTerminal() {
		return ErrInstructionTerminal
	}

	if i.Status == InstructionStatusDelivered {
		return ErrInvalidInstructionTransition
	}

	now := time.Now()
	i.Status = InstructionStatusExpired
	i.CompletedAt = &now
	i.UpdatedAt = now
	return nil
}

// Cancel transitions the instruction to CANCELLED.
// Per the proto state machine, only PENDING instructions can be cancelled
// (before dispatch has started). DISPATCHING, DELIVERED, and RETRYING instructions
// cannot be cancelled.
// Idempotent: Returns nil if already cancelled.
// Returns ErrInstructionNotCancellable if not in PENDING state.
func (i *Instruction) Cancel() error {
	// Idempotent: already cancelled
	if i.Status == InstructionStatusCancelled {
		return nil
	}

	if i.Status != InstructionStatusPending {
		return ErrInstructionNotCancellable
	}

	now := time.Now()
	i.Status = InstructionStatusCancelled
	i.CompletedAt = &now
	i.UpdatedAt = now
	return nil
}

// IsTerminal returns true if the instruction is in a terminal state.
// Terminal states: ACKNOWLEDGED, FAILED, EXPIRED, CANCELLED
func (i *Instruction) IsTerminal() bool {
	switch i.Status {
	case InstructionStatusPending,
		InstructionStatusDispatching,
		InstructionStatusDelivered,
		InstructionStatusRetrying:
		return false
	case InstructionStatusAcknowledged,
		InstructionStatusFailed,
		InstructionStatusExpired,
		InstructionStatusCancelled:
		return true
	}
	return false
}

// CanCancel returns true if the instruction can be cancelled.
// Per the proto state machine, only PENDING instructions can be cancelled.
func (i *Instruction) CanCancel() bool {
	return i.Status == InstructionStatusPending
}

// CanRetry returns true if the instruction can be retried.
// Only DISPATCHING instructions with remaining attempts are retryable.
func (i *Instruction) CanRetry() bool {
	return i.Status == InstructionStatusDispatching && i.AttemptCount < i.MaxAttempts
}

// NeedsDispatch returns true if the instruction should be picked up for dispatch.
// An instruction needs dispatch if it is PENDING or RETRYING, and its scheduled time
// (if set) has passed.
func (i *Instruction) NeedsDispatch() bool {
	if i.Status != InstructionStatusPending && i.Status != InstructionStatusRetrying {
		return false
	}
	if i.ScheduledAt != nil && time.Now().Before(*i.ScheduledAt) {
		return false
	}
	return true
}
