# Local Go Documentation Server

This guide explains how to run a local documentation server for browsing Go package documentation, similar to [pkg.go.dev](https://pkg.go.dev).

## Overview

Go provides a built-in documentation system that generates web-based documentation from code comments. The `pkgsite`
tool creates a local version of pkg.go.dev that lets you browse your project's documentation in a web browser.

## Prerequisites

- Go 1.16 or later
- Internet connection (for initial installation)

## Installation

Install `pkgsite` using Go's package manager:

```bash
go install golang.org/x/pkgsite/cmd/pkgsite@latest
```text

This installs the `pkgsite` binary to `~/go/bin/` (or `$GOPATH/bin/`).

## Running the Documentation Server

### Start the Server

From your project root directory:

```bash

# Using full path (if ~/go/bin is not in PATH)

~/go/bin/pkgsite -open=false -http=:6060

# Or if ~/go/bin is in your PATH

pkgsite -open=false -http=:6060
```text

**Options:**

- `-open=false`: Don't automatically open browser (optional)
- `-http=:6060`: Listen on port 6060 (default is 8080)

### Initial Load Time

The first time you run `pkgsite`, it needs to load and index all packages:

```text
Info: go/packages.Load(["all"]) loaded 999 packages from . in 461ms
Info: Listening on addr http://:6060
```text

This typically takes 5-30 seconds depending on project size.

### Access the Documentation

Once the server is running, open your browser to:

**Main URLs:**

- **Project homepage**: http://localhost:6060/github.com/meridianhub/meridian
- **Server root**: http://localhost:6060/

**Example package URLs:**

- Current Account domain: http://localhost:6060/github.com/meridianhub/meridian/internal/current-account/domain
- Financial Accounting service: http://localhost:6060/github.com/meridianhub/meridian/internal/financial-accounting/service
- Position Keeping repository: http://localhost:6060/github.com/meridianhub/meridian/internal/position-keeping/repository

### Stop the Server

Press `Ctrl+C` in the terminal where pkgsite is running.

## Writing Documentation

Go documentation is generated from comments in your code. Follow these conventions:

### Package Documentation

```go
// Package domain implements the core business logic for current accounts
// following Domain-Driven Design principles with aggregate roots and value objects.
//
// The package provides implementations for:
//   - CurrentAccountFacility (aggregate root)
//   - Financial transactions (deposits, withdrawals, interest)
//   - Account lifecycle management
package domain
```text

**Rules:**

- Must start with `// Package <name>`
- Place at the top of any `.go` file in the package
- Use complete sentences
- Add blank comment line (`//`) for paragraph breaks

### Type Documentation

```go
// CurrentAccountFacility represents a bank current account with overdraft capability.
// It is the aggregate root for all account-related operations and maintains
// consistency boundaries for account state transitions.
//
// The facility tracks:
//   - Account balance and available funds
//   - Transaction history
//   - Overdraft limits and usage
type CurrentAccountFacility struct {
    // ID uniquely identifies this account facility
    ID string

    // Balance represents the current account balance in minor currency units
    Balance int64
}
```text

**Rules:**

- First sentence appears in package index
- Start with the type name
- Use complete sentences
- Document exported fields

### Function Documentation

```go
// Initiate creates a new current account facility with the specified parameters.
// It validates the account holder information and initializes the account with
// zero balance.
//
// Returns an error if:
//   - Customer ID is invalid or empty
//   - Account parameters fail validation
//   - Database constraints are violated
func (f *CurrentAccountFacility) Initiate(customerID string, params AccountParams) error {
    // implementation
}
```text

**Rules:**

- Start with function name
- Explain what the function does (not how)
- Document error conditions
- Document special cases or side effects

### Examples

Add runnable examples that appear in documentation:

```go
// Example_basicDeposit demonstrates creating an account and making a deposit
func Example_basicDeposit() {
    account := NewCurrentAccountFacility("CUST-001")
    err := account.Deposit(Money{Amount: 10000, Currency: "USD"})
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Balance: %d\n", account.Balance)
    // Output: Balance: 10000
}
```text

## Troubleshooting

### Port Already in Use

If port 6060 is already in use, choose a different port:

```bash
pkgsite -http=:8080
```text

### Packages Not Showing

If your packages don't appear:

1. **Check module path**: Ensure `go.mod` has correct module name
2. **Restart server**: Stop and restart pkgsite after significant changes
3. **Check package visibility**: Only exported (capitalized) names appear in docs

### Documentation Not Updating

pkgsite caches documentation. To see changes:

1. Stop the server (Ctrl+C)
2. Make your code changes
3. Restart the server

### License Shows as "UNKNOWN"

<!-- markdownlint-disable-next-line MD013 -->
Ensure your LICENSE file matches a canonical license format. See [this commit](https://github.com/meridianhub/meridian/pull/118) for the fix we applied.

## Alternative: Command-Line Documentation

For quick reference without the web UI, use `go doc`:

```bash

# View package documentation

go doc internal/current-account/domain

# View specific type

go doc internal/current-account/domain.CurrentAccountFacility

# View specific method

go doc internal/current-account/domain.CurrentAccountFacility.Initiate
```text

## Best Practices

1. **Write docs as you code**: Document public APIs immediately
2. **Use examples**: Provide `Example_*` functions for common use cases
3. **Document errors**: Clearly state error conditions
4. **Keep it current**: Update docs when changing APIs
5. **Test examples**: Example functions are compiled and tested

## Additional Resources

- [Effective Go - Commentary](https://go.dev/doc/effective_go#commentary)
- [Go Doc Comments](https://go.dev/doc/comment)
- [pkg.go.dev About](https://pkg.go.dev/about)

## Makefile Target (Optional)

Add to your `Makefile` for convenience:

```makefile
.PHONY: docs
docs: ## Start local documentation server
 @echo "Starting pkgsite on http://localhost:6060"
 @echo "Press Ctrl+C to stop"
 pkgsite -open=false -http=:6060
```text

Then run:

```bash
make docs
```text
