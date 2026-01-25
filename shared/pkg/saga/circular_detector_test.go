package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCircularDetectorDraftPhase tests static AST analysis for invoke_saga cycles.
func TestCircularDetectorDraftPhase(t *testing.T) {
	t.Run("detects direct self-reference", func(t *testing.T) {
		script := `
saga("recursive-saga")
step("do-work")
invoke_saga("recursive-saga")
`
		detector := NewCircularDetector()
		cycles, err := detector.AnalyzeDraft("recursive-saga", script)
		require.NoError(t, err)
		require.Len(t, cycles, 1)
		assert.Equal(t, []string{"recursive-saga", "recursive-saga"}, cycles[0])
	})

	t.Run("detects invoke_saga calls in script", func(t *testing.T) {
		script := `
saga("parent-saga")
step("step-1")
invoke_saga("child-saga-a")
step("step-2")
invoke_saga("child-saga-b")
`
		detector := NewCircularDetector()
		refs := detector.ExtractInvokeSagaCalls(script)
		require.Len(t, refs, 2)
		assert.Contains(t, refs, "child-saga-a")
		assert.Contains(t, refs, "child-saga-b")
	})

	t.Run("handles script with no invoke_saga calls", func(t *testing.T) {
		script := `
saga("simple-saga")
step("step-1")
result = "done"
`
		detector := NewCircularDetector()
		refs := detector.ExtractInvokeSagaCalls(script)
		assert.Empty(t, refs)
	})

	t.Run("extracts saga name from various call patterns", func(t *testing.T) {
		testCases := []struct {
			script   string
			expected []string
		}{
			{
				script:   `invoke_saga("child")`,
				expected: []string{"child"},
			},
			{
				script:   `invoke_saga(saga_name="child")`,
				expected: []string{"child"},
			},
			{
				script:   `invoke_saga("child", context={"key": "value"})`,
				expected: []string{"child"},
			},
			{
				script:   `invoke_saga(saga_name="child", version=2)`,
				expected: []string{"child"},
			},
		}

		detector := NewCircularDetector()
		for _, tc := range testCases {
			refs := detector.ExtractInvokeSagaCalls(tc.script)
			assert.Equal(t, tc.expected, refs, "script: %s", tc.script)
		}
	})
}

// TestCircularDetectorActivationPhase tests graph traversal for cycle detection.
func TestCircularDetectorActivationPhase(t *testing.T) {
	t.Run("detects two-saga cycle", func(t *testing.T) {
		// A invokes B, B invokes A
		sagaGraph := map[string][]string{
			"saga-A": {"saga-B"},
			"saga-B": {"saga-A"},
		}

		detector := NewCircularDetector()
		detector.SetSagaGraph(sagaGraph)

		cycles := detector.FindCyclesAtActivation("saga-A")
		require.Len(t, cycles, 1)
		// Cycle should be A -> B -> A
		assert.Contains(t, cycles[0], "saga-A")
		assert.Contains(t, cycles[0], "saga-B")
	})

	t.Run("detects three-saga cycle", func(t *testing.T) {
		// A -> B -> C -> A
		sagaGraph := map[string][]string{
			"saga-A": {"saga-B"},
			"saga-B": {"saga-C"},
			"saga-C": {"saga-A"},
		}

		detector := NewCircularDetector()
		detector.SetSagaGraph(sagaGraph)

		cycles := detector.FindCyclesAtActivation("saga-A")
		require.Len(t, cycles, 1)
		assert.Len(t, cycles[0], 4) // A, B, C, A
	})

	t.Run("no cycle in valid DAG", func(t *testing.T) {
		// A -> B, A -> C, B -> D, C -> D (diamond, no cycle)
		sagaGraph := map[string][]string{
			"saga-A": {"saga-B", "saga-C"},
			"saga-B": {"saga-D"},
			"saga-C": {"saga-D"},
			"saga-D": {},
		}

		detector := NewCircularDetector()
		detector.SetSagaGraph(sagaGraph)

		cycles := detector.FindCyclesAtActivation("saga-A")
		assert.Empty(t, cycles)
	})

	t.Run("finds all cycles from entry point", func(t *testing.T) {
		// Multiple cycles: A -> B -> A, A -> C -> D -> A
		sagaGraph := map[string][]string{
			"saga-A": {"saga-B", "saga-C"},
			"saga-B": {"saga-A"},
			"saga-C": {"saga-D"},
			"saga-D": {"saga-A"},
		}

		detector := NewCircularDetector()
		detector.SetSagaGraph(sagaGraph)

		cycles := detector.FindCyclesAtActivation("saga-A")
		require.Len(t, cycles, 2)
	})

	t.Run("detects self-loop", func(t *testing.T) {
		sagaGraph := map[string][]string{
			"saga-A": {"saga-A"},
		}

		detector := NewCircularDetector()
		detector.SetSagaGraph(sagaGraph)

		cycles := detector.FindCyclesAtActivation("saga-A")
		require.Len(t, cycles, 1)
		assert.Equal(t, []string{"saga-A", "saga-A"}, cycles[0])
	})
}

