package result

import (
	"errors"
	"testing"
)

var (
	errBenchmark = errors.New("benchmark error")
	benchSink    interface{}
	benchSinkOk  bool
)

// BenchmarkOk measures the overhead of creating an Ok result
func BenchmarkOk(b *testing.B) {
	b.Run("int", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = Ok(42)
		}
	})

	b.Run("string", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = Ok("hello world")
		}
	})

	b.Run("struct", func(b *testing.B) {
		type Data struct {
			ID    int
			Name  string
			Value float64
		}
		data := Data{ID: 1, Name: "test", Value: 3.14}
		for i := 0; i < b.N; i++ {
			benchSink = Ok(data)
		}
	})
}

// BenchmarkErr measures the overhead of creating an Err result
func BenchmarkErr(b *testing.B) {
	for i := 0; i < b.N; i++ {
		benchSink = Err[int](errBenchmark)
	}
}

// BenchmarkIsOk measures the cost of checking success
func BenchmarkIsOk(b *testing.B) {
	okResult := Ok(42)
	errResult := Err[int](errBenchmark)

	b.Run("ok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSinkOk = okResult.IsOk()
		}
	})

	b.Run("err", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSinkOk = errResult.IsOk()
		}
	})
}

// BenchmarkUnwrap measures the cost of unwrapping
func BenchmarkUnwrap(b *testing.B) {
	okResult := Ok(42)
	errResult := Err[int](errBenchmark)

	b.Run("ok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink, _ = okResult.Unwrap()
		}
	})

	b.Run("err", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, benchSink = errResult.Unwrap()
		}
	})
}

// BenchmarkUnwrapOr measures UnwrapOr performance
func BenchmarkUnwrapOr(b *testing.B) {
	okResult := Ok(42)
	errResult := Err[int](errBenchmark)

	b.Run("ok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = okResult.UnwrapOr(0)
		}
	})

	b.Run("err", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = errResult.UnwrapOr(0)
		}
	})
}

// BenchmarkMap measures Map transformation performance
func BenchmarkMap(b *testing.B) {
	double := func(x int) int { return x * 2 }
	okResult := Ok(42)
	errResult := Err[int](errBenchmark)

	b.Run("ok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = Map(okResult, double)
		}
	})

	b.Run("err", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = Map(errResult, double)
		}
	})
}

// BenchmarkFlatMap measures FlatMap chaining performance
func BenchmarkFlatMap(b *testing.B) {
	validate := func(x int) Result[int] {
		if x > 0 {
			return Ok(x)
		}
		return Err[int](errBenchmark)
	}
	okResult := Ok(42)
	errResult := Err[int](errBenchmark)

	b.Run("ok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = FlatMap(okResult, validate)
		}
	})

	b.Run("err", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = FlatMap(errResult, validate)
		}
	})
}

// BenchmarkCollect measures Collect performance
func BenchmarkCollect(b *testing.B) {
	results := make([]Result[int], 100)
	for i := range results {
		results[i] = Ok(i)
	}

	b.Run("100_ok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = Collect(results)
		}
	})

	resultsWithErr := make([]Result[int], 100)
	for i := range resultsWithErr {
		if i == 50 {
			resultsWithErr[i] = Err[int](errBenchmark)
		} else {
			resultsWithErr[i] = Ok(i)
		}
	}

	b.Run("100_with_err_at_50", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = Collect(resultsWithErr)
		}
	})
}

// BenchmarkFromTuple measures FromTuple conversion performance
func BenchmarkFromTuple(b *testing.B) {
	b.Run("ok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = FromTuple(42, nil)
		}
	})

	b.Run("err", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = FromTuple(0, errBenchmark)
		}
	})
}

// BenchmarkTraditionalVsResult compares traditional error handling with Result
func BenchmarkTraditionalVsResult(b *testing.B) {
	// Traditional approach
	traditionalDouble := func(x int) (int, error) {
		if x < 0 {
			return 0, errBenchmark
		}
		return x * 2, nil
	}

	// Result approach
	resultDouble := func(x int) Result[int] {
		if x < 0 {
			return Err[int](errBenchmark)
		}
		return Ok(x * 2)
	}

	b.Run("traditional_success", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			v, err := traditionalDouble(42)
			if err != nil {
				benchSink = err
			} else {
				benchSink = v
			}
		}
	})

	b.Run("result_success", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r := resultDouble(42)
			if r.IsErr() {
				benchSink = r.Error()
			} else {
				benchSink, _ = r.Unwrap()
			}
		}
	})

	b.Run("traditional_error", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			v, err := traditionalDouble(-1)
			if err != nil {
				benchSink = err
			} else {
				benchSink = v
			}
		}
	})

	b.Run("result_error", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r := resultDouble(-1)
			if r.IsErr() {
				benchSink = r.Error()
			} else {
				benchSink, _ = r.Unwrap()
			}
		}
	})
}

// BenchmarkChainedOperations compares chained operations
func BenchmarkChainedOperations(b *testing.B) {
	// Traditional chaining
	traditionalPipeline := func(x int) (int, error) {
		// Step 1: double
		if x < 0 {
			return 0, errBenchmark
		}
		x = x * 2

		// Step 2: add 10
		if x > 1000 {
			return 0, errBenchmark
		}
		x = x + 10

		// Step 3: divide by 2
		if x == 0 {
			return 0, errBenchmark
		}
		return x / 2, nil
	}

	// Result chaining
	double := func(x int) Result[int] {
		if x < 0 {
			return Err[int](errBenchmark)
		}
		return Ok(x * 2)
	}

	addTen := func(x int) Result[int] {
		if x > 1000 {
			return Err[int](errBenchmark)
		}
		return Ok(x + 10)
	}

	halve := func(x int) Result[int] {
		if x == 0 {
			return Err[int](errBenchmark)
		}
		return Ok(x / 2)
	}

	b.Run("traditional_chain", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			v, err := traditionalPipeline(42)
			if err != nil {
				benchSink = err
			} else {
				benchSink = v
			}
		}
	})

	b.Run("result_chain", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r := FlatMap(FlatMap(double(42), addTen), halve)
			if r.IsErr() {
				benchSink = r.Error()
			} else {
				benchSink, _ = r.Unwrap()
			}
		}
	})
}
