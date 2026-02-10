package domain

import (
	"time"

	"github.com/google/uuid"
)

// Dispute is a Business Query (BQ) entity representing a formal dispute
// raised against a detected variance.
type Dispute struct {
	// DisputeID is the unique identifier for this dispute.
	DisputeID uuid.UUID

	// VarianceID references the variance being disputed.
	VarianceID uuid.UUID

	// RunID references the settlement run.
	RunID uuid.UUID

	// AccountID identifies the account.
	AccountID string

	// Status is the current state of the dispute.
	Status DisputeStatus

	// Reason describes why the variance is being disputed.
	Reason string

	// Resolution records how the dispute was resolved.
	Resolution string

	// RaisedBy records who raised the dispute.
	RaisedBy string

	// ResolvedBy records who resolved the dispute.
	ResolvedBy string

	// ResolvedAt records when the dispute was resolved.
	ResolvedAt *time.Time

	// Attributes stores flexible metadata.
	Attributes map[string]string

	// CreatedAt is when this record was created.
	CreatedAt time.Time

	// UpdatedAt is when this record was last updated.
	UpdatedAt time.Time
}

// NewDispute creates a new Dispute with validation.
func NewDispute(
	varianceID uuid.UUID,
	runID uuid.UUID,
	accountID string,
	reason string,
	raisedBy string,
) (*Dispute, error) {
	if varianceID == uuid.Nil {
		return nil, ErrEmptyVarianceID
	}
	if accountID == "" {
		return nil, ErrEmptyAccountID
	}
	if reason == "" {
		return nil, ErrEmptyDisputeReason
	}

	now := time.Now().UTC()
	return &Dispute{
		DisputeID:  uuid.New(),
		VarianceID: varianceID,
		RunID:      runID,
		AccountID:  accountID,
		Status:     DisputeStatusOpen,
		Reason:     reason,
		RaisedBy:   raisedBy,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

// Review transitions the dispute to UNDER_REVIEW.
func (d *Dispute) Review() error {
	if !d.Status.CanTransitionTo(DisputeStatusUnderReview) {
		return ErrInvalidStatusTransition
	}
	d.Status = DisputeStatusUnderReview
	d.UpdatedAt = time.Now().UTC()
	return nil
}

// Escalate transitions the dispute to ESCALATED.
func (d *Dispute) Escalate() error {
	if !d.Status.CanTransitionTo(DisputeStatusEscalated) {
		return ErrInvalidStatusTransition
	}
	d.Status = DisputeStatusEscalated
	d.UpdatedAt = time.Now().UTC()
	return nil
}

// Resolve transitions the dispute to RESOLVED.
func (d *Dispute) Resolve(resolution string, resolvedBy string) error {
	if !d.Status.CanTransitionTo(DisputeStatusResolved) {
		return ErrInvalidStatusTransition
	}
	now := time.Now().UTC()
	d.Status = DisputeStatusResolved
	d.Resolution = resolution
	d.ResolvedBy = resolvedBy
	d.ResolvedAt = &now
	d.UpdatedAt = now
	return nil
}

// Reject transitions the dispute to REJECTED.
func (d *Dispute) Reject(resolution string, rejectedBy string) error {
	if !d.Status.CanTransitionTo(DisputeStatusRejected) {
		return ErrInvalidStatusTransition
	}
	now := time.Now().UTC()
	d.Status = DisputeStatusRejected
	d.Resolution = resolution
	d.ResolvedBy = rejectedBy
	d.ResolvedAt = &now
	d.UpdatedAt = now
	return nil
}
