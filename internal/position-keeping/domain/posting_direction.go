package domain

// PostingDirection represents the direction of a ledger posting.
type PostingDirection string

// Supported posting directions for double-entry accounting.
const (
	PostingDirectionDebit  PostingDirection = "DEBIT"  // Debit posting (increase assets/expenses, decrease liabilities/income)
	PostingDirectionCredit PostingDirection = "CREDIT" // Credit posting (decrease assets/expenses, increase liabilities/income)
)

// IsValid checks if the posting direction is valid.
func (p PostingDirection) IsValid() bool {
	return p == PostingDirectionDebit || p == PostingDirectionCredit
}

// String returns the string representation of the posting direction.
func (p PostingDirection) String() string {
	return string(p)
}

// Opposite returns the opposite posting direction.
func (p PostingDirection) Opposite() PostingDirection {
	if p == PostingDirectionDebit {
		return PostingDirectionCredit
	}
	return PostingDirectionDebit
}

// ParsePostingDirection converts a string to PostingDirection.
// Returns PostingDirectionDebit for unrecognized values.
func ParsePostingDirection(s string) PostingDirection {
	direction := PostingDirection(s)
	if direction.IsValid() {
		return direction
	}
	return PostingDirectionDebit
}
