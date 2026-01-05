package cel

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Performance Benchmarks for CEL Compilation and Evaluation
//
// These benchmarks measure the performance characteristics of CEL operations
// to ensure they meet operational requirements. Run with:
//
//   go test -bench=. -benchmem ./services/reference-data/cel/
//
// Target metrics (documented for audit):
// - Compilation: <1ms per expression
// - Evaluation: <100µs per evaluation
// - Bucket key generation: <50µs per hash
//
// cel-go version: 0.26.1

// BenchmarkCompilerCreation measures the time to create a new CEL compiler.
// This is typically done once at application startup.
func BenchmarkCompilerCreation(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		c, err := NewCompiler()
		if err != nil {
			b.Fatal(err)
		}
		_ = c
	}
}

// BenchmarkCompileValidationSimple measures compilation of simple validation expressions.
func BenchmarkCompileValidationSimple(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	expression := `attributes["region"] == "US"`

	b.ResetTimer()
	for b.Loop() {
		_, err := c.CompileValidation(expression)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompileValidationComplex measures compilation of complex validation expressions.
func BenchmarkCompileValidationComplex(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	expression := `attributes["type"] in ["A", "B", "C"] && ` +
		`parse_decimal(amount) > 0.0 && parse_decimal(amount) < 1000000.0 && ` +
		`source != "" && valid_from < valid_to`

	b.ResetTimer()
	for b.Loop() {
		_, err := c.CompileValidation(expression)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompileBucketKeySimple measures compilation of simple bucket key expressions.
func BenchmarkCompileBucketKeySimple(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	expression := `bucket_key([attributes["region"]])`

	b.ResetTimer()
	for b.Loop() {
		_, err := c.CompileBucketKey(expression)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompileBucketKeyComplex measures compilation of multi-attribute bucket keys.
func BenchmarkCompileBucketKeyComplex(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	expression := `bucket_key([attributes["type"], attributes["region"], attributes["vintage"], attributes["grade"], attributes["source"]])`

	b.ResetTimer()
	for b.Loop() {
		_, err := c.CompileBucketKey(expression)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEvalValidationSimple measures evaluation of pre-compiled simple validation.
func BenchmarkEvalValidationSimple(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	prg, err := c.CompileValidation(`attributes["region"] == "US"`)
	require.NoError(b, err)

	now := time.Now()
	input := map[string]any{
		"attributes": map[string]string{"region": "US"},
		"amount":     "100",
		"valid_from": now,
		"valid_to":   now.Add(time.Hour),
		"source":     "test",
	}

	b.ResetTimer()
	for b.Loop() {
		_, _, err := prg.Eval(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEvalValidationComplex measures evaluation of pre-compiled complex validation.
func BenchmarkEvalValidationComplex(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	prg, err := c.CompileValidation(`attributes["type"] in ["A", "B", "C"] && ` +
		`parse_decimal(amount) > 0.0 && parse_decimal(amount) < 1000000.0 && ` +
		`source != "" && valid_from < valid_to`)
	require.NoError(b, err)

	now := time.Now()
	input := map[string]any{
		"attributes": map[string]string{"type": "B"},
		"amount":     "500.25",
		"valid_from": now,
		"valid_to":   now.Add(time.Hour),
		"source":     "meter-001",
	}

	b.ResetTimer()
	for b.Loop() {
		_, _, err := prg.Eval(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEvalBucketKeySimple measures bucket key generation with single attribute.
func BenchmarkEvalBucketKeySimple(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["region"]])`)
	require.NoError(b, err)

	input := map[string]any{
		"attributes": map[string]string{"region": "US"},
	}

	b.ResetTimer()
	for b.Loop() {
		_, _, err := prg.Eval(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEvalBucketKeyComplex measures bucket key generation with five attributes.
func BenchmarkEvalBucketKeyComplex(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["type"], attributes["region"], attributes["vintage"], attributes["grade"], attributes["source"]])`)
	require.NoError(b, err)

	input := map[string]any{
		"attributes": map[string]string{
			"type":    "carbon",
			"region":  "eu-west",
			"vintage": "2024",
			"grade":   "A",
			"source":  "verified",
		},
	}

	b.ResetTimer()
	for b.Loop() {
		_, _, err := prg.Eval(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEvalParallelValidation measures validation under concurrent load.
func BenchmarkEvalParallelValidation(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	prg, err := c.CompileValidation(`attributes["region"] == "US" && parse_decimal(amount) > 0.0`)
	require.NoError(b, err)

	now := time.Now()
	input := map[string]any{
		"attributes": map[string]string{"region": "US"},
		"amount":     "100.50",
		"valid_from": now,
		"valid_to":   now.Add(time.Hour),
		"source":     "test",
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _, err := prg.Eval(input)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkEvalParallelBucketKey measures bucket key generation under concurrent load.
func BenchmarkEvalParallelBucketKey(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["type"], attributes["region"], attributes["vintage"]])`)
	require.NoError(b, err)

	input := map[string]any{
		"attributes": map[string]string{
			"type":    "energy",
			"region":  "us-east",
			"vintage": "2024",
		},
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _, err := prg.Eval(input)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkConstraintValidation measures the overhead of security constraint checking.
func BenchmarkConstraintValidation(b *testing.B) {
	expression := `attributes["region"] == "US" && attributes["type"] in ["A", "B"]`

	b.ResetTimer()
	for b.Loop() {
		_ = validateExpressionConstraints(expression)
	}
}

// BenchmarkMeasureExpressionDepth measures the depth calculation overhead.
func BenchmarkMeasureExpressionDepth(b *testing.B) {
	expression := `((attributes["a"] == "1") && (attributes["b"] in ["x", "y", "z"]))`

	b.ResetTimer()
	for b.Loop() {
		_ = measureExpressionDepth(expression)
	}
}

// TestBenchmarkBaseline runs a basic performance test with assertions.
// This is a test, not a benchmark, so it runs with regular test execution.
func TestBenchmarkBaseline(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	// Measure compilation time
	t.Run("compilation_time", func(t *testing.T) {
		expression := `bucket_key([attributes["region"], attributes["type"]])`

		start := time.Now()
		iterations := 100
		for i := 0; i < iterations; i++ {
			_, err := c.CompileBucketKey(expression)
			require.NoError(t, err)
		}
		elapsed := time.Since(start)

		avgTime := elapsed / time.Duration(iterations)
		t.Logf("Average compilation time: %v", avgTime)

		// Compilation should be under 1ms
		if avgTime > 1*time.Millisecond {
			t.Errorf("Compilation too slow: %v (target: <1ms)", avgTime)
		}
	})

	// Measure evaluation time
	t.Run("evaluation_time", func(t *testing.T) {
		prg, err := c.CompileBucketKey(`bucket_key([attributes["region"], attributes["type"]])`)
		require.NoError(t, err)

		input := map[string]any{
			"attributes": map[string]string{"region": "US", "type": "A"},
		}

		start := time.Now()
		iterations := 10000
		for i := 0; i < iterations; i++ {
			_, _, err := prg.Eval(input)
			require.NoError(t, err)
		}
		elapsed := time.Since(start)

		avgTime := elapsed / time.Duration(iterations)
		t.Logf("Average evaluation time: %v", avgTime)

		// Evaluation should be under 100µs
		if avgTime > 100*time.Microsecond {
			t.Errorf("Evaluation too slow: %v (target: <100µs)", avgTime)
		}
	})
}

// TestThroughputUnderLoad measures sustained throughput over time.
func TestThroughputUnderLoad(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["region"], attributes["grade"]])`)
	require.NoError(t, err)

	input := map[string]any{
		"attributes": map[string]string{"region": "EU", "grade": "B"},
	}

	// Run for 1 second and count operations
	duration := 1 * time.Second
	deadline := time.Now().Add(duration)

	var count int64
	numWorkers := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			localCount := int64(0)
			for time.Now().Before(deadline) {
				_, _, _ = prg.Eval(input)
				localCount++
			}
			mu.Lock()
			count += localCount
			mu.Unlock()
		}()
	}

	wg.Wait()

	opsPerSecond := float64(count) / duration.Seconds()
	t.Logf("Throughput: %.0f ops/sec across %d workers", opsPerSecond, numWorkers)
	t.Logf("Per-worker throughput: %.0f ops/sec", opsPerSecond/float64(numWorkers))

	// Target: at least 100k ops/sec total
	if opsPerSecond < 100000 {
		t.Errorf("Throughput below target: %.0f (want: >100000)", opsPerSecond)
	}
}

// TestMemoryAllocations verifies memory allocation patterns during evaluation.
func TestMemoryAllocations(t *testing.T) {
	c, err := NewCompiler()
	require.NoError(t, err)

	prg, err := c.CompileBucketKey(`bucket_key([attributes["region"], attributes["type"]])`)
	require.NoError(t, err)

	input := map[string]any{
		"attributes": map[string]string{"region": "US", "type": "carbon"},
	}

	// Warm up
	for i := 0; i < 100; i++ {
		_, _, _ = prg.Eval(input)
	}

	// Force GC and measure
	runtime.GC()

	var m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m1)

	iterations := 10000
	for i := 0; i < iterations; i++ {
		_, _, _ = prg.Eval(input)
	}

	runtime.ReadMemStats(&m2)

	allocsPerOp := (m2.Mallocs - m1.Mallocs) / uint64(iterations)
	bytesPerOp := (m2.TotalAlloc - m1.TotalAlloc) / uint64(iterations)

	t.Logf("Allocations per evaluation: %d", allocsPerOp)
	t.Logf("Bytes allocated per evaluation: %d", bytesPerOp)

	// Log for monitoring trends, not hard assertion since this depends on cel-go version
}

// BenchmarkCompileVsEvalRatio shows the compile/eval cost ratio.
// This helps determine if caching compiled programs is beneficial.
func BenchmarkCompileVsEvalRatio(b *testing.B) {
	expression := `bucket_key([attributes["type"], attributes["region"], attributes["vintage"]])`
	input := map[string]any{
		"attributes": map[string]string{
			"type":    "carbon",
			"region":  "eu-west",
			"vintage": "2024",
		},
	}

	b.Run("compile_and_eval", func(b *testing.B) {
		c, _ := NewCompiler()
		for b.Loop() {
			prg, _ := c.CompileBucketKey(expression)
			_, _, _ = prg.Eval(input)
		}
	})

	b.Run("eval_only", func(b *testing.B) {
		c, _ := NewCompiler()
		prg, _ := c.CompileBucketKey(expression)
		b.ResetTimer()
		for b.Loop() {
			_, _, _ = prg.Eval(input)
		}
	})
}

// TestCELVersionForBenchmarks logs the CEL version for benchmark audit trail.
func TestCELVersionForBenchmarks(t *testing.T) {
	t.Logf("=== CEL Performance Benchmarks ===")
	t.Logf("cel-go version: %s", CELVersion)
	t.Logf("GOMAXPROCS: %d", runtime.GOMAXPROCS(0))
	t.Logf("NumCPU: %d", runtime.NumCPU())
	t.Logf("")
	t.Logf("Run benchmarks with: go test -bench=. -benchmem ./services/reference-data/cel/")
	t.Logf("")
	t.Logf("Expected performance targets:")
	t.Logf("  - Compilation: <1ms per expression")
	t.Logf("  - Evaluation: <100µs per evaluation")
	t.Logf("  - Throughput: >100k ops/sec")
}

// BenchmarkSafeParseLibFunctions benchmarks the custom parse functions.
func BenchmarkSafeParseLibFunctions(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	now := time.Now()
	input := map[string]any{
		"attributes": map[string]string{},
		"amount":     "12345.67",
		"valid_from": now,
		"valid_to":   now.Add(time.Hour),
		"source":     "test",
	}

	benchmarks := []struct {
		name       string
		expression string
	}{
		{"parse_int", `parse_int("12345") > 0`},
		{"parse_decimal", `parse_decimal(amount) > 0.0`},
		{"parse_bool", `parse_bool("true")`},
		{"parse_iso_date", `parse_iso_date("2024-01-15T10:30:00Z") < valid_to`},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			prg, err := c.CompileValidation(bm.expression)
			require.NoError(b, err)

			b.ResetTimer()
			for b.Loop() {
				_, _, err := prg.Eval(input)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkVaryingAttributeCounts measures performance with different attribute counts.
func BenchmarkVaryingAttributeCounts(b *testing.B) {
	c, err := NewCompiler()
	require.NoError(b, err)

	counts := []int{1, 2, 3, 5, 10}

	for _, count := range counts {
		b.Run(fmt.Sprintf("attrs_%d", count), func(b *testing.B) {
			// Build expression and attributes
			var keys []string
			attrs := make(map[string]string)
			for i := 0; i < count; i++ {
				key := fmt.Sprintf("attr%d", i)
				keys = append(keys, fmt.Sprintf(`attributes["%s"]`, key))
				attrs[key] = fmt.Sprintf("value%d", i)
			}

			expression := fmt.Sprintf("bucket_key([%s])", joinStrings(keys, ", "))
			prg, err := c.CompileBucketKey(expression)
			require.NoError(b, err)

			input := map[string]any{"attributes": attrs}

			b.ResetTimer()
			for b.Loop() {
				_, _, err := prg.Eval(input)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func joinStrings(s []string, sep string) string {
	result := ""
	for i, str := range s {
		if i > 0 {
			result += sep
		}
		result += str
	}
	return result
}
