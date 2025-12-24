// Package money_test provides performance benchmarks comparing shopspring/decimal
// (arbitrary precision) vs float64 (standard floating-point) for financial operations.
//
// This benchmark suite measures the computational overhead of using arbitrary precision
// decimal arithmetic compared to standard floating-point operations. The goal is to
// quantify the performance impact of choosing precision over speed.
//
// Run with: go test -bench=BenchmarkComparison -benchmem -benchtime=10s
//
// Expected results:
//   - shopspring/decimal operations are 10-100x slower than float64
//   - shopspring/decimal allocates memory, float64 does not
//   - The precision guarantee is worth the overhead for financial systems
package money_test

import (
	"fmt"
	"testing"

	"github.com/shopspring/decimal"
)

// BenchmarkComparison_Addition compares decimal.Decimal vs float64 addition performance.
func BenchmarkComparison_Addition(b *testing.B) {
	b.Run("decimal_Add", func(b *testing.B) {
		d1 := decimal.NewFromFloat(100.50)
		d2 := decimal.NewFromFloat(50.25)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = d1.Add(d2)
		}
	})

	b.Run("float64_add", func(b *testing.B) {
		f1 := 100.50
		f2 := 50.25
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = f1 + f2
		}
	})
}

// BenchmarkComparison_Subtraction compares decimal.Decimal vs float64 subtraction performance.
func BenchmarkComparison_Subtraction(b *testing.B) {
	b.Run("decimal_Sub", func(b *testing.B) {
		d1 := decimal.NewFromFloat(100.50)
		d2 := decimal.NewFromFloat(50.25)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = d1.Sub(d2)
		}
	})

	b.Run("float64_sub", func(b *testing.B) {
		f1 := 100.50
		f2 := 50.25
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = f1 - f2
		}
	})
}

// BenchmarkComparison_Multiplication compares decimal.Decimal vs float64 multiplication performance.
func BenchmarkComparison_Multiplication(b *testing.B) {
	b.Run("decimal_Mul", func(b *testing.B) {
		d1 := decimal.NewFromFloat(100.50)
		d2 := decimal.NewFromFloat(1.15)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = d1.Mul(d2)
		}
	})

	b.Run("float64_mul", func(b *testing.B) {
		f1 := 100.50
		f2 := 1.15
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = f1 * f2
		}
	})
}

// BenchmarkComparison_Division compares decimal.Decimal vs float64 division performance.
func BenchmarkComparison_Division(b *testing.B) {
	b.Run("decimal_Div", func(b *testing.B) {
		d1 := decimal.NewFromFloat(100.50)
		d2 := decimal.NewFromFloat(3.0)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = d1.Div(d2)
		}
	})

	b.Run("float64_div", func(b *testing.B) {
		f1 := 100.50
		f2 := 3.0
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = f1 / f2
		}
	})
}

// BenchmarkComparison_Comparison compares decimal.Decimal vs float64 comparison performance.
func BenchmarkComparison_Comparison(b *testing.B) {
	b.Run("decimal_Equal", func(b *testing.B) {
		d1 := decimal.NewFromFloat(100.50)
		d2 := decimal.NewFromFloat(100.50)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = d1.Equal(d2)
		}
	})

	b.Run("float64_equal", func(b *testing.B) {
		f1 := 100.50
		f2 := 100.50
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = f1 == f2
		}
	})

	b.Run("decimal_GreaterThan", func(b *testing.B) {
		d1 := decimal.NewFromFloat(100.50)
		d2 := decimal.NewFromFloat(50.25)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = d1.GreaterThan(d2)
		}
	})

	b.Run("float64_greater_than", func(b *testing.B) {
		f1 := 100.50
		f2 := 50.25
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = f1 > f2
		}
	})
}

// BenchmarkComparison_Creation compares decimal.Decimal vs float64 creation/initialization.
func BenchmarkComparison_Creation(b *testing.B) {
	b.Run("decimal_NewFromFloat", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = decimal.NewFromFloat(123.45)
		}
	})

	b.Run("decimal_NewFromInt", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = decimal.NewFromInt(12345)
		}
	})

	b.Run("float64_literal", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = 123.45
		}
	})
}

// BenchmarkComparison_StringConversion compares decimal.Decimal vs float64 string conversion.
func BenchmarkComparison_StringConversion(b *testing.B) {
	b.Run("decimal_String", func(b *testing.B) {
		d := decimal.NewFromFloat(123.45)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = d.String()
		}
	})

	b.Run("decimal_NewFromString", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = decimal.NewFromString("123.45")
		}
	})

	b.Run("float64_String_Sprintf", func(b *testing.B) {
		f := 123.45
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = fmt.Sprintf("%.2f", f)
		}
	})
}

