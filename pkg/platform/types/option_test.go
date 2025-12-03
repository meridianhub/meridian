package types //nolint:revive // "types" is intentional, similar to go/types in stdlib

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errNotFound = errors.New("not found")

func TestSome(t *testing.T) {
	opt := Some(42)
	assert.True(t, opt.IsSome())
	assert.False(t, opt.IsNone())
	assert.Equal(t, 42, opt.Unwrap())
}

func TestSomeWithString(t *testing.T) {
	opt := Some("hello")
	assert.True(t, opt.IsSome())
	assert.Equal(t, "hello", opt.Unwrap())
}

func TestSomeWithStruct(t *testing.T) {
	type Person struct {
		Name string
		Age  int
	}
	p := Person{Name: "Alice", Age: 30}
	opt := Some(p)
	assert.True(t, opt.IsSome())
	assert.Equal(t, p, opt.Unwrap())
}

func TestNone(t *testing.T) {
	opt := None[int]()
	assert.True(t, opt.IsNone())
	assert.False(t, opt.IsSome())
}

func TestNoneWithString(t *testing.T) {
	opt := None[string]()
	assert.True(t, opt.IsNone())
	assert.False(t, opt.IsSome())
}

func TestUnwrapPanicsOnNone(t *testing.T) {
	opt := None[int]()
	require.Panics(t, func() {
		opt.Unwrap()
	})
}

func TestUnwrapOr(t *testing.T) {
	t.Run("returns value when Some", func(t *testing.T) {
		opt := Some(42)
		assert.Equal(t, 42, opt.UnwrapOr(0))
	})

	t.Run("returns default when None", func(t *testing.T) {
		opt := None[int]()
		assert.Equal(t, 99, opt.UnwrapOr(99))
	})
}

func TestUnwrapOrElse(t *testing.T) {
	t.Run("returns value when Some", func(t *testing.T) {
		opt := Some(42)
		result := opt.UnwrapOrElse(func() int { return 0 })
		assert.Equal(t, 42, result)
	})

	t.Run("calls function when None", func(t *testing.T) {
		called := false
		opt := None[int]()
		result := opt.UnwrapOrElse(func() int {
			called = true
			return 99
		})
		assert.True(t, called)
		assert.Equal(t, 99, result)
	})
}

func TestGet(t *testing.T) {
	t.Run("returns value and true when Some", func(t *testing.T) {
		opt := Some(42)
		val, ok := opt.Get()
		assert.True(t, ok)
		assert.Equal(t, 42, val)
	})

	t.Run("returns zero and false when None", func(t *testing.T) {
		opt := None[int]()
		val, ok := opt.Get()
		assert.False(t, ok)
		assert.Equal(t, 0, val)
	})
}

func TestOptionMap(t *testing.T) {
	t.Run("transforms value when Some", func(t *testing.T) {
		opt := Some(21)
		result := OptionMap(opt, func(x int) int { return x * 2 })
		assert.True(t, result.IsSome())
		assert.Equal(t, 42, result.Unwrap())
	})

	t.Run("returns None when original is None", func(t *testing.T) {
		opt := None[int]()
		result := OptionMap(opt, func(x int) int { return x * 2 })
		assert.True(t, result.IsNone())
	})

	t.Run("transforms to different type", func(t *testing.T) {
		opt := Some(42)
		result := OptionMap(opt, func(_ int) string { return "number" })
		assert.True(t, result.IsSome())
		assert.Equal(t, "number", result.Unwrap())
	})
}

func TestOptionFlatMap(t *testing.T) {
	t.Run("chains Some to Some", func(t *testing.T) {
		opt := Some(21)
		result := OptionFlatMap(opt, func(x int) Option[int] {
			return Some(x * 2)
		})
		assert.True(t, result.IsSome())
		assert.Equal(t, 42, result.Unwrap())
	})

	t.Run("chains Some to None", func(t *testing.T) {
		opt := Some(21)
		result := OptionFlatMap(opt, func(_ int) Option[int] {
			return None[int]()
		})
		assert.True(t, result.IsNone())
	})

	t.Run("returns None when original is None", func(t *testing.T) {
		opt := None[int]()
		called := false
		result := OptionFlatMap(opt, func(x int) Option[int] {
			called = true
			return Some(x * 2)
		})
		assert.False(t, called)
		assert.True(t, result.IsNone())
	})
}

func TestFilter(t *testing.T) {
	t.Run("keeps Some when predicate is true", func(t *testing.T) {
		opt := Some(42)
		result := opt.Filter(func(x int) bool { return x > 0 })
		assert.True(t, result.IsSome())
		assert.Equal(t, 42, result.Unwrap())
	})

	t.Run("returns None when predicate is false", func(t *testing.T) {
		opt := Some(42)
		result := opt.Filter(func(x int) bool { return x < 0 })
		assert.True(t, result.IsNone())
	})

	t.Run("returns None when original is None", func(t *testing.T) {
		opt := None[int]()
		called := false
		result := opt.Filter(func(_ int) bool {
			called = true
			return true
		})
		assert.False(t, called)
		assert.True(t, result.IsNone())
	})
}

func TestToResult(t *testing.T) {
	t.Run("Some becomes Ok", func(t *testing.T) {
		opt := Some(42)
		result := opt.ToResult(errNotFound)
		assert.True(t, result.IsOk())
		val, err := result.Unwrap()
		assert.NoError(t, err)
		assert.Equal(t, 42, val)
	})

	t.Run("None becomes Err", func(t *testing.T) {
		opt := None[int]()
		result := opt.ToResult(errNotFound)
		assert.True(t, result.IsErr())
		_, err := result.Unwrap()
		assert.Equal(t, errNotFound, err)
	})
}

