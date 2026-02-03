package saga

import (
	"errors"
	"fmt"
	"log/slog"

	"go.starlark.net/starlark"
)

// Builtin errors.
var (
	// ErrSagaFailed is returned when a saga explicitly fails via fail().
	ErrSagaFailed = errors.New("saga failed")

	// ErrUnhashable is returned when attempting to hash an unhashable type.
	ErrUnhashable = errors.New("unhashable type")
)

// NewRestrictedBuiltins creates a hardened Starlark environment with whitelisted functions.
// Per PRD Section 6.1, only safe operations are allowed.
//
// Whitelisted DSL functions:
//   - saga() - Define a saga
//   - step() - Define a saga step
//   - posting() - Create a ledger posting
//   - cel_eval() - Evaluate a CEL expression
//   - resolve_account() - Resolve account ID from reference
//   - resolve_instrument() - Resolve instrument ID from reference
//   - invoke_saga() - Invoke a child saga
//   - fail() - Explicitly fail the saga
//   - log() - Log a message (routed to audit logger)
//   - Decimal() - Create arbitrary-precision decimal
//
// Safe stdlib functions included from starlark.Universe.
//
// BLOCKED: load(), print() (redirected), time.now(), random(), exec(), compile(), open(), http
//
//nolint:gocognit // This function deliberately configures many builtins; complexity is unavoidable
func NewRestrictedBuiltins(logger *slog.Logger) starlark.StringDict {
	if logger == nil {
		logger = slog.Default()
	}

	builtins := make(starlark.StringDict)

	// Copy safe builtins from Starlark Universe
	safeFunctions := []string{
		"True",
		"False",
		"None",
		"len",
		"str",
		"int",
		"float", // Safe for computation, but Decimal preferred for financial
		"bool",
		"list",
		"dict",
		"tuple",
		"range",
		"enumerate",
		"zip",
		"sorted",
		"reversed",
		"min",
		"max",
		"abs",
		"any",
		"all",
		"hasattr",
		"getattr",
		"dir",
		"type",
		"repr",
		"hash",
		"print", // Will be overridden with audit-logging version
	}

	for _, name := range safeFunctions {
		if val, ok := starlark.Universe[name]; ok {
			builtins[name] = val
		}
	}

	// Override print to route to audit logger
	builtins["print"] = starlark.NewBuiltin("print", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, _ []starlark.Tuple) (starlark.Value, error) {
		var msg string
		for i, arg := range args {
			if i > 0 {
				msg += " "
			}
			msg += arg.String()
		}
		logger.Info("saga script print", "message", msg, "thread", thread.Name)
		return starlark.None, nil
	})

	// Add DSL functions

	// Decimal - arbitrary precision decimal type
	builtins["Decimal"] = DecimalBuiltin()

	// saga - define a saga
	builtins["saga"] = starlark.NewBuiltin("saga", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &name); err != nil {
			return nil, err
		}
		// Return a saga definition object (placeholder implementation)
		return &sagaDefinitionValue{name: name}, nil
	})

	// step - define a saga step
	builtins["step"] = starlark.NewBuiltin("step", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &name); err != nil {
			return nil, err
		}
		// Return a step definition object (placeholder implementation)
		return &stepDefinitionValue{name: name}, nil
	})

	// posting - create a ledger posting
	builtins["posting"] = starlark.NewBuiltin("posting", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var debit, credit, amount string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "debit", &debit, "credit", &credit, "amount", &amount); err != nil {
			return nil, err
		}
		// Return a posting object (placeholder implementation)
		return &postingValue{debit: debit, credit: credit, amount: amount}, nil
	})

	// cel_eval - evaluate a CEL expression
	builtins["cel_eval"] = starlark.NewBuiltin("cel_eval", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var expression string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "expression", &expression); err != nil {
			return nil, err
		}

		// Get StarlarkContext from thread local storage
		ctxVal := thread.Local("saga.StarlarkContext")
		if ctxVal == nil {
			return nil, fmt.Errorf("cel_eval: StarlarkContext not found in thread")
		}
		starlarkCtx, ok := ctxVal.(*StarlarkContext)
		if !ok {
			return nil, fmt.Errorf("cel_eval: invalid StarlarkContext type")
		}

		// Create CEL evaluator
		evaluator, err := NewCELEvaluator()
		if err != nil {
			return nil, fmt.Errorf("cel_eval: %w", err)
		}

		// Build evaluation context with saga metadata
		variables := map[string]interface{}{
			"ctx": map[string]interface{}{
				"saga_execution_id": starlarkCtx.SagaExecutionID.String(),
				"correlation_id":    starlarkCtx.CorrelationID.String(),
			},
		}

		// Evaluate expression
		result, err := evaluator.Eval(expression, variables)
		if err != nil {
			return nil, fmt.Errorf("cel_eval: %w", err)
		}

		return goToStarlark(result), nil
	})

	// resolve_account - resolve account ID from reference
	builtins["resolve_account"] = starlark.NewBuiltin("resolve_account", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var reference string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "reference", &reference); err != nil {
			return nil, err
		}

		// Get StarlarkContext from thread
		ctxVal := thread.Local("saga.StarlarkContext")
		if ctxVal == nil {
			return nil, fmt.Errorf("resolve_account: StarlarkContext not found")
		}
		starlarkCtx, ok := ctxVal.(*StarlarkContext)
		if !ok {
			return nil, fmt.Errorf("resolve_account: invalid StarlarkContext type")
		}

		// Check lookup cache for deterministic replay (FR-34)
		cacheKey := "account:" + reference
		if starlarkCtx.LookupCache != nil {
			if cached, found := starlarkCtx.LookupCache.Get(cacheKey); found {
				if accountID, ok := cached.(string); ok {
					return starlark.String(accountID), nil
				}
			}
		}

		// Get reference-data client from thread
		clientVal := thread.Local("saga.ReferenceDataClient")
		if clientVal == nil {
			return nil, fmt.Errorf("resolve_account: reference-data client not configured")
		}
		refDataClient, ok := clientVal.(ReferenceDataClient)
		if !ok {
			return nil, fmt.Errorf("resolve_account: invalid client type")
		}

		// Query reference-data service with bi-temporal KnowledgeAt
		accountID, err := refDataClient.ResolveAccount(starlarkCtx.Context, reference, starlarkCtx.KnowledgeAt)
		if err != nil {
			return nil, fmt.Errorf("resolve_account(%q): %w", reference, err)
		}

		// Cache result for replay
		if starlarkCtx.LookupCache != nil {
			starlarkCtx.LookupCache.Set(cacheKey, accountID)
		}

		return starlark.String(accountID), nil
	})

	// resolve_instrument - resolve instrument ID from reference
	builtins["resolve_instrument"] = starlark.NewBuiltin("resolve_instrument", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var reference string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "reference", &reference); err != nil {
			return nil, err
		}

		// Get StarlarkContext from thread
		ctxVal := thread.Local("saga.StarlarkContext")
		if ctxVal == nil {
			return nil, fmt.Errorf("resolve_instrument: StarlarkContext not found")
		}
		starlarkCtx, ok := ctxVal.(*StarlarkContext)
		if !ok {
			return nil, fmt.Errorf("resolve_instrument: invalid StarlarkContext type")
		}

		// Check cache
		cacheKey := "instrument:" + reference
		if starlarkCtx.LookupCache != nil {
			if cached, found := starlarkCtx.LookupCache.Get(cacheKey); found {
				if instrumentID, ok := cached.(string); ok {
					return starlark.String(instrumentID), nil
				}
			}
		}

		// Get reference-data client from thread
		clientVal := thread.Local("saga.ReferenceDataClient")
		if clientVal == nil {
			return nil, fmt.Errorf("resolve_instrument: reference-data client not configured")
		}
		refDataClient, ok := clientVal.(ReferenceDataClient)
		if !ok {
			return nil, fmt.Errorf("resolve_instrument: invalid client type")
		}

		// Query with KnowledgeAt for bi-temporal lookup
		instrumentID, err := refDataClient.ResolveInstrument(starlarkCtx.Context, reference, starlarkCtx.KnowledgeAt)
		if err != nil {
			return nil, fmt.Errorf("resolve_instrument(%q): %w", reference, err)
		}

		// Cache result
		if starlarkCtx.LookupCache != nil {
			starlarkCtx.LookupCache.Set(cacheKey, instrumentID)
		}

		return starlark.String(instrumentID), nil
	})

	// invoke_saga - invoke a child saga
	builtins["invoke_saga"] = starlark.NewBuiltin("invoke_saga", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var sagaName string
		inputDict := starlark.NewDict(0)

		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"saga_name", &sagaName,
			"input?", &inputDict,
		); err != nil {
			return nil, err
		}

		// Get StarlarkContext
		ctxVal := thread.Local("saga.StarlarkContext")
		if ctxVal == nil {
			return nil, fmt.Errorf("invoke_saga: StarlarkContext not found")
		}
		starlarkCtx, ok := ctxVal.(*StarlarkContext)
		if !ok {
			return nil, fmt.Errorf("invoke_saga: invalid StarlarkContext type")
		}

		// Get Composer from thread
		composerVal := thread.Local("saga.Composer")
		if composerVal == nil {
			return nil, fmt.Errorf("invoke_saga: Composer not configured")
		}
		composer, ok := composerVal.(*Composer)
		if !ok {
			return nil, fmt.Errorf("invoke_saga: invalid Composer type")
		}

		// Get CallStack for nesting tracking
		stackVal := thread.Local("saga.CallStack")
		if stackVal == nil {
			return nil, fmt.Errorf("invoke_saga: CallStack not found")
		}
		stack, ok := stackVal.(*CallStack)
		if !ok {
			return nil, fmt.Errorf("invoke_saga: invalid CallStack type")
		}

		// Convert Starlark dict to Go map
		input := make(map[string]interface{})
		for _, item := range inputDict.Items() {
			if key, ok := item[0].(starlark.String); ok {
				input[string(key)] = convertStarlarkToGo(item[1])
			}
		}

		// Invoke child saga with scope inheritance and circular detection
		result, err := composer.InvokeSaga(
			starlarkCtx.Context,
			sagaName,
			input,
			starlarkCtx.PartyScope, // Inherit parent scope - child cannot escalate
			stack,
		)
		if err != nil {
			return nil, fmt.Errorf("invoke_saga(%q): %w", sagaName, err)
		}

		// Return sagaResultValue from composition.go
		// goToStarlark handles map[string]interface{} -> *starlark.Dict conversion
		outputDict, ok := goToStarlark(result.Output).(*starlark.Dict)
		if !ok {
			outputDict = starlark.NewDict(0)
		}
		return &sagaResultValue{
			executionID:    result.ExecutionID,
			status:         result.Status,
			output:         outputDict,
			stepsCompleted: result.StepsCompleted,
		}, nil
	})

	// fail - explicitly fail the saga
	builtins["fail"] = starlark.NewBuiltin("fail", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var message string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "message", &message); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", ErrSagaFailed, message)
	})

	// log - log a message to audit logger
	builtins["log"] = starlark.NewBuiltin("log", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var message string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "message", &message); err != nil {
			return nil, err
		}
		logger.Info("saga log", "message", message, "thread", thread.Name)
		return starlark.None, nil
	})

	// BLOCKED functions are simply not added to the builtins dict
	// load(), time.now(), random(), exec(), compile(), open(), http are all absent

	return builtins
}

