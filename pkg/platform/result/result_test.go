package result

import (
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	errTest           = errors.New("test error")
	errCustom         = errors.New("custom error")
	errDivisionByZero = errors.New("division by zero")
	errFirst          = errors.New("error 1")
	errSecond         = errors.New("error 2")
	errIDMustPositive = errors.New("ID must be positive")
	errUserNotFound   = errors.New("user not found")
)

func TestOk(t *testing.T) {
	t.Run("creates successful result with value", func(t *testing.T) {
		result := Ok(42)

		assert.True(t, result.IsOk())
		assert.False(t, result.IsErr())

		value, err := result.Unwrap()
		assert.Equal(t, 42, value)
		assert.NoError(t, err)
	})

	t.Run("works with string type", func(t *testing.T) {
		result := Ok("hello")

		value, err := result.Unwrap()
		assert.Equal(t, "hello", value)
		assert.NoError(t, err)
	})

	t.Run("works with struct type", func(t *testing.T) {
		type User struct {
			ID   string
			Name string
		}
		user := User{ID: "123", Name: "Alice"}
		result := Ok(user)

		value, err := result.Unwrap()
		assert.Equal(t, user, value)
		assert.NoError(t, err)
	})

	t.Run("works with pointer type", func(t *testing.T) {
		value := 42
		result := Ok(&value)

		ptr, err := result.Unwrap()
		assert.Equal(t, &value, ptr)
		assert.NoError(t, err)
	})

	t.Run("works with slice type", func(t *testing.T) {
		slice := []int{1, 2, 3}
		result := Ok(slice)

		value, err := result.Unwrap()
		assert.Equal(t, slice, value)
		assert.NoError(t, err)
	})
}

func TestErr(t *testing.T) {
	t.Run("creates failed result with error", func(t *testing.T) {
		result := Err[int](errTest)

		assert.False(t, result.IsOk())
		assert.True(t, result.IsErr())

		value, err := result.Unwrap()
		assert.Zero(t, value)
		assert.ErrorIs(t, err, errTest)
	})

	t.Run("sets zero value for complex types", func(t *testing.T) {
		type User struct {
			ID   string
			Name string
		}
		result := Err[User](errTest)

		value, err := result.Unwrap()
		assert.Equal(t, User{}, value)
		assert.ErrorIs(t, err, errTest)
	})

	t.Run("works with pointer type - zero is nil", func(t *testing.T) {
		result := Err[*int](errTest)

		value, err := result.Unwrap()
		assert.Nil(t, value)
		assert.ErrorIs(t, err, errTest)
	})
}

func TestIsOk(t *testing.T) {
	t.Run("returns true for Ok", func(t *testing.T) {
		result := Ok(42)
		assert.True(t, result.IsOk())
	})

	t.Run("returns false for Err", func(t *testing.T) {
		result := Err[int](errTest)
		assert.False(t, result.IsOk())
	})
}

func TestIsErr(t *testing.T) {
	t.Run("returns false for Ok", func(t *testing.T) {
		result := Ok(42)
		assert.False(t, result.IsErr())
	})

	t.Run("returns true for Err", func(t *testing.T) {
		result := Err[int](errTest)
		assert.True(t, result.IsErr())
	})
}

func TestUnwrap(t *testing.T) {
	t.Run("returns value and nil for Ok", func(t *testing.T) {
		result := Ok("success")
		value, err := result.Unwrap()

		assert.Equal(t, "success", value)
		assert.NoError(t, err)
	})

	t.Run("returns zero value and error for Err", func(t *testing.T) {
		result := Err[string](errTest)
		value, err := result.Unwrap()

		assert.Empty(t, value)
		assert.ErrorIs(t, err, errTest)
	})
}

func TestUnwrapOr(t *testing.T) {
	t.Run("returns value for Ok", func(t *testing.T) {
		result := Ok(42)
		value := result.UnwrapOr(0)

		assert.Equal(t, 42, value)
	})

	t.Run("returns default for Err", func(t *testing.T) {
		result := Err[int](errTest)
		value := result.UnwrapOr(-1)

		assert.Equal(t, -1, value)
	})
}

func TestUnwrapOrElse(t *testing.T) {
	t.Run("returns value for Ok without calling function", func(t *testing.T) {
		called := false
		result := Ok(42)
		value := result.UnwrapOrElse(func(error) int {
			called = true
			return -1
		})

		assert.Equal(t, 42, value)
		assert.False(t, called)
	})

	t.Run("calls function for Err", func(t *testing.T) {
		result := Err[int](errTest)
		value := result.UnwrapOrElse(func(err error) int {
			if errors.Is(err, errTest) {
				return -1
			}
			return 0
		})

		assert.Equal(t, -1, value)
	})

	t.Run("passes error to function", func(t *testing.T) {
		result := Err[string](errCustom)

		var receivedErr error
		result.UnwrapOrElse(func(err error) string {
			receivedErr = err
			return ""
		})

		assert.ErrorIs(t, receivedErr, errCustom)
	})
}

