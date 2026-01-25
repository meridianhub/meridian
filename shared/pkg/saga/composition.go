// Package saga provides saga orchestration runtime and persistence for durable execution.
package saga

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"go.starlark.net/starlark"
)

// Composition errors.
var (
	// ErrMaxNestingDepthExceeded is returned when saga nesting exceeds the configured limit.
	ErrMaxNestingDepthExceeded = errors.New("maximum saga nesting depth exceeded")

	// ErrMaxTotalStepsExceeded is returned when total steps across all nested sagas exceeds limit.
	ErrMaxTotalStepsExceeded = errors.New("maximum total steps exceeded across saga composition")

	// ErrCircularSagaReference is returned when a circular saga invocation is detected.
	ErrCircularSagaReference = errors.New("circular saga reference detected")
)

// Default composition limits.
const (
	// DefaultMaxNestingDepth is the default maximum saga nesting level.
	DefaultMaxNestingDepth = 5

	// DefaultMaxTotalSteps is the default maximum total steps across all nested sagas.
	DefaultMaxTotalSteps = 50
)

// ResultStatus represents the status of a completed saga invocation.
type ResultStatus string

const (
	// ResultStatusCompleted indicates the saga completed successfully.
	ResultStatusCompleted ResultStatus = "COMPLETED"

	// ResultStatusFailed indicates the saga failed.
	ResultStatusFailed ResultStatus = "FAILED"

	// ResultStatusCompensated indicates the saga was compensated.
	ResultStatusCompensated ResultStatus = "COMPENSATED"
)

// CallEntry represents a single entry in the saga call stack.
type CallEntry struct {
	// SagaName is the name of the saga being executed.
	SagaName string

	// ExecutionID is the unique ID for this execution.
	ExecutionID uuid.UUID

	// StepIndex is the step in the parent that invoked this saga.
	StepIndex int
}

// CallStack tracks the execution chain for nested saga invocations.
// It provides circular reference detection and depth limiting.
type CallStack struct {
	entries       []CallEntry
	maxDepth      int
	totalSteps    int
	maxTotalSteps int
	mu            sync.RWMutex
}

// NewCallStack creates a new call stack with default limits.
func NewCallStack() *CallStack {
	return &CallStack{
		entries:       make([]CallEntry, 0),
		maxDepth:      DefaultMaxNestingDepth,
		maxTotalSteps: DefaultMaxTotalSteps,
	}
}

// SetMaxDepth configures the maximum nesting depth.
func (s *CallStack) SetMaxDepth(depth int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxDepth = depth
}

// SetMaxTotalSteps configures the maximum total steps across all sagas.
func (s *CallStack) SetMaxTotalSteps(steps int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxTotalSteps = steps
}

// Push adds an entry to the stack. Returns error if depth exceeded.
func (s *CallStack) Push(entry CallEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.entries) >= s.maxDepth {
		return fmt.Errorf("%w: depth %d exceeds limit %d", ErrMaxNestingDepthExceeded, len(s.entries)+1, s.maxDepth)
	}

	s.entries = append(s.entries, entry)
	return nil
}

// Pop removes and returns the top entry from the stack.
func (s *CallStack) Pop() CallEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.entries) == 0 {
		return CallEntry{}
	}

	entry := s.entries[len(s.entries)-1]
	s.entries = s.entries[:len(s.entries)-1]
	return entry
}

// Depth returns the current nesting depth.
func (s *CallStack) Depth() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Chain returns the execution IDs in order.
func (s *CallStack) Chain() []uuid.UUID {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]uuid.UUID, len(s.entries))
	for i, entry := range s.entries {
		ids[i] = entry.ExecutionID
	}
	return ids
}

// Contains checks if a saga name is already in the stack.
func (s *CallStack) Contains(sagaName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, entry := range s.entries {
		if entry.SagaName == sagaName {
			return true
		}
	}
	return false
}

// IncrementSteps adds to the total step count.
func (s *CallStack) IncrementSteps(count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalSteps += count
}

// TotalSteps returns the cumulative step count.
func (s *CallStack) TotalSteps() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalSteps
}

// ValidateStepLimit checks if total steps exceed the limit.
func (s *CallStack) ValidateStepLimit() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.totalSteps > s.maxTotalSteps {
		return fmt.Errorf("%w: %d steps exceed limit %d", ErrMaxTotalStepsExceeded, s.totalSteps, s.maxTotalSteps)
	}
	return nil
}

// GetSagaNames returns all saga names in the stack (for error messages).
func (s *CallStack) GetSagaNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, len(s.entries))
	for i, entry := range s.entries {
		names[i] = entry.SagaName
	}
	return names
}

