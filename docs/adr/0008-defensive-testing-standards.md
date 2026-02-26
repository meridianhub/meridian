---
name: adr-008-defensive-testing-standards
description: Defensive testing framework for happy paths, unhappy paths, edge cases, and negative testing
triggers:

  - Writing tests for new features
  - Testing financial domain logic
  - Validating input handling
  - Testing boundary conditions
  - Reviewing test coverage
  - Ensuring robustness

instructions: |
  Apply defensive testing practices: test happy paths AND unhappy paths.
  Every function must test: valid inputs, invalid inputs, edge cases, and
  values that shouldn't occur. Use boundary value analysis. Test error paths.
  Include rationale field in test cases explaining WHY each scenario matters.
---

# 8. Defensive Testing Standards

Date: 2025-10-31

## Status

Accepted

## Context

Our financial services application handles monetary transactions, account balances, and currency operations. Bugs in
these areas can lead to:

- Financial loss or incorrect balances
- Regulatory compliance violations
- Security vulnerabilities from unvalidated inputs
- Silent data corruption from undetected overflows
- Production incidents from edge cases not caught in testing

Traditional "happy path only" testing is insufficient for financial domain logic. We need a systematic framework for
testing beyond expected inputs.

## Decision

We will adopt **Defensive Testing Standards** as our testing philosophy. All tests must cover:

1. **Happy Path Testing**: Expected behaviour with valid inputs
2. **Unhappy Path Testing**: Graceful failure with invalid inputs
3. **Edge Case Testing**: Boundary conditions (min/max, zero, empty)
4. **Negative Testing**: Values that should never occur but might due to bugs

### Testing Framework

We use established testing methodologies:

- **Boundary Value Analysis**: Test at edges of valid input ranges
- **Error Path Coverage**: Every error return must have a test
- **Defensive Programming Verification**: Validate assumptions don't silently fail
- **Rationale Documentation**: Every test case explains WHY it matters

### Test Case Structure

```go
tests := []struct {
    name      string
    input     InputType
    want      OutputType
    wantErr   bool
    rationale string  // WHY this test case matters
}{
    // Happy path
    {
        name:      "valid input",
        input:     validInput,
        want:      expectedOutput,
        wantErr:   false,
        rationale: "Standard valid use case",
    },

    // Edge cases
    {
        name:      "zero value",
        input:     zeroInput,
        want:      zeroOutput,
        wantErr:   false,
        rationale: "Zero is a valid boundary",
    },
    {
        name:      "maximum value",
        input:     math.MaxInt64,
        want:      expectedMax,
        wantErr:   false,
        rationale: "Test upper boundary",
    },

    // Unhappy paths
    {
        name:      "empty input",
        input:     emptyInput,
        wantErr:   true,
        rationale: "Must reject empty inputs",
    },

    // Negative testing
    {
        name:      "overflow condition",
        input:     overflowInput,
        wantErr:   true,
        rationale: "Must detect arithmetic overflow",
    },
}
```

### Mandatory Test Categories

#### For Financial Operations (Money, Balances, Calculations)

- ✅ Valid amounts (happy path)
- ✅ Zero amounts
- ✅ Negative amounts (debts/credits)
- ✅ Very large amounts (near int64 limits)
- ✅ Overflow conditions
- ✅ Currency mismatches
- ✅ Precision/rounding edge cases

#### For String Inputs (Currency codes, IBANs, IDs)

- ✅ Valid formatted strings
- ✅ Empty strings
- ✅ Whitespace-only strings
- ✅ Invalid formats
- ✅ Very long strings
- ✅ Special characters
- ✅ Case sensitivity

#### For State Transitions (Account status, transaction states)

- ✅ Valid transitions
- ✅ Invalid transitions (must be rejected)
- ✅ Idempotent operations (repeat same transition)
- ✅ Concurrent state changes
- ✅ Rollback on failure

#### For Error Handling

- ✅ Every error path has a test
- ✅ Error messages are descriptive
- ✅ State is not modified on error
- ✅ Resources are cleaned up on error
- ✅ Errors wrap correctly (errors.Is/As work)

## Examples

### Example 1: Money Constructor Defensive Tests

```go
func TestMoney_NewMoney_DefensiveTests(t *testing.T) {
    tests := []struct {
        name      string
        currency  string
        amount    int64
        wantErr   bool
        rationale string
    }{
        // Happy path
        {
            name:      "valid GBP amount",
            currency:  "GBP",
            amount:    100,
            wantErr:   false,
            rationale: "Standard valid input",
        },

        // Edge cases - boundaries
        {
            name:      "zero amount",
            currency:  "GBP",
            amount:    0,
            wantErr:   false,
            rationale: "Zero is a valid monetary value",
        },
        {
            name:      "maximum int64 value",
            currency:  "GBP",
            amount:    math.MaxInt64,
            wantErr:   false,
            rationale: "Test upper boundary",
        },
        {
            name:      "minimum int64 value",
            currency:  "GBP",
            amount:    math.MinInt64,
            wantErr:   false,
            rationale: "Test lower boundary (large debt)",
        },

        // Unhappy paths - invalid inputs
        {
            name:      "empty currency",
            currency:  "",
            amount:    100,
            wantErr:   true,
            rationale: "Currency is required - must fail",
        },
        {
            name:      "whitespace-only currency",
            currency:  "   ",
            amount:    100,
            wantErr:   true,
            rationale: "Whitespace should not be valid",
        },

        // Negative testing - strange values
        {
            name:      "negative amount",
            currency:  "GBP",
            amount:    -100,
            wantErr:   false,
            rationale: "Negative values represent debts/credits",
        },
        {
            name:      "very large negative",
            currency:  "GBP",
            amount:    -999999999999,
            wantErr:   false,
            rationale: "System should handle large debts",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            money, err := NewMoney(tt.currency, tt.amount)

            if tt.wantErr {
                assert.Error(t, err, tt.rationale)
                return
            }

            assert.NoError(t, err, tt.rationale)
            assert.Equal(t, tt.currency, money.Currency())
            assert.Equal(t, tt.amount, money.AmountCents())
        })
    }
}
```