// sagaDefinitionValue represents a saga definition in Starlark.
// This is a placeholder that will be expanded with actual saga metadata.
type sagaDefinitionValue struct {
	name   string
	frozen bool
}

// String implements starlark.Value.
func (s *sagaDefinitionValue) String() string { return fmt.Sprintf("saga(%q)", s.name) }

// Type implements starlark.Value.
func (s *sagaDefinitionValue) Type() string { return "SagaDefinition" }

// Freeze implements starlark.Value.
func (s *sagaDefinitionValue) Freeze() { s.frozen = true }

// Truth implements starlark.Value.
func (s *sagaDefinitionValue) Truth() starlark.Bool { return starlark.True }

// Hash implements starlark.Value.
func (s *sagaDefinitionValue) Hash() (uint32, error) { return starlark.String(s.name).Hash() }

// stepDefinitionValue represents a saga step definition in Starlark.
type stepDefinitionValue struct {
	name   string
	frozen bool
}

// String implements starlark.Value.
func (s *stepDefinitionValue) String() string { return fmt.Sprintf("step(%q)", s.name) }

// Type implements starlark.Value.
func (s *stepDefinitionValue) Type() string { return "StepDefinition" }

// Freeze implements starlark.Value.
func (s *stepDefinitionValue) Freeze() { s.frozen = true }

