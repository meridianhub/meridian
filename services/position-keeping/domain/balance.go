package domain

import "time"

// BalanceType represents the type of a financial balance per BIAN Position Keeping specifications.
// Different balance types serve distinct purposes in financial accounting and reporting.
type BalanceType string

// Supported balance types aligned with BIAN Position Keeping service domain.
const (
	// BalanceTypeOpening represents the balance at the start of a period.
	// This is typically set at the beginning of a business day or accounting period
	// and serves as the baseline for all subsequent transactions.
	BalanceTypeOpening BalanceType = "OPENING"

	// BalanceTypeClosing represents the balance at the end of a period.
	// This reflects all posted transactions and becomes the opening balance
	// for the next period.
	BalanceTypeClosing BalanceType = "CLOSING"

	// BalanceTypeCurrent represents the real-time balance including all transactions.
	// This includes both posted and pending transactions, providing the most
	// up-to-date view of the position.
	BalanceTypeCurrent BalanceType = "CURRENT"

	// BalanceTypeAvailable represents the funds available for immediate withdrawal or use.
	// This excludes reserved funds, pending debits, and any holds that may affect
	// the customer's ability to access funds.
	BalanceTypeAvailable BalanceType = "AVAILABLE"

	// BalanceTypeLedger represents the balance of posted transactions only.
	// This excludes pending or uncleared transactions and reflects the
	// official accounting position.
	BalanceTypeLedger BalanceType = "LEDGER"

	// BalanceTypeReserve represents funds that are held or reserved.
	// This includes regulatory reserves, collateral holds, and other
	// encumbrances that reduce available funds.
	BalanceTypeReserve BalanceType = "RESERVE"

	// BalanceTypeFree represents unencumbered funds with no restrictions.
	// This is the amount that can be freely used without any holds,
	// reserves, or pending obligations.
	BalanceTypeFree BalanceType = "FREE"

	// BalanceTypeUnknown represents an unrecognized or invalid balance type.
	// This is used as the default when parsing fails.
	BalanceTypeUnknown BalanceType = "UNKNOWN"
)

// IsValid checks if the balance type is a recognized value.
func (b BalanceType) IsValid() bool {
	switch b {
	case BalanceTypeOpening, BalanceTypeClosing, BalanceTypeCurrent,
		BalanceTypeAvailable, BalanceTypeLedger, BalanceTypeReserve, BalanceTypeFree:
		return true
	case BalanceTypeUnknown:
		return false
	}
	return false
}

// String returns the string representation of the balance type.
func (b BalanceType) String() string {
	return string(b)
}

// ParseBalanceType converts a string to BalanceType.
// Returns BalanceTypeUnknown for unrecognized values.
func ParseBalanceType(s string) BalanceType {
	bt := BalanceType(s)
	if bt.IsValid() {
		return bt
	}
	return BalanceTypeUnknown
}

// Balance represents a financial balance at a specific point in time.
// It captures the type of balance, the monetary amount, and the timestamp
// at which the balance was calculated.
type Balance struct {
	// Type indicates the category of this balance (opening, closing, current, etc.)
	Type BalanceType

	// Amount is the monetary value of this balance.
	Amount Money

	// AsOf is the timestamp when this balance was calculated or effective.
	AsOf time.Time
}