### Example 2: Deposit Operation Defensive Tests

```go
func TestAccount_Deposit_DefensiveTests(t *testing.T) {
    tests := []struct {
        name           string
        initialBalance int64
        depositAmount  int64
        wantErr        bool
        expectedError  error
        rationale      string
    }{
        // Happy path
        {
            name:           "normal deposit",
            initialBalance: 1000,
            depositAmount:  500,
            wantErr:        false,
            rationale:      "Standard valid deposit",
        },

        // Unhappy paths
        {
            name:           "zero deposit",
            initialBalance: 1000,
            depositAmount:  0,
            wantErr:        true,
            expectedError:  ErrInvalidAmount,
            rationale:      "Zero deposits are meaningless",
        },
        {
            name:           "negative deposit",
            initialBalance: 1000,
            depositAmount:  -500,
            wantErr:        true,
            expectedError:  ErrInvalidAmount,
            rationale:      "Negative deposits don't make sense (use withdraw)",
        },

        // Edge cases
        {
            name:           "deposit causing overflow",
            initialBalance: math.MaxInt64 - 100,
            depositAmount:  200,
            wantErr:        true,
            expectedError:  ErrOverflow,
            rationale:      "Must detect arithmetic overflow",
        },
        {
            name:           "very small deposit (1 cent)",
            initialBalance: 1000,
            depositAmount:  1,
            wantErr:        false,
            rationale:      "Even 1 cent is a valid deposit",
        },

        // Defensive: Values that shouldn't happen but might
        {
            name:           "extremely large deposit",
            initialBalance: 0,
            depositAmount:  math.MaxInt64,
            wantErr:        false,
            rationale:      "System should handle large values gracefully",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            account := createTestAccount(tt.initialBalance)
            depositMoney, _ := NewMoney("GBP", tt.depositAmount)

            err := account.Deposit(depositMoney)

            if tt.wantErr {
                assert.Error(t, err, tt.rationale)
                if tt.expectedError != nil {
                    assert.ErrorIs(t, err, tt.expectedError)
                }
                // Verify account wasn't modified on error
                assert.Equal(t, tt.initialBalance, account.Balance().AmountCents(),
                    "balance should not change on failed deposit")
                return
            }

            assert.NoError(t, err, tt.rationale)
            expected := tt.initialBalance + tt.depositAmount
            assert.Equal(t, expected, account.Balance().AmountCents())
        })
    }
}
```

## Red Flags Requiring Defensive Tests

If a function has ANY of these characteristics, it MUST have defensive tests:

1. **Accepts numeric inputs**: Test overflow, underflow, zero, negative, max/min
2. **Accepts string inputs**: Test empty, whitespace, special chars, very long
3. **Returns errors**: Test every error path
4. **Has preconditions**: Test what happens when violated
5. **Modifies state**: Test rollback on failure
6. **Performs calculations**: Test precision, rounding, overflow
7. **Converts between types**: Test loss of precision, range errors
8. **Handles currency/money**: Test mismatches, invalid codes, zero, negative

## Consequences

### Positive Consequences

- **Fewer production bugs**: Edge cases caught before deployment
- **Better error messages**: Error paths are tested and refined
- **Increased confidence**: Comprehensive test coverage reduces anxiety
- **Compliance**: Demonstrates due diligence for financial regulations
- **Documentation**: Rationale field explains business rules
- **Regression prevention**: Strange bugs can't reoccur silently

### Negative Consequences

- **More tests to write**: Defensive testing requires 3-5x more test cases
- **Longer test runs**: More comprehensive testing takes more time
- **More test maintenance**: Changes require updating more tests
- **Learning curve**: Team needs to understand defensive testing patterns

### Risk Mitigation

To address the negative consequences:

- Use table-driven tests to reduce boilerplate
- Implement test helpers for common patterns
- Run critical tests first, comprehensive tests in CI
- Document patterns in this ADR for easy reference
- Consider property-based testing for complex domains

## Compliance

This ADR is **mandatory** for:

- All domain model constructors and methods
- All financial calculations
- All input validation functions
- All state transition logic
- All public API endpoints

This ADR is **recommended** for:

- Internal utility functions
- Adapter layer conversions
- Repository queries
- Service layer orchestration

## See Also

- CONTRIBUTING.md - Testing Standards section
- [Boundary Value Analysis](https://en.wikipedia.org/wiki/Boundary-value_analysis)
- [Negative Testing](https://en.wikipedia.org/wiki/Negative_testing)
- [Defensive Programming](https://en.wikipedia.org/wiki/Defensive_programming)
