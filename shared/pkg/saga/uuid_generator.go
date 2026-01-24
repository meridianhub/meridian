// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"
)

// UUIDGenerator generates deterministic UUIDs using UUID v5 (SHA-1 namespace-based).
// It uses the saga instance ID as the namespace, ensuring UUIDs are unique per saga
// but deterministic for replay (FR-21, FR-26).
//
// The name format is "{StepIndex}:{CallIndex}" which allows multiple UUIDs to be
// generated within a single step while maintaining determinism on replay.
type UUIDGenerator struct {
	namespace uuid.UUID
}

// NewUUIDGenerator creates a new UUIDGenerator with the given namespace (saga instance ID).
func NewUUIDGenerator(namespace uuid.UUID) *UUIDGenerator {
	return &UUIDGenerator{
		namespace: namespace,
	}
}

// NewUUID generates a deterministic UUID v5 for the given step and call indices.
// The name is formatted as "{stepIndex}:{callIndex}" (e.g., "2:0" for first call in step 2).
func (g *UUIDGenerator) NewUUID(stepIndex, callIndex int) uuid.UUID {
	name := fmt.Sprintf("%d:%d", stepIndex, callIndex)
	return uuid.NewSHA1(g.namespace, []byte(name))
}

// StepContext provides step-scoped UUID generation with automatic call index tracking.
// It implements thread-safe atomic increment of the call index counter, which resets
// to 0 at the start of each step (FR-26 seed reset).
//
// This ensures that:
// 1. Multiple NewUUID() calls within a step get unique, sequential UUIDs
// 2. Replay of the same step produces identical UUIDs
// 3. Hot-fix (definition change mid-flight) doesn't affect UUID generation (FR-21)
type StepContext struct {
	instanceID uuid.UUID
	stepIndex  int
	callIndex  int32 // atomic counter, reset to 0 per step
	generator  *UUIDGenerator
}

// NewStepContext creates a new StepContext for UUID generation within a saga step.
// The instanceID must remain stable even after saga definition hot-fixes (FR-21).
func NewStepContext(instanceID uuid.UUID, stepIndex int) *StepContext {
	return &StepContext{
		instanceID: instanceID,
		stepIndex:  stepIndex,
		callIndex:  0,
		generator:  NewUUIDGenerator(instanceID),
	}
}

// NewUUID generates the next deterministic UUID for this step.
// It atomically increments the call index and returns a UUID v5 based on
// the instance ID namespace and "{stepIndex}:{callIndex}" name.
//
// Thread-safe: can be called from multiple goroutines concurrently.
func (ctx *StepContext) NewUUID() uuid.UUID {
	// Atomic increment and fetch previous value
	index := atomic.AddInt32(&ctx.callIndex, 1) - 1
	return ctx.generator.NewUUID(ctx.stepIndex, int(index))
}

// ResetForStep resets the call index counter and updates the step index.
// This must be called when entering a new step to ensure UUID determinism (FR-26).
//
// IMPORTANT: This method is NOT safe to call concurrently with NewUUID.
// It must be called only during step transitions when no UUID generation
// is in progress (i.e., between steps, not during step execution).
// The saga orchestrator is responsible for ensuring this serialization.
func (ctx *StepContext) ResetForStep(stepIndex int) {
	ctx.stepIndex = stepIndex
	atomic.StoreInt32(&ctx.callIndex, 0)
}

// InstanceID returns the saga instance ID (namespace for UUID generation).
func (ctx *StepContext) InstanceID() uuid.UUID {
	return ctx.instanceID
}

// StepIndex returns the current step index.
func (ctx *StepContext) StepIndex() int {
	return ctx.stepIndex
}

// CallIndex returns the number of UUIDs generated so far in the current step.
// This value equals the call index that will be used for the next NewUUID call.
func (ctx *StepContext) CallIndex() int32 {
	return atomic.LoadInt32(&ctx.callIndex)
}
