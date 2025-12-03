package functional

import (
	"strconv"
	"testing"
)

func TestMap(t *testing.T) {
	tests := []struct {
		name  string
		input []int
		fn    func(int) string
		want  []string
	}{
		{
			name:  "int to string",
			input: []int{1, 2, 3},
			fn:    func(i int) string { return strconv.Itoa(i) },
			want:  []string{"1", "2", "3"},
		},
		{
			name:  "empty slice",
			input: []int{},
			fn:    func(i int) string { return strconv.Itoa(i) },
			want:  []string{},
		},
		{
			name:  "double values",
			input: []int{1, 2, 3},
			fn:    func(i int) string { return strconv.Itoa(i * 2) },
			want:  []string{"2", "4", "6"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Map(tt.input, tt.fn)
			if len(got) != len(tt.want) {
				t.Fatalf("Map() len = %v, want %v", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Map()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFilter(t *testing.T) {
	tests := []struct {
		name  string
		input []int
		pred  func(int) bool
		want  []int
	}{
		{
			name:  "filter even numbers",
			input: []int{1, 2, 3, 4, 5, 6},
			pred:  func(i int) bool { return i%2 == 0 },
			want:  []int{2, 4, 6},
		},
		{
			name:  "filter none match",
			input: []int{1, 3, 5},
			pred:  func(i int) bool { return i%2 == 0 },
			want:  []int{},
		},
		{
			name:  "filter all match",
			input: []int{2, 4, 6},
			pred:  func(i int) bool { return i%2 == 0 },
			want:  []int{2, 4, 6},
		},
		{
			name:  "empty slice",
			input: []int{},
			pred:  func(_ int) bool { return true },
			want:  []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Filter(tt.input, tt.pred)
			if len(got) != len(tt.want) {
				t.Fatalf("Filter() len = %v, want %v", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Filter()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestReduce(t *testing.T) {
	tests := []struct {
		name  string
		input []int
		init  int
		fn    func(int, int) int
		want  int
	}{
		{
			name:  "sum",
			input: []int{1, 2, 3, 4, 5},
			init:  0,
			fn:    func(acc, v int) int { return acc + v },
			want:  15,
		},
		{
			name:  "product",
			input: []int{1, 2, 3, 4},
			init:  1,
			fn:    func(acc, v int) int { return acc * v },
			want:  24,
		},
		{
			name:  "empty slice returns init",
			input: []int{},
			init:  42,
			fn:    func(acc, v int) int { return acc + v },
			want:  42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Reduce(tt.input, tt.init, tt.fn)
			if got != tt.want {
				t.Errorf("Reduce() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReduce_StringConcatenation(t *testing.T) {
	input := []string{"a", "b", "c"}
	got := Reduce(input, "", func(acc, v string) string { return acc + v })
	if got != "abc" {
		t.Errorf("Reduce() = %v, want abc", got)
	}
}

func TestFind(t *testing.T) {
	tests := []struct {
		name   string
		input  []int
		pred   func(int) bool
		wantOk bool
		want   int
	}{
		{
			name:   "found first match",
			input:  []int{1, 2, 3, 4, 5},
			pred:   func(i int) bool { return i > 2 },
			wantOk: true,
			want:   3,
		},
		{
			name:   "not found",
			input:  []int{1, 2, 3},
			pred:   func(i int) bool { return i > 10 },
			wantOk: false,
		},
		{
			name:   "empty slice",
			input:  []int{},
			pred:   func(_ int) bool { return true },
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Find(tt.input, tt.pred)
			if got.IsSome() != tt.wantOk {
				t.Errorf("Find().IsSome() = %v, want %v", got.IsSome(), tt.wantOk)
			}
			if tt.wantOk {
				if val := got.Unwrap(); val != tt.want {
					t.Errorf("Find().Unwrap() = %v, want %v", val, tt.want)
				}
			}
		})
	}
}

func TestAny(t *testing.T) {
	tests := []struct {
		name  string
		input []int
		pred  func(int) bool
		want  bool
	}{
		{
			name:  "any match",
			input: []int{1, 2, 3, 4},
			pred:  func(i int) bool { return i > 3 },
			want:  true,
		},
		{
			name:  "none match",
			input: []int{1, 2, 3},
			pred:  func(i int) bool { return i > 10 },
			want:  false,
		},
		{
			name:  "empty slice",
			input: []int{},
			pred:  func(_ int) bool { return true },
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Any(tt.input, tt.pred)
			if got != tt.want {
				t.Errorf("Any() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAll(t *testing.T) {
	tests := []struct {
		name  string
		input []int
		pred  func(int) bool
		want  bool
	}{
		{
			name:  "all match",
			input: []int{2, 4, 6, 8},
			pred:  func(i int) bool { return i%2 == 0 },
			want:  true,
		},
		{
			name:  "not all match",
			input: []int{2, 4, 5, 8},
			pred:  func(i int) bool { return i%2 == 0 },
			want:  false,
		},
		{
			name:  "empty slice (vacuous truth)",
			input: []int{},
			pred:  func(_ int) bool { return false },
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := All(tt.input, tt.pred)
			if got != tt.want {
				t.Errorf("All() = %v, want %v", got, tt.want)
			}
		})
	}
}

type person struct {
	Name string
	Age  int
}

func TestGroupBy(t *testing.T) {
	people := []person{
		{Name: "Alice", Age: 30},
		{Name: "Bob", Age: 25},
		{Name: "Charlie", Age: 30},
		{Name: "Diana", Age: 25},
	}

	got := GroupBy(people, func(p person) int { return p.Age })

	if len(got) != 2 {
		t.Fatalf("GroupBy() len = %v, want 2", len(got))
	}
	if len(got[30]) != 2 {
		t.Errorf("GroupBy()[30] len = %v, want 2", len(got[30]))
	}
	if len(got[25]) != 2 {
		t.Errorf("GroupBy()[25] len = %v, want 2", len(got[25]))
	}
}

func TestGroupBy_Empty(t *testing.T) {
	var empty []int
	got := GroupBy(empty, func(i int) int { return i })
	if len(got) != 0 {
		t.Errorf("GroupBy() len = %v, want 0", len(got))
	}
}

func TestFirst(t *testing.T) {
	tests := []struct {
		name   string
		input  []int
		wantOk bool
		want   int
	}{
		{
			name:   "non-empty slice",
			input:  []int{1, 2, 3},
			wantOk: true,
			want:   1,
		},
		{
			name:   "empty slice",
			input:  []int{},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := First(tt.input)
			if got.IsSome() != tt.wantOk {
				t.Errorf("First().IsSome() = %v, want %v", got.IsSome(), tt.wantOk)
			}
			if tt.wantOk && got.Unwrap() != tt.want {
				t.Errorf("First().Unwrap() = %v, want %v", got.Unwrap(), tt.want)
			}
		})
	}
}

func TestLast(t *testing.T) {
	tests := []struct {
		name   string
		input  []int
		wantOk bool
		want   int
	}{
		{
			name:   "non-empty slice",
			input:  []int{1, 2, 3},
			wantOk: true,
			want:   3,
		},
		{
			name:   "empty slice",
			input:  []int{},
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Last(tt.input)
			if got.IsSome() != tt.wantOk {
				t.Errorf("Last().IsSome() = %v, want %v", got.IsSome(), tt.wantOk)
			}
			if tt.wantOk && got.Unwrap() != tt.want {
				t.Errorf("Last().Unwrap() = %v, want %v", got.Unwrap(), tt.want)
			}
		})
	}
}

func TestFindIndex(t *testing.T) {
	tests := []struct {
		name   string
		input  []int
		pred   func(int) bool
		wantOk bool
		want   int
	}{
		{
			name:   "found",
			input:  []int{1, 2, 3, 4, 5},
			pred:   func(i int) bool { return i == 3 },
			wantOk: true,
			want:   2,
		},
		{
			name:   "not found",
			input:  []int{1, 2, 3},
			pred:   func(i int) bool { return i == 10 },
			wantOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindIndex(tt.input, tt.pred)
			if got.IsSome() != tt.wantOk {
				t.Errorf("FindIndex().IsSome() = %v, want %v", got.IsSome(), tt.wantOk)
			}
			if tt.wantOk && got.Unwrap() != tt.want {
				t.Errorf("FindIndex().Unwrap() = %v, want %v", got.Unwrap(), tt.want)
			}
		})
	}
}

func TestCount(t *testing.T) {
	tests := []struct {
		name  string
		input []int
		pred  func(int) bool
		want  int
	}{
		{
			name:  "count evens",
			input: []int{1, 2, 3, 4, 5, 6},
			pred:  func(i int) bool { return i%2 == 0 },
			want:  3,
		},
		{
			name:  "count none",
			input: []int{1, 3, 5},
			pred:  func(i int) bool { return i%2 == 0 },
			want:  0,
		},
		{
			name:  "empty slice",
			input: []int{},
			pred:  func(_ int) bool { return true },
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Count(tt.input, tt.pred)
			if got != tt.want {
				t.Errorf("Count() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPartition(t *testing.T) {
	input := []int{1, 2, 3, 4, 5, 6}
	isEven := func(i int) bool { return i%2 == 0 }

	evens, odds := Partition(input, isEven)

	wantEvens := []int{2, 4, 6}
	wantOdds := []int{1, 3, 5}

	if len(evens) != len(wantEvens) {
		t.Fatalf("evens len = %v, want %v", len(evens), len(wantEvens))
	}
	if len(odds) != len(wantOdds) {
		t.Fatalf("odds len = %v, want %v", len(odds), len(wantOdds))
	}

	for i := range evens {
		if evens[i] != wantEvens[i] {
			t.Errorf("evens[%d] = %v, want %v", i, evens[i], wantEvens[i])
		}
	}
	for i := range odds {
		if odds[i] != wantOdds[i] {
			t.Errorf("odds[%d] = %v, want %v", i, odds[i], wantOdds[i])
		}
	}
}

func TestFlatten(t *testing.T) {
	tests := []struct {
		name  string
		input [][]int
		want  []int
	}{
		{
			name:  "flatten multiple slices",
			input: [][]int{{1, 2}, {3, 4}, {5}},
			want:  []int{1, 2, 3, 4, 5},
		},
		{
			name:  "flatten with empty slice",
			input: [][]int{{1, 2}, {}, {3}},
			want:  []int{1, 2, 3},
		},
		{
			name:  "flatten empty",
			input: [][]int{},
			want:  []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Flatten(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("Flatten() len = %v, want %v", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Flatten()[%d] = %v, want %v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFlatMap(t *testing.T) {
	input := []int{1, 2, 3}
	duplicate := func(i int) []int { return []int{i, i} }

	got := FlatMap(input, duplicate)
	want := []int{1, 1, 2, 2, 3, 3}

	if len(got) != len(want) {
		t.Fatalf("FlatMap() len = %v, want %v", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("FlatMap()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// Benchmarks

func BenchmarkMap(b *testing.B) {
	input := make([]int, 1000)
	for i := range input {
		input[i] = i
	}
	double := func(i int) int { return i * 2 }

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Map(input, double)
	}
}

func BenchmarkMap_Manual(b *testing.B) {
	input := make([]int, 1000)
	for i := range input {
		input[i] = i
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := make([]int, len(input))
		for j, v := range input {
			result[j] = v * 2
		}
		_ = result
	}
}

func BenchmarkFilter(b *testing.B) {
	input := make([]int, 1000)
	for i := range input {
		input[i] = i
	}
	isEven := func(i int) bool { return i%2 == 0 }

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Filter(input, isEven)
	}
}

func BenchmarkFilter_Manual(b *testing.B) {
	input := make([]int, 1000)
	for i := range input {
		input[i] = i
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := make([]int, 0, len(input))
		for _, v := range input {
			if v%2 == 0 {
				result = append(result, v)
			}
		}
		_ = result
	}
}

func BenchmarkReduce(b *testing.B) {
	input := make([]int, 1000)
	for i := range input {
		input[i] = i
	}
	sum := func(acc, v int) int { return acc + v }

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Reduce(input, 0, sum)
	}
}

func BenchmarkReduce_Manual(b *testing.B) {
	input := make([]int, 1000)
	for i := range input {
		input[i] = i
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		acc := 0
		for _, v := range input {
			acc += v
		}
		_ = acc
	}
}

func BenchmarkFind(b *testing.B) {
	input := make([]int, 1000)
	for i := range input {
		input[i] = i
	}
	// Find element near the end
	pred := func(i int) bool { return i == 999 }

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Find(input, pred)
	}
}

func BenchmarkGroupBy(b *testing.B) {
	type item struct {
		category int
		value    int
	}
	input := make([]item, 1000)
	for i := range input {
		input[i] = item{category: i % 10, value: i}
	}
	keyFn := func(it item) int { return it.category }

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GroupBy(input, keyFn)
	}
}

func BenchmarkGroupBy_Manual(b *testing.B) {
	type item struct {
		category int
		value    int
	}
	input := make([]item, 1000)
	for i := range input {
		input[i] = item{category: i % 10, value: i}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := make(map[int][]item)
		for _, v := range input {
			result[v.category] = append(result[v.category], v)
		}
		_ = result
	}
}
