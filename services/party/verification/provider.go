// Package verification provides KYC/AML verification capabilities for party onboarding.
package verification

import (
	"context"
	"errors"
	"time"

	"github.com/meridianhub/meridian/services/party/domain"
)

// Domain errors for verification operations
var (
	ErrInvalidRiskScore          = errors.New("invalid risk score: must be between 0.0 and 1.0")
	ErrVerificationNotFound      = errors.New("verification not found")
	ErrVerificationCancelled     = errors.New("verification cancelled")
	ErrInvalidVerificationStatus = errors.New("invalid verification status")
	ErrInvalidSanctionsStatus    = errors.New("invalid sanctions status")
)

// Status represents the status of a verification request
type Status string

// Verification status constants
const (
	StatusPending      Status = "PENDING"
	StatusApproved     Status = "APPROVED"
	StatusRejected     Status = "REJECTED"
	StatusManualReview Status = "MANUAL_REVIEW"
)

// IsValid checks if the verification status is valid
func (vs Status) IsValid() bool {
	switch vs {
	case StatusPending, StatusApproved,
		StatusRejected, StatusManualReview:
		return true
	default:
		return false
	}
}

// SanctionsStatus represents the status of a sanctions screening
type SanctionsStatus string

// Sanctions status constants
const (
	SanctionsStatusClear   SanctionsStatus = "CLEAR"
	SanctionsStatusMatch   SanctionsStatus = "MATCH"
	SanctionsStatusPending SanctionsStatus = "PENDING"
	SanctionsStatusError   SanctionsStatus = "ERROR"
)

// IsValid checks if the sanctions status is valid
func (ss SanctionsStatus) IsValid() bool {
	switch ss {
	case SanctionsStatusClear, SanctionsStatusMatch, SanctionsStatusPending, SanctionsStatusError:
		return true
	default:
		return false
	}
}

// Result represents the outcome of an identity verification check
type Result struct {
	VerificationID string
	Status         Status
	Reason         string
	RiskScore      float64
	CompletedAt    *time.Time
	Metadata       map[string]string
}

// Validate validates the Result fields
func (vr Result) Validate() error {
	if vr.RiskScore < 0.0 || vr.RiskScore > 1.0 {
		return ErrInvalidRiskScore
	}
	if !vr.Status.IsValid() {
		return ErrInvalidVerificationStatus
	}
	return nil
}

// SanctionsMatch represents a potential match found during sanctions screening
type SanctionsMatch struct {
	ListName        string
	MatchedName     string
	MatchConfidence float64
	ListEntryID     string
}

// SanctionsResult represents the outcome of a sanctions screening check
type SanctionsResult struct {
	ScreeningID string
	Status      SanctionsStatus
	Matches     []SanctionsMatch
	ScreenedAt  time.Time
	Metadata    map[string]string
}

// Validate validates the SanctionsResult fields
func (sr SanctionsResult) Validate() error {
	if !sr.Status.IsValid() {
		return ErrInvalidSanctionsStatus
	}
	return nil
}

// Provider defines the interface for KYC/AML verification services.
// Implementations may integrate with external providers like Onfido, Stripe Identity, etc.
type Provider interface {
	// VerifyIdentity initiates an identity verification check for the given party.
	// Returns a Result with a unique VerificationID that can be used
	// to track the verification status.
	VerifyIdentity(ctx context.Context, party *domain.Party) (Result, error)

	// CheckSanctions performs a sanctions screening check against global watchlists.
	// Returns a SanctionsResult indicating whether any matches were found.
	CheckSanctions(ctx context.Context, party *domain.Party) (SanctionsResult, error)

	// GetVerificationStatus retrieves the current status of a verification request.
	// Returns ErrVerificationNotFound if the verification ID does not exist.
	GetVerificationStatus(ctx context.Context, verificationID string) (Result, error)
}
