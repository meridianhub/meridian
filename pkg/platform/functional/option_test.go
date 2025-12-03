package functional

import (
	"testing"
)

func TestSome(t *testing.T) {
	opt := Some(42)
	if !opt.IsSome() {
		t.Error("Some should return IsSome() = true")
	}
	if opt.IsNone() {
		t.Error("Some should return IsNone() = false")
	}
}

func TestNone(t *testing.T) {
	opt := None[int]()
	if opt.IsSome() {
		t.Error("None should return IsSome() = false")
	}
	if !opt.IsNone() {
		t.Error("None should return IsNone() = true")
	}
}

func TestOption_Unwrap(t *testing.T) {
	opt := Some("hello")
	if got := opt.Unwrap(); got != "hello" {
		t.Errorf("Unwrap() = %v, want hello", got)
	}
}

func TestOption_Unwrap_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Unwrap on None should panic")
		}
	}()
	opt := None[string]()
	opt.Unwrap()
}

func TestOption_UnwrapOr(t *testing.T) {
	tests := []struct {
		name         string
		opt          Option[int]
		defaultValue int
		want         int
	}{
		{
			name:         "some returns value",
			opt:          Some(42),
			defaultValue: 0,
			want:         42,
		},
		{
			name:         "none returns default",
			opt:          None[int](),
			defaultValue: 99,
			want:         99,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.opt.UnwrapOr(tt.defaultValue); got != tt.want {
				t.Errorf("UnwrapOr() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOption_UnwrapOrElse(t *testing.T) {
	called := false
	getDefault := func() int {
		called = true
		return 99
	}

	// Some should not call the function
	opt := Some(42)
	result := opt.UnwrapOrElse(getDefault)
	if result != 42 {
		t.Errorf("UnwrapOrElse() = %v, want 42", result)
	}
	if called {
		t.Error("UnwrapOrElse should not call function for Some")
	}

	// None should call the function
	none := None[int]()
	result = none.UnwrapOrElse(getDefault)
	if result != 99 {
		t.Errorf("UnwrapOrElse() = %v, want 99", result)
	}
	if !called {
		t.Error("UnwrapOrElse should call function for None")
	}
}

func TestOption_Get(t *testing.T) {
	const testValue = "testval"

	// Test Some
	opt := Some(testValue)
	val, ok := opt.Get()
	if !ok {
		t.Error("Get() should return true for Some")
	}
	if val != testValue {
		t.Errorf("Get() = %v, want %s", val, testValue)
	}

	// Test None
	none := None[string]()
	_, ok = none.Get()
	if ok {
		t.Error("Get() should return false for None")
	}
}

func TestMapOption(t *testing.T) {
	tests := []struct {
		name   string
		opt    Option[int]
		fn     func(int) string
		wantOk bool
		want   string
	}{
		{
			name:   "map over some",
			opt:    Some(42),
			fn:     func(_ int) string { return "mapped" },
			wantOk: true,
			want:   "mapped",
		},
		{
			name:   "map over none",
			opt:    None[int](),
			fn:     func(_ int) string { return "mapped" },
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MapOption(tt.opt, tt.fn)
			if result.IsSome() != tt.wantOk {
				t.Errorf("MapOption().IsSome() = %v, want %v", result.IsSome(), tt.wantOk)
			}
			if tt.wantOk {
				if got := result.Unwrap(); got != tt.want {
					t.Errorf("MapOption().Unwrap() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestFlatMapOption(t *testing.T) {
	tests := []struct {
		name   string
		opt    Option[int]
		fn     func(int) Option[string]
		wantOk bool
		want   string
	}{
		{
			name:   "flatmap some to some",
			opt:    Some(42),
			fn:     func(_ int) Option[string] { return Some("result") },
			wantOk: true,
			want:   "result",
		},
		{
			name:   "flatmap some to none",
			opt:    Some(42),
			fn:     func(_ int) Option[string] { return None[string]() },
			wantOk: false,
		},
		{
			name:   "flatmap none",
			opt:    None[int](),
			fn:     func(_ int) Option[string] { return Some("result") },
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FlatMapOption(tt.opt, tt.fn)
			if result.IsSome() != tt.wantOk {
				t.Errorf("FlatMapOption().IsSome() = %v, want %v", result.IsSome(), tt.wantOk)
			}
			if tt.wantOk {
				if got := result.Unwrap(); got != tt.want {
					t.Errorf("FlatMapOption().Unwrap() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestOption_ZeroValue(t *testing.T) {
	var opt Option[int]
	if opt.IsSome() {
		t.Error("Zero-value Option should be None")
	}
	if !opt.IsNone() {
		t.Error("Zero-value Option should be None")
	}
}
