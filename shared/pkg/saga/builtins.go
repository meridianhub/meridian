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
func NewRestrictedBuiltins(logger *slog.Logger) starlark.StringDict {
	if logger == nil {
		logger = slog.Default()
	}

	builtins := make(starlark.StringDict)
	addSafeUniverseBuiltins(builtins, logger)
	addDSLCoreBuiltins(builtins, logger)
	addCELBuiltin(builtins)
	addResolverBuiltins(builtins)
	addCompositionBuiltins(builtins)

	// BLOCKED functions are simply not added to the builtins dict
	// load(), time.now(), random(), exec(), compile(), open(), http are all absent

	return builtins
}

// addSafeUniverseBuiltins copies whitelisted builtins from starlark.Universe
// and overrides print to route to the audit logger.
func addSafeUniverseBuiltins(builtins starlark.StringDict, logger *slog.Logger) {
	safeFunctions := []string{
		"True", "False", "None",
		"len", "str", "int", "float", "bool",
		"list", "dict", "tuple", "range",
		"enumerate", "zip", "sorted", "reversed",
		"min", "max", "abs", "any", "all",
		"hasattr", "getattr", "dir", "type", "repr", "hash",
		"print", // Will be overridden below
	}

	for _, name := range safeFunctions {
		if val, ok := starlark.Universe[name]; ok {
			builtins[name] = val
		}
	}

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
}

// addDSLCoreBuiltins registers the saga DSL primitives: Decimal, saga, step, posting, fail, log.
func addDSLCoreBuiltins(builtins starlark.StringDict, logger *slog.Logger) {
	builtins["Decimal"] = DecimalBuiltin()

	builtins["saga"] = starlark.NewBuiltin("saga", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &name); err != nil {
			return nil, err
		}
		return &sagaDefinitionValue{name: name}, nil
	})

	builtins["step"] = starlark.NewBuiltin("step", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var name string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &name); err != nil {
			return nil, err
		}
		return &stepDefinitionValue{name: name}, nil
	})

	builtins["posting"] = starlark.NewBuiltin("posting", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var debit, credit, amount string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "debit", &debit, "credit", &credit, "amount", &amount); err != nil {
			return nil, err
		}
		return &postingValue{debit: debit, credit: credit, amount: amount}, nil
	})

	builtins["fail"] = starlark.NewBuiltin("fail", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var message string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "message", &message); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s", ErrSagaFailed, message)
	})

	builtins["log"] = starlark.NewBuiltin("log", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var message string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "message", &message); err != nil {
			return nil, err
		}
		logger.Info("saga log", "message", message, "thread", thread.Name)
		return starlark.None, nil
	})
}

// addCELBuiltin registers the cel_eval function for evaluating CEL expressions within sagas.
func addCELBuiltin(builtins starlark.StringDict) {
	builtins["cel_eval"] = starlark.NewBuiltin("cel_eval", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var expression string
		var inputDict *starlark.Dict
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "expression", &expression, "variables?", &inputDict); err != nil {
			return nil, err
		}

		starlarkCtx, err := getStarlarkContext(thread, "cel_eval")
		if err != nil {
			return nil, err
		}

		evaluator, err := NewCELEvaluator()
		if err != nil {
			return nil, fmt.Errorf("cel_eval: %w", err)
		}

		variables := map[string]interface{}{
			"ctx": map[string]interface{}{
				"saga_execution_id": starlarkCtx.SagaExecutionID.String(),
				"correlation_id":    starlarkCtx.CorrelationID.String(),
			},
		}

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

		result, err := evaluator.Eval(expression, variables)
		if err != nil {
			return nil, fmt.Errorf("cel_eval: %w", err)
		}

		return goToStarlark(result), nil
	})
}

// addResolverBuiltins registers resolve_account, resolve_instrument, and build_org_account_ref.
func addResolverBuiltins(builtins starlark.StringDict) {
	builtins["resolve_account"] = resolveAccountBuiltin()
	builtins["resolve_instrument"] = resolveInstrumentBuiltin()
	builtins["build_org_account_ref"] = buildOrgAccountRefBuiltin()
}

func resolveAccountBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("resolve_account", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var reference string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "reference", &reference); err != nil {
			return nil, err
		}

		if strings.Contains(reference, ":") {
			if _, err := ParseCompositeAccountRef(reference); err != nil {
				return nil, fmt.Errorf("resolve_account: %w", err)
			}
		}

		starlarkCtx, err := getStarlarkContext(thread, "resolve_account")
		if err != nil {
			return nil, err
		}

		if val, ok := cachedLookup(starlarkCtx, "account:"+reference); ok {
			return starlark.String(val), nil
		}

		refDataClient, err := getRefDataClient(thread, "resolve_account")
		if err != nil {
			return nil, err
		}

		accountID, err := refDataClient.ResolveAccount(starlarkCtx.Context, reference, starlarkCtx.KnowledgeAt)
		if err != nil {
			return nil, fmt.Errorf("resolve_account(%q): %w", reference, err)
		}

		cacheLookup(starlarkCtx, "account:"+reference, accountID)
		return starlark.String(accountID), nil
	})
}

func resolveInstrumentBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("resolve_instrument", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var reference string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "reference", &reference); err != nil {
			return nil, err
		}

		starlarkCtx, err := getStarlarkContext(thread, "resolve_instrument")
		if err != nil {
			return nil, err
		}

		if val, ok := cachedLookup(starlarkCtx, "instrument:"+reference); ok {
			return starlark.String(val), nil
		}

		refDataClient, err := getRefDataClient(thread, "resolve_instrument")
		if err != nil {
			return nil, err
		}

		instrumentID, err := refDataClient.ResolveInstrument(starlarkCtx.Context, reference, starlarkCtx.KnowledgeAt)
		if err != nil {
			return nil, fmt.Errorf("resolve_instrument(%q): %w", reference, err)
		}

		cacheLookup(starlarkCtx, "instrument:"+reference, instrumentID)
		return starlark.String(instrumentID), nil
	})
}

func buildOrgAccountRefBuiltin() *starlark.Builtin {
	return starlark.NewBuiltin("build_org_account_ref", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
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
}

// cachedLookup checks the StarlarkContext lookup cache for a previously resolved value.
func cachedLookup(ctx *StarlarkContext, cacheKey string) (string, bool) {
	if ctx.LookupCache == nil {
		return "", false
	}
	cached, found := ctx.LookupCache.Get(cacheKey)
	if !found {
		return "", false
	}
	val, ok := cached.(string)
	return val, ok
}

// cacheLookup stores a resolved value in the StarlarkContext lookup cache.
func cacheLookup(ctx *StarlarkContext, cacheKey, value string) {
	if ctx.LookupCache != nil {
		ctx.LookupCache.Set(cacheKey, value)
	}
}

// addCompositionBuiltins registers invoke_saga for child saga invocation.
func addCompositionBuiltins(builtins starlark.StringDict) {
	builtins["invoke_saga"] = starlark.NewBuiltin("invoke_saga", func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var sagaName string
		inputDict := starlark.NewDict(0)
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "saga_name", &sagaName, "input?", &inputDict); err != nil {
			return nil, err
		}

		starlarkCtx, err := getStarlarkContext(thread, "invoke_saga")
		if err != nil {
			return nil, err
		}
		composer, err := getThreadLocal[*Composer](thread, "saga.Composer", "invoke_saga")
		if err != nil {
			return nil, err
		}
		stack, err := getThreadLocal[*CallStack](thread, "saga.CallStack", "invoke_saga")
		if err != nil {
			return nil, err
		}

		input, err := starlarkDictToGoMap(inputDict, "invoke_saga")
		if err != nil {
			return nil, err
		}

		result, err := composer.InvokeSaga(starlarkCtx.Context, sagaName, input, starlarkCtx.PartyScope, stack)
		if err != nil {
			return nil, fmt.Errorf("invoke_saga(%q): %w", sagaName, err)
		}

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
}

// getThreadLocal extracts a typed value from thread local storage.
func getThreadLocal[T any](thread *starlark.Thread, key, caller string) (T, error) {
	var zero T
	val := thread.Local(key)
	if val == nil {
		return zero, fmt.Errorf("%s: %w", caller, ErrMissingClient)
	}
	typed, ok := val.(T)
	if !ok {
		return zero, fmt.Errorf("%s: %w", caller, ErrInvalidClientType)
	}
	return typed, nil
}

// starlarkDictToGoMap converts a Starlark dict to a Go map[string]interface{}.
func starlarkDictToGoMap(dict *starlark.Dict, caller string) (map[string]interface{}, error) {
	result := make(map[string]interface{})
	for _, item := range dict.Items() {
		key, ok := item[0].(starlark.String)
		if !ok {
			return nil, fmt.Errorf("%s: %w: input keys must be strings, got %s", caller, ErrInvalidParameterType, item[0].Type())
		}
		result[string(key)] = convertStarlarkToGo(item[1])
	}
	return result, nil
}

// getStarlarkContext extracts the StarlarkContext from thread local storage.
func getStarlarkContext(thread *starlark.Thread, caller string) (*StarlarkContext, error) {
	ctxVal := thread.Local("saga.StarlarkContext")
	if ctxVal == nil {
		return nil, fmt.Errorf("%s: %w", caller, ErrMissingStarlarkContext)
	}
	starlarkCtx, ok := ctxVal.(*StarlarkContext)
	if !ok {
		return nil, fmt.Errorf("%s: %w", caller, ErrInvalidStarlarkContext)
	}
	return starlarkCtx, nil
}

// getRefDataClient extracts the ReferenceDataClient from thread local storage.
func getRefDataClient(thread *starlark.Thread, caller string) (ReferenceDataClient, error) {
	clientVal := thread.Local("saga.ReferenceDataClient")
	if clientVal == nil {
		return nil, fmt.Errorf("%s: %w", caller, ErrMissingClient)
	}
	refDataClient, ok := clientVal.(ReferenceDataClient)
	if !ok {
		return nil, fmt.Errorf("%s: %w", caller, ErrInvalidClientType)
	}
	return refDataClient, nil
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
