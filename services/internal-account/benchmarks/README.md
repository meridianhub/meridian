# Internal Account Service - Performance Benchmarks

This directory contains comprehensive performance benchmarks and NFR validation tests
for the Internal Account service.

## NFR Targets (from PRD)

| Operation | Production Target | CI Threshold | Notes |
|-----------|------------------|--------------|-------|
| Balance queries | P99 < 50ms | P99 < 200ms | Via Position Keeping integration |
| Account creation | P99 < 50ms | P99 < 200ms | Includes validation + persistence |
| Account lookups | P99 < 5ms | P99 < 50ms | By UUID or account_code |
| Concurrent operations | 1000 simultaneous | 1000 simultaneous | All complete successfully |

**Note**: CI thresholds include 4-10x headroom to account for shared CI runner variability and testcontainer overhead.

## Running Benchmarks

### All Benchmarks

```bash
go test -bench=. -benchmem -benchtime=5s -timeout=30m \
  ./services/internal-account/benchmarks/
```

### Specific Benchmark Categories

```bash
# Core operations only
go test -bench=Benchmark -benchmem -benchtime=5s \
  ./services/internal-account/benchmarks/

# Concurrent operations only
go test -bench=BenchmarkConcurrent -benchmem -benchtime=5s \
  ./services/internal-account/benchmarks/

# Mixed workload only
go test -bench=BenchmarkMixed -benchmem -benchtime=5s \
  ./services/internal-account/benchmarks/
```

### NFR Validation Tests

```bash
# Run all NFR validation tests
go test -v -run TestNFR -timeout=10m \
  ./services/internal-account/benchmarks/

# Run specific NFR test
go test -v -run TestNFR_BalanceQueryLatency \
  ./services/internal-account/benchmarks/
```

### Load and Throughput Tests

```bash
go test -v -run TestThroughput -timeout=5m \
  ./services/internal-account/benchmarks/

go test -v -run TestConcurrent -timeout=5m \
  ./services/internal-account/benchmarks/
```

## Interpreting Results

### Benchmark Output

```text
BenchmarkInitiateAccount_Single-10    5000    234567 ns/op    4096 B/op    42 allocs/op
```

- `10`: Number of CPU cores used (GOMAXPROCS)
- `5000`: Number of iterations completed
- `234567 ns/op`: Nanoseconds per operation (~0.23ms)
- `4096 B/op`: Bytes allocated per operation
- `42 allocs/op`: Memory allocations per operation

### NFR Test Output

```text
=== RUN   TestNFR_BalanceQueryLatency
    nfr_validation_test.go:45: Balance query P50 latency: 1.234ms
    nfr_validation_test.go:46: Balance query P99 latency: 12.345ms
    nfr_validation_test.go:52: Note: P99 12.345ms exceeds production target (50ms) but passes CI threshold
--- PASS: TestNFR_BalanceQueryLatency (5.23s)
```

## File Structure

| File | Purpose |
|------|---------|
| `helpers_test.go` | Test infrastructure, container setup, mock clients |
| `performance_bench_test.go` | Core operation benchmarks (create, read, update, list) |
| `load_test.go` | Concurrent operations and mixed workload benchmarks |
| `nfr_validation_test.go` | NFR validation tests with P99 latency measurement |

## Test Infrastructure

### testContainer

The `testContainer` struct provides:

- PostgreSQL testcontainer with tenant schema
- Repository instance for persistence
- gRPC service instance
- Mock Position Keeping client for balance queries
- Tenant context (`tc.ctx`)

### Mock Position Keeping Client

Returns realistic balance data with three balance types:

- `BALANCE_TYPE_CURRENT` - Current balance
- `BALANCE_TYPE_AVAILABLE` - Available balance
- `BALANCE_TYPE_LEDGER` - Ledger balance

## CI Integration

Benchmarks run automatically on PRs that modify:

- `services/internal-account/**`
- `.github/workflows/benchmarks.yml`

Results are written to GitHub Step Summary for easy review.

## Performance Regression Detection

To detect regressions, compare benchmark results:

```bash
# Run benchmarks and save baseline
go test -bench=. -benchmem -count=5 \
  ./services/internal-account/benchmarks/ > baseline.txt

# After changes, compare
go test -bench=. -benchmem -count=5 \
  ./services/internal-account/benchmarks/ > current.txt

# Use benchstat to compare
benchstat baseline.txt current.txt
```

Flag >20% degradation in P99 latencies for investigation.

## Testcontainer Considerations

- Container startup adds ~2-5 seconds to test initialization
- Container is reused across benchmark iterations via `setupBenchContainer`
- Each NFR test creates a fresh container via `setupTestContainer`
- Uses `shared/platform/testdb.SetupPostgres` for consistent setup
- **Never uses `time.Sleep`** - uses `shared/platform/await` if waiting needed

## Multi-Asset Support

Test data includes varied instrument codes to validate multi-asset performance:

- Currencies: GBP, USD, EUR
- Energy: KWH (future)
- Compute: GPU_HOUR (future)

Account types tested:

- CLEARING, HOLDING, SUSPENSE, REVENUE, EXPENSE
- NOSTRO/VOSTRO (with counterparty details)
