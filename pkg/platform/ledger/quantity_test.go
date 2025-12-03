package ledger

import (
	"math"
	"testing"

	"github.com/meridianhub/meridian/pkg/platform/types"
)

func TestNewQuantity(t *testing.T) {
	t.Run("creates quantity with valid amount", func(t *testing.T) {
		result := NewQuantity(USD, 10000)
		q, err := result.Unwrap()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if q.Amount() != 10000 {
			t.Errorf("expected 10000, got %d", q.Amount())
		}
		if q.Unit() != USD {
			t.Errorf("expected USD, got %v", q.Unit())
		}
	})

	t.Run("handles negative amounts", func(t *testing.T) {
		result := NewQuantity(USD, -5000)
		q, err := result.Unwrap()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if q.Amount() != -5000 {
			t.Errorf("expected -5000, got %d", q.Amount())
		}
	})
}

func TestNewQuantityFromMajor(t *testing.T) {
	tests := []struct {
		name     string
		unit     CurrencyUnit
		major    float64
		expected int64
	}{
		{"USD 100.50", USD, 100.50, 10050},
		{"USD 0.01", USD, 0.01, 1},
		{"JPY 100", JPY, 100, 100},       // No decimals
		{"BTC 1.5", BTC, 1.5, 150000000}, // 8 decimals
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewQuantityFromMajor(tt.unit, tt.major)
			q, err := result.Unwrap()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if q.Amount() != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, q.Amount())
			}
		})
	}
}

func TestQuantityMajorAmount(t *testing.T) {
	tests := []struct {
		name     string
		unit     CurrencyUnit
		minor    int64
		expected float64
	}{
		{"USD 100.50", USD, 10050, 100.50},
		{"JPY 100", JPY, 100, 100.0},
		{"BTC 1.5", BTC, 150000000, 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, _ := NewQuantity(tt.unit, tt.minor).Unwrap()
			if q.MajorAmount() != tt.expected {
				t.Errorf("expected %f, got %f", tt.expected, q.MajorAmount())
			}
		})
	}
}

func TestQuantityAdd(t *testing.T) {
	t.Run("adds two quantities", func(t *testing.T) {
		q1, _ := NewQuantity(USD, 10000).Unwrap()
		q2, _ := NewQuantity(USD, 5000).Unwrap()
		result := q1.Add(q2)
		sum, err := result.Unwrap()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sum.Amount() != 15000 {
			t.Errorf("expected 15000, got %d", sum.Amount())
		}
	})

	t.Run("handles negative amounts", func(t *testing.T) {
		q1, _ := NewQuantity(USD, 10000).Unwrap()
		q2, _ := NewQuantity(USD, -3000).Unwrap()
		sum, _ := q1.Add(q2).Unwrap()
		if sum.Amount() != 7000 {
			t.Errorf("expected 7000, got %d", sum.Amount())
		}
	})

	t.Run("detects overflow", func(t *testing.T) {
		q1, _ := NewQuantity(USD, math.MaxInt64).Unwrap()
		q2, _ := NewQuantity(USD, 1).Unwrap()
		result := q1.Add(q2)
		if !result.IsErr() {
			t.Error("expected overflow error")
		}
		if result.Error() != ErrAmountOverflow {
			t.Errorf("expected ErrAmountOverflow, got %v", result.Error())
		}
	})
}

func TestQuantitySub(t *testing.T) {
	t.Run("subtracts two quantities", func(t *testing.T) {
		q1, _ := NewQuantity(USD, 10000).Unwrap()
		q2, _ := NewQuantity(USD, 3000).Unwrap()
		diff, _ := q1.Sub(q2).Unwrap()
		if diff.Amount() != 7000 {
			t.Errorf("expected 7000, got %d", diff.Amount())
		}
	})

	t.Run("detects underflow", func(t *testing.T) {
		q1, _ := NewQuantity(USD, math.MinInt64).Unwrap()
		q2, _ := NewQuantity(USD, 1).Unwrap()
		result := q1.Sub(q2)
		if !result.IsErr() {
			t.Error("expected underflow error")
		}
	})
}

func TestQuantityMul(t *testing.T) {
	t.Run("multiplies by scalar", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		product, _ := q.Mul(5).Unwrap()
		if product.Amount() != 500 {
			t.Errorf("expected 500, got %d", product.Amount())
		}
	})

	t.Run("multiply by zero", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		product, _ := q.Mul(0).Unwrap()
		if product.Amount() != 0 {
			t.Errorf("expected 0, got %d", product.Amount())
		}
	})

	t.Run("detects overflow", func(t *testing.T) {
		q, _ := NewQuantity(USD, math.MaxInt64/2+1).Unwrap()
		result := q.Mul(2)
		if !result.IsErr() {
			t.Error("expected overflow error")
		}
	})
}

