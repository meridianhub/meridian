package types //nolint:revive // "types" is intentional, similar to go/types in stdlib

import (
	"encoding/json"
)

// Option represents an optional value that may or may not be present.
// It forces explicit handling of the absence case at compile time,
// eliminating nil pointer dereference bugs.
type Option[T any] struct {
	value T
	valid bool
}

// Some creates an Option containing the given value.
func Some[T any](value T) Option[T] {
	return Option[T]{value: value, valid: true}
}

// None creates an empty Option.
func None[T any]() Option[T] {
	var zero T
	return Option[T]{value: zero, valid: false}
}

// IsSome returns true if the Option contains a value.
func (o Option[T]) IsSome() bool {
	return o.valid
}

// IsNone returns true if the Option is empty.
func (o Option[T]) IsNone() bool {
	return !o.valid
}

// Unwrap returns the value or panics if None.
// Use with caution - prefer UnwrapOr or pattern matching via IsSome.
func (o Option[T]) Unwrap() T {
	if !o.valid {
		panic("called Unwrap on None")
	}
	return o.value
}

// UnwrapOr returns the value if Some, or the provided default if None.
func (o Option[T]) UnwrapOr(def T) T {
	if !o.valid {
		return def
	}
	return o.value
}

// UnwrapOrElse returns the value if Some, or calls the provided function if None.
func (o Option[T]) UnwrapOrElse(f func() T) T {
	if !o.valid {
		return f()
	}
	return o.value
}

// Get returns the value and a boolean indicating presence.
// This is the idiomatic Go pattern for optional values.
func (o Option[T]) Get() (T, bool) {
	return o.value, o.valid
}

// OptionMap transforms the value if Some using the provided function.
// If None, returns None.
func OptionMap[T, U any](o Option[T], f func(T) U) Option[U] {
	if !o.valid {
		return None[U]()
	}
	return Some(f(o.value))
}

// OptionFlatMap chains Option-returning operations.
// If the original Option is None, returns None.
func OptionFlatMap[T, U any](o Option[T], f func(T) Option[U]) Option[U] {
	if !o.valid {
		return None[U]()
	}
	return f(o.value)
}

// Filter returns the Option if Some and the predicate returns true, otherwise None.
func (o Option[T]) Filter(predicate func(T) bool) Option[T] {
	if !o.valid || !predicate(o.value) {
		return None[T]()
	}
	return o
}

// ToResult converts an Option to a Result.
// Some becomes Ok, None becomes Err with the provided error.
func (o Option[T]) ToResult(err error) Result[T] {
	if !o.valid {
		return Err[T](err)
	}
	return Ok(o.value)
}

// MarshalJSON implements json.Marshaler.
// Some values are marshaled as the value itself, None as null.
func (o Option[T]) MarshalJSON() ([]byte, error) {
	if !o.valid {
		return []byte("null"), nil
	}
	return json.Marshal(o.value)
}

// UnmarshalJSON implements json.Unmarshaler.
// null becomes None, any other value becomes Some.
func (o *Option[T]) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*o = None[T]()
		return nil
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*o = Some(value)
	return nil
}

// FromPtr creates an Option from a pointer.
// nil becomes None, non-nil becomes Some containing the dereferenced value.
func FromPtr[T any](ptr *T) Option[T] {
	if ptr == nil {
		return None[T]()
	}
	return Some(*ptr)
}

// ToPtr converts an Option to a pointer.
// Some returns a pointer to the value, None returns nil.
func (o Option[T]) ToPtr() *T {
	if !o.valid {
		return nil
	}
	v := o.value
	return &v
}
