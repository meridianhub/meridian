// Package domain contains the core business logic for Position Keeping service
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrInvalidUserID is returned when user ID is empty
	ErrInvalidUserID = errors.New("user ID cannot be empty")
	// ErrInvalidAction is returned when action is empty
	ErrInvalidAction = errors.New("action cannot be empty")
)

// AuditTrailEntry captures audit information for compliance.
// It records who performed what action, when, and from where.
type AuditTrailEntry struct {
	AuditID       uuid.UUID
	Timestamp     time.Time
	UserID        string
	Action        string
	Details       string
	IPAddress     string
	SystemContext map[string]string
}

// NewAuditTrailEntry creates a validated AuditTrailEntry for the given user and action.
// If userID is empty it returns ErrInvalidUserID; if action is empty it returns ErrInvalidAction.
// If systemContext is nil it is initialized to an empty map. The returned entry has a new UUID
// in AuditID and the current UTC time in Timestamp, with remaining fields set from the inputs.
func NewAuditTrailEntry(
	userID string,
	action string,
	details string,
	ipAddress string,
	systemContext map[string]string,
) (*AuditTrailEntry, error) {
	if userID == "" {
		return nil, ErrInvalidUserID
	}

	if action == "" {
		return nil, ErrInvalidAction
	}

	if systemContext == nil {
		systemContext = make(map[string]string)
	}

	return &AuditTrailEntry{
		AuditID:       uuid.New(),
		Timestamp:     time.Now().UTC(),
		UserID:        userID,
		Action:        action,
		Details:       details,
		IPAddress:     ipAddress,
		SystemContext: systemContext,
	}, nil
}