func TestFromPtr(t *testing.T) {
	t.Run("nil becomes None", func(t *testing.T) {
		var ptr *int
		opt := FromPtr(ptr)
		assert.True(t, opt.IsNone())
	})

	t.Run("non-nil becomes Some", func(t *testing.T) {
		val := 42
		ptr := &val
		opt := FromPtr(ptr)
		assert.True(t, opt.IsSome())
		assert.Equal(t, 42, opt.Unwrap())
	})
}

func TestToPtr(t *testing.T) {
	t.Run("Some returns pointer to value", func(t *testing.T) {
		opt := Some(42)
		ptr := opt.ToPtr()
		require.NotNil(t, ptr)
		assert.Equal(t, 42, *ptr)
	})

	t.Run("None returns nil", func(t *testing.T) {
		opt := None[int]()
		ptr := opt.ToPtr()
		assert.Nil(t, ptr)
	})

	t.Run("modifying returned pointer does not affect Option", func(t *testing.T) {
		opt := Some(42)
		ptr := opt.ToPtr()
		*ptr = 100
		assert.Equal(t, 42, opt.Unwrap())
	})
}

func TestMarshalJSON(t *testing.T) {
	t.Run("Some marshals as value", func(t *testing.T) {
		opt := Some(42)
		data, err := json.Marshal(opt)
		require.NoError(t, err)
		assert.Equal(t, "42", string(data))
	})

	t.Run("None marshals as null", func(t *testing.T) {
		opt := None[int]()
		data, err := json.Marshal(opt)
		require.NoError(t, err)
		assert.Equal(t, "null", string(data))
	})

	t.Run("Some string marshals correctly", func(t *testing.T) {
		opt := Some("hello")
		data, err := json.Marshal(opt)
		require.NoError(t, err)
		assert.Equal(t, `"hello"`, string(data))
	})

	t.Run("Some struct marshals correctly", func(t *testing.T) {
		type Item struct {
			Name string `json:"name"`
		}
		opt := Some(Item{Name: "test"})
		data, err := json.Marshal(opt)
		require.NoError(t, err)
		assert.Equal(t, `{"name":"test"}`, string(data))
	})
}

func TestUnmarshalJSON(t *testing.T) {
	t.Run("value unmarshals as Some", func(t *testing.T) {
		var opt Option[int]
		err := json.Unmarshal([]byte("42"), &opt)
		require.NoError(t, err)
		assert.True(t, opt.IsSome())
		assert.Equal(t, 42, opt.Unwrap())
	})

	t.Run("null unmarshals as None", func(t *testing.T) {
		var opt Option[int]
		err := json.Unmarshal([]byte("null"), &opt)
		require.NoError(t, err)
		assert.True(t, opt.IsNone())
	})

	t.Run("string unmarshals correctly", func(t *testing.T) {
		var opt Option[string]
		err := json.Unmarshal([]byte(`"hello"`), &opt)
		require.NoError(t, err)
		assert.True(t, opt.IsSome())
		assert.Equal(t, "hello", opt.Unwrap())
	})

	t.Run("struct unmarshals correctly", func(t *testing.T) {
		type Item struct {
			Name string `json:"name"`
		}
		var opt Option[Item]
		err := json.Unmarshal([]byte(`{"name":"test"}`), &opt)
		require.NoError(t, err)
		assert.True(t, opt.IsSome())
		assert.Equal(t, "test", opt.Unwrap().Name)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		var opt Option[int]
		err := json.Unmarshal([]byte(`"not a number"`), &opt)
		assert.Error(t, err)
	})
}

func TestOptionInStruct(t *testing.T) {
	type User struct {
		Name  string         `json:"name"`
		Email Option[string] `json:"email"`
	}

	t.Run("marshals with Some field", func(t *testing.T) {
		user := User{Name: "Alice", Email: Some("alice@example.com")}
		data, err := json.Marshal(user)
		require.NoError(t, err)
		assert.Equal(t, `{"name":"Alice","email":"alice@example.com"}`, string(data))
	})

	t.Run("marshals with None field", func(t *testing.T) {
		user := User{Name: "Bob", Email: None[string]()}
		data, err := json.Marshal(user)
		require.NoError(t, err)
		assert.Equal(t, `{"name":"Bob","email":null}`, string(data))
	})

	t.Run("unmarshals with value", func(t *testing.T) {
		var user User
		err := json.Unmarshal([]byte(`{"name":"Charlie","email":"charlie@example.com"}`), &user)
		require.NoError(t, err)
		assert.Equal(t, "Charlie", user.Name)
		assert.True(t, user.Email.IsSome())
		assert.Equal(t, "charlie@example.com", user.Email.Unwrap())
	})

	t.Run("unmarshals with null", func(t *testing.T) {
		var user User
		err := json.Unmarshal([]byte(`{"name":"Dana","email":null}`), &user)
		require.NoError(t, err)
		assert.Equal(t, "Dana", user.Name)
		assert.True(t, user.Email.IsNone())
	})
}

func TestOptionZeroValue(t *testing.T) {
	var opt Option[int]
	assert.True(t, opt.IsNone())
	assert.False(t, opt.IsSome())
}
