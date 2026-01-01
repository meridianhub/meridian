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
func TestPoolCloseWithContextCancellation(t *testing.T) {
    pool := NewPool(testConfig)

    // Get baseline goroutine count
    before := runtime.NumGoroutine()

    // Cancel context immediately
    ctx, cancel := context.WithCancel(context.Background())
    cancel()

    err := pool.CloseWithContext(ctx)
    assert.ErrorIs(t, err, context.Canceled)

    // Allow time for any leaked goroutines to show up
    time.Sleep(50 * time.Millisecond)

    after := runtime.NumGoroutine()
    // Should not have leaked goroutines (allow +/- 1 for GC)
    assert.InDelta(t, before, after, 1, "goroutine leak detected")
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

// Fixed
if _, err := w.Write([]byte("NOT_SERVING")); err != nil {
    logger.Warn("failed to write health response", "error", err)
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
        {"no dashes", "550e8400e29b41d4a716446655440000", true}, // uuid.Parse accepts this!
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

#### 4.1 Nightly Workflow Failing for 9 Days

**Problem:** The nightly GitHub Action has been failing consistently. Primary suspect: invalid Go version `1.25.5` specified in workflow (Go 1.25 doesn't exist).

**File:** `.github/workflows/nightly.yml:31,101`

```yaml
# Current (WRONG - Go 1.25 doesn't exist)
go-version: '1.25.5'

# Fixed (use valid version)
go-version: '1.23'
```

**Jobs Affected:**
- `benchmark-comparison` (line 31)
- `slow-integration-tests` (line 101)

**Risk:** No nightly test coverage, benchmark regressions go undetected.

**Acceptance Criteria:**
- [ ] Fix Go version to valid release (1.22.x or 1.23.x)
- [ ] Verify workflow runs successfully
- [ ] Add Go version as workflow input or matrix for easier updates
- [ ] Consider pinning to go.mod version using `go-version-file: 'go.mod'`

**Testing Strategy:**
```yaml
# Use go.mod as source of truth for Go version
- name: Set up Go
  uses: actions/setup-go@v5
  with:
    go-version-file: 'go.mod'  # Automatically uses version from go.mod
    cache: true

# Or test with workflow_dispatch before merging:
# 1. Push fix to branch
# 2. Manually trigger workflow via Actions tab
# 3. Verify all jobs pass
```

**Estimated Effort:** 1 hour

---

## Summary Table

| ID | Work Item | Priority | Effort | Dependencies |
|----|-----------|----------|--------|--------------|
| 1.1 | Channel Buffer Deadlock | P0 | 1h | None |
| 2.1 | Missing signal.Stop() | P1 | 0.5d | None |
| 2.2 | Pool Goroutine Leak | P1 | 0.5d | None |
| 2.3 | CachedRegistry Idempotency | P1 | 0.5d | None |
| 3.1 | HTTP Write Error Handling | P2 | 0.5d | None |
| 3.2 | UUID Validation | P2 | 1h | None |
| 4.1 | Nightly Workflow Fix | P0 | 1h | None |

**Total Estimated Effort:** 2.5-3 days

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

---

## Related Documents

- `docs/prd/tech-debt-remediation-q1-2026.md` - Broader tech debt remediation
