package schema

import (
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"go.starlark.net/starlark"
)

// Thread-local storage key for StarlarkContext.
// This key is used to pass the StarlarkContext through the Starlark thread
// so handlers can access it during execution.
const starlarkContextKey = "saga.StarlarkContext"

// setStarlarkContext stores the StarlarkContext in thread-local storage.
func setStarlarkContext(thread *starlark.Thread, ctx *saga.StarlarkContext) {
	thread.SetLocal(starlarkContextKey, ctx)
}

// getStarlarkContext retrieves the StarlarkContext from thread-local storage.
func getStarlarkContext(thread *starlark.Thread) *saga.StarlarkContext {
	val := thread.Local(starlarkContextKey)
	if val == nil {
		return nil
	}
	ctx, ok := val.(*saga.StarlarkContext)
	if !ok {
		return nil
	}
	return ctx
}

// SetStarlarkContext is the exported version for external packages to set context.
func SetStarlarkContext(thread *starlark.Thread, ctx *saga.StarlarkContext) {
	setStarlarkContext(thread, ctx)
}

// GetStarlarkContext is the exported version for external packages to get context.
func GetStarlarkContext(thread *starlark.Thread) *saga.StarlarkContext {
	return getStarlarkContext(thread)
}