// BenchmarkComparison_ComplexCalculation compares complex financial calculations.
// Simulates calculating compound interest: principal * (1 + rate)^periods
func BenchmarkComparison_ComplexCalculation(b *testing.B) {
	b.Run("decimal_CompoundInterest", func(b *testing.B) {
		principal := decimal.NewFromFloat(1000.00)
		rate := decimal.NewFromFloat(0.05) // 5%
		one := decimal.NewFromFloat(1.0)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// (1 + rate)
			baseRate := one.Add(rate)
			// (1 + rate)^periods - using repeated multiplication as approximation
			result := principal
			for j := 0; j < 12; j++ {
				result = result.Mul(baseRate)
			}
			_ = result
		}
	})

	b.Run("float64_CompoundInterest", func(b *testing.B) {
		principal := 1000.00
		rate := 0.05

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			baseRate := 1.0 + rate
			result := principal
			for j := 0; j < 12; j++ {
				result = result * baseRate
			}
			_ = result
		}
	})
}

// BenchmarkComparison_BulkOperations simulates bulk transaction processing.
// Measures performance impact at realistic transaction volumes.
func BenchmarkComparison_BulkOperations(b *testing.B) {
	const numTransactions = 1000

	b.Run("decimal_BulkSum", func(b *testing.B) {
		// Pre-generate test data
		amounts := make([]decimal.Decimal, numTransactions)
		for i := 0; i < numTransactions; i++ {
			amounts[i] = decimal.NewFromFloat(float64(i) * 0.01)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			sum := decimal.Zero
			for _, amount := range amounts {
				sum = sum.Add(amount)
			}
			_ = sum
		}
	})

	b.Run("float64_BulkSum", func(b *testing.B) {
		// Pre-generate test data
		amounts := make([]float64, numTransactions)
		for i := 0; i < numTransactions; i++ {
			amounts[i] = float64(i) * 0.01
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var sum float64
			for _, amount := range amounts {
				sum += amount
			}
			_ = sum
		}
	})
}

// BenchmarkComparison_RoundingOperations compares rounding performance.
func BenchmarkComparison_RoundingOperations(b *testing.B) {
	b.Run("decimal_RoundBank", func(b *testing.B) {
		d := decimal.NewFromFloat(123.456789)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = d.RoundBank(2)
		}
	})

	b.Run("decimal_Round", func(b *testing.B) {
		d := decimal.NewFromFloat(123.456789)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = d.Round(2)
		}
	})

	b.Run("float64_round_manual", func(b *testing.B) {
		f := 123.456789
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Manual rounding to 2 decimal places
			_ = float64(int(f*100+0.5)) / 100
		}
	})
}

// BenchmarkComparison_MemoryAllocation measures memory allocation differences.
func BenchmarkComparison_MemoryAllocation(b *testing.B) {
	b.Run("decimal_allocations", func(b *testing.B) {
		d1 := decimal.NewFromFloat(100.50)
		d2 := decimal.NewFromFloat(50.25)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result := d1.Add(d2)
			result = result.Mul(decimal.NewFromFloat(1.1))
			_ = result.String()
		}
	})

	b.Run("float64_allocations", func(b *testing.B) {
		f1 := 100.50
		f2 := 50.25

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result := f1 + f2
			result = result * 1.1
			_ = fmt.Sprintf("%.2f", result)
		}
	})
}

// BenchmarkComparison_ChainedOperations measures performance of chained calculations.
func BenchmarkComparison_ChainedOperations(b *testing.B) {
	b.Run("decimal_chained", func(b *testing.B) {
		d1 := decimal.NewFromFloat(100.00)
		d2 := decimal.NewFromFloat(50.00)
		d3 := decimal.NewFromFloat(25.00)
		d4 := decimal.NewFromFloat(2.00)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// ((100 + 50) - 25) * 2 / 5
			result := d1.Add(d2).Sub(d3).Mul(d4).Div(decimal.NewFromFloat(5.0))
			_ = result
		}
	})

	b.Run("float64_chained", func(b *testing.B) {
		f1 := 100.00
		f2 := 50.00
		f3 := 25.00
		f4 := 2.00

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// ((100 + 50) - 25) * 2 / 5
			result := ((f1 + f2) - f3) * f4 / 5.0
			_ = result
		}
	})
}

// BenchmarkComparison_PrecisionPreservation demonstrates the precision vs performance tradeoff.
// This benchmark shows WHERE float64 loses precision and decimal maintains it.
func BenchmarkComparison_PrecisionPreservation(b *testing.B) {
	b.Run("decimal_repeated_division", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result := decimal.NewFromFloat(1.0)
			for j := 0; j < 100; j++ {
				result = result.Div(decimal.NewFromFloat(3.0))
			}
			for j := 0; j < 100; j++ {
				result = result.Mul(decimal.NewFromFloat(3.0))
			}
			_ = result
		}
	})

	b.Run("float64_repeated_division", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			result := 1.0
			for j := 0; j < 100; j++ {
				result = result / 3.0
			}
			for j := 0; j < 100; j++ {
				result = result * 3.0
			}
			_ = result
		}
	})
}
