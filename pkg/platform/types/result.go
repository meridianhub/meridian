// Package types provides generic type-safe primitives for explicit error and nil handling.
package types //nolint:revive // "types" is intentional, similar to go/types in stdlib

// Result represents either a successful value or an error.
// It forces explicit handling of both success and failure cases at compile time.
type Result[T any] struct {
	value T
	err   error
}

// Ok creates a successful Result containing the given value.
func Ok[T any](value T) Result[T] {
	return Result[T]{value: value, err: nil}
}

// Err creates a failed Result containing the given error.
func Err[T any](err error) Result[T] {
	var zero T
	return Result[T]{value: zero, err: err}
}

// IsOk returns true if the Result contains a value.
func (r Result[T]) IsOk() bool {
	return r.err == nil
}

// IsErr returns true if the Result contains an error.
func (r Result[T]) IsErr() bool {
	return r.err != nil
}

// Unwrap returns the value and error, forcing explicit handling.
// This is the preferred way to extract values from a Result.
func (r Result[T]) Unwrap() (T, error) {
	return r.value, r.err
}

// UnwrapOr returns the value if Ok, or the provided default if Err.
func (r Result[T]) UnwrapOr(def T) T {
	if r.err != nil {
		return def
	}
	return r.value
}

// UnwrapOrElse returns the value if Ok, or calls the provided function if Err.
func (r Result[T]) UnwrapOrElse(f func(error) T) T {
	if r.err != nil {
		return f(r.err)
	}
	return r.value
}

// Error returns the error if present, or nil if Ok.
func (r Result[T]) Error() error {
	return r.err
}

// Map transforms the value if Ok using the provided function.
// If Err, the error is propagated unchanged.
func Map[T, U any](r Result[T], f func(T) U) Result[U] {
	if r.err != nil {
		return Err[U](r.err)
	}
	return Ok(f(r.value))
}

// FlatMap chains operations that return Results.
// If the original Result is Err, the error is propagated.
func FlatMap[T, U any](r Result[T], f func(T) Result[U]) Result[U] {
	if r.err != nil {
		return Err[U](r.err)
	}
	return f(r.value)
}

// ToOption converts a Result to an Option, discarding any error.
// Ok becomes Some, Err becomes None.
func (r Result[T]) ToOption() Option[T] {
	if r.err != nil {
		return None[T]()
	}
	return Some(r.value)
}
