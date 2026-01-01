# PRD: Concurrency & Reliability Fixes Q1 2026

**Author:** Engineering
**Status:** Draft
**Created:** 2026-01-01
**Target:** Q1 2026

---

## Executive Summary

This PRD addresses concurrency bugs, resource leaks, and reliability issues discovered during a deep audit of the Meridian codebase. These issues can cause deadlocks, goroutine leaks, and silent failures in production.

---

## Goals

1. **Eliminate Deadlocks**: Fix channel buffer sizing issues that can cause goroutine deadlocks
2. **Prevent Resource Leaks**: Ensure proper cleanup of signals, goroutines, and connections
3. **Improve Error Visibility**: Surface silent failures in HTTP response handling
4. **Harden Startup/Shutdown**: Make service lifecycle more robust

---

## Non-Goals

- New feature development
- Performance optimization beyond fixing leaks
- Refactoring beyond targeted fixes

---

## Work Items

### Stream 1: Critical Fixes (P0)

#### 1.1 Financial Accounting Channel Buffer Deadlock

**Problem:** The `serverErrors` channel has buffer size 1, but two goroutines (gRPC server and HTTP server) write to it. If both fail simultaneously, one goroutine will block forever.

**File:** `services/financial-accounting/cmd/main.go:297`

```go
// Current (WRONG)
serverErrors := make(chan error, 1)

// Fixed
serverErrors := make(chan error, 2)
```

**Risk:** Service hangs on dual startup failure, requiring manual restart.

**Acceptance Criteria:**
- [ ] Channel buffer size matches number of writing goroutines
- [ ] Audit all services for similar patterns
- [ ] Add comment documenting buffer size rationale

**Testing Strategy:**
```go
func TestServerErrorChannelDoesNotDeadlock(t *testing.T) {
    serverErrors := make(chan error, 2) // Must match goroutine count

    // Simulate both servers failing simultaneously
    go func() { serverErrors <- errors.New("grpc failed") }()
    go func() { serverErrors <- errors.New("http failed") }()

    // With correct buffer size, both sends complete without blocking
    timeout := time.After(100 * time.Millisecond)
    for i := 0; i < 2; i++ {
        select {
        case <-serverErrors:
        case <-timeout:
            t.Fatal("deadlock: channel send blocked")
        }
    }
}
```

**Estimated Effort:** 1 hour

---

### Stream 2: Resource Leak Fixes (P1)

#### 2.1 Missing signal.Stop() Cleanup

**Problem:** `signal.Notify()` registers signal handlers but `signal.Stop()` is never called, leaving handlers registered after shutdown.

**Files Affected:**
- `services/payment-order/cmd/main.go:238`
- `services/financial-accounting/cmd/main.go:348`
- `services/position-keeping/cmd/main.go:289`
- `services/gateway/cmd/main.go:83`
- `services/utilization-metering-consumer/cmd/main.go:228`
- `services/audit-worker/main.go:173`
- `services/current-account/cmd/main.go`
- `services/party/cmd/main.go`
- `services/tenant/cmd/main.go`

**Acceptance Criteria:**
- [ ] Add `defer signal.Stop(sigChan)` after `signal.Notify()` in all services
- [ ] Create shared shutdown helper in `shared/platform/bootstrap/`

**Testing Strategy:**
```go
func TestSignalHandlerCleanup(t *testing.T) {
    // Use a subprocess to test signal handling cleanup
    // Or verify with runtime inspection:

    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT)
    defer signal.Stop(sigChan) // This is what we're testing

    // Send signal to self
    syscall.Kill(syscall.Getpid(), syscall.SIGINT)

    select {
    case <-sigChan:
        // Good - signal received
    case <-time.After(100 * time.Millisecond):
        t.Fatal("signal not received")
    }

    // After Stop(), signals should not be delivered to channel
    signal.Stop(sigChan)
    syscall.Kill(syscall.Getpid(), syscall.SIGINT)

    select {
    case <-sigChan:
        t.Fatal("signal received after Stop()")
    case <-time.After(100 * time.Millisecond):
        // Good - signal not delivered
    }
}
```

**Estimated Effort:** 0.5 days

---

#### 2.2 Database Pool Goroutine Leak on Context Cancel

**Problem:** In `CloseWithContext`, when context is cancelled, the method returns immediately but the background goroutine calling `p.Close()` continues running without coordination.

**File:** `shared/platform/db/pool.go:162-178`

```go
go func() {
    done <- p.Close()  // May never complete if context already done
}()
```

**Risk:** Lingering goroutines during interrupted shutdowns.

**Acceptance Criteria:**
- [ ] Background goroutine respects context cancellation
- [ ] Use sync.WaitGroup or channel coordination for cleanup
- [ ] Add test for context cancellation during close

**Testing Strategy:**
```go
import "go.uber.org/goleak"

func TestPoolCloseWithContextCancellation(t *testing.T) {
    defer goleak.VerifyNone(t) // More reliable than runtime.NumGoroutine()

    pool := NewPool(testConfig)

    // Cancel context immediately
    ctx, cancel := context.WithCancel(context.Background())
    cancel()

    err := pool.CloseWithContext(ctx)
    assert.ErrorIs(t, err, context.Canceled)

    // goleak will detect any lingering goroutines at test end
}
```

