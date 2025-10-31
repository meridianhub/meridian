# Immutability Audit Report

**Date**: 2025-10-31
**Auditor**: Claude Code (Terminal 4)
**Scope**: Core domain models in `internal/current-account/domain` and `internal/financial-accounting/domain`

## Executive Summary

The current codebase violates immutability principles extensively. All domain models use mutable patterns with exported fields and pointer receivers for state modification. This audit identifies violations and proposes immutable refactoring.

## Critical Violations

### 1. CurrentAccount Domain (`internal/current-account/domain/account.go`)

**Violations**:
- ❌ **Exported Struct Fields** (lines 39-53): All fields are public, allowing external mutation
- ❌ **Pointer Return from Constructor** (line 56): `NewCurrentAccount` returns `*CurrentAccount`
- ❌ **Mutable Methods**: `Deposit()`, `Withdraw()`, `SetOverdraftLimit()` all use pointer receivers and mutate state
- ❌ **Direct Field Mutation** (lines 103, 107-108, 136, 140-141): Direct assignment to fields

**Example Violation**:
```go
// CURRENT (MUTABLE)
type CurrentAccount struct {
    ID         uuid.UUID       // ❌ Exported
    Balance    Money           // ❌ Exported
    Status     AccountStatus   // ❌ Exported
    // ... more exported fields
}

func (a *CurrentAccount) Deposit(amount Money) error {  // ❌ Pointer receiver
    a.Balance.AmountCents += amount.AmountCents         // ❌ Mutation
    a.UpdatedAt = time.Now()                             // ❌ Mutation
    return nil
}
```

**Recommended Fix**:
```go
// IMMUTABLE VERSION
type CurrentAccount struct {
    id         uuid.UUID       // ✅ Unexported
    balance    Money           // ✅ Unexported
    status     AccountStatus   // ✅ Unexported
    // ... more unexported fields
}

// ✅ Value receiver, returns new instance
func (a CurrentAccount) Deposit(amount Money) (CurrentAccount, error) {
    if amount.AmountCents() <= 0 {
        return CurrentAccount{}, ErrInvalidAmount
    }

    newBalance := a.balance.Add(amount)
    now := time.Now()

    return CurrentAccount{
        id:                a.id,
        balance:           newBalance,
        availableBalance:  calculateAvailableBalance(newBalance, a.overdraftLimit, a.overdraftEnabled),
        status:            a.status,
        balanceUpdatedAt:  now,
        updatedAt:         now,
        version:           a.version + 1,
        // ... copy other fields
    }, nil
}

// ✅ Accessors for unexported fields
func (a CurrentAccount) Balance() Money { return a.balance }
func (a CurrentAccount) Status() AccountStatus { return a.status }
```

### 2. Money Type (`internal/current-account/domain/account.go:32-35`)

**Violations**:
- ❌ **Exported Fields**: `AmountCents`, `Currency` are public
- ❌ **No Constructor**: Direct struct literal construction allowed
- ❌ **No Validation**: Invalid money can be created

**Current**:
```go
type Money struct {
    AmountCents int64   // ❌ Exported
    Currency    string  // ❌ Exported
}
```

**Recommended**:
```go
type Money struct {
    amountCents int64   // ✅ Unexported
    currency    string  // ✅ Unexported
}

func NewMoney(currency string, amountCents int64) (Money, error) {
    if currency == "" {
        return Money{}, errors.New("currency required")
    }
    return Money{
        currency:    currency,
        amountCents: amountCents,
    }, nil
}

// Value receiver returning new instance
func (m Money) Add(other Money) (Money, error) {
    if m.currency != other.currency {
        return Money{}, ErrCurrencyMismatch
    }
    return Money{
        currency:    m.currency,
        amountCents: m.amountCents + other.amountCents,
    }, nil
}

// Accessors
func (m Money) AmountCents() int64 { return m.amountCents }
func (m Money) Currency() string { return m.currency }
```

### 3. LedgerPosting Domain (`internal/financial-accounting/domain/ledger_posting.go`)

**Violations**:
- ❌ **Exported Fields** (lines 26-36): All fields public
- ❌ **Pointer Return** (line 46): Constructor returns `*LedgerPosting`
- ❌ **Mutable Methods**: `Post()`, `Fail()` mutate via pointer receivers (lines 73, 84)

**Current**:
```go
type LedgerPosting struct {
    ID        uuid.UUID         // ❌ Exported
    Amount    Money             // ❌ Exported
    Status    TransactionStatus // ❌ Exported
    // ...
}

func (p *LedgerPosting) Post(result string) error {  // ❌ Pointer receiver
    p.Status = TransactionStatusPosted               // ❌ Mutation
    p.PostingResult = result                         // ❌ Mutation
    return nil
}
```

