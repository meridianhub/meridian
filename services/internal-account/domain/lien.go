package domain

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Lien domain errors
var (
	ErrLienNotActive                = errors.New("lien is not in active status")
	ErrInvalidLienTransition        = errors.New("invalid lien status transition")
	ErrLienExpired                  = errors.New("lien has expired")
	ErrInvalidLienAmount            = errors.New("lien amount must be positive")
	ErrInvalidLienInstrumentCode    = errors.New("lien instrument code must not be empty")
	ErrInvalidPaymentOrderReference = errors.New("payment order reference must not be empty")
	ErrInvalidInstrumentAmount      = errors.New("instrument amount must be positive with instrument code")
)

// InstrumentAmount represents a quantity of a specific instrument for valuation tracking.
type InstrumentAmount struct {
	Amount         decimal.Decimal `json:"amount"`
	InstrumentCode string          `json:"instrument_code"`
}

// IsZero returns true if the instrument amount has not been set.
func (ia InstrumentAmount) IsZero() bool {
	return ia.Amount.IsZero() && ia.InstrumentCode == ""
}

// LienStatus represents the lifecycle state of a lien.
type LienStatus string

// Lien lifecycle states.
const (
	LienStatusActive     LienStatus = "ACTIVE"
	LienStatusExecuted   LienStatus = "EXECUTED"
	LienStatusTerminated LienStatus = "TERMINATED"
)

// Lien represents a fund reservation on an internal account.
// Liens are used by the Payment Order saga to reserve funds before executing payments.
// Invariant: Available Balance = Current Balance - Sum(Active Liens)
//
// Unlike Current Account liens which use a Money type restricted to known currencies,
// IBA liens store amount_cents + currency directly to support any instrument code
// (GBP, kWh, GPU_HOUR, TONNE_CO2E, etc.).
type Lien struct {
	ID        uuid.UUID
	AccountID uuid.UUID

	// AmountCents is the reserved amount in minor units of the account's native instrument.
	AmountCents int64
	// InstrumentCode is the instrument code of the reserved amount (matches account's native instrument).
	InstrumentCode string

	BucketID              string
	Status                LienStatus
	PaymentOrderReference string
	TerminationReason     string
	ExpiresAt             *time.Time
	Version               int
	CreatedAt             time.Time
	UpdatedAt             time.Time

	// ReservedQuantity stores the original input before valuation (e.g., 100 kWh).
	ReservedQuantity *InstrumentAmount
	// ValuedAmount stores the price-locked valuation result in the account's native instrument.
	// IMMUTABLE once the lien is ACTIVE — cannot be modified to prevent Ghost Pricing.
	ValuedAmount *InstrumentAmount
	// ValuationAnalysis stores the full audit trail of the valuation computation as JSON.
	ValuationAnalysis json.RawMessage
}

// NewLien creates a new lien in ACTIVE status.
func NewLien(accountID uuid.UUID, amountCents int64, instrumentCode, bucketID, paymentOrderReference string, expiresAt *time.Time) (*Lien, error) {
	if amountCents <= 0 {
		return nil, ErrInvalidLienAmount
	}
	if instrumentCode == "" {
		return nil, ErrInvalidLienInstrumentCode
	}
	if paymentOrderReference == "" {
		return nil, ErrInvalidPaymentOrderReference
	}

	now := time.Now()
	return &Lien{
		ID:                    uuid.New(),
		AccountID:             accountID,
		AmountCents:           amountCents,
		InstrumentCode:        instrumentCode,
		BucketID:              bucketID,
		Status:                LienStatusActive,
		PaymentOrderReference: paymentOrderReference,
		ExpiresAt:             expiresAt,
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}, nil
}

// NewValuedLien creates a new lien in ACTIVE status with atomic valuation data (price lock).
// The reservedQuantity is the original input (e.g., 100 kWh), valuedAmount is the
// price-locked conversion (e.g., 35.00 GBP), and analysisJSON is the full audit trail.
func NewValuedLien(
	accountID uuid.UUID,
	amountCents int64,
	instrumentCode, bucketID, paymentOrderReference string,
	expiresAt *time.Time,
	reservedQuantity *InstrumentAmount,
	valuedAmount *InstrumentAmount,
	analysisJSON json.RawMessage,
) (*Lien, error) {
	if reservedQuantity == nil || reservedQuantity.InstrumentCode == "" || reservedQuantity.Amount.Sign() <= 0 {
		return nil, ErrInvalidInstrumentAmount
	}
	if valuedAmount == nil || valuedAmount.InstrumentCode == "" || valuedAmount.Amount.Sign() <= 0 {
		return nil, ErrInvalidInstrumentAmount
	}

	lien, err := NewLien(accountID, amountCents, instrumentCode, bucketID, paymentOrderReference, expiresAt)
	if err != nil {
		return nil, err
	}
	lien.ReservedQuantity = reservedQuantity
	lien.ValuedAmount = valuedAmount
	lien.ValuationAnalysis = analysisJSON
	return lien, nil
}

// HasValuation returns true if this lien was created through atomic valuation.
func (l *Lien) HasValuation() bool {
	return l.ValuedAmount != nil && !l.ValuedAmount.IsZero()
}

// Execute transitions the lien to EXECUTED status (terminal state).
// Idempotent: returns nil if already executed.
// Returns ErrLienExpired if the lien has passed its expiration time.
func (l *Lien) Execute() error {
	if l.Status == LienStatusExecuted {
		return nil
	}
	if l.Status != LienStatusActive {
		return ErrLienNotActive
	}
	if l.IsExpired() {
		return ErrLienExpired
	}

	l.Status = LienStatusExecuted
	l.UpdatedAt = time.Now()
	return nil
}

// Terminate transitions the lien to TERMINATED status (terminal state).
// Idempotent: returns nil if already terminated (preserves original reason).
func (l *Lien) Terminate(reason string) error {
	if l.Status == LienStatusTerminated {
		return nil
	}
	if l.Status != LienStatusActive {
		return ErrLienNotActive
	}

	l.Status = LienStatusTerminated
	l.TerminationReason = reason
	l.UpdatedAt = time.Now()
	return nil
}

// IsActive returns true if the lien is in ACTIVE status.
func (l *Lien) IsActive() bool {
	return l.Status == LienStatusActive
}

// IsTerminal returns true if the lien is in a terminal state (EXECUTED or TERMINATED).
func (l *Lien) IsTerminal() bool {
	return l.Status == LienStatusExecuted || l.Status == LienStatusTerminated
}

// IsExpired returns true if the lien has an expiration time that has passed.
func (l *Lien) IsExpired() bool {
	if l.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*l.ExpiresAt)
}

// CanExecute returns true if the lien can be executed.
func (l *Lien) CanExecute() bool {
	return l.Status == LienStatusActive && !l.IsExpired()
}

// CanTerminate returns true if the lien can be terminated.
func (l *Lien) CanTerminate() bool {
	return l.Status == LienStatusActive
}
