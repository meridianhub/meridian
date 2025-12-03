// Package types provides core generic types for the Meridian platform,
// including Result[T] for explicit error handling.
package types

// Result represents either a successful value or an error.
// It provides explicit error handling through a monadic interface.
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

// Unwrap returns the value and error for explicit handling.
func (r Result[T]) Unwrap() (T, error) {
	return r.value, r.err
}

// UnwrapOr returns the value if Ok, otherwise returns the default.
func (r Result[T]) UnwrapOr(def T) T {
	if r.err != nil {
		return def
	}
	return r.value
}

// UnwrapOrElse returns the value if Ok, otherwise calls f to compute a default.
func (r Result[T]) UnwrapOrElse(f func(error) T) T {
	if r.err != nil {
		return f(r.err)
	}
	return r.value
}

// Error returns the error if present, nil otherwise.
func (r Result[T]) Error() error {
	return r.err
}

// Value returns the value if present, zero value otherwise.
func (r Result[T]) Value() T {
	return r.value
}

// Map transforms the value if Ok using the provided function.
func Map[T, U any](r Result[T], f func(T) U) Result[U] {
	if r.err != nil {
		return Err[U](r.err)
	}
	return Ok(f(r.value))
}

// FlatMap chains operations that return Results.
func FlatMap[T, U any](r Result[T], f func(T) Result[U]) Result[U] {
	if r.err != nil {
		return Err[U](r.err)
	}
	return f(r.value)
}

// MapErr transforms the error if present using the provided function.
func MapErr[T any](r Result[T], f func(error) error) Result[T] {
	if r.err != nil {
		return Err[T](f(r.err))
	}
	return r
}

// And returns other if Ok, otherwise returns the error.
func And[T, U any](r Result[T], other Result[U]) Result[U] {
	if r.err != nil {
		return Err[U](r.err)
	}
	return other
}

// Or returns the result if Ok, otherwise returns other.
func Or[T any](r Result[T], other Result[T]) Result[T] {
	if r.err != nil {
		return other
	}
	return r
}

// Inspect calls f with the value if Ok, then returns the original Result.
func Inspect[T any](r Result[T], f func(T)) Result[T] {
	if r.err == nil {
		f(r.value)
	}
	return r
}

// InspectErr calls f with the error if Err, then returns the original Result.
func InspectErr[T any](r Result[T], f func(error)) Result[T] {
	if r.err != nil {
		f(r.err)
	}
	return r
}
