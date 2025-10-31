# Contributing to Meridian

Thank you for your interest in contributing to Meridian! This guide will help you get started with development.

## Table of Contents

- [Development Environment Setup](#development-environment-setup)
- [Development Workflow](#development-workflow)
- [Code Standards](#code-standards)
- [Testing](#testing)
- [Pull Request Process](#pull-request-process)
- [Architecture Decisions](#architecture-decisions)

## Development Environment Setup

### Quick Setup

Run the setup verification script to check your environment:

```bash
./scripts/setup-check.sh
```

If tools are missing, install them automatically (macOS/Linux):

```bash
./scripts/install-tools.sh
```

### Manual Setup

#### 1. Core Tools

**Go 1.23+**
```bash
# macOS
brew install go

# Linux
sudo apt-get install golang-go
```

**Make and Git**
```bash
# macOS (pre-installed)
# Linux
sudo apt-get install build-essential git
```

#### 2. Container & Kubernetes

**Docker**
```bash
# macOS
brew install --cask docker

# Linux
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh
```

**Kubernetes Cluster**:
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

**kubectl and Helm**
```bash
brew install kubectl helm
```

**Tilt** (for local development)
```bash
brew install tilt-dev/tap/tilt
```

#### 3. API Development Tools

**buf CLI** (Protocol Buffers)
```bash
brew install bufbuild/buf/buf
```

**protoc** (Protocol Buffer compiler)
```bash
brew install protobuf
```

#### 4. Code Quality Tools

**golangci-lint**
```bash
brew install golangci-lint
```

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

## Development Workflow

### Standard Workflow

1. **Create a feature branch**
   ```bash
   git checkout -b feature/my-feature
   ```

2. **Make changes following code standards**

3. **Run tests and linters**
   ```bash
   make test
   make lint
   ```

4. **Commit changes** (pre-commit hooks will run automatically)
   ```bash
   git add .
   git commit -m "feat: add new feature"
   ```

5. **Push and create PR**
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

See [docs/skills/tilt.md](docs/skills/tilt.md) for detailed Tilt usage.

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

**Immutability First**: Prefer immutable data structures wherever possible. While Go lacks Java's `final` keyword, we enforce immutability through coding patterns and conventions.

#### Immutability Guidelines

1. **Structs Should Be Immutable by Default**
   - Design structs as immutable value types
   - Return new instances rather than modifying existing ones
   - Use constructor functions that return fully initialized structs

   ```go
   // Good: Immutable struct with constructor
   type Money struct {
       currency string
       units    int64
       nanos    int32
   }

   func NewMoney(currency string, units int64, nanos int32) Money {
       return Money{
           currency: currency,
           units:    units,
           nanos:    nanos,
       }
   }

   // Methods return new instances
   func (m Money) Add(other Money) Money {
       // Return new Money instance
       return NewMoney(m.currency, m.units+other.units, m.nanos+other.nanos)
   }

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

2. **Use Value Receivers, Not Pointer Receivers**
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

3. **Avoid Mutable Slices and Maps in Structs**
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

4. **Constructor Functions for Complex Initialization**
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

5. **Functional Transformations Over Mutations**
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

**Test-Driven Development (TDD)**: All production code must be developed using the Red-Green-Refactor cycle.

#### Red-Green-Refactor Methodology

We follow strict TDD practices to ensure code quality, correctness, and maintainability.

**The Cycle:**

1. **Red**: Write a failing test first
   - Define the expected behavior before implementation
   - Test should fail for the right reason (not compile error)
   - Verify the test fails by running it

2. **Green**: Write minimal code to make the test pass
   - Implement just enough to make the test pass
   - Don't worry about elegance yet
   - All tests must pass

3. **Refactor**: Improve code quality without changing behavior
   - Apply immutability principles
   - Remove duplication
   - Improve naming and structure
   - All tests must still pass

**Example TDD Workflow:**

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

2. **One Assertion Focus Per Test**
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

3. **Test Immutability**
   ```go
   func TestMoney_Add_DoesNotMutateOriginal(t *testing.T) {
       m1 := NewMoney("GBP", 100, 0)
       original := m1

       _ = m1.Add(NewMoney("GBP", 50, 0))

       assert.Equal(t, original.Units(), m1.Units(), "original should not be mutated")
   }
   ```

4. **Write Tests Before Fixing Bugs**
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

```
<type>: <description>

[optional body]

[optional footer]
```

**Types:**
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `refactor`: Code refactoring
- `test`: Test additions or changes
- `chore`: Build/tooling changes
- `perf`: Performance improvements

**Examples:**
```
feat: add account reconciliation service

Implements BIAN AccountReconciliation domain with transaction
verification and position checking.

Closes #123
```

```
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
```

### Writing Tests

1. **Unit Tests**: Test individual functions/methods
2. **Integration Tests**: Test component interactions
3. **Table-Driven Tests**: Test multiple scenarios
4. **Test Fixtures**: Use testdata/ for sample data
5. **Mocking**: Use interfaces for external dependencies

### Test Organization

```
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

Be respectful, professional, and collaborative. This is a learning project—questions and mistakes are opportunities for growth.

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