func TestQuantityDiv(t *testing.T) {
	t.Run("divides by scalar", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		quotient, _ := q.Div(4).Unwrap()
		if quotient.Amount() != 25 {
			t.Errorf("expected 25, got %d", quotient.Amount())
		}
	})

	t.Run("division by zero", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		result := q.Div(0)
		if !result.IsErr() {
			t.Error("expected division by zero error")
		}
		if result.Error() != ErrDivisionByZero {
			t.Errorf("expected ErrDivisionByZero, got %v", result.Error())
		}
	})

	t.Run("MinInt64 divided by -1 overflows", func(t *testing.T) {
		q, _ := NewQuantity(USD, math.MinInt64).Unwrap()
		result := q.Div(-1)
		if !result.IsErr() {
			t.Error("expected overflow error")
		}
	})
}

func TestQuantityNegate(t *testing.T) {
	t.Run("negates positive", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		neg, _ := q.Negate().Unwrap()
		if neg.Amount() != -100 {
			t.Errorf("expected -100, got %d", neg.Amount())
		}
	})

	t.Run("negates negative", func(t *testing.T) {
		q, _ := NewQuantity(USD, -100).Unwrap()
		neg, _ := q.Negate().Unwrap()
		if neg.Amount() != 100 {
			t.Errorf("expected 100, got %d", neg.Amount())
		}
	})

	t.Run("MinInt64 overflow", func(t *testing.T) {
		q, _ := NewQuantity(USD, math.MinInt64).Unwrap()
		result := q.Negate()
		if !result.IsErr() {
			t.Error("expected overflow error")
		}
	})
}

func TestQuantityAbs(t *testing.T) {
	t.Run("abs of positive", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		abs, _ := q.Abs().Unwrap()
		if abs.Amount() != 100 {
			t.Errorf("expected 100, got %d", abs.Amount())
		}
	})

	t.Run("abs of negative", func(t *testing.T) {
		q, _ := NewQuantity(USD, -100).Unwrap()
		abs, _ := q.Abs().Unwrap()
		if abs.Amount() != 100 {
			t.Errorf("expected 100, got %d", abs.Amount())
		}
	})
}

func TestQuantityComparisons(t *testing.T) {
	q100, _ := NewQuantity(USD, 100).Unwrap()
	q200, _ := NewQuantity(USD, 200).Unwrap()
	q100b, _ := NewQuantity(USD, 100).Unwrap()

	t.Run("Equal", func(t *testing.T) {
		if !q100.Equal(q100b) {
			t.Error("expected equal")
		}
		if q100.Equal(q200) {
			t.Error("expected not equal")
		}
	})

	t.Run("Less", func(t *testing.T) {
		if !q100.Less(q200) {
			t.Error("expected 100 < 200")
		}
		if q200.Less(q100) {
			t.Error("expected 200 not < 100")
		}
	})

	t.Run("LessOrEqual", func(t *testing.T) {
		if !q100.LessOrEqual(q200) {
			t.Error("expected 100 <= 200")
		}
		if !q100.LessOrEqual(q100b) {
			t.Error("expected 100 <= 100")
		}
	})

	t.Run("Greater", func(t *testing.T) {
		if !q200.Greater(q100) {
			t.Error("expected 200 > 100")
		}
	})

	t.Run("GreaterOrEqual", func(t *testing.T) {
		if !q200.GreaterOrEqual(q100) {
			t.Error("expected 200 >= 100")
		}
		if !q100.GreaterOrEqual(q100b) {
			t.Error("expected 100 >= 100")
		}
	})

	t.Run("Compare", func(t *testing.T) {
		if q100.Compare(q200) != -1 {
			t.Error("expected -1")
		}
		if q200.Compare(q100) != 1 {
			t.Error("expected 1")
		}
		if q100.Compare(q100b) != 0 {
			t.Error("expected 0")
		}
	})

	t.Run("Min", func(t *testing.T) {
		if q100.Min(q200).Amount() != 100 {
			t.Error("expected min to be 100")
		}
	})

	t.Run("Max", func(t *testing.T) {
		if q100.Max(q200).Amount() != 200 {
			t.Error("expected max to be 200")
		}
	})
}

