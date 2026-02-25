// Package domain provides the core business logic for the Internal Account service.
//
// This package implements the BIAN Internal Account service domain for managing
// non-customer-facing accounts used for internal accounting purposes.
//
// # Account Types
//
// Internal accounts serve specific operational purposes:
//
//   - CLEARING: Settlement and clearing operations between accounts
//   - NOSTRO: Our account at another bank (their books, our money)
//   - VOSTRO: Their account at our bank (our books, their money)
//   - HOLDING: Temporary holding of funds during processing
//   - SUSPENSE: Unidentified or pending transactions awaiting resolution
//   - REVENUE: Revenue/income tracking for internal accounting
//   - EXPENSE: Expense/cost tracking for operational expenses
//   - INVENTORY: Non-cash assets (energy kWh, compute GPU-hours, carbon credits)
//
// # Account Status Lifecycle
//
// Accounts follow a strict state machine:
//
//	ACTIVE -> SUSPENDED (reversible, for temporary holds)
//	SUSPENDED -> ACTIVE (reactivation)
//	ACTIVE -> CLOSED (permanent, terminal state)
//	SUSPENDED -> CLOSED (permanent, terminal state)
//
// # Correspondent Banking
//
// NOSTRO and VOSTRO accounts require [CorrespondentDetails] to track the relationship
// with the correspondent bank, including bank identifiers and external account references.
//
// # Design Decisions
//
// Key architectural decisions implemented in this package:
//
//   - Balance is NOT stored locally: Delegated to Position Keeping service per ADR-0023.
//     This avoids dual-write problems and ensures Position Keeping is the single source
//     of truth for all balances.
//
//   - No DELETE operations: Accounts are managed through status transitions (CLOSED)
//     for audit compliance and regulatory requirements.
//
//   - Multi-asset support: The instrument_code field references Reference Data service,
//     allowing accounts to hold fiat currency (USD, EUR), energy (KWH), compute resources
//     (GPU_HOURS), or any other registered instrument.
//
//   - Immutable value types: [InternalAccount] is an immutable value type.
//     All modification methods return new instances rather than mutating state.
//
// # Key Types
//
//   - [InternalAccount]: The aggregate root representing an internal account
//   - [AccountType]: Enumeration of account types (CLEARING, NOSTRO, etc.)
//   - [AccountStatus]: Enumeration of lifecycle states (ACTIVE, SUSPENDED, CLOSED)
//   - [CorrespondentDetails]: Bank relationship details for NOSTRO/VOSTRO accounts
//   - [Repository]: Interface for persistence operations
//
// # Example Usage
//
// Creating a new clearing account:
//
//	account, err := domain.NewInternalAccount(
//	    "CLR-001",           // accountID
//	    "GBP_CLEARING",      // accountCode
//	    "GBP Clearing Pool", // name
//	    domain.AccountTypeClearing,
//	    "GBP",               // instrumentCode
//	    "CURRENCY",          // dimension
//	)
//
// Creating a nostro account with correspondent details:
//
//	account, _ := domain.NewInternalAccount(
//	    "NOSTRO-USD-001",
//	    "USD_NOSTRO_HSBC",
//	    "USD Nostro at HSBC",
//	    domain.AccountTypeNostro,
//	    "USD",
//	    "CURRENCY",
//	)
//	correspondent := domain.NewCorrespondentDetails(
//	    "HSBC-UK",
//	    "HSBC UK",
//	    "GB12345678",
//	    "HSBCGB2L",
//	    domain.CorrespondentTypeNostro,
//	)
//	account, err = account.UpdateCorrespondent(&correspondent)
package domain
