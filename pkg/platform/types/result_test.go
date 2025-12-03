package types //nolint:revive // "types" is intentional, similar to go/types in stdlib

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	errTest      = errors.New("error")
	errSomething = errors.New("something went wrong")
	errInner     = errors.New("inner error")
	errOriginal  = errors.New("original error")
	errDivByZero = errors.New("division by zero")
)

func TestOk(t *testing.T) {
	result := Ok(42)
	assert.True(t, result.IsOk())
	assert.False(t, result.IsErr())
	val, err := result.Unwrap()
	assert.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestOkWithString(t *testing.T) {
	result := Ok("hello")
	assert.True(t, result.IsOk())
	val, err := result.Unwrap()
	assert.NoError(t, err)
	assert.Equal(t, "hello", val)
}

func TestErr(t *testing.T) {
	result := Err[int](errSomething)
	assert.True(t, result.IsErr())
	assert.False(t, result.IsOk())
	_, err := result.Unwrap()
	assert.Equal(t, errSomething, err)
}

func TestResultError(t *testing.T) {
	t.Run("returns nil when Ok", func(t *testing.T) {
		result := Ok(42)
		assert.Nil(t, result.Error())
	})

	t.Run("returns error when Err", func(t *testing.T) {
		result := Err[int](errTest)
		assert.Equal(t, errTest, result.Error())
	})
}

func TestResultUnwrapOr(t *testing.T) {
	t.Run("returns value when Ok", func(t *testing.T) {
		result := Ok(42)
		assert.Equal(t, 42, result.UnwrapOr(0))
	})

	t.Run("returns default when Err", func(t *testing.T) {
		result := Err[int](errTest)
		assert.Equal(t, 99, result.UnwrapOr(99))
	})
}

func TestResultUnwrapOrElse(t *testing.T) {
	t.Run("returns value when Ok", func(t *testing.T) {
		result := Ok(42)
		val := result.UnwrapOrElse(func(_ error) int { return 0 })
		assert.Equal(t, 42, val)
	})

	t.Run("calls function with error when Err", func(t *testing.T) {
		result := Err[int](errTest)
		var receivedErr error
		val := result.UnwrapOrElse(func(err error) int {
			receivedErr = err
			return 99
		})
		assert.Equal(t, 99, val)
		assert.Equal(t, errTest, receivedErr)
	})
}

func TestResultMap(t *testing.T) {
	t.Run("transforms value when Ok", func(t *testing.T) {
		result := Ok(21)
		mapped := Map(result, func(x int) int { return x * 2 })
		assert.True(t, mapped.IsOk())
		val, err := mapped.Unwrap()
		assert.NoError(t, err)
		assert.Equal(t, 42, val)
	})

	t.Run("propagates error when Err", func(t *testing.T) {
		result := Err[int](errTest)
		called := false
		mapped := Map(result, func(x int) int {
			called = true
			return x * 2
		})
		assert.False(t, called)
		assert.True(t, mapped.IsErr())
		assert.Equal(t, errTest, mapped.Error())
	})

	t.Run("transforms to different type", func(t *testing.T) {
		result := Ok(42)
		mapped := Map(result, func(_ int) string { return "value" })
		assert.True(t, mapped.IsOk())
		val, _ := mapped.Unwrap()
		assert.Equal(t, "value", val)
	})
}

func TestResultFlatMap(t *testing.T) {
	t.Run("chains Ok to Ok", func(t *testing.T) {
		result := Ok(21)
		chained := FlatMap(result, func(x int) Result[int] {
			return Ok(x * 2)
		})
		assert.True(t, chained.IsOk())
		val, _ := chained.Unwrap()
		assert.Equal(t, 42, val)
	})

	t.Run("chains Ok to Err", func(t *testing.T) {
		result := Ok(21)
		chained := FlatMap(result, func(_ int) Result[int] {
			return Err[int](errInner)
		})
		assert.True(t, chained.IsErr())
		assert.Equal(t, errInner, chained.Error())
	})

	t.Run("propagates error when original is Err", func(t *testing.T) {
		result := Err[int](errOriginal)
		called := false
		chained := FlatMap(result, func(x int) Result[int] {
			called = true
			return Ok(x * 2)
		})
		assert.False(t, called)
		assert.True(t, chained.IsErr())
		assert.Equal(t, errOriginal, chained.Error())
	})
}

func TestResultToOption(t *testing.T) {
	t.Run("Ok becomes Some", func(t *testing.T) {
		result := Ok(42)
		opt := result.ToOption()
		assert.True(t, opt.IsSome())
		assert.Equal(t, 42, opt.Unwrap())
	})

	t.Run("Err becomes None", func(t *testing.T) {
		result := Err[int](errTest)
		opt := result.ToOption()
		assert.True(t, opt.IsNone())
	})
}

func TestResultChaining(t *testing.T) {
	divide := func(a, b int) Result[int] {
		if b == 0 {
			return Err[int](errDivByZero)
		}
		return Ok(a / b)
	}

	t.Run("successful chain", func(t *testing.T) {
		r1 := divide(100, 2)
		r2 := FlatMap(r1, func(x int) Result[int] { return divide(x, 5) })
		r3 := Map(r2, func(x int) int { return x + 1 })

		assert.True(t, r3.IsOk())
		val, _ := r3.Unwrap()
		assert.Equal(t, 11, val) // (100/2)/5 + 1 = 11
	})

	t.Run("error propagation", func(t *testing.T) {
		r1 := divide(100, 0) // Error here
		r2 := FlatMap(r1, func(x int) Result[int] { return divide(x, 5) })
		r3 := Map(r2, func(x int) int { return x + 1 })

		assert.True(t, r3.IsErr())
		assert.Equal(t, "division by zero", r3.Error().Error())
	})
}
