# Contributing to Meridian

Thank you for your interest in contributing to Meridian! This guide will help you get started with development.

## Contributor License Agreement

All contributions to this project require agreement to our [Contributor License Agreement](CLA.md). By submitting a
pull request, you confirm that you have read and agree to the terms of the CLA, which assigns copyright and
intellectual property rights to the copyright holder.

## Table of Contents

- [Contributor License Agreement](#contributor-license-agreement)
- [Development Environment Setup](#development-environment-setup)
- [Development Workflow](#development-workflow)
- [Code Standards](#code-standards)
- [Testing](#testing)
- [Pull Request Process](#pull-request-process)
- [Architecture Decisions](#architecture-decisions)

## Development Environment Setup

### Quick Setup (Recommended)

**⚡ The fastest way to get started** is using our unified doctor script:

1. **Check your environment** - See what's working and what needs fixing:

   ```bash
   ./scripts/doctor.sh
   ```

2. **Automatically fix all issues** - Install missing tools and configure everything:

   ```bash
   ./scripts/doctor.sh --fix
   ```

3. **Install Go dependencies**:

   ```bash
   go mod download      # Install Go dependencies
   ```

The doctor script will automatically set up:

- Go, Docker, Kubernetes tools (kubectl, helm, kind, tilt)
- API development tools (buf, protoc)
- Code quality tools (golangci-lint, markdownlint-cli2)
- Node.js dependencies (npm install)
- Git hooks (pre-commit)
- Local Kubernetes cluster (kind-meridian-local)

**Note**: The script is idempotent - safe to run multiple times. It will skip what's already working and fix what's broken.

### Manual Setup (Alternative)

**Note**: For faster setup, use the [automated scripts above](#quick-setup-recommended). Manual installation is only
needed for custom setups or unsupported platforms.

#### 1. Core Tools

#### Go 1.25+

```bash

# macOS

brew install go

# Linux

sudo apt-get install golang-go
```

#### Make and Git

```bash

# macOS (pre-installed)

# Linux

sudo apt-get install build-essential git
```

#### 2. Container & Kubernetes

#### Docker

```bash

# macOS

brew install --cask docker

# Linux

curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh
```

#### Kubernetes Cluster

```bash

# Option 1: Kind with ctlptl and local registry (Recommended)

brew install kind
brew install tilt-dev/tap/ctlptl
ctlptl create cluster kind --registry=ctlptl-registry --name=kind-meridian-local

# Option 2: Docker Desktop

# Enable Kubernetes in Docker Desktop settings

# Option 3: minikube

brew install minikube
minikube start
```

#### kubectl and Helm

```bash
brew install kubectl helm
```

#### Tilt (for local development)

```bash
brew install tilt-dev/tap/tilt
```

#### 3. API Development Tools

#### buf CLI (Protocol Buffers)

```bash
brew install bufbuild/buf/buf
```

#### protoc (Protocol Buffer compiler)

```bash
brew install protobuf
```

#### 4. Code Quality Tools

#### golangci-lint

```bash
brew install golangci-lint
```

#### markdownlint-cli2 (Documentation Linting)

**Note**: Node.js and markdownlint-cli2 are automatically installed by `./scripts/doctor.sh --fix` (see [Quick Setup](#quick-setup-recommended)).

For manual installation:

```bash
brew install node      # macOS (if not already installed)
npm install           # Install markdownlint-cli2 from package.json
```

**Important**: Run `npm install` once after cloning the repository to install markdownlint-cli2 locally. This ensures
optimal performance during pre-commit checks (without this, the hook will use `npx` which downloads the package on each
commit).

**Version strategy**: We use `markdownlint-cli2 ^0.19.0` to stay current with the latest features and fixes. While
CodeRabbit uses 0.18.1, the markdown rules are stable and compatible across minor versions.

Markdown linting is enforced via:

- **Pre-commit hooks**: Automatically validates staged `.md` files before commit
- **GitHub Actions**: CI pipeline validates all markdown files in PRs

**Configuration**: `.markdownlint-cli2.jsonc` defines the linting rules (120 char line length, inline HTML allowed, etc.)

**Manual usage**:

```bash
npm run lint:md        # Check all markdown files
npm run lint:md:fix    # Auto-fix markdown issues
```

The pre-commit hook will prevent commits with markdown errors and suggest fixes.

#### 5. Project Setup

```bash

# Clone repository

git clone git@github.com:meridianhub/meridian.git
cd meridian

# Install Go dependencies

go mod download

# Install git hooks

.githooks/install.sh

# Generate protobuf code

make proto

# Run tests to verify setup

make test
```

The git hooks installed by `.githooks/install.sh` run secret scanning and markdown linting on every commit;
see [.githooks/README.md](.githooks/README.md) for what each hook enforces and how to skip them when needed.

## Development Workflow

### Standard Workflow

1. **Create a feature branch**

   ```bash
   git checkout -b feature/my-feature
   ```

1. **Make changes following code standards**

1. **Run tests and linters**

   ```bash
   make test
   make lint
   npm run lint:md     # Lint markdown files (also runs in pre-commit hook)
   ```

1. **Commit changes** (pre-commit hooks will run automatically)

   Pre-commit hooks automatically validate:
   - Go code formatting (gofumpt)
   - Go code quality (golangci-lint)
   - Protocol buffers (buf lint)
   - Markdown files (markdownlint-cli2)

   ```bash
   git add .
   git commit -m "feat: add new feature"
   ```

1. **Push and create PR**

   ```bash
   git push origin feature/my-feature
   gh pr create
   ```

### Local Development with Tilt

For rapid iteration with Kubernetes:

```bash

# Start development environment

tilt up

# Edit code - changes hot-reload automatically

# View logs and resources in Tilt UI: http://localhost:10350

# Stop environment

tilt down
```

See [.claude/skills/tilt/SKILL.md](.claude/skills/tilt/SKILL.md) for detailed Tilt usage.

### Working with Protocol Buffers

When modifying API definitions:

```bash

# Lint protobuf files

make proto-lint

# Check for breaking changes

make proto-breaking

# Generate Go code

make proto

# Run tests to verify

make test
```

### Browsing Code Documentation

View Go package documentation locally:

```bash

# Start local documentation server

make docs

# Access at http://localhost:6060/github.com/meridianhub/meridian

# Press Ctrl+C to stop

```

The documentation server provides a local version of pkg.go.dev for browsing:

- All exported types, functions, and methods
- Package-level documentation
- Code examples

See [docs/local-documentation.md](docs/local-documentation.md) for details on writing good documentation comments.

### Make Targets

Common development commands:

```bash
make help          # Show all available targets
make build         # Build the binary
make test          # Run all tests
make test-unit     # Run unit tests only
make test-integration  # Run integration tests
make lint          # Run all linters
make fmt           # Format code
make tidy          # Tidy go.mod
make proto         # Generate protobuf code
make proto-lint    # Lint protobuf files
make docker-build  # Build Docker image
make docs          # Start local documentation server
make clean         # Clean build artifacts
```

## Code Standards

### Go Code Style

- Follow [Effective Go](https://golang.org/doc/effective_go.html)
- Use `gofmt` for formatting (enforced by pre-commit hooks)
- Run `golangci-lint` before committing
- Write clear, self-documenting code
- Add comments for exported types and functions

### Immutability and Functional Programming Principles

#### Immutability First

Prefer immutable data structures wherever possible. While Go lacks Java's `final` keyword, we enforce immutability
through coding patterns and conventions.

#### Immutability Guidelines

1. **Structs Should Be Immutable by Default**
   - Design structs as immutable value types
   - Return new instances rather than modifying existing ones
   - Use constructor functions that return fully initialized structs

   ```go
   // Good: Immutable struct with constructor
   type Money struct {
       currency    string
       amountCents int64
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

   // Methods return new instances
   func (m Money) Add(other Money) (Money, error) {
       if m.currency != other.currency {
           return Money{}, ErrCurrencyMismatch
       }
       return Money{
           currency:    m.currency,
           amountCents: m.amountCents + other.amountCents,
       }, nil
   }

   // Accessors for unexported fields
   func (m Money) AmountCents() int64 { return m.amountCents }
   func (m Money) Currency() string { return m.currency }

   // Bad: Mutable struct with setter methods
   type Money struct {
       Currency string
       Units    int64
       Nanos    int32
   }

   func (m *Money) SetUnits(units int64) {
       m.Units = units  // Mutation!
   }

   ```

1. **Use Value Receivers, Not Pointer Receivers**
   - Use value receivers for immutable types
   - Only use pointer receivers when mutation is explicitly required
   - Exception: Large structs where copying is expensive (document why)

   ```go
   // Good: Value receiver preserves immutability
   func (a Account) WithBalance(newBalance Money) Account {
       return Account{
           id:       a.id,
           balance:  newBalance,
           status:   a.status,
       }
   }

   // Bad: Pointer receiver enables mutation
   func (a *Account) SetBalance(newBalance Money) {
       a.balance = newBalance
   }
   ```

1. **Avoid Mutable Slices and Maps in Structs**
   - Don't expose internal slices/maps directly
   - Return copies of internal collections
   - Accept parameters as values, not pointers to collections

   ```go
   // Good: Defensive copying
   type Transaction struct {
       id       string
       postings []Posting  // unexported
   }

   func (t Transaction) Postings() []Posting {
       // Return a copy
       result := make([]Posting, len(t.postings))
       copy(result, t.postings)
       return result
   }

   // Bad: Exposes internal mutable state
   type Transaction struct {
       ID       string
       Postings []Posting  // Can be modified externally!
   }

   ```

1. **Constructor Functions for Complex Initialization**
   - Use `NewX()` constructors that return fully initialized, valid instances
   - Validate inputs in constructors
   - Return errors for invalid states rather than creating invalid objects

   ```go
   func NewAccount(customerID, currency string) (Account, error) {
       if customerID == "" {
           return Account{}, errors.New("customer ID required")
       }
       return Account{
           id:         uuid.New().String(),
           customerID: customerID,
           currency:   currency,
           balance:    NewMoney(currency, 0, 0),
           status:     AccountStatusPending,
           createdAt:  time.Now(),
       }, nil
   }
   ```

1. **Functional Transformations Over Mutations**
   - Use `map`, `filter`, `reduce` patterns
   - Chain transformations returning new values
   - Avoid loops that mutate shared state

   ```go
   // Good: Functional transformation
   func ApplyFees(postings []Posting, feeRate decimal.Decimal) []Posting {
       result := make([]Posting, len(postings))
       for i, p := range postings {
           result[i] = p.WithAmount(p.Amount().Mul(feeRate))
       }
       return result
   }

   // Bad: Mutation in loop
   func ApplyFees(postings []Posting, feeRate decimal.Decimal) {
       for i := range postings {
           postings[i].Amount = postings[i].Amount.Mul(feeRate)
       }
   }

   ```

#### When Mutation Is Acceptable

- **Performance-critical loops**: Document why with benchmark results
- **Builder patterns**: For complex object construction (but return immutable result)
- **Internal implementation details**: When mutation is hidden behind immutable API
- **Database/persistence layer**: Scanning into structs

```go
// Acceptable: Builder pattern with mutable state during construction
type AccountBuilder struct {
    account Account  // mutable during building
}

func (b *AccountBuilder) WithCustomer(id string) *AccountBuilder {
    b.account.customerID = id
    return b
}

func (b *AccountBuilder) Build() Account {
    // Return immutable copy
    return b.account
}
```

#### Code Review Checklist for Immutability

- [ ] Are struct fields unexported (lowercase)?
- [ ] Do methods use value receivers?
- [ ] Do methods return new instances instead of modifying receivers?
- [ ] Are slices/maps/channels defensively copied?
- [ ] Is mutation justified with a comment?
- [ ] Are there setter methods? (usually a smell)

### Testing Standards

#### Test-Driven Development (TDD)

All production code must be developed using the Red-Green-Refactor cycle.

#### Red-Green-Refactor Methodology

We follow strict TDD practices to ensure code quality, correctness, and maintainability.

#### The Cycle

1. **Red**: Write a failing test first
   - Define the expected behavior before implementation
   - Test should fail for the right reason (not compile error)
   - Verify the test fails by running it

1. **Green**: Write minimal code to make the test pass
   - Implement just enough to make the test pass
   - Don't worry about elegance yet
   - All tests must pass

1. **Refactor**: Improve code quality without changing behavior
   - Apply immutability principles
   - Remove duplication
   - Improve naming and structure
   - All tests must still pass

#### Example TDD Workflow

```go
// Step 1 (RED): Write failing test
func TestMoney_Add_SameCurrency_ReturnsSum(t *testing.T) {
    m1 := NewMoney("GBP", 100, 0)
    m2 := NewMoney("GBP", 50, 0)

    result := m1.Add(m2)

    assert.Equal(t, int64(150), result.Units())
    assert.Equal(t, "GBP", result.Currency())
}
// Run test: FAILS (method doesn't exist)

// Step 2 (GREEN): Minimal implementation
func (m Money) Add(other Money) Money {
    return Money{
        currency: m.currency,
        units:    m.units + other.units,
        nanos:    m.nanos + other.nanos,
    }
}
// Run test: PASSES

// Step 3 (REFACTOR): Improve implementation
func (m Money) Add(other Money) Money {
    if m.currency != other.currency {
        panic("cannot add different currencies") // Will add proper error handling
    }

    totalNanos := m.nanos + other.nanos
    carryUnits := totalNanos / nanosPerUnit

    return Money{
        currency: m.currency,
        units:    m.units + other.units + int64(carryUnits),
        nanos:    totalNanos % nanosPerUnit,
    }
}
// Run test: STILL PASSES
```

#### TDD Best Practices

1. **Write Test Names as Specifications**

   ```go
   // Good: Clear specification of behavior
   func TestAccount_Deposit_PositiveAmount_IncreasesBalance(t *testing.T)
   func TestAccount_Deposit_NegativeAmount_ReturnsError(t *testing.T)

   // Bad: Vague test name
   func TestDeposit(t *testing.T)

   ```

1. **One Assertion Focus Per Test**

   ```go
   // Good: Single focused assertion
   func TestMoney_Add_SameCurrency_ReturnsCorrectUnits(t *testing.T) {
       result := NewMoney("GBP", 100, 0).Add(NewMoney("GBP", 50, 0))
       assert.Equal(t, int64(150), result.Units())
   }

   func TestMoney_Add_SameCurrency_PreservesCurrency(t *testing.T) {
       result := NewMoney("GBP", 100, 0).Add(NewMoney("GBP", 50, 0))
       assert.Equal(t, "GBP", result.Currency())
   }
   ```

1. **Test Immutability**

   ```go
   func TestMoney_Add_DoesNotMutateOriginal(t *testing.T) {
       m1 := NewMoney("GBP", 100, 0)
       original := m1

       _ = m1.Add(NewMoney("GBP", 50, 0))

       assert.Equal(t, original.Units(), m1.Units(), "original should not be mutated")
   }

   ```

1. **Write Tests Before Fixing Bugs**

   ```go
   // 1. Reproduce the bug with a failing test
   func TestAccount_ConcurrentDeposits_MaintainsConsistency(t *testing.T) {
       // Test that currently fails, reproducing the bug
   }

   // 2. Fix the code to make test pass
   // 3. Refactor if needed
   ```

#### Test Organization

- Write table-driven tests for multiple scenarios
- Use meaningful test names: `TestFunctionName_Scenario_ExpectedBehavior`
- Aim for high test coverage (minimum 50%)
- Use `testify/assert` for assertions
- Mock external dependencies
- Write integration tests for critical paths

#### Defensive Testing: Happy Path AND Unhappy Path

#### Principle

Test not only the expected behavior but also how the system handles unexpected, invalid, or malicious inputs.

> 📖 **See [ADR-008: Defensive Testing Standards](docs/adr/0008-defensive-testing-standards.md)** for comprehensive
guidelines, examples, and rationale.

We follow **defensive testing** practices:

1. **Happy Path Testing**: Verify expected behavior with valid inputs
2. **Unhappy Path Testing**: Verify graceful failure with invalid inputs
3. **Edge Case Testing**: Test boundary conditions and extreme values
4. **Negative Testing**: Test with values that should never occur

#### Key Testing Frameworks

- **Boundary Value Analysis**: Test at the edges of valid input ranges
- **Error Path Coverage**: Every error condition must have a test
- **Defensive Programming Verification**: Validate assumptions don't silently fail

#### Examples of Defensive Test Cases

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

        // Edge cases
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

        // Negative testing (invalid inputs)
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
        {
            name:      "invalid currency code",
            currency:  "INVALID",
            amount:    100,
            wantErr:   true,
            rationale: "Only ISO 4217 codes allowed",
        },

        // Strange values that might occur in distributed systems
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

        // Defensive: Values that shouldn't happen but might in bugs
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

#### Rationale Documentation

- Every test case should include a `rationale` field explaining WHY this case matters
- Document assumptions being tested ("assumes negative amounts are debts")
- Note edge cases that caught bugs in other systems
- Reference requirements or specifications when applicable

#### When to Apply Defensive Testing

- ✅ All public APIs and domain model constructors
- ✅ Financial calculations (money, interest, balances)
- ✅ Currency operations (conversion, validation)
- ✅ State transitions (account status changes)
- ✅ Boundary conditions (max/min values)
- ✅ Input validation (empty strings, nulls, special characters)
- ✅ Concurrent operations (race conditions, deadlocks)
- ✅ Network boundaries (malformed data, timeouts)

#### Red Flags Requiring Unhappy Path Tests

- Functions that accept numeric inputs (test overflow, underflow, zero, negative)
- Functions that accept strings (test empty, whitespace, special chars, very long)
- Functions that return errors (test every error path)
- Functions with preconditions (test what happens when violated)
- Functions that modify state (test rollback on failure)

### Example: Table-Driven Test with Immutability Check

```go
func TestAccountService_CreateAccount_ValidInput_ReturnsImmutableAccount(t *testing.T) {
    tests := []struct {
        name    string
        input   AccountInput
        want    Account
        wantErr bool
    }{
        {
            name: "standard checking account",
            input: AccountInput{
                Type: AccountTypeChecking,
                Currency: "GBP",
            },
            want: Account{
                Type: AccountTypeChecking,
                Currency: "GBP",
                Status: AccountStatusActive,
            },
            wantErr: false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            svc := NewAccountService()
            got, err := svc.CreateAccount(context.Background(), tt.input)

            if tt.wantErr {
                assert.Error(t, err)
                return
            }

            assert.NoError(t, err)
            assert.Equal(t, tt.want.Type, got.Type)
            assert.Equal(t, tt.want.Currency, got.Currency)

            // Test immutability: modifying input shouldn't affect result
            tt.input.Type = AccountTypeSavings
            assert.Equal(t, AccountTypeChecking, got.Type, "account should not be affected by input mutation")
        })
    }
}
```

### Protocol Buffer Standards

- Follow [buf style guide](https://buf.build/docs/best-practices/style-guide)
- Use snake_case for field names
- Include detailed comments
- Maintain backward compatibility
- Use appropriate field numbers (1-15 for frequent fields)
- Version packages (v1, v2, etc.)

### Commit Message Format

Use [Conventional Commits](https://www.conventionalcommits.org/):

```text
<type>: <description>

[optional body]

[optional footer]
```

#### Types

- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `refactor`: Code refactoring
- `test`: Test additions or changes
- `chore`: Build/tooling changes
- `perf`: Performance improvements

**Examples:**

```text
feat: add position keeping batch operations

Implements bulk import for transaction logs with validation
and audit trail support.

Closes #123
```

```text
fix: correct double-entry posting logic

Ensure credit and debit postings are atomic and balanced.
```

## Testing

### Running Tests

```bash

# All tests

make test

# Unit tests only

make test-unit

# Integration tests only

make test-integration

# With coverage

make test-coverage

# Specific package

go test ./internal/accounting/...

# Specific test

go test -run TestAccountService_CreateAccount ./internal/...

# Run tests without -short flag (includes timing-sensitive tests)

go test ./...
```

### Timing-Sensitive Tests

Some tests validate time-based behavior (e.g., exponential backoff, jitter, timeouts) and are sensitive to CPU
scheduler variance in CI environments.

**Local Development:**

- Run full test suite without `-short` flag: `go test ./...`
- This includes all timing validation tests

**CI Environment:**

- Tests run with `-short` flag to skip timing-sensitive tests
- Functional correctness is still validated (retry attempts, error handling, context cancellation)

**Before Committing Timing-Sensitive Changes:**

1. Run tests without `-short` flag locally: `go test ./...`
2. Verify timing assertions pass consistently
3. If tests are flaky locally, they will definitely flake in CI

**Writing Timing-Sensitive Tests:**

- Add `testing.Short()` guard at the start:

  ```go
  func TestRetryExponentialBackoff(t *testing.T) {
      if testing.Short() {
          t.Skip("Skipping timing-sensitive test in short mode")
      }
      // ... timing assertions ...
  }

  ```

- Use generous tolerance ranges (±30% or more for CI variance)
- Document why specific tolerance values are chosen
- Test functional behavior separately without timing assertions

### Writing Tests

1. **Unit Tests**: Test individual functions/methods
2. **Integration Tests**: Test component interactions
3. **Table-Driven Tests**: Test multiple scenarios
4. **Test Fixtures**: Use testdata/ for sample data
5. **Mocking**: Use interfaces for external dependencies

### Test Organization

```text
internal/
├── accounting/
│   ├── service.go
│   ├── service_test.go       # Unit tests
│   ├── integration_test.go   # Integration tests
│   └── testdata/             # Test fixtures
│       ├── accounts.json
│       └── transactions.json
```

## Pull Request Process

### Before Creating PR

1. ✓ All tests pass: `make test`
2. ✓ Linters pass: `make lint`
3. ✓ Code formatted: `make fmt`
4. ✓ Proto files updated: `make proto`
5. ✓ Documentation updated if needed
6. ✓ Commits follow conventional format

### PR Guidelines

1. **Title**: Use conventional commit format
2. **Description**: Explain what and why, not how
3. **Reference Issues**: Link related issues
4. **Add Tests**: Include test coverage for changes
5. **Update ADRs**: Document architectural decisions
6. **Keep Focused**: One feature/fix per PR
7. **Respond to Feedback**: Address review comments promptly

### PR Template

```markdown

## Summary

Brief description of changes

## Motivation

Why this change is needed

## Changes

- Change 1
- Change 2

## Testing

How the changes were tested

## Related Issues

Closes #123
```

### Review Process

1. Automated checks run (tests, linting, build)
2. Code review by maintainer
3. Address feedback
4. Approval and merge

Pull requests are also reviewed by an automated Claude reviewer. The conventions it follows -
what it checks for and how it comments - are documented in
[.github/claude-review-instructions.md](.github/claude-review-instructions.md).

## Architecture Decisions

### When to Create an ADR

Create an Architecture Decision Record (ADR) when making decisions about:

- Technology choices (databases, frameworks, tools)
- API design patterns
- Data models and schemas
- Deployment strategies
- Security approaches
- BIAN compliance patterns

### ADR Format

Use [MADR (Markdown Any Decision Records)](https://adr.github.io/madr/):

```markdown

# [Short title]

## Context and Problem Statement

What is the issue we're facing?

## Decision Drivers

- Driver 1
- Driver 2

## Considered Options

- Option 1
- Option 2

## Decision Outcome

Chosen option: "option 1", because [justification]

### Consequences

- Good, because [positive outcome]
- Bad, because [negative outcome]

```

### ADR Location

Place ADRs in `docs/adr/` with numbering:

- `docs/adr/0001-record-architecture-decisions.md`
- `docs/adr/0002-microservices-per-bian-domain.md`
- `docs/adr/0003-database-schema-migrations.md`

## Getting Help

- **Documentation**: Check `docs/` directory
- **Issues**: Browse existing [GitHub issues](https://github.com/meridianhub/meridian/issues)
- **Discussions**: Use [GitHub Discussions](https://github.com/meridianhub/meridian/discussions)
- **Questions**: Ask in PR comments or create an issue

## Code of Conduct

Be respectful, professional, and collaborative. We value diverse perspectives and view questions and feedback as
opportunities for continuous improvement. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) for the full policy.

## License

By contributing, you agree to the [Contributor License Agreement](CLA.md), which assigns copyright to the copyright
holder while your contribution remains publicly available under the Business Source License 1.1.
