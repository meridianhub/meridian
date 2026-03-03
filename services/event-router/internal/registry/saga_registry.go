// Package registry provides an in-memory registry mapping event channels to
// applicable saga definitions with precompiled CEL filter programs.
package registry

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/google/cel-go/cel"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	sharedcel "github.com/meridianhub/meridian/shared/pkg/cel"
)

const eventTriggerPrefix = "event:"

// ErrNilSagaDefinition is returned by Reload when a nil entry is found in the input slice.
var ErrNilSagaDefinition = errors.New("nil saga definition")

// CompiledSaga pairs a saga definition with its precompiled CEL filter program.
// FilterProgram is nil when the saga definition carries no filter expression.
type CompiledSaga struct {
	// Definition is the original saga definition from the manifest.
	Definition *controlplanev1.SagaDefinition

	// FilterProgram is the precompiled CEL program for the filter expression.
	// Nil when the definition has no filter — the saga matches all events on the channel.
	FilterProgram cel.Program
}

// SagaRegistry is a thread-safe, in-memory index mapping event channel names to
// the compiled sagas that should be triggered when an event on that channel arrives.
//
// The registry is populated by calling Reload, which atomically replaces all
// registrations. Only sagas whose trigger starts with "event:" are indexed.
type SagaRegistry struct {
	mu        sync.RWMutex
	byChannel map[string][]*CompiledSaga
	compiler  *sharedcel.Compiler
}

// NewSagaRegistry creates an empty SagaRegistry with a CEL compiler ready to
// compile event-filter expressions.
func NewSagaRegistry() (*SagaRegistry, error) {
	compiler, err := sharedcel.NewCompiler()
	if err != nil {
		return nil, fmt.Errorf("create CEL compiler: %w", err)
	}

	return &SagaRegistry{
		byChannel: make(map[string][]*CompiledSaga),
		compiler:  compiler,
	}, nil
}

// Reload atomically replaces all registrations from the provided saga definitions.
// Only definitions whose trigger begins with "event:" are registered; all others
// are silently skipped.
//
// If any definition is nil or carries an invalid CEL filter expression, Reload
// returns an error and leaves the registry unchanged (atomic replacement guarantee).
func (r *SagaRegistry) Reload(sagas []*controlplanev1.SagaDefinition) error {
	newByChannel := make(map[string][]*CompiledSaga)

	for i, def := range sagas {
		if def == nil {
			return fmt.Errorf("%w at index %d", ErrNilSagaDefinition, i)
		}

		if !strings.HasPrefix(def.GetTrigger(), eventTriggerPrefix) {
			continue
		}

		channel := strings.TrimPrefix(def.GetTrigger(), eventTriggerPrefix)

		compiled := &CompiledSaga{Definition: def}

		if def.Filter != nil {
			prg, err := r.compiler.CompileEventFilter(def.GetFilter())
			if err != nil {
				return fmt.Errorf("compile CEL filter for saga %q: %w", def.GetName(), err)
			}
			compiled.FilterProgram = prg
		}

		newByChannel[channel] = append(newByChannel[channel], compiled)
	}

	r.mu.Lock()
	r.byChannel = newByChannel
	r.mu.Unlock()

	return nil
}

// GetApplicableSagas returns a defensive copy of the compiled sagas registered
// for the given channel. Returns nil if no sagas are registered for the channel.
func (r *SagaRegistry) GetApplicableSagas(channel string) []*CompiledSaga {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sagas, ok := r.byChannel[channel]
	if !ok {
		return nil
	}

	out := make([]*CompiledSaga, len(sagas))
	copy(out, sagas)
	return out
}