func TestError(t *testing.T) {
	t.Run("returns nil for Ok", func(t *testing.T) {
		result := Ok(42)
		assert.NoError(t, result.Error())
	})

	t.Run("returns error for Err", func(t *testing.T) {
		result := Err[int](errTest)
		assert.ErrorIs(t, result.Error(), errTest)
	})
}

func TestValue(t *testing.T) {
	t.Run("returns value for Ok", func(t *testing.T) {
		result := Ok(42)
		assert.Equal(t, 42, result.Value())
	})

	t.Run("panics for Err", func(t *testing.T) {
		result := Err[int](errTest)

		assert.Panics(t, func() {
			result.Value()
		})
	})
}

func TestMap(t *testing.T) {
	t.Run("transforms value for Ok", func(t *testing.T) {
		result := Ok(5)
		mapped := Map(result, func(x int) int { return x * 2 })

		assert.True(t, mapped.IsOk())
		value, _ := mapped.Unwrap()
		assert.Equal(t, 10, value)
	})

	t.Run("changes type for Ok", func(t *testing.T) {
		result := Ok(42)
		mapped := Map(result, func(x int) string { return strconv.Itoa(x) })

		assert.True(t, mapped.IsOk())
		value, _ := mapped.Unwrap()
		assert.Equal(t, "42", value)
	})

	t.Run("propagates error for Err", func(t *testing.T) {
		result := Err[int](errTest)
		mapped := Map(result, func(x int) string { return strconv.Itoa(x) })

		assert.True(t, mapped.IsErr())
		_, err := mapped.Unwrap()
		assert.ErrorIs(t, err, errTest)
	})

	t.Run("does not call function for Err", func(t *testing.T) {
		called := false
		result := Err[int](errTest)
		Map(result, func(x int) int {
			called = true
			return x * 2
		})

		assert.False(t, called)
	})
}

func TestFlatMap(t *testing.T) {
	divide := func(x int) Result[int] {
		if x == 0 {
			return Err[int](errDivisionByZero)
		}
		return Ok(100 / x)
	}

	t.Run("chains successful operations", func(t *testing.T) {
		result := Ok(5)
		chained := FlatMap(result, divide)

		assert.True(t, chained.IsOk())
		value, _ := chained.Unwrap()
		assert.Equal(t, 20, value)
	})

	t.Run("propagates error from original", func(t *testing.T) {
		result := Err[int](errTest)
		chained := FlatMap(result, divide)

		assert.True(t, chained.IsErr())
		_, err := chained.Unwrap()
		assert.ErrorIs(t, err, errTest)
	})

	t.Run("propagates error from chained function", func(t *testing.T) {
		result := Ok(0)
		chained := FlatMap(result, divide)

		assert.True(t, chained.IsErr())
		_, err := chained.Unwrap()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "division by zero")
	})

	t.Run("does not call function for Err", func(t *testing.T) {
		called := false
		result := Err[int](errTest)
		FlatMap(result, func(x int) Result[int] {
			called = true
			return Ok(x)
		})

		assert.False(t, called)
	})

	t.Run("chains multiple operations", func(t *testing.T) {
		parse := func(s string) Result[int] {
			n, err := strconv.Atoi(s)
			return FromTuple(n, err)
		}

		double := func(n int) Result[int] {
			return Ok(n * 2)
		}

		result := parse("21")
		chained := FlatMap(FlatMap(result, double), double)

		assert.True(t, chained.IsOk())
		value, _ := chained.Unwrap()
		assert.Equal(t, 84, value)
	})
}

func TestMapErr(t *testing.T) {
	t.Run("returns Ok unchanged", func(t *testing.T) {
		result := Ok(42)
		mapped := MapErr(result, func(err error) error {
			return fmt.Errorf("wrapped: %w", err)
		})

		assert.True(t, mapped.IsOk())
		value, _ := mapped.Unwrap()
		assert.Equal(t, 42, value)
	})

	t.Run("transforms error for Err", func(t *testing.T) {
		result := Err[int](errTest)
		mapped := MapErr(result, func(err error) error {
			return fmt.Errorf("wrapped: %w", err)
		})

		assert.True(t, mapped.IsErr())
		_, err := mapped.Unwrap()
		assert.Contains(t, err.Error(), "wrapped:")
		assert.ErrorIs(t, err, errTest)
	})

	t.Run("does not call function for Ok", func(t *testing.T) {
		called := false
		result := Ok(42)
		MapErr(result, func(err error) error {
			called = true
			return err
		})

		assert.False(t, called)
	})
}