// Truth implements starlark.Value.
func (s *stepDefinitionValue) Truth() starlark.Bool { return starlark.True }

// Hash implements starlark.Value.
func (s *stepDefinitionValue) Hash() (uint32, error) { return starlark.String(s.name).Hash() }

// postingValue represents a ledger posting in Starlark.
type postingValue struct {
	debit  string
	credit string
	amount string
	frozen bool
}

// String implements starlark.Value.
func (p *postingValue) String() string {
	return fmt.Sprintf("posting(%q, %q, %q)", p.debit, p.credit, p.amount)
}

// Type implements starlark.Value.
func (p *postingValue) Type() string { return "Posting" }

// Freeze implements starlark.Value.
func (p *postingValue) Freeze() { p.frozen = true }

// Truth implements starlark.Value.
func (p *postingValue) Truth() starlark.Bool { return starlark.True }

// Hash implements starlark.Value.
func (p *postingValue) Hash() (uint32, error) { return 0, fmt.Errorf("%w: Posting", ErrUnhashable) }

// convertStarlarkToGo converts a Starlark value to a Go value.
// This is used when passing input parameters to child sagas.
func convertStarlarkToGo(v starlark.Value) interface{} {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case starlark.String:
		return string(val)
	case starlark.Int:
		if i, ok := val.Int64(); ok {
			return i
		}
		return val.String()
	case starlark.Float:
		return float64(val)
	case starlark.Bool:
		return bool(val)
	case starlark.NoneType:
		return nil
	case *starlark.List:
		result := make([]interface{}, val.Len())
		for i := 0; i < val.Len(); i++ {
			result[i] = convertStarlarkToGo(val.Index(i))
		}
		return result
	case *starlark.Dict:
		result := make(map[string]interface{})
		for _, item := range val.Items() {
			if key, ok := item[0].(starlark.String); ok {
				result[string(key)] = convertStarlarkToGo(item[1])
			}
		}
		return result
	default:
		return val.String()
	}
}
