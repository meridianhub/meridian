package functional

// Map applies a function to each element of a slice, returning a new slice with the results.
// The original slice is not modified.
func Map[T, U any](slice []T, f func(T) U) []U {
	result := make([]U, len(slice))
	for i, v := range slice {
		result[i] = f(v)
	}
	return result
}

// Filter returns a new slice containing only elements that match the predicate.
// The original slice is not modified.
func Filter[T any](slice []T, pred func(T) bool) []T {
	result := make([]T, 0, len(slice))
	for _, v := range slice {
		if pred(v) {
			result = append(result, v)
		}
	}
	return result
}

// Reduce folds a slice into a single value by applying a function to each element.
// The function receives the accumulator and current element, returning the new accumulator.
func Reduce[T, U any](slice []T, init U, f func(U, T) U) U {
	acc := init
	for _, v := range slice {
		acc = f(acc, v)
	}
	return acc
}

// Find returns the first element matching the predicate wrapped in Some,
// or None if no element matches.
func Find[T any](slice []T, pred func(T) bool) Option[T] {
	for _, v := range slice {
		if pred(v) {
			return Some(v)
		}
	}
	return None[T]()
}

// Any returns true if any element in the slice matches the predicate.
// Returns false for empty slices.
func Any[T any](slice []T, pred func(T) bool) bool {
	for _, v := range slice {
		if pred(v) {
			return true
		}
	}
	return false
}

// All returns true if all elements in the slice match the predicate.
// Returns true for empty slices (vacuous truth).
func All[T any](slice []T, pred func(T) bool) bool {
	for _, v := range slice {
		if !pred(v) {
			return false
		}
	}
	return true
}

// GroupBy groups slice elements by a key function, returning a map of keys to element slices.
func GroupBy[T any, K comparable](slice []T, keyFn func(T) K) map[K][]T {
	result := make(map[K][]T)
	for _, v := range slice {
		key := keyFn(v)
		result[key] = append(result[key], v)
	}
	return result
}

// First returns the first element of the slice wrapped in Some,
// or None if the slice is empty.
func First[T any](slice []T) Option[T] {
	if len(slice) == 0 {
		return None[T]()
	}
	return Some(slice[0])
}

// Last returns the last element of the slice wrapped in Some,
// or None if the slice is empty.
func Last[T any](slice []T) Option[T] {
	if len(slice) == 0 {
		return None[T]()
	}
	return Some(slice[len(slice)-1])
}

// FindIndex returns the index of the first element matching the predicate wrapped in Some,
// or None if no element matches.
func FindIndex[T any](slice []T, pred func(T) bool) Option[int] {
	for i, v := range slice {
		if pred(v) {
			return Some(i)
		}
	}
	return None[int]()
}

// Count returns the number of elements matching the predicate.
func Count[T any](slice []T, pred func(T) bool) int {
	count := 0
	for _, v := range slice {
		if pred(v) {
			count++
		}
	}
	return count
}

// Partition splits a slice into two: elements matching the predicate and those that don't.
// Returns (matching, notMatching).
func Partition[T any](slice []T, pred func(T) bool) ([]T, []T) {
	matching := make([]T, 0, len(slice))
	notMatching := make([]T, 0, len(slice))
	for _, v := range slice {
		if pred(v) {
			matching = append(matching, v)
		} else {
			notMatching = append(notMatching, v)
		}
	}
	return matching, notMatching
}

// Flatten converts a slice of slices into a single flat slice.
func Flatten[T any](slices [][]T) []T {
	totalLen := 0
	for _, s := range slices {
		totalLen += len(s)
	}
	result := make([]T, 0, totalLen)
	for _, s := range slices {
		result = append(result, s...)
	}
	return result
}

// FlatMap applies a function that returns a slice to each element, then flattens the results.
func FlatMap[T, U any](slice []T, f func(T) []U) []U {
	return Flatten(Map(slice, f))
}