func TestCollect(t *testing.T) {
	t.Run("collects all Ok values", func(t *testing.T) {
		results := []Result[int]{Ok(1), Ok(2), Ok(3)}
		collected := Collect(results)

		assert.True(t, collected.IsOk())
		values, _ := collected.Unwrap()
		assert.Equal(t, []int{1, 2, 3}, values)
	})

	t.Run("returns first error", func(t *testing.T) {
		results := []Result[int]{Ok(1), Err[int](errFirst), Err[int](errSecond)}
		collected := Collect(results)

		assert.True(t, collected.IsErr())
		_, err := collected.Unwrap()
		assert.ErrorIs(t, err, errFirst)
	})

	t.Run("handles empty slice", func(t *testing.T) {
		results := []Result[int]{}
		collected := Collect(results)

		assert.True(t, collected.IsOk())
		values, _ := collected.Unwrap()
		assert.Empty(t, values)
	})

	t.Run("handles single Ok", func(t *testing.T) {
		results := []Result[string]{Ok("hello")}
		collected := Collect(results)

		assert.True(t, collected.IsOk())
		values, _ := collected.Unwrap()
		assert.Equal(t, []string{"hello"}, values)
	})

	t.Run("handles single Err", func(t *testing.T) {
		results := []Result[string]{Err[string](errTest)}
		collected := Collect(results)

		assert.True(t, collected.IsErr())
		_, err := collected.Unwrap()
		assert.ErrorIs(t, err, errTest)
	})
}

func TestFromTuple(t *testing.T) {
	t.Run("creates Ok from nil error", func(t *testing.T) {
		result := FromTuple(42, nil)

		assert.True(t, result.IsOk())
		value, _ := result.Unwrap()
		assert.Equal(t, 42, value)
	})

	t.Run("creates Err from error", func(t *testing.T) {
		result := FromTuple(0, errTest)

		assert.True(t, result.IsErr())
		_, err := result.Unwrap()
		assert.ErrorIs(t, err, errTest)
	})

	t.Run("works with strconv.Atoi success", func(t *testing.T) {
		result := FromTuple(strconv.Atoi("123"))

		assert.True(t, result.IsOk())
		value, _ := result.Unwrap()
		assert.Equal(t, 123, value)
	})

	t.Run("works with strconv.Atoi failure", func(t *testing.T) {
		result := FromTuple(strconv.Atoi("not a number"))

		assert.True(t, result.IsErr())
	})
}

func TestMust(t *testing.T) {
	t.Run("returns value for Ok", func(t *testing.T) {
		result := Ok(42)
		value := Must(result)

		assert.Equal(t, 42, value)
	})

	t.Run("panics for Err", func(t *testing.T) {
		result := Err[int](errTest)

		assert.Panics(t, func() {
			Must(result)
		})
	})

	t.Run("panics with the original error", func(t *testing.T) {
		result := Err[int](errTest)

		defer func() {
			r := recover()
			require.NotNil(t, r)
			assert.Equal(t, errTest, r)
		}()

		Must(result)
	})
}

func TestResultWithComplexTypes(t *testing.T) {
	t.Run("works with map type", func(t *testing.T) {
		m := map[string]int{"a": 1, "b": 2}
		result := Ok(m)

		assert.True(t, result.IsOk())
		value, _ := result.Unwrap()
		assert.Equal(t, m, value)
	})

	t.Run("works with channel type", func(t *testing.T) {
		ch := make(chan int, 1)
		result := Ok(ch)

		assert.True(t, result.IsOk())
		value, _ := result.Unwrap()
		assert.Equal(t, ch, value)
	})

	t.Run("works with function type", func(t *testing.T) {
		fn := func(x int) int { return x * 2 }
		result := Ok(fn)

		assert.True(t, result.IsOk())
		value, _ := result.Unwrap()
		assert.Equal(t, 10, value(5))
	})

	t.Run("works with interface type", func(t *testing.T) {
		var iface interface{} = "hello"
		result := Ok(iface)

		assert.True(t, result.IsOk())
		value, _ := result.Unwrap()
		assert.Equal(t, "hello", value)
	})
}

func TestResultChaining(t *testing.T) {
	t.Run("complex chaining example", func(t *testing.T) {
		// Simulate a series of operations that could fail
		parseID := func(s string) Result[int] {
			id, err := strconv.Atoi(s)
			if err != nil {
				return Err[int](fmt.Errorf("invalid ID: %w", err))
			}
			return Ok(id)
		}

		validateID := func(id int) Result[int] {
			if id <= 0 {
				return Err[int](errIDMustPositive)
			}
			return Ok(id)
		}

		fetchName := func(id int) Result[string] {
			names := map[int]string{1: "Alice", 2: "Bob", 3: "Charlie"}
			name, ok := names[id]
			if !ok {
				return Err[string](fmt.Errorf("%w: %d", errUserNotFound, id))
			}
			return Ok(name)
		}

		// Success case
		result := FlatMap(FlatMap(parseID("2"), validateID), fetchName)
		assert.True(t, result.IsOk())
		name, _ := result.Unwrap()
		assert.Equal(t, "Bob", name)

		// Parse failure
		result = FlatMap(FlatMap(parseID("abc"), validateID), fetchName)
		assert.True(t, result.IsErr())
		assert.Contains(t, result.Error().Error(), "invalid ID")

		// Validation failure
		result = FlatMap(FlatMap(parseID("-1"), validateID), fetchName)
		assert.True(t, result.IsErr())
		assert.Contains(t, result.Error().Error(), "must be positive")

		// Not found
		result = FlatMap(FlatMap(parseID("999"), validateID), fetchName)
		assert.True(t, result.IsErr())
		assert.Contains(t, result.Error().Error(), "not found")
	})
}
