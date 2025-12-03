// Package functional provides generic collection operations for type-safe slice manipulation.
package functional

// Option represents an optional value that may or may not be present.
// It provides compile-time safety for handling nullable values without using pointers.
type Option[T any] struct {
	value   T
	present bool
}

// Some creates an Option containing the given value.
func Some[T any](value T) Option[T] {
	return Option[T]{value: value, present: true}
}

// None creates an empty Option with no value.
func None[T any]() Option[T] {
	return Option[T]{}
}

// IsSome returns true if the Option contains a value.
func (o Option[T]) IsSome() bool {
	return o.present
}

// IsNone returns true if the Option contains no value.
func (o Option[T]) IsNone() bool {
	return !o.present
}

// Unwrap returns the contained value.
// Panics if the Option is None. Use with caution.
func (o Option[T]) Unwrap() T {
	if !o.present {
		panic("called Unwrap on None Option")
	}
	return o.value
}

// UnwrapOr returns the contained value or the provided default if None.
func (o Option[T]) UnwrapOr(defaultValue T) T {
	if o.present {
		return o.value
	}
	return defaultValue
}

// UnwrapOrElse returns the contained value or calls the function to get a default if None.
func (o Option[T]) UnwrapOrElse(f func() T) T {
	if o.present {
		return o.value
	}
	return f()
}

// Get returns the contained value and a boolean indicating if a value was present.
// This is the safe way to access the value.
func (o Option[T]) Get() (T, bool) {
	return o.value, o.present
}

// MapOption transforms the contained value using the provided function.
// Returns None if the Option is None.
func MapOption[T, U any](o Option[T], f func(T) U) Option[U] {
	if !o.present {
		return None[U]()
	}
	return Some(f(o.value))
}

// FlatMapOption transforms the contained value using a function that returns an Option.
// Returns None if the original Option is None.
func FlatMapOption[T, U any](o Option[T], f func(T) Option[U]) Option[U] {
	if !o.present {
		return None[U]()
	}
	return f(o.value)
}
