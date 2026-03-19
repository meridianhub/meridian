// Package types provides type aliases and helpers for explicit error and nil handling.
//
// This package re-exports types from github.com/samber/mo for consistent usage
// across the Meridian codebase. The samber/mo library provides battle-tested
// implementations of functional programming primitives.
//
// # Option[T] - Explicit nil handling
//
// Option represents an optional value that may or may not be present.
// Use it to eliminate nil pointer dereference bugs at compile time.
//
//	import "github.com/samber/mo"
//
//	// Creating Options
//	opt := mo.Some(42)         // Contains a value
//	opt := mo.None[int]()      // Empty option
//
//	// Checking and extracting values
//	if opt.IsPresent() {
//	    value := opt.MustGet()  // Safe after IsPresent check
//	}
//
//	// Safe extraction with defaults
//	value := opt.OrElse(0)     // Returns 0 if None
//
//	// Functional composition
//	doubled := opt.Map(func(x int) int { return x * 2 })
//
// # Result[T] - Explicit error handling
//
// Result represents either a successful value or an error.
// Use it to make error handling explicit and prevent ignored errors.
//
//	import "github.com/samber/mo"
//
//	// Creating Results
//	result := mo.Ok(42)                    // Success
//	result := mo.Err[int](errors.New(""))  // Failure
//
//	// Wrapping traditional Go returns
//	result := mo.TupleToResult(strconv.Atoi("123"))
//
//	// Checking and extracting
//	if result.IsOk() {
//	    value := result.MustGet()  // Safe after IsOk check
//	}
//
//	// Functional composition
//	mapped := result.Map(func(x int) int { return x * 2 })
//
// # Why use mo types?
//
//   - Compile-time safety: Prevents nil dereferences and ignored errors
//   - Self-documenting: Function signatures clearly show optional/fallible operations
//   - Composable: Chain operations without nested if/else blocks
//   - Zero overhead: Benchmarks show no performance cost vs traditional patterns
//
// For more details, see: https://github.com/samber/mo
package types
