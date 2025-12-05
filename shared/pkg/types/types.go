package types //nolint:revive // "types" is intentional, similar to go/types in stdlib

import (
	"time"

	"github.com/samber/mo"
)

// Type aliases for consistent naming across the Meridian codebase.
// These provide ergonomic access to mo types without requiring every file
// to import github.com/samber/mo directly.

// Option represents an optional value that may or may not be present.
// Use instead of *T when the absence of a value is semantically meaningful.
type Option[T any] = mo.Option[T]

// Result represents either a successful value or an error.
// Use instead of (T, error) when you want to enforce explicit error handling.
type Result[T any] = mo.Result[T]

// Constructor functions - re-exported for convenience

// Some creates an Option containing the given value.
func Some[T any](value T) Option[T] {
	return mo.Some(value)
}

// None creates an empty Option of the given type.
func None[T any]() Option[T] {
	return mo.None[T]()
}

// Ok creates a Result containing a successful value.
func Ok[T any](value T) Result[T] {
	return mo.Ok(value)
}

// Err creates a Result containing an error.
func Err[T any](err error) Result[T] {
	return mo.Err[T](err)
}

// TupleToResult converts a traditional Go (value, error) return into a Result.
// This is useful for wrapping existing Go functions that return (T, error).
//
// Example:
//
//	result := types.TupleToResult(strconv.Atoi("123"))
func TupleToResult[T any](value T, err error) Result[T] {
	return mo.TupleToResult(value, err)
}

// PointerToOption converts a pointer to an Option.
// nil becomes None, non-nil becomes Some(*ptr).
// This is useful for migrating from *T to Option[T] patterns.
//
// Example:
//
//	var expiresAt *time.Time = nil
//	opt := types.PointerToOption(expiresAt) // None[time.Time]
//
//	t := time.Now()
//	expiresAt = &t
//	opt = types.PointerToOption(expiresAt) // Some(time.Now())
func PointerToOption[T any](ptr *T) Option[T] {
	if ptr == nil {
		return mo.None[T]()
	}
	return mo.Some(*ptr)
}

// OptionToPointer converts an Option to a pointer.
// None becomes nil, Some(v) becomes &v.
// This is useful when interfacing with APIs that expect pointers.
//
// Example:
//
//	opt := types.Some(time.Now())
//	ptr := types.OptionToPointer(opt) // *time.Time pointing to now
//
//	opt = types.None[time.Time]()
//	ptr = types.OptionToPointer(opt) // nil
func OptionToPointer[T any](opt Option[T]) *T {
	if opt.IsAbsent() {
		return nil
	}
	value := opt.MustGet()
	return &value
}

// OptionalTime is a type alias for Option[time.Time], commonly used for
// optional timestamps like ExpiresAt, CompletedAt, etc.
type OptionalTime = Option[time.Time]

// SomeTime creates an OptionalTime containing the given time.
func SomeTime(t time.Time) OptionalTime {
	return mo.Some(t)
}

// NoTime creates an empty OptionalTime.
func NoTime() OptionalTime {
	return mo.None[time.Time]()
}
