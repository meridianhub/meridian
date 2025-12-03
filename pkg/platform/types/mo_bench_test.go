package types

import (
	"errors"
	"testing"

	"github.com/samber/mo"
)

var (
	errBenchmark = errors.New("benchmark error")
	benchSink    interface{}
	benchSinkOk  bool
)

// BenchmarkMoOk measures the overhead of creating an Ok result using samber/mo.
func BenchmarkMoOk(b *testing.B) {
	b.Run("int", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = mo.Ok(42)
		}
	})

	b.Run("string", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = mo.Ok("hello world")
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
			benchSink = mo.Ok(data)
		}
	})
}

// BenchmarkMoErr measures the overhead of creating an Err result.
func BenchmarkMoErr(b *testing.B) {
	for i := 0; i < b.N; i++ {
		benchSink = mo.Err[int](errBenchmark)
	}
}

// BenchmarkMoSome measures the overhead of creating a Some option.
func BenchmarkMoSome(b *testing.B) {
	b.Run("int", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = mo.Some(42)
		}
	})

	b.Run("string", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = mo.Some("hello world")
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
			benchSink = mo.Some(data)
		}
	})
}

// BenchmarkMoNone measures the overhead of creating a None option.
func BenchmarkMoNone(b *testing.B) {
	for i := 0; i < b.N; i++ {
		benchSink = mo.None[int]()
	}
}

// BenchmarkMoIsOk measures the cost of checking success on Result.
func BenchmarkMoIsOk(b *testing.B) {
	okResult := mo.Ok(42)
	errResult := mo.Err[int](errBenchmark)

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

// BenchmarkMoIsPresent measures the cost of checking presence on Option.
func BenchmarkMoIsPresent(b *testing.B) {
	someOpt := mo.Some(42)
	noneOpt := mo.None[int]()

	b.Run("some", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSinkOk = someOpt.IsPresent()
		}
	})

	b.Run("none", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSinkOk = noneOpt.IsPresent()
		}
	})
}

// BenchmarkMoGet measures the cost of extracting values from Result.
func BenchmarkMoGet(b *testing.B) {
	okResult := mo.Ok(42)
	errResult := mo.Err[int](errBenchmark)

	b.Run("ok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink, _ = okResult.Get()
		}
	})

	b.Run("err", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, benchSink = errResult.Get()
		}
	})
}

// BenchmarkMoOrElse measures OrElse performance on Result.
func BenchmarkMoOrElse(b *testing.B) {
	okResult := mo.Ok(42)
	errResult := mo.Err[int](errBenchmark)

	b.Run("ok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = okResult.OrElse(0)
		}
	})

	b.Run("err", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = errResult.OrElse(0)
		}
	})
}

// BenchmarkMoOptionOrElse measures OrElse performance on Option.
func BenchmarkMoOptionOrElse(b *testing.B) {
	someOpt := mo.Some(42)
	noneOpt := mo.None[int]()

	b.Run("some", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = someOpt.OrElse(0)
		}
	})

	b.Run("none", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = noneOpt.OrElse(0)
		}
	})
}

// BenchmarkMoMap measures Map transformation performance on Result.
func BenchmarkMoMap(b *testing.B) {
	double := func(x int) (int, error) { return x * 2, nil }
	okResult := mo.Ok(42)
	errResult := mo.Err[int](errBenchmark)

	b.Run("ok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = okResult.Map(double)
		}
	})

	b.Run("err", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = errResult.Map(double)
		}
	})
}

// BenchmarkMoOptionMap measures Map transformation performance on Option.
func BenchmarkMoOptionMap(b *testing.B) {
	double := func(x int) (int, bool) { return x * 2, true }
	someOpt := mo.Some(42)
	noneOpt := mo.None[int]()

	b.Run("some", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = someOpt.Map(double)
		}
	})

	b.Run("none", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = noneOpt.Map(double)
		}
	})
}

// BenchmarkMoFlatMap measures FlatMap chaining performance on Result.
func BenchmarkMoFlatMap(b *testing.B) {
	validate := func(x int) mo.Result[int] {
		if x > 0 {
			return mo.Ok(x)
		}
		return mo.Err[int](errBenchmark)
	}
	okResult := mo.Ok(42)
	errResult := mo.Err[int](errBenchmark)

	b.Run("ok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = okResult.FlatMap(validate)
		}
	})

	b.Run("err", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = errResult.FlatMap(validate)
		}
	})
}

// BenchmarkMoTupleToResult measures TupleToResult conversion performance.
func BenchmarkMoTupleToResult(b *testing.B) {
	b.Run("ok", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = mo.TupleToResult(42, nil)
		}
	})

	b.Run("err", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			benchSink = mo.TupleToResult(0, errBenchmark)
		}
	})
}

// BenchmarkTraditionalVsMo compares traditional error handling with mo.Result.
func BenchmarkTraditionalVsMo(b *testing.B) {
	// Traditional approach
	traditionalDouble := func(x int) (int, error) {
		if x < 0 {
			return 0, errBenchmark
		}
		return x * 2, nil
	}

	// mo.Result approach
	moDouble := func(x int) mo.Result[int] {
		if x < 0 {
			return mo.Err[int](errBenchmark)
		}
		return mo.Ok(x * 2)
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

	b.Run("mo_success", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r := moDouble(42)
			if r.IsError() {
				benchSink = r.Error()
			} else {
				benchSink = r.MustGet()
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

	b.Run("mo_error", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r := moDouble(-1)
			if r.IsError() {
				benchSink = r.Error()
			} else {
				benchSink = r.MustGet()
			}
		}
	})
}

// BenchmarkTraditionalNilVsMoOption compares traditional nil checks with mo.Option.
func BenchmarkTraditionalNilVsMoOption(b *testing.B) {
	// Traditional approach with pointer
	traditionalFind := func(id int) *int {
		if id < 0 {
			return nil
		}
		result := id * 2
		return &result
	}

	// mo.Option approach
	moFind := func(id int) mo.Option[int] {
		if id < 0 {
			return mo.None[int]()
		}
		return mo.Some(id * 2)
	}

	b.Run("traditional_found", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ptr := traditionalFind(42)
			if ptr != nil {
				benchSink = *ptr
			} else {
				benchSink = 0
			}
		}
	})

	b.Run("mo_found", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			opt := moFind(42)
			if opt.IsPresent() {
				benchSink = opt.MustGet()
			} else {
				benchSink = 0
			}
		}
	})

	b.Run("traditional_not_found", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ptr := traditionalFind(-1)
			if ptr != nil {
				benchSink = *ptr
			} else {
				benchSink = 0
			}
		}
	})

	b.Run("mo_not_found", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			opt := moFind(-1)
			if opt.IsPresent() {
				benchSink = opt.MustGet()
			} else {
				benchSink = 0
			}
		}
	})
}

// BenchmarkChainedOperations compares chained operations.
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

	// mo.Result chaining
	double := func(x int) mo.Result[int] {
		if x < 0 {
			return mo.Err[int](errBenchmark)
		}
		return mo.Ok(x * 2)
	}

	addTen := func(x int) mo.Result[int] {
		if x > 1000 {
			return mo.Err[int](errBenchmark)
		}
		return mo.Ok(x + 10)
	}

	halve := func(x int) mo.Result[int] {
		if x == 0 {
			return mo.Err[int](errBenchmark)
		}
		return mo.Ok(x / 2)
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

	b.Run("mo_chain", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r := double(42).FlatMap(addTen).FlatMap(halve)
			if r.IsError() {
				benchSink = r.Error()
			} else {
				benchSink = r.MustGet()
			}
		}
	})
}