**Recommended**:
```go
type LedgerPosting struct {
    id        uuid.UUID         // ✅ Unexported
    amount    Money             // ✅ Unexported
    status    TransactionStatus // ✅ Unexported
    // ...
}

func (p LedgerPosting) Post(result string) (LedgerPosting, error) {  // ✅ Value receiver
    if p.status == TransactionStatusPosted {
        return LedgerPosting{}, ErrAlreadyPosted
    }

    return LedgerPosting{
        id:            p.id,
        amount:        p.amount,
        status:        TransactionStatusPosted,
        postingResult: result,
        // ... copy other fields
    }, nil
}

// Accessors
func (p LedgerPosting) ID() uuid.UUID { return p.id }
func (p LedgerPosting) Amount() Money { return p.amount }
func (p LedgerPosting) Status() TransactionStatus { return p.status }
```

### 4. Financial Accounting Money Type (`internal/financial-accounting/domain/money.go`)

**Status**: ✅ **Already Immutable** (Good example!)

```go
type Money struct {
    currency string             // ✅ Unexported
    units    int64              // ✅ Unexported
    nanos    int32              // ✅ Unexported
}

func (m Money) Add(other Money) (Money, error) {  // ✅ Value receiver, returns new
    // ... implementation
}
```

This implementation already follows immutability principles and should be the model for refactoring.

## Repository/Persistence Layer Concerns

The mutable domain models create issues in the persistence layer:

### Current Pattern (Mutable):
```go
func (r *Repository) GetAccount(id string) (*CurrentAccount, error) {
    var acc CurrentAccount
    err := r.db.QueryRow("SELECT ...").Scan(&acc.ID, &acc.Balance, ...)
    return &acc, err  // Returns mutable pointer
}
```

### Immutable Pattern:
```go
func (r *Repository) GetAccount(id string) (CurrentAccount, error) {
    // Builder pattern for database scanning
    var builder accountBuilder
    err := r.db.QueryRow("SELECT ...").Scan(&builder.id, &builder.balance, ...)
    if err != nil {
        return CurrentAccount{}, err
    }
    return builder.Build(), nil  // Returns immutable value
}

type accountBuilder struct {
    id      uuid.UUID
    balance Money
    // ... other fields for scanning
}

func (b accountBuilder) Build() CurrentAccount {
    return CurrentAccount{
        id:      b.id,
        balance: b.balance,
        // ...
    }
}
```

## Impact Assessment

### Breaking Changes
- **Public API**: Methods that returned pointers now return values
- **Method Signatures**: Methods return `(T, error)` instead of `error`
- **Repository Layer**: Must adapt to value-based domain models

### Benefits
- **Thread Safety**: Immutable values are inherently thread-safe
- **Testability**: Easier to test without side effects
- **Reasoning**: Functions are pure, easier to understand
- **No Defensive Copying**: Can share instances safely

## Refactoring Plan

### Phase 1: Core Domain Types (This PR)
1. ✅ Update `CONTRIBUTING.md` with immutability guidelines
2. Refactor `Money` types to be immutable with accessors
3. Refactor `CurrentAccount` domain model
4. Refactor `LedgerPosting` domain model
5. Write comprehensive tests for immutability

### Phase 2: Repository Adapters (Follow-up PR)
1. Update repository interfaces to use values
2. Implement builder patterns for database scanning
3. Update service layers
4. Integration tests

### Phase 3: gRPC/API Boundaries (Follow-up PR)
1. Update adapters between proto and domain
2. Ensure proto → domain conversion creates immutable instances
3. Update all service implementations

## Test Strategy

For each refactored type, write tests that verify:

1. **Immutability**: Original instance unchanged after operations
```go
func TestCurrentAccount_Deposit_DoesNotMutateOriginal(t *testing.T) {
    original := NewCurrentAccount(...)
    originalBalance := original.Balance()

    _, _ = original.Deposit(NewMoney("GBP", 100))

    assert.Equal(t, originalBalance, original.Balance(),
        "original account balance should not be mutated")
}
```

2. **Value Semantics**: Copy behaves correctly
```go
func TestCurrentAccount_CopyIndependence(t *testing.T) {
    acc1 := NewCurrentAccount(...)
    acc2 := acc1  // Copy

    acc2, _ = acc2.Deposit(NewMoney("GBP", 100))

    assert.NotEqual(t, acc1.Balance(), acc2.Balance(),
        "modifying copy should not affect original")
}
```

3. **Constructor Validation**: Invalid states prevented
```go
func TestNewMoney_EmptyCurrency_ReturnsError(t *testing.T) {
    _, err := NewMoney("", 100)
    assert.Error(t, err)
}
```

## Files Requiring Changes

### Immediate (This PR):
- `internal/current-account/domain/account.go` - Full refactor
- `internal/current-account/domain/account_test.go` - Add immutability tests
- `internal/financial-accounting/domain/ledger_posting.go` - Full refactor
- `internal/financial-accounting/domain/ledger_posting_test.go` - Add immutability tests
- Consider unifying Money types (two different implementations exist)

### Follow-up PRs:
- All repository implementations
- All service implementations
- All adapter layers
- Integration tests

## Timeline Estimate

- Documentation updates: ✅ Complete
- Core domain refactoring: ~3-5 story points
- Test additions: ~2-3 story points
- Repository updates: ~5-8 story points (separate PR)

## Conclusion

The codebase requires systematic refactoring to enforce immutability. The financial-accounting Money type demonstrates the target pattern. This audit provides the foundation for red-green-refactor TDD approach to implement these changes.