**Estimated Effort:** 0.5 days

---

#### 2.3 CachedRegistry Non-Idempotent Start

**Problem:** Calling `Start()` multiple times spawns multiple background refresh goroutines, all writing to the same cache map.

**File:** `services/tenant/service/cached_registry.go:76-90`

**Risk:** Duplicate work, increased memory usage, potential race conditions.

**Acceptance Criteria:**
- [ ] Use `sync.Once` to ensure single refresh loop
- [ ] Add `Started()` method to check state
- [ ] Add test for multiple Start() calls

**Testing Strategy:**
```go
func TestCachedRegistryStartIsIdempotent(t *testing.T) {
    registry := NewCachedRegistry(mockSource, 1*time.Second)

    before := runtime.NumGoroutine()

    // Call Start multiple times
    registry.Start()
    registry.Start()
    registry.Start()

    time.Sleep(50 * time.Millisecond)
    after := runtime.NumGoroutine()

    // Should only have ONE additional goroutine, not three
    assert.Equal(t, before+1, after, "multiple refresh goroutines spawned")

    // Verify Started() returns true
    assert.True(t, registry.Started())

    registry.Stop()
}

func TestCachedRegistryStartWithRaceDetector(t *testing.T) {
    // Run with -race flag to detect concurrent map writes
    registry := NewCachedRegistry(mockSource, 10*time.Millisecond)

    var wg sync.WaitGroup
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            registry.Start() // Should be safe to call concurrently
        }()
    }
    wg.Wait()

    registry.Stop()
}

func TestCachedRegistryRefreshActuallyRuns(t *testing.T) {
    callCount := atomic.Int32{}
    mockSource := &MockSource{
        fetchFunc: func() ([]Tenant, error) {
            callCount.Add(1)
            return []Tenant{{ID: "t1"}}, nil
        },
    }

    registry := NewCachedRegistry(mockSource, 50*time.Millisecond)
    registry.Start()

    // Wait for at least 2 refresh cycles
    time.Sleep(120 * time.Millisecond)
    registry.Stop()

    assert.GreaterOrEqual(t, callCount.Load(), int32(2), "refresh should have run multiple times")
}
```

**Estimated Effort:** 0.5 days

---

### Stream 3: Error Visibility (P2)

#### 3.1 HTTP Response Write Errors Ignored

**Problem:** Calls to `w.Write()` in health check endpoints don't check for errors. Network errors or client disconnections are silently ignored.

**Files Affected:**
- `services/financial-accounting/cmd/main.go:313,317,324,328`
- `services/gateway/server.go:124,134,148`

```go
// Current (WRONG)
w.Write([]byte("NOT_SERVING"))

// Fixed (with context for debugging)
if _, err := w.Write([]byte("NOT_SERVING")); err != nil {
    logger.Warn("failed to write health response",
        "error", err,
        "endpoint", r.URL.Path,
        "remote_addr", r.RemoteAddr)
}
```

**Acceptance Criteria:**
- [ ] All `w.Write()` calls check errors
- [ ] Log warnings for write failures (not errors—client disconnect is normal)
- [ ] Audit all HTTP handlers for similar patterns

**Testing Strategy:**
```go
func TestHealthEndpointHandlesWriteError(t *testing.T) {
    // Use a mock ResponseWriter that fails on Write
    mockWriter := &failingResponseWriter{
        ResponseWriter: httptest.NewRecorder(),
        failOnWrite:    true,
    }

    handler := NewHealthHandler(logger)
    req := httptest.NewRequest("GET", "/health", nil)

    // Should not panic, should log warning
    handler.ServeHTTP(mockWriter, req)

    // Verify warning was logged (check logger mock)
    assert.Contains(t, logBuffer.String(), "failed to write health response")
}

type failingResponseWriter struct {
    http.ResponseWriter
    failOnWrite bool
}

func (f *failingResponseWriter) Write(b []byte) (int, error) {
    if f.failOnWrite {
        return 0, errors.New("connection reset by peer")
    }
    return f.ResponseWriter.Write(b)
}
```

**Estimated Effort:** 0.5 days

---

#### 3.2 Hardcoded UUID Validation

**Problem:** Manual string length and character checks instead of proper UUID parsing.

**File:** `services/financial-accounting/cmd/main.go:171-172`

```go
// Current (fragile)
if len(bankCashAccountID) != 36 || bankCashAccountID[8] != '-' {
    return fmt.Errorf(...)
}

// Fixed
if _, err := uuid.Parse(bankCashAccountID); err != nil {
    return fmt.Errorf("%w: %v", ErrBankCashAccountIDInvalid, err)
}
```

**Acceptance Criteria:**
- [ ] Use `uuid.Parse()` for all UUID validation
- [ ] Consistent error messages across services