func TestQuantityPredicates(t *testing.T) {
	t.Run("IsZero", func(t *testing.T) {
		zero := Zero(USD)
		if !zero.IsZero() {
			t.Error("expected zero to be zero")
		}
		q, _ := NewQuantity(USD, 100).Unwrap()
		if q.IsZero() {
			t.Error("expected 100 to not be zero")
		}
	})

	t.Run("IsNegative", func(t *testing.T) {
		neg, _ := NewQuantity(USD, -100).Unwrap()
		if !neg.IsNegative() {
			t.Error("expected -100 to be negative")
		}
		pos, _ := NewQuantity(USD, 100).Unwrap()
		if pos.IsNegative() {
			t.Error("expected 100 to not be negative")
		}
	})

	t.Run("IsPositive", func(t *testing.T) {
		pos, _ := NewQuantity(USD, 100).Unwrap()
		if !pos.IsPositive() {
			t.Error("expected 100 to be positive")
		}
		neg, _ := NewQuantity(USD, -100).Unwrap()
		if neg.IsPositive() {
			t.Error("expected -100 to not be positive")
		}
	})
}

func TestQuantitySplit(t *testing.T) {
	t.Run("even split", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		parts, _ := q.Split(4).Unwrap()
		if len(parts) != 4 {
			t.Fatalf("expected 4 parts, got %d", len(parts))
		}
		for i, p := range parts {
			if p.Amount() != 25 {
				t.Errorf("part %d: expected 25, got %d", i, p.Amount())
			}
		}
	})

	t.Run("uneven split with remainder", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		parts, _ := q.Split(3).Unwrap()
		// 100 / 3 = 33 remainder 1
		// First part gets the remainder
		if parts[0].Amount() != 34 {
			t.Errorf("first part: expected 34, got %d", parts[0].Amount())
		}
		if parts[1].Amount() != 33 {
			t.Errorf("second part: expected 33, got %d", parts[1].Amount())
		}
		if parts[2].Amount() != 33 {
			t.Errorf("third part: expected 33, got %d", parts[2].Amount())
		}
		// Verify total
		total := parts[0].Amount() + parts[1].Amount() + parts[2].Amount()
		if total != 100 {
			t.Errorf("expected total 100, got %d", total)
		}
	})

	t.Run("split by zero", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		result := q.Split(0)
		if !result.IsErr() {
			t.Error("expected error for split by zero")
		}
	})
}

func TestQuantityAllocate(t *testing.T) {
	t.Run("allocate proportionally", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		parts, _ := q.Allocate([]int64{1, 2, 1}).Unwrap()
		// Total ratio = 4
		// Expected: 25, 50, 25
		if parts[0].Amount() != 25 {
			t.Errorf("first part: expected 25, got %d", parts[0].Amount())
		}
		if parts[1].Amount() != 50 {
			t.Errorf("second part: expected 50, got %d", parts[1].Amount())
		}
		if parts[2].Amount() != 25 {
			t.Errorf("third part: expected 25, got %d", parts[2].Amount())
		}
	})

	t.Run("allocate with remainder distribution", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		parts, _ := q.Allocate([]int64{1, 1, 1}).Unwrap()
		// 100 / 3 = 33 remainder 1
		total := parts[0].Amount() + parts[1].Amount() + parts[2].Amount()
		if total != 100 {
			t.Errorf("expected total 100, got %d", total)
		}
	})

	t.Run("allocate empty ratios", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		result := q.Allocate([]int64{})
		if !result.IsErr() {
			t.Error("expected error for empty ratios")
		}
	})

	t.Run("allocate zero total ratio", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		result := q.Allocate([]int64{0, 0, 0})
		if !result.IsErr() {
			t.Error("expected error for zero total ratio")
		}
	})

	t.Run("allocate negative ratio", func(t *testing.T) {
		q, _ := NewQuantity(USD, 100).Unwrap()
		result := q.Allocate([]int64{1, -1, 1})
		if !result.IsErr() {
			t.Error("expected error for negative ratio")
		}
	})
}

func TestQuantityString(t *testing.T) {
	tests := []struct {
		name     string
		q        Quantity[CurrencyUnit]
		expected string
	}{
		{"USD 100.00", mustQuantity(NewQuantity(USD, 10000)), "100.00 USD"},
		{"USD 0.01", mustQuantity(NewQuantity(USD, 1)), "0.01 USD"},
		{"JPY 100", mustQuantity(NewQuantity(JPY, 100)), "100 JPY"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.q.String() != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, tt.q.String())
			}
		})
	}
}

func TestZero(t *testing.T) {
	zero := Zero(USD)
	if !zero.IsZero() {
		t.Error("expected zero")
	}
	if zero.Unit() != USD {
		t.Error("expected USD unit")
	}
}

// Helper function for tests
func mustQuantity[U UnitMarker](r types.Result[Quantity[U]]) Quantity[U] {
	q, err := r.Unwrap()
	if err != nil {
		panic(err)
	}
	return q
}