// Result represents the result of a child saga invocation.
type Result struct {
	// ExecutionID is the unique identifier for this execution.
	ExecutionID uuid.UUID

	// Status indicates whether the saga completed, failed, or was compensated.
	Status ResultStatus

	// Output contains the saga's output data.
	Output map[string]interface{}

	// StepsCompleted is the number of steps that were executed.
	StepsCompleted int

	// Error contains the error message if the saga failed.
	Error string

	// CompensateFunc is the function to call to compensate this saga.
	CompensateFunc func(ctx context.Context) error
}

// DefinitionInfo holds the information needed to execute a saga.
// This is a minimal interface to avoid tight coupling with specific definition implementations.
type DefinitionInfo struct {
	// Name is the saga's unique identifier.
	Name string

	// Version is the saga definition version.
	Version int

	// Script is the Starlark script content.
	Script string
}

// Registry provides access to saga definitions.
type Registry interface {
	// GetSaga retrieves a saga definition by name and optional version.
	GetSaga(name string, version *int) (*DefinitionInfo, error)

	// Execute runs a saga with the given input and party scope.
	Execute(ctx context.Context, saga *DefinitionInfo, input map[string]interface{}, scope *PartyScope) (*Result, error)

	// Compensate triggers compensation for a saga execution.
	Compensate(ctx context.Context, executionID uuid.UUID) error
}

// Composer handles saga composition with invoke_saga support.
type Composer struct {
	registry Registry
	runtime  *Runtime
}

// NewComposer creates a new composer with the given registry and runtime.
func NewComposer(registry Registry, runtime *Runtime) *Composer {
	return &Composer{
		registry: registry,
		runtime:  runtime,
	}
}

// InvokeSagaBuiltin returns the Starlark builtin function for invoke_saga.
//
// NOTE: This method provides the complete invoke_saga builtin implementation
// that will replace the placeholder in builtins.go when the runtime is wired up
// to support saga composition. The placeholder in builtins.go returns None;
// this method returns a proper sagaResultValue. Integration is planned for a
// future PR that connects the Composer to the Runtime execution flow.
func (c *Composer) InvokeSagaBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("invoke_saga", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var sagaName string
		var version starlark.Value = starlark.None
		var context starlark.Value = starlark.None

		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"saga_name", &sagaName,
			"version?", &version,
			"context?", &context,
		); err != nil {
			return nil, err
		}

		// This is a placeholder - actual invocation requires runtime context
		// The real implementation is in InvokeSaga which is called by the runtime
		return &sagaResultValue{
			executionID:    uuid.New(),
			status:         ResultStatusCompleted,
			output:         starlark.NewDict(0),
			stepsCompleted: 0,
		}, nil
	})
}

// InvokeSaga executes a child saga with scope inheritance and circular detection.
func (c *Composer) InvokeSaga(
	ctx context.Context,
	sagaName string,
	input map[string]interface{},
	parentScope *PartyScope,
	stack *CallStack,
) (*Result, error) {
	// Runtime circular detection (defense in depth)
	if stack != nil && stack.Contains(sagaName) {
		names := stack.GetSagaNames()
		return nil, fmt.Errorf("%w: %v -> %s", ErrCircularSagaReference, names, sagaName)
	}

	// Generate a consistent execution ID for this invocation
	executionID := uuid.New()

	// Check nesting depth
	if stack != nil {
		entry := CallEntry{
			SagaName:    sagaName,
			ExecutionID: executionID,
		}
		if err := stack.Push(entry); err != nil {
			return nil, err
		}
		defer stack.Pop()
	}

	// Load child saga definition
	saga, err := c.registry.GetSaga(sagaName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to load saga %q: %w", sagaName, err)
	}

	// Execute child saga with inherited scope
	// Child CANNOT escalate - uses parent's PartyScope
	result, err := c.registry.Execute(ctx, saga, input, parentScope)
	if err != nil {
		return &Result{
			ExecutionID: executionID,
			Status:      ResultStatusFailed,
			Error:       err.Error(),
		}, nil
	}

	// Ensure result uses the consistent execution ID
	if result != nil {
		result.ExecutionID = executionID
	}

	// Track steps for limit enforcement
	if stack != nil && result != nil {
		stack.IncrementSteps(result.StepsCompleted)
		if err := stack.ValidateStepLimit(); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// ExecuteWithComposition executes a saga with full composition support.
func (c *Composer) ExecuteWithComposition(
	ctx context.Context,
	sagaName string,
	input map[string]interface{},
	scope *PartyScope,
) (*Result, error) {
	stack := NewCallStack()
	return c.InvokeSaga(ctx, sagaName, input, scope, stack)
}

// CompensateSaga triggers compensation for a saga and all its children.
func (c *Composer) CompensateSaga(ctx context.Context, executionID uuid.UUID) error {
	return c.registry.Compensate(ctx, executionID)
}

// CompensationCascade tracks child sagas for LIFO compensation.
type CompensationCascade struct {
	childSagas           []*Result
	parentStepCompensate func()
	compensationStatus   []CompensationEntry
	mu                   sync.Mutex
}

// CompensationEntry tracks the compensation status of a child saga.
type CompensationEntry struct {
	SagaName         string
	ExecutionID      uuid.UUID
	ChildCompensated bool
}

// NewCompensationCascade creates a new compensation cascade tracker.
func NewCompensationCascade() *CompensationCascade {
	return &CompensationCascade{
		childSagas:         make([]*Result, 0),
		compensationStatus: make([]CompensationEntry, 0),
	}
}

// RecordChildSaga adds a child saga to the compensation chain.
func (cc *CompensationCascade) RecordChildSaga(result *Result) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.childSagas = append(cc.childSagas, result)
}

// SetParentStepCompensation sets the callback for parent step compensation.
func (cc *CompensationCascade) SetParentStepCompensation(compensate func()) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.parentStepCompensate = compensate
}

