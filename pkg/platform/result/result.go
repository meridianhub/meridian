// Package result provides a generic Result[T] type for explicit error handling.
package result

// Result represents either a successful value of type T or an error.
// It provides a functional approach to error handling that makes error paths
// explicit at compile time, preventing ignored errors.
//
// Example usage:
//
//	func GetUser(id string) Result[User] {
//	    user, err := db.FindUser(id)
//	    if err != nil {
//	        return Err[User](err)
//	    }
//	    return Ok(user)
//	}
//
//	// Caller must handle the result
//	result := GetUser("123")
//	if result.IsErr() {
//	    log.Error("failed to get user", "error", result.Error())
//	    return
//	}
//	user, _ := result.Unwrap() // Safe after IsOk check
type Result[T any] struct {
	value T
	err   error
}

// Ok creates a successful Result containing the given value.
func Ok[T any](value T) Result[T] {
	return Result[T]{value: value, err: nil}
}

// Err creates a failed Result containing the given error.
// The value is set to the zero value of type T.
func Err[T any](err error) Result[T] {
	var zero T
	return Result[T]{value: zero, err: err}
}

// IsOk returns true if the Result contains a successful value.
func (r Result[T]) IsOk() bool {
	return r.err == nil
}

// IsErr returns true if the Result contains an error.
func (r Result[T]) IsErr() bool {
	return r.err != nil
}

// Unwrap returns both the value and error, forcing explicit handling.
// This is the primary way to extract the result, ensuring callers
// consider both success and failure cases.
func (r Result[T]) Unwrap() (T, error) {
	return r.value, r.err
}

// UnwrapOr returns the value if Ok, or the provided default if Err.
// Useful when a sensible default exists for error cases.
func (r Result[T]) UnwrapOr(def T) T {
	if r.err != nil {
		return def
	}
	return r.value
}

// UnwrapOrElse returns the value if Ok, or calls the provided function
// to compute a default if Err. Useful when computing the default is expensive
// and should only be done when needed.
func (r Result[T]) UnwrapOrElse(f func(error) T) T {
	if r.err != nil {
		return f(r.err)
	}
	return r.value
}

// Error returns the error if present, or nil if Ok.
// Useful for logging or when you only need to check the error.
func (r Result[T]) Error() error {
	return r.err
}

// Value returns the value without the error.
// Panics if called on an Err result. Use only after confirming IsOk().
func (r Result[T]) Value() T {
	if r.err != nil {
		panic("called Value() on an Err result")
	}
	return r.value
}

// Map transforms the value inside a Result using the provided function.
// If the Result is Err, the error is propagated unchanged.
//
// Example:
//
//	result := Ok(5).Map(func(x int) int { return x * 2 }) // Ok(10)
//	result := Err[int](err).Map(func(x int) int { return x * 2 }) // Err(err)
func Map[T, U any](r Result[T], f func(T) U) Result[U] {
	if r.err != nil {
		return Err[U](r.err)
	}
	return Ok(f(r.value))
}

// FlatMap chains operations that return Results.
// If the Result is Err, the error is propagated without calling f.
// This enables clean composition of fallible operations.
//
// Example:
//
//	func GetUser(id string) Result[User] { ... }
//	func GetUserOrders(user User) Result[[]Order] { ... }
//
//	result := FlatMap(GetUser("123"), GetUserOrders)
func FlatMap[T, U any](r Result[T], f func(T) Result[U]) Result[U] {
	if r.err != nil {
		return Err[U](r.err)
	}
	return f(r.value)
}

// MapErr transforms the error inside a Result using the provided function.
// If the Result is Ok, it is returned unchanged.
// Useful for wrapping errors with additional context.
func MapErr[T any](r Result[T], f func(error) error) Result[T] {
	if r.err == nil {
		return r
	}
	return Err[T](f(r.err))
}

// Collect converts a slice of Results into a Result containing a slice.
// If any Result is Err, returns the first error encountered.
// Useful for aggregating multiple fallible operations.
//
// Example:
//
//	results := []Result[int]{Ok(1), Ok(2), Ok(3)}
//	collected := Collect(results) // Ok([]int{1, 2, 3})
//
//	results := []Result[int]{Ok(1), Err[int](err), Ok(3)}
//	collected := Collect(results) // Err(err)
func Collect[T any](results []Result[T]) Result[[]T] {
	values := make([]T, 0, len(results))
	for _, r := range results {
		if r.err != nil {
			return Err[[]T](r.err)
		}
		values = append(values, r.value)
	}
	return Ok(values)
}

// FromTuple converts a traditional (value, error) tuple to a Result.
// Useful for wrapping existing functions that return error tuples.
//
// Example:
//
//	result := FromTuple(strconv.Atoi("123"))
func FromTuple[T any](value T, err error) Result[T] {
	if err != nil {
		return Err[T](err)
	}
	return Ok(value)
}

// Must converts a Result to its value, panicking if it's an Err.
// Use sparingly, typically only in initialization code or tests.
func Must[T any](r Result[T]) T {
	if r.err != nil {
		panic(r.err)
	}
	return r.value
}
