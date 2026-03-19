package types

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Test-scoped sentinel errors to satisfy err113 linter rule
var (
	errTest       = errors.New("test error")
	errConversion = errors.New("conversion failed")
)

func TestOption(t *testing.T) {
	t.Run("Some contains value", func(t *testing.T) {
		opt := Some(42)

		assert.True(t, opt.IsPresent())
		assert.False(t, opt.IsAbsent())
		assert.Equal(t, 42, opt.MustGet())
	})

	t.Run("None is absent", func(t *testing.T) {
		opt := None[int]()

		assert.False(t, opt.IsPresent())
		assert.True(t, opt.IsAbsent())
	})

	t.Run("OrElse returns default for None", func(t *testing.T) {
		opt := None[string]()
		value := opt.OrElse("default")

		assert.Equal(t, "default", value)
	})

	t.Run("OrElse returns value for Some", func(t *testing.T) {
		opt := Some("hello")
		value := opt.OrElse("default")

		assert.Equal(t, "hello", value)
	})
}

func TestResult(t *testing.T) {
	t.Run("Ok contains success value", func(t *testing.T) {
		result := Ok(42)

		assert.True(t, result.IsOk())
		assert.False(t, result.IsError())
		assert.Equal(t, 42, result.MustGet())
	})

	t.Run("Err contains error", func(t *testing.T) {
		result := Err[int](errTest)

		assert.False(t, result.IsOk())
		assert.True(t, result.IsError())
		assert.ErrorIs(t, result.Error(), errTest)
	})

	t.Run("TupleToResult with nil error creates Ok", func(t *testing.T) {
		result := TupleToResult(42, nil)

		assert.True(t, result.IsOk())
		assert.Equal(t, 42, result.MustGet())
	})

	t.Run("TupleToResult with error creates Err", func(t *testing.T) {
		result := TupleToResult(0, errConversion)

		assert.True(t, result.IsError())
		assert.ErrorIs(t, result.Error(), errConversion)
	})
}

func TestPointerToOption(t *testing.T) {
	t.Run("nil pointer becomes None", func(t *testing.T) {
		var ptr *int

		opt := PointerToOption(ptr)

		assert.True(t, opt.IsAbsent())
	})

	t.Run("non-nil pointer becomes Some", func(t *testing.T) {
		value := 42
		ptr := &value

		opt := PointerToOption(ptr)

		assert.True(t, opt.IsPresent())
		assert.Equal(t, 42, opt.MustGet())
	})

	t.Run("works with time.Time", func(t *testing.T) {
		now := time.Now()
		ptr := &now

		opt := PointerToOption(ptr)

		assert.True(t, opt.IsPresent())
		assert.Equal(t, now, opt.MustGet())
	})

	t.Run("nil time.Time becomes None", func(t *testing.T) {
		var ptr *time.Time

		opt := PointerToOption(ptr)

		assert.True(t, opt.IsAbsent())
	})
}

func TestOptionToPointer(t *testing.T) {
	t.Run("None becomes nil", func(t *testing.T) {
		opt := None[int]()

		ptr := OptionToPointer(opt)

		assert.Nil(t, ptr)
	})

	t.Run("Some becomes pointer to value", func(t *testing.T) {
		opt := Some(42)

		ptr := OptionToPointer(opt)

		assert.NotNil(t, ptr)
		assert.Equal(t, 42, *ptr)
	})

	t.Run("works with time.Time", func(t *testing.T) {
		now := time.Now()
		opt := Some(now)

		ptr := OptionToPointer(opt)

		assert.NotNil(t, ptr)
		assert.Equal(t, now, *ptr)
	})
}

func TestOptionalTime(t *testing.T) {
	t.Run("SomeTime creates present option", func(t *testing.T) {
		now := time.Now()
		opt := SomeTime(now)

		assert.True(t, opt.IsPresent())
		assert.Equal(t, now, opt.MustGet())
	})

	t.Run("NoTime creates absent option", func(t *testing.T) {
		opt := NoTime()

		assert.True(t, opt.IsAbsent())
	})

	t.Run("OptionalTime can use OrElse for defaults", func(t *testing.T) {
		opt := NoTime()
		defaultTime := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

		result := opt.OrElse(defaultTime)

		assert.Equal(t, defaultTime, result)
	})
}

func TestRoundTrip(t *testing.T) {
	t.Run("pointer to option to pointer preserves value", func(t *testing.T) {
		value := 42
		original := &value

		opt := PointerToOption(original)
		result := OptionToPointer(opt)

		assert.NotNil(t, result)
		assert.Equal(t, *original, *result)
	})

	t.Run("nil pointer round trips to nil", func(t *testing.T) {
		var original *int

		opt := PointerToOption(original)
		result := OptionToPointer(opt)

		assert.Nil(t, result)
	})

	t.Run("time.Time round trips correctly", func(t *testing.T) {
		now := time.Now()
		original := &now

		opt := PointerToOption(original)
		result := OptionToPointer(opt)

		assert.NotNil(t, result)
		assert.Equal(t, *original, *result)
	})
}

// ExampleUsage demonstrates how these types would be used in domain code
func TestExampleUsage(t *testing.T) {
	// Simulating a domain object with optional expiration
	type Lien struct {
		ID        string
		ExpiresAt OptionalTime
	}

	// Helper function to check expiration - this is how you'd implement IsExpired()
	isExpired := func(l Lien) bool {
		if l.ExpiresAt.IsAbsent() {
			return false // No expiration time = never expires
		}
		return time.Now().After(l.ExpiresAt.MustGet())
	}

	t.Run("lien without expiration never expires", func(t *testing.T) {
		lien := Lien{
			ID:        "lien-1",
			ExpiresAt: NoTime(),
		}

		assert.False(t, isExpired(lien))
	})

	t.Run("lien with future expiration is not expired", func(t *testing.T) {
		futureTime := time.Now().Add(time.Hour)
		lien := Lien{
			ID:        "lien-2",
			ExpiresAt: SomeTime(futureTime),
		}

		assert.False(t, isExpired(lien))
	})

	t.Run("lien with past expiration is expired", func(t *testing.T) {
		pastTime := time.Now().Add(-time.Hour)
		lien := Lien{
			ID:        "lien-3",
			ExpiresAt: SomeTime(pastTime),
		}

		assert.True(t, isExpired(lien))
	})

	t.Run("converting from legacy pointer pattern", func(t *testing.T) {
		// Simulating migration from *time.Time to OptionalTime
		var legacyExpiresAt *time.Time // nil - no expiration

		lien := Lien{
			ID:        "lien-4",
			ExpiresAt: PointerToOption(legacyExpiresAt),
		}

		assert.True(t, lien.ExpiresAt.IsAbsent())
		assert.False(t, isExpired(lien))

		// Now with a value
		t2 := time.Now().Add(time.Hour)
		legacyExpiresAt = &t2
		lien.ExpiresAt = PointerToOption(legacyExpiresAt)

		assert.True(t, lien.ExpiresAt.IsPresent())
		assert.Equal(t, t2, lien.ExpiresAt.MustGet())
	})

	t.Run("converting to legacy pointer pattern for persistence", func(t *testing.T) {
		// When saving to DB, convert back to pointer
		lien := Lien{
			ID:        "lien-5",
			ExpiresAt: NoTime(),
		}

		// For persistence layer
		ptr := OptionToPointer(lien.ExpiresAt)
		assert.Nil(t, ptr)

		// With a value
		lien.ExpiresAt = SomeTime(time.Now())
		ptr = OptionToPointer(lien.ExpiresAt)
		assert.NotNil(t, ptr)
	})
}
