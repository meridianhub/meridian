package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// BalanceAssertion is a Business Query (BQ) entity that represents a
// cross-account balance assertion check. It verifies that the sum of
// balances across a set of accounts equals an expected value.
type BalanceAssertion struct {
	// AssertionID is the unique identifier for this assertion.
	AssertionID uuid.UUID

	// RunID references the settlement run (optional, can be standalone).
	RunID *uuid.UUID

	// AccountID identifies the primary account being asserted.
	AccountID string

	// InstrumentCode identifies the asset type being asserted.
	InstrumentCode string

	// Expression is the assertion rule (e.g., a CEL expression).
	Expression string

	// ExpectedBalance is the expected balance value.
	ExpectedBalance decimal.Decimal

	// ActualBalance is the computed actual balance.
	ActualBalance decimal.Decimal

	// Status is the assertion result.
	Status AssertionStatus

	// FailureReason records why the assertion failed (if applicable).
	FailureReason string

	// Attributes stores flexible metadata.
	Attributes map[string]string

	// AssertedAt is when the assertion was evaluated.
	AssertedAt time.Time

	// CreatedAt is when this record was created.
	CreatedAt time.Time
}

// NewBalanceAssertion creates a new BalanceAssertion with validation.
func NewBalanceAssertion(
	runID *uuid.UUID,
	accountID string,
	instrumentCode string,
	expression string,
	expectedBalance decimal.Decimal,
) (*BalanceAssertion, error) {
	if accountID == "" {
		return nil, ErrEmptyAccountID
	}
	if instrumentCode == "" {
		return nil, ErrEmptyInstrumentCode
	}
	if expression == "" {
		return nil, ErrEmptyAssertionExpression
	}

	now := time.Now().UTC()
	return &BalanceAssertion{
		AssertionID:     uuid.New(),
		RunID:           runID,
		AccountID:       accountID,
		InstrumentCode:  instrumentCode,
		Expression:      expression,
		ExpectedBalance: expectedBalance,
		Status:          AssertionStatusPending,
		AssertedAt:      now,
		CreatedAt:       now,
	}, nil
}

// Pass marks the assertion as passed with the actual balance.
func (a *BalanceAssertion) Pass(actualBalance decimal.Decimal) error {
	if !a.Status.CanTransitionTo(AssertionStatusPassed) {
		return ErrInvalidStatusTransition
	}
	a.Status = AssertionStatusPassed
	a.ActualBalance = actualBalance
	return nil
}

// Fail marks the assertion as failed with the actual balance and reason.
func (a *BalanceAssertion) Fail(actualBalance decimal.Decimal, reason string) error {
	if !a.Status.CanTransitionTo(AssertionStatusFailed) {
		return ErrInvalidStatusTransition
	}
	a.Status = AssertionStatusFailed
	a.ActualBalance = actualBalance
	a.FailureReason = reason
	return nil
}
