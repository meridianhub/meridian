package types

import (
	"errors"
	"testing"
)

func TestOk(t *testing.T) {
	result := Ok(42)
	if !result.IsOk() {
		t.Error("expected IsOk to be true")
	}
	if result.IsErr() {
		t.Error("expected IsErr to be false")
	}
	value, err := result.Unwrap()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if value != 42 {
		t.Errorf("expected 42, got %d", value)
	}
}

func TestErr(t *testing.T) {
	testErr := errors.New("test error")
	result := Err[int](testErr)
	if result.IsOk() {
		t.Error("expected IsOk to be false")
	}
	if !result.IsErr() {
		t.Error("expected IsErr to be true")
	}
	_, err := result.Unwrap()
	if err != testErr {
		t.Errorf("expected test error, got %v", err)
	}
}

func TestUnwrapOr(t *testing.T) {
	t.Run("Ok returns value", func(t *testing.T) {
		result := Ok(42)
		if got := result.UnwrapOr(0); got != 42 {
			t.Errorf("expected 42, got %d", got)
		}
	})

	t.Run("Err returns default", func(t *testing.T) {
		result := Err[int](errors.New("error"))
		if got := result.UnwrapOr(99); got != 99 {
			t.Errorf("expected 99, got %d", got)
		}
	})
}

func TestUnwrapOrElse(t *testing.T) {
	t.Run("Ok returns value", func(t *testing.T) {
		result := Ok(42)
		got := result.UnwrapOrElse(func(err error) int { return 0 })
		if got != 42 {
			t.Errorf("expected 42, got %d", got)
		}
	})

	t.Run("Err calls fallback", func(t *testing.T) {
		result := Err[int](errors.New("error"))
		got := result.UnwrapOrElse(func(err error) int { return 99 })
		if got != 99 {
			t.Errorf("expected 99, got %d", got)
		}
	})
}

func TestMap(t *testing.T) {
	t.Run("Ok transforms value", func(t *testing.T) {
		result := Ok(21)
		mapped := Map(result, func(v int) int { return v * 2 })
		value, err := mapped.Unwrap()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if value != 42 {
			t.Errorf("expected 42, got %d", value)
		}
	})

	t.Run("Err propagates error", func(t *testing.T) {
		testErr := errors.New("error")
		result := Err[int](testErr)
		mapped := Map(result, func(v int) int { return v * 2 })
		if !mapped.IsErr() {
			t.Error("expected error to propagate")
		}
		if mapped.Error() != testErr {
			t.Error("expected same error")
		}
	})

	t.Run("Map changes type", func(t *testing.T) {
		result := Ok(42)
		mapped := Map(result, func(v int) string { return "the answer" })
		value, err := mapped.Unwrap()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if value != "the answer" {
			t.Errorf("expected 'the answer', got %s", value)
		}
	})
}

func TestFlatMap(t *testing.T) {
	t.Run("Ok chains operations", func(t *testing.T) {
		result := Ok(21)
		chained := FlatMap(result, func(v int) Result[int] {
			return Ok(v * 2)
		})
		value, err := chained.Unwrap()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if value != 42 {
			t.Errorf("expected 42, got %d", value)
		}
	})

	t.Run("Ok chains to error", func(t *testing.T) {
		testErr := errors.New("chain error")
		result := Ok(21)
		chained := FlatMap(result, func(v int) Result[int] {
			return Err[int](testErr)
		})
		if !chained.IsErr() {
			t.Error("expected error from chain")
		}
	})

	t.Run("Err short-circuits", func(t *testing.T) {
		testErr := errors.New("original error")
		result := Err[int](testErr)
		called := false
		chained := FlatMap(result, func(v int) Result[int] {
			called = true
			return Ok(v * 2)
		})
		if called {
			t.Error("chain function should not be called")
		}
		if chained.Error() != testErr {
			t.Error("expected original error")
		}
	})
}

func TestMapErr(t *testing.T) {
	t.Run("Ok passes through", func(t *testing.T) {
		result := Ok(42)
		mapped := MapErr(result, func(err error) error {
			return errors.New("transformed")
		})
		if !mapped.IsOk() {
			t.Error("expected Ok")
		}
	})

	t.Run("Err transforms error", func(t *testing.T) {
		result := Err[int](errors.New("original"))
		mapped := MapErr(result, func(err error) error {
			return errors.New("transformed: " + err.Error())
		})
		if mapped.Error().Error() != "transformed: original" {
			t.Errorf("expected transformed error, got %v", mapped.Error())
		}
	})
}

func TestAnd(t *testing.T) {
	t.Run("Ok and Ok returns second", func(t *testing.T) {
		first := Ok(42)
		second := Ok("hello")
		result := And(first, second)
		value, _ := result.Unwrap()
		if value != "hello" {
			t.Errorf("expected 'hello', got %s", value)
		}
	})

	t.Run("Err and Ok returns first error", func(t *testing.T) {
		testErr := errors.New("first error")
		first := Err[int](testErr)
		second := Ok("hello")
		result := And(first, second)
		if result.Error() != testErr {
			t.Error("expected first error")
		}
	})
}

func TestOr(t *testing.T) {
	t.Run("Ok or _ returns Ok", func(t *testing.T) {
		first := Ok(42)
		second := Ok(99)
		result := Or(first, second)
		value, _ := result.Unwrap()
		if value != 42 {
			t.Errorf("expected 42, got %d", value)
		}
	})

	t.Run("Err or Ok returns Ok", func(t *testing.T) {
		first := Err[int](errors.New("error"))
		second := Ok(99)
		result := Or(first, second)
		value, _ := result.Unwrap()
		if value != 99 {
			t.Errorf("expected 99, got %d", value)
		}
	})
}

func TestInspect(t *testing.T) {
	t.Run("Ok calls function", func(t *testing.T) {
		called := false
		result := Ok(42)
		Inspect(result, func(v int) {
			called = true
			if v != 42 {
				t.Errorf("expected 42, got %d", v)
			}
		})
		if !called {
			t.Error("inspect function should be called")
		}
	})

	t.Run("Err does not call function", func(t *testing.T) {
		called := false
		result := Err[int](errors.New("error"))
		Inspect(result, func(v int) {
			called = true
		})
		if called {
			t.Error("inspect function should not be called")
		}
	})
}

func TestInspectErr(t *testing.T) {
	t.Run("Ok does not call function", func(t *testing.T) {
		called := false
		result := Ok(42)
		InspectErr(result, func(err error) {
			called = true
		})
		if called {
			t.Error("inspect function should not be called")
		}
	})

	t.Run("Err calls function", func(t *testing.T) {
		testErr := errors.New("test error")
		called := false
		result := Err[int](testErr)
		InspectErr(result, func(err error) {
			called = true
			if err != testErr {
				t.Errorf("expected test error, got %v", err)
			}
		})
		if !called {
			t.Error("inspect function should be called")
		}
	})
}

func TestValueAndError(t *testing.T) {
	t.Run("Ok value", func(t *testing.T) {
		result := Ok(42)
		if result.Value() != 42 {
			t.Errorf("expected 42, got %d", result.Value())
		}
		if result.Error() != nil {
			t.Errorf("expected nil error, got %v", result.Error())
		}
	})

	t.Run("Err value", func(t *testing.T) {
		testErr := errors.New("error")
		result := Err[int](testErr)
		if result.Value() != 0 {
			t.Errorf("expected 0, got %d", result.Value())
		}
		if result.Error() != testErr {
			t.Errorf("expected test error, got %v", result.Error())
		}
	})
}