// TestCircularDetectorRuntimePhase tests call stack based cycle detection.
func TestCircularDetectorRuntimePhase(t *testing.T) {
	t.Run("detects circular reference in call stack", func(t *testing.T) {
		detector := NewCircularDetector()
		stack := NewCallStack()

		// Push saga-A onto stack
		stack.Push(CallEntry{SagaName: "saga-A"})
		stack.Push(CallEntry{SagaName: "saga-B"})

		// Trying to invoke saga-A again should be detected
		err := detector.CheckRuntimeCircular("saga-A", stack)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrCircularSagaReference)
	})

	t.Run("allows non-circular invocation", func(t *testing.T) {
		detector := NewCircularDetector()
		stack := NewCallStack()

		stack.Push(CallEntry{SagaName: "saga-A"})
		stack.Push(CallEntry{SagaName: "saga-B"})

		// saga-C is not in stack, should be allowed
		err := detector.CheckRuntimeCircular("saga-C", stack)
		assert.NoError(t, err)
	})

	t.Run("returns detailed error message with call chain", func(t *testing.T) {
		detector := NewCircularDetector()
		stack := NewCallStack()

		stack.Push(CallEntry{SagaName: "saga-A"})
		stack.Push(CallEntry{SagaName: "saga-B"})
		stack.Push(CallEntry{SagaName: "saga-C"})

		err := detector.CheckRuntimeCircular("saga-A", stack)
		require.Error(t, err)
		// Error should contain the call chain
		assert.Contains(t, err.Error(), "saga-A")
		assert.Contains(t, err.Error(), "saga-B")
		assert.Contains(t, err.Error(), "saga-C")
	})
}

// TestCircularDetectorIntegration tests the full detection workflow.
func TestCircularDetectorIntegration(t *testing.T) {
	t.Run("validate saga definition at all phases", func(t *testing.T) {
		scripts := map[string]string{
			"saga-A": `invoke_saga("saga-B")`,
			"saga-B": `invoke_saga("saga-C")`,
			"saga-C": `invoke_saga("saga-A")`, // Creates cycle
		}

		detector := NewCircularDetector()

		// Phase 1: DRAFT - Static analysis
		for name, script := range scripts {
			cycles, err := detector.AnalyzeDraft(name, script)
			require.NoError(t, err)
			// Direct self-reference check (none here)
			if name != "saga-C" {
				assert.Empty(t, cycles, "unexpected cycle in draft phase for %s", name)
			}
		}

		// Build graph from scripts
		graph := make(map[string][]string)
		for name, script := range scripts {
			graph[name] = detector.ExtractInvokeSagaCalls(script)
		}
		detector.SetSagaGraph(graph)

		// Phase 2: ACTIVATION - Graph traversal
		cycles := detector.FindCyclesAtActivation("saga-A")
		require.Len(t, cycles, 1, "expected one cycle at activation")

		// Phase 3: RUNTIME - Call stack check (simulated)
		stack := NewCallStack()
		stack.Push(CallEntry{SagaName: "saga-A"})
		stack.Push(CallEntry{SagaName: "saga-B"})
		stack.Push(CallEntry{SagaName: "saga-C"})

		err := detector.CheckRuntimeCircular("saga-A", stack)
		require.Error(t, err, "runtime should detect circular reference")
	})

	t.Run("reports cycle path in human-readable format", func(t *testing.T) {
		sagaGraph := map[string][]string{
			"order-saga":        {"payment-saga"},
			"payment-saga":      {"notification-saga"},
			"notification-saga": {"order-saga"}, // cycle
		}

		detector := NewCircularDetector()
		detector.SetSagaGraph(sagaGraph)

		cycles := detector.FindCyclesAtActivation("order-saga")
		require.Len(t, cycles, 1)

		formatted := detector.FormatCycle(cycles[0])
		assert.Contains(t, formatted, "order-saga")
		assert.Contains(t, formatted, "->")
		assert.Contains(t, formatted, "payment-saga")
		assert.Contains(t, formatted, "notification-saga")
	})
}

// TestCircularDetectorEdgeCases tests edge cases and error handling.
func TestCircularDetectorEdgeCases(t *testing.T) {
	t.Run("handles empty script", func(t *testing.T) {
		detector := NewCircularDetector()
		cycles, err := detector.AnalyzeDraft("empty", "")
		assert.NoError(t, err)
		assert.Empty(t, cycles)
	})

	t.Run("handles syntax error in script gracefully", func(t *testing.T) {
		detector := NewCircularDetector()
		_, err := detector.AnalyzeDraft("invalid", "invoke_saga(")
		// Should return error for invalid syntax
		assert.Error(t, err)
	})

	t.Run("handles missing saga in graph", func(t *testing.T) {
		sagaGraph := map[string][]string{
			"saga-A": {"saga-B"}, // saga-B not defined
		}

		detector := NewCircularDetector()
		detector.SetSagaGraph(sagaGraph)

		// Should not panic, just not find cycles
		cycles := detector.FindCyclesAtActivation("saga-A")
		assert.Empty(t, cycles)
	})

	t.Run("handles nil stack in runtime check", func(t *testing.T) {
		detector := NewCircularDetector()
		err := detector.CheckRuntimeCircular("saga-A", nil)
		// Should not error - empty stack means no cycle possible
		assert.NoError(t, err)
	})
}
