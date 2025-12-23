---
name: skill-testing-standards
description: Testing standards and best practices for Meridian services
triggers:

  - Writing tests
  - Test flakiness or failures
  - Using time.Sleep in tests
  - Async or polling tests
  - Integration tests
  - Database tests
  - Test coverage

instructions: |
  Use await package instead of time.Sleep. Use testcontainers for database tests.
  Apply defensive testing (ADR-0008): test happy paths AND unhappy paths.
  Table-driven tests with rationale field. Use testify assert/require.
---

# Testing Standards

This document defines testing standards for Meridian services.

## Core Principles

1. **No `time.Sleep`** - Use the `await` package for async assertions
2. **Defensive testing** - Test happy paths AND unhappy paths (see ADR-0008)
3. **Table-driven tests** - Use Go table-driven patterns with rationale
4. **Isolation** - Tests must not depend on each other
5. **Fast feedback** - Unit tests should complete in milliseconds

## Async Testing: Use `await` Not `time.Sleep`

**NEVER use `time.Sleep` in tests.** It creates flaky tests - too short causes failures, too long wastes CI time.

Use `shared/platform/await` instead. It polls until conditions are met or timeout.

```go
import "github.com/meridianhub/meridian/shared/platform/await"

// BAD - arbitrary sleep, flaky and slow
time.Sleep(2 * time.Second)
assert.Equal(t, "COMPLETED", order.Status)

// GOOD - polls until condition met or timeout
err := await.Until(func() bool {
    return order.Status == "COMPLETED"
})
require.NoError(t, err)

// With custom timeout and poll interval
err := await.New().
    AtMost(5 * time.Second).
    PollInterval(50 * time.Millisecond).
    Until(func() bool {
        return repo.FindByID(ctx, id) != nil
    })

// Wait for an operation to succeed (no error)
err := await.UntilNoError(func() error {
    return client.HealthCheck()
})

// With context cancellation
err := await.New().
    WithContext(ctx).
    AtMost(10 * time.Second).
    Until(condition)
```

**Defaults**: 10s timeout, 100ms poll interval.

For advanced matchers and more expressive assertions, consider `gomega.Eventually()`.

## Database Integration Tests: Use Testcontainers

Use `shared/platform/testdb/` for PostgreSQL integration tests. Each test gets an isolated container.

```go
import "github.com/meridianhub/meridian/internal/<service>/repository/testhelpers"

func TestRepository(t *testing.T) {
    tc := testhelpers.SetupTestContainer(t)
    defer tc.Cleanup(t)  // CRITICAL - always defer cleanup

    // Use tc.Repo for repository operations
    err := tc.Repo.Create(ctx, entity)
    require.NoError(t, err)

    // Or use tc.Pool for direct SQL
    var count int
    err = tc.Pool.QueryRow(ctx, "SELECT COUNT(*) FROM table").Scan(&count)
    require.NoError(t, err)
}
```

See [Testcontainers Usage Guide](../guides/testcontainers-usage.md) for detailed patterns.

## Table-Driven Tests with Rationale

Every test case must include a `rationale` field explaining WHY it matters:

```go
func TestDeposit(t *testing.T) {
    tests := []struct {
        name      string
        amount    int64
        wantErr   bool
        rationale string  // WHY this test case matters
    }{
        // Happy path
        {
            name:      "valid deposit",
            amount:    100,
            wantErr:   false,
            rationale: "Standard valid deposit should succeed",
        },

        // Edge cases
        {
            name:      "minimum valid amount",
            amount:    1,
            wantErr:   false,
            rationale: "Even 1 cent is a valid deposit",
        },

        // Unhappy paths
        {
            name:      "zero amount rejected",
            amount:    0,
            wantErr:   true,
            rationale: "Zero deposits are meaningless operations",
        },
        {
            name:      "negative amount rejected",
            amount:    -100,
            wantErr:   true,
            rationale: "Negative deposits don't make sense (use withdraw)",
        },

        // Defensive: values that shouldn't happen but might
        {
            name:      "overflow detection",
            amount:    math.MaxInt64,
            wantErr:   true,
            rationale: "Must detect arithmetic overflow to prevent corruption",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := account.Deposit(tt.amount)
            if tt.wantErr {
                assert.Error(t, err, tt.rationale)
            } else {
                assert.NoError(t, err, tt.rationale)
            }
        })
    }
}
```