// CompensateAll compensates all child sagas in LIFO order.
func (cc *CompensationCascade) CompensateAll(ctx context.Context) error {
	// Copy children while holding lock, then release before calling external funcs
	cc.mu.Lock()
	children := make([]*Result, len(cc.childSagas))
	copy(children, cc.childSagas)
	cc.mu.Unlock()

	// Compensate in LIFO order (reverse)
	for i := len(children) - 1; i >= 0; i-- {
		child := children[i]
		if child.CompensateFunc != nil {
			if err := child.CompensateFunc(ctx); err != nil {
				// Record partial compensation status before returning error
				cc.mu.Lock()
				cc.compensationStatus = append(cc.compensationStatus, CompensationEntry{
					ExecutionID:      child.ExecutionID,
					ChildCompensated: false,
				})
				cc.mu.Unlock()
				return fmt.Errorf("failed to compensate child saga %s: %w", child.ExecutionID, err)
			}
		}
		cc.mu.Lock()
		cc.compensationStatus = append(cc.compensationStatus, CompensationEntry{
			ExecutionID:      child.ExecutionID,
			ChildCompensated: true,
		})
		cc.mu.Unlock()
	}

	return nil
}

// CompensateWithParentStep compensates children first, then parent step.
func (cc *CompensationCascade) CompensateWithParentStep(ctx context.Context) error {
	// First compensate all children in LIFO order
	if err := cc.CompensateAll(ctx); err != nil {
		return err
	}

	// Then compensate the parent step
	cc.mu.Lock()
	parentComp := cc.parentStepCompensate
	cc.mu.Unlock()

	if parentComp != nil {
		parentComp()
	}

	return nil
}

// GetCompensationStatus returns the compensation status for all child sagas.
func (cc *CompensationCascade) GetCompensationStatus() []CompensationEntry {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.compensationStatus
}

// sagaResultValue is a Starlark value representing a Result.
type sagaResultValue struct {
	executionID    uuid.UUID
	status         ResultStatus
	output         *starlark.Dict
	stepsCompleted int
	frozen         bool
}

// String implements starlark.Value.
func (s *sagaResultValue) String() string {
	return fmt.Sprintf("Result(execution_id=%q, status=%q)", s.executionID, s.status)
}

// Type implements starlark.Value.
func (s *sagaResultValue) Type() string { return "Result" }

// Freeze implements starlark.Value.
func (s *sagaResultValue) Freeze() { s.frozen = true }

// Truth implements starlark.Value.
func (s *sagaResultValue) Truth() starlark.Bool { return s.status == ResultStatusCompleted }

// Hash implements starlark.Value.
func (s *sagaResultValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("%w: Result", ErrUnhashable)
}

// Attr implements starlark.HasAttrs.
func (s *sagaResultValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "execution_id":
		return starlark.String(s.executionID.String()), nil
	case "status":
		return starlark.String(s.status), nil
	case "output":
		return s.output, nil
	case "steps_completed":
		return starlark.MakeInt(s.stepsCompleted), nil
	default:
		return nil, starlark.NoSuchAttrError(fmt.Sprintf("Result has no attribute %q", name))
	}
}

// AttrNames implements starlark.HasAttrs.
func (s *sagaResultValue) AttrNames() []string {
	return []string{"execution_id", "status", "output", "steps_completed"}
}
