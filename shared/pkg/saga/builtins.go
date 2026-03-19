package saga

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"go.starlark.net/starlark"
)

// Builtin errors.
var (
	// ErrSagaFailed is returned when a saga explicitly fails via fail().
	ErrSagaFailed = errors.New("saga failed")

	// ErrUnhashable is returned when attempting to hash an unhashable type.
	ErrUnhashable = errors.New("unhashable type")

	// ErrMissingStarlarkContext is returned when StarlarkContext is not found in thread locals.
	ErrMissingStarlarkContext = errors.New("StarlarkContext not found in thread")

	// ErrInvalidStarlarkContext is returned when StarlarkContext has invalid type.
	ErrInvalidStarlarkContext = errors.New("invalid StarlarkContext type")

	// ErrMissingClient is returned when a required client is not configured in thread locals.
	ErrMissingClient = errors.New("required client not configured")

	// ErrInvalidClientType is returned when a client has invalid type.
	ErrInvalidClientType = errors.New("invalid client type")

	// ErrInvalidParameterType is returned when a parameter has an unexpected type.
	ErrInvalidParameterType = errors.New("invalid parameter type")

	// ErrEmptyParam is returned when a required parameter is provided but empty.
	ErrEmptyParam = errors.New("parameter must not be empty")
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
		var inputDict *starlark.Dict
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "expression", &expression, "variables?", &inputDict); err != nil {
			return nil, err
		}

		// Get StarlarkContext from thread local storage
		ctxVal := thread.Local("saga.StarlarkContext")
		if ctxVal == nil {
			return nil, fmt.Errorf("cel_eval: %w", ErrMissingStarlarkContext)
		}
		starlarkCtx, ok := ctxVal.(*StarlarkContext)
		if !ok {
			return nil, fmt.Errorf("cel_eval: %w", ErrInvalidStarlarkContext)
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

		// Add optional input variables if provided
		if inputDict != nil {
			inputMap := make(map[string]interface{})
			for _, item := range inputDict.Items() {
				key, ok := item[0].(starlark.String)
				if !ok {
					return nil, fmt.Errorf("cel_eval: %w: variables keys must be strings, got %s", ErrInvalidParameterType, item[0].Type())
				}
				inputMap[string(key)] = convertStarlarkToGo(item[1])
			}
			variables["input"] = inputMap
		}

		// Evaluate expression
		result, err := evaluator.Eval(expression, variables)
		if err != nil {
			return nil, fmt.Errorf("cel_eval: %w", err)
		}

		return goToStarlark(result), nil
	})

	// resolve_account - resolve account ID from reference
	// Supports both simple references (e.g., "ACC-001") and composite references
	// (e.g., "party:<party_id>:org:<org_id>:currency:<code>") for org-scoped account resolution.
	// Composite references are validated before being passed to the ReferenceDataClient.
	builtins["resolve_account"] = starlark.NewBuiltin("resolve_account", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var reference string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "reference", &reference); err != nil {
			return nil, err
		}

		// Validate composite reference format if it contains colons
		if strings.Contains(reference, ":") {
			if _, err := ParseCompositeAccountRef(reference); err != nil {
				return nil, fmt.Errorf("resolve_account: %w", err)
			}
		}

		// Get StarlarkContext from thread
		ctxVal := thread.Local("saga.StarlarkContext")
		if ctxVal == nil {
			return nil, fmt.Errorf("resolve_account: %w", ErrMissingStarlarkContext)
		}
		starlarkCtx, ok := ctxVal.(*StarlarkContext)
		if !ok {
			return nil, fmt.Errorf("resolve_account: %w", ErrInvalidStarlarkContext)
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
			return nil, fmt.Errorf("resolve_account: %w", ErrMissingClient)
		}
		refDataClient, ok := clientVal.(ReferenceDataClient)
		if !ok {
			return nil, fmt.Errorf("resolve_account: %w", ErrInvalidClientType)
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
			return nil, fmt.Errorf("resolve_instrument: %w", ErrMissingStarlarkContext)
		}
		starlarkCtx, ok := ctxVal.(*StarlarkContext)
		if !ok {
			return nil, fmt.Errorf("resolve_instrument: %w", ErrInvalidStarlarkContext)
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
			return nil, fmt.Errorf("resolve_instrument: %w", ErrMissingClient)
		}
		refDataClient, ok := clientVal.(ReferenceDataClient)
		if !ok {
			return nil, fmt.Errorf("resolve_instrument: %w", ErrInvalidClientType)
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

	// build_org_account_ref - build a composite account reference for org-scoped resolution
	// Returns a properly formatted composite reference string:
	//   party:<party_id>:org:<org_id>:currency:<currency_code>
	// This prevents manual string concatenation errors in saga scripts.
	builtins["build_org_account_ref"] = starlark.NewBuiltin("build_org_account_ref", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var partyID, orgID, currency string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs,
			"party_id", &partyID,
			"org_id", &orgID,
			"currency", &currency,
		); err != nil {
			return nil, err
		}

		if partyID == "" {
			return nil, fmt.Errorf("build_org_account_ref: %w: party_id", ErrEmptyParam)
		}
		if orgID == "" {
			return nil, fmt.Errorf("build_org_account_ref: %w: org_id", ErrEmptyParam)
		}
		if currency == "" {
			return nil, fmt.Errorf("build_org_account_ref: %w: currency", ErrEmptyParam)
		}

		ref := BuildCompositeAccountRef(partyID, orgID, currency)
		return starlark.String(ref), nil
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
			return nil, fmt.Errorf("invoke_saga: %w", ErrMissingStarlarkContext)
		}
		starlarkCtx, ok := ctxVal.(*StarlarkContext)
		if !ok {
			return nil, fmt.Errorf("invoke_saga: %w", ErrInvalidStarlarkContext)
		}

		// Get Composer from thread
		composerVal := thread.Local("saga.Composer")
		if composerVal == nil {
			return nil, fmt.Errorf("invoke_saga: %w", ErrMissingClient)
		}
		composer, ok := composerVal.(*Composer)
		if !ok {
			return nil, fmt.Errorf("invoke_saga: %w", ErrInvalidClientType)
		}

		// Get CallStack for nesting tracking
		stackVal := thread.Local("saga.CallStack")
		if stackVal == nil {
			return nil, fmt.Errorf("invoke_saga: %w", ErrMissingClient)
		}
		stack, ok := stackVal.(*CallStack)
		if !ok {
			return nil, fmt.Errorf("invoke_saga: %w", ErrInvalidClientType)
		}

		// Convert Starlark dict to Go map
		input := make(map[string]interface{})
		for _, item := range inputDict.Items() {
			key, ok := item[0].(starlark.String)
			if !ok {
				return nil, fmt.Errorf("invoke_saga: %w: input keys must be strings, got %s", ErrInvalidParameterType, item[0].Type())
			}
			input[string(key)] = convertStarlarkToGo(item[1])
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
		if i64, ok := val.Int64(); ok {
			// Preserve int vs int64 type: if value fits in int, return int
			if i := int(i64); int64(i) == i64 {
				return i
			}
			return i64
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
			} else {
				// Log warning - non-string keys are not supported in Go map conversion
				slog.Warn("convertStarlarkToGo: ignoring non-string dict key",
					"key_type", item[0].Type(),
					"key_value", item[0].String())
			}
		}
		return result
	default:
		return val.String()
	}
}
