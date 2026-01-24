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
	builtins["cel_eval"] = starlark.NewBuiltin("cel_eval", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var expression string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "expression", &expression); err != nil {
			return nil, err
		}
		// Placeholder - actual CEL evaluation would happen at runtime
		return starlark.String(expression), nil
	})

	// resolve_account - resolve account ID from reference
	builtins["resolve_account"] = starlark.NewBuiltin("resolve_account", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var reference string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "reference", &reference); err != nil {
			return nil, err
		}
		// Placeholder - actual resolution happens at runtime
		return starlark.String(reference), nil
	})

	// resolve_instrument - resolve instrument ID from reference
	builtins["resolve_instrument"] = starlark.NewBuiltin("resolve_instrument", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var reference string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "reference", &reference); err != nil {
			return nil, err
		}
		// Placeholder - actual resolution happens at runtime
		return starlark.String(reference), nil
	})

	// invoke_saga - invoke a child saga
	builtins["invoke_saga"] = starlark.NewBuiltin("invoke_saga", func(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		var sagaName string
		if err := starlark.UnpackArgs(b.Name(), args, kwargs, "saga_name", &sagaName); err != nil {
			return nil, err
		}
		// Placeholder - actual invocation happens at runtime
		return starlark.None, nil
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