## Defensive Testing Categories

Apply these categories to all tests (see [ADR-0008](../adr/0008-defensive-testing-standards.md)):

### For Numeric Inputs

- Valid values (happy path)
- Zero
- Negative values
- Maximum/minimum boundaries
- Overflow conditions

### For String Inputs

- Valid formatted strings
- Empty strings
- Whitespace-only strings
- Invalid formats
- Very long strings

### For State Transitions

- Valid transitions
- Invalid transitions (must be rejected)
- Idempotent operations
- Concurrent state changes

### For Error Handling

- Every error path has a test
- State is not modified on error
- Resources are cleaned up on error
- Errors wrap correctly (`errors.Is`/`errors.As` work)

## Test Assertions: Use testify

Use `github.com/stretchr/testify` for assertions:

```go
import (
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

// Use require for setup - fails fast
tc := testhelpers.SetupTestContainer(t)
require.NotNil(t, tc.Pool, "database pool required")

// Use assert for verifications - continues on failure
assert.Equal(t, expected, actual)
assert.NoError(t, err)
assert.ErrorIs(t, err, ErrNotFound)
assert.Contains(t, logs, "expected message")
```

**require vs assert**:

- `require` - Stops test immediately on failure (use for setup/preconditions)
- `assert` - Records failure but continues (use for verifications)

## Test Naming

Use descriptive names that explain the scenario:

```go
// GOOD - describes scenario and expected outcome
func TestDeposit_ZeroAmount_ReturnsError(t *testing.T)
func TestFindByID_NonExistent_ReturnsNotFoundError(t *testing.T)
func TestTransfer_InsufficientFunds_RollsBackTransaction(t *testing.T)

// BAD - vague names
func TestDeposit1(t *testing.T)
func TestError(t *testing.T)
func TestIt(t *testing.T)
```

## Parallel Tests

Run independent tests in parallel for faster feedback:

```go
func TestConcurrentOperations(t *testing.T) {
    t.Parallel()  // Mark test as safe for parallel execution

    tc := testhelpers.SetupTestContainer(t)
    defer tc.Cleanup(t)

    // Each parallel test gets its own container
}
```

**When NOT to parallelize**:

- Tests sharing global state
- Tests with fixed port bindings
- Tests modifying environment variables

## Mocking Guidelines

Minimize mocking. Prefer real implementations where practical:

1. **Use testcontainers** for database tests (real PostgreSQL)
2. **Use in-memory implementations** for simple interfaces
3. **Mock only external services** that can't be containerized
4. **Never mock the thing you're testing**

When mocking is necessary, use `github.com/stretchr/testify/mock`:

```go
type MockClient struct {
    mock.Mock
}

func (m *MockClient) Send(ctx context.Context, msg Message) error {
    args := m.Called(ctx, msg)
    return args.Error(0)
}

// In test
client := new(MockClient)
client.On("Send", mock.Anything, expectedMsg).Return(nil)
```

## Test Organization

```text
service/
├── domain/
│   ├── account.go
│   └── account_test.go          # Unit tests (same package)
├── repository/
│   ├── postgres_repository.go
│   ├── postgres_repository_test.go      # Integration tests
│   └── testhelpers/
│       └── container.go         # Shared test setup
└── handler/
    ├── grpc_handler.go
    └── grpc_handler_test.go     # Handler tests
```

## CI Considerations

- Unit tests run on every commit
- Integration tests (testcontainers) run on PR
- Tests must pass before merge
- Flaky tests must be fixed immediately (not skipped)

## Related Documentation

- [ADR-0008: Defensive Testing Standards](../adr/0008-defensive-testing-standards.md) - Testing philosophy
- [Testcontainers Usage Guide](../guides/testcontainers-usage.md) - Database test setup
- [await package](../../shared/platform/await/await.go) - Async polling utility