**Testing Strategy:**
```go
func TestUUIDValidation(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        wantErr bool
    }{
        {"valid uuid", "550e8400-e29b-41d4-a716-446655440000", false},
        {"valid uuid uppercase", "550E8400-E29B-41D4-A716-446655440000", false},
        {"empty string", "", true},
        {"too short", "550e8400-e29b-41d4", true},
        {"invalid chars", "550e8400-e29b-41d4-a716-44665544ZZZZ", true},
        {"no dashes", "550e8400e29b41d4a716446655440000", false}, // Note: uuid.Parse accepts dashless format
        {"wrong dash positions", "550e-8400-e29b-41d4-a716446655440000", true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := uuid.Parse(tt.input)
            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
            }
        })
    }
}
```

**Estimated Effort:** 1 hour

---

### Stream 4: CI/CD Fixes (P0)

#### 4.1 Improve Nightly Test Reporting

**Problem:** Nightly workflow produces 74k lines of logs. With 2727 passed, 9 failed, 38 skipped - it's impossible to identify which 9 tests are failing without scrolling through massive output.

**File:** `.github/workflows/nightly.yml`

**Current State:**
```
=== Test Summary ===
Passed: 2727
Failed: 9
Skipped: 38
```

**Acceptance Criteria:**
- [ ] Failed test names prominently displayed at end of output
- [ ] Use `gotestsum` or `go-test-json` for structured output
- [ ] Generate JUnit XML for GitHub Actions test summary
- [ ] Failed tests listed in workflow step summary (not buried in logs)

**Implementation:**
```yaml
- name: Install gotestsum
  run: go install gotest.tools/gotestsum@latest

- name: Run tests with structured output
  run: |
    gotestsum --format testdox \
      --junitfile test-results.xml \
      --jsonfile test-results.json \
      -- -race -timeout 15m ./...

- name: Upload test results
  uses: actions/upload-artifact@v4
  if: always()
  with:
    name: test-results
    path: |
      test-results.xml
      test-results.json

- name: Test Summary
  uses: test-summary/action@v2
  if: always()
  with:
    paths: test-results.xml

# Also add inline annotations for failed tests in PR diff view
- name: Annotate failures
  if: failure()
  run: |
    # Parse JSON and emit GitHub Actions annotations
    jq -r '.[] | select(.Action == "fail") |
      "::error file=\(.Package | sub("github.com/meridianhub/meridian/"; "")),line=1::\(.Test) failed"' \
      test-results.json || true
```

**Testing Strategy:**
```bash
# Test locally:
go install gotest.tools/gotestsum@latest
gotestsum --format testdox -- -race ./... 2>&1 | tail -50

# Output shows clear pass/fail per test:
# ✓ TestAccountCreate (0.02s)
# ✓ TestAccountUpdate (0.01s)
# ✗ TestFlakyTimeout (0.50s)
#   Error: context deadline exceeded
```

**Estimated Effort:** 2 hours

---

#### 4.2 Fix 9 Failing Nightly Tests

**Problem:** 9 tests fail in nightly run (full suite without `-short` flag). These are likely timing-sensitive or flaky tests.

**Depends On:** 4.1 (need to identify which tests are failing first)

**Acceptance Criteria:**
- [ ] Identify all 9 failing tests from improved reporting
- [ ] Categorize failures (flaky, timeout, race condition, environment)
- [ ] Fix or quarantine each failing test
- [ ] Nightly passes for 3 consecutive nights

**Testing Strategy:**
```bash
# Once we know which tests fail, run them in isolation:
go test -v -race -count=10 ./path/to/package -run TestName

# Check for race conditions:
go test -race -count=100 ./path/to/flaky/package
```

**Estimated Effort:** 0.5-1 day (depends on root causes)

---

## Summary Table

| ID | Work Item | Priority | Effort | Dependencies |
|----|-----------|----------|--------|--------------|
| 1.1 | Channel Buffer Deadlock | P0 | 1h | None |
| 2.1 | Missing signal.Stop() | P1 | 4h | None |
| 2.2 | Pool Goroutine Leak | P1 | 4h | None |
| 2.3 | CachedRegistry Idempotency | P1 | 4h | None |
| 3.1 | HTTP Write Error Handling | P2 | 4h | None |
| 3.2 | UUID Validation | P2 | 1h | None |
| 4.1 | Nightly Test Reporting | P0 | 2h | None |
| 4.2 | Fix 9 Failing Tests | P0 | 4-8h | 4.1 |

**Total Estimated Effort:** 24-28 hours (~3-4 days)

---

## Success Metrics

1. **No Deadlocks:** Channel buffer sizes match writer count
2. **Clean Shutdown:** No goroutine leaks detected by race detector
3. **Error Visibility:** All HTTP write operations log failures
4. **Idempotent Services:** Multiple Start() calls are safe

---

## Rollout Plan

**Phase 1 (Day 1):** P0 - Fix channel buffer deadlock
**Phase 2 (Day 1-2):** P1 - Resource leak fixes
**Phase 3 (Day 2-3):** P2 - Error visibility improvements

