package domain

import (
	"context"
	"time"
)

// CurrentAccountClient defines the interface for querying fund reservations
// from the Current Account service. This abstraction allows Position Keeping
// to compute Reserve balance without direct coupling to the Current Account
// implementation details.
//
// The interface follows the Dependency Inversion Principle: high-level domain
// logic depends on this interface, which is implemented by infrastructure-level
// adapters (e.g., gRPC client wrapper).
type CurrentAccountClient interface {
	// GetActiveAmountBlocks retrieves all active fund reservations (liens) for
	// an account. These blocks represent funds that are reserved but not yet
	// debited, and should be subtracted from the current balance to compute
	// the available/reserve balance.
	//
	// Returns an empty slice if no active blocks exist.
	// Returns an error if the account is not found or the service is unavailable.
	GetActiveAmountBlocks(ctx context.Context, accountID string) ([]AmountBlock, error)
}

// AmountBlockType categorizes the nature of a fund reservation.
// Different block types may have different implications for balance calculations
// and business rules.
type AmountBlockType string

const (
	// AmountBlockTypePending indicates a temporary hold pending settlement.
	// Example: Payment Order lien awaiting external payment confirmation.
	AmountBlockTypePending AmountBlockType = "PENDING"

	// AmountBlockTypeFinal indicates a permanent reservation.
	// Example: Regulatory hold, court order freeze.
	AmountBlockTypeFinal AmountBlockType = "FINAL"

	// AmountBlockTypeTemporary indicates a short-term hold.
	// Example: Authorization hold for card transactions.
	AmountBlockTypeTemporary AmountBlockType = "TEMPORARY"
)

// AmountBlock represents a fund reservation from the perspective of Position Keeping.
// This domain type abstracts the Current Account lien implementation, allowing
// Position Keeping to query blocked amounts without coupling to lien-specific details.
//
// The Reserve balance = sum of all active AmountBlock.Amount values.
type AmountBlock struct {
	// BlockID is the unique identifier for this block (maps to lien_id in Current Account).
	BlockID string

	// Amount is the reserved amount. Must have the same currency as the account.
	Amount Money

	// BlockType indicates the nature of the reservation (PENDING, FINAL, TEMPORARY).
	BlockType AmountBlockType

	// Purpose describes why the funds are blocked.
	// Example: "Payment Order: PO-12345", "Regulatory hold".
	Purpose string

	// ExpiresAt is when the block expires. Nil means no expiry.
	// Expired blocks should not be included in reserve calculations.
	ExpiresAt *time.Time
}
