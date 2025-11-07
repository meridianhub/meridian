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

// NewAuditTrailEntry creates a new audit trail entry with validation.
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

	// Clone the map to prevent external mutation of audit data
	clonedContext := make(map[string]string)
	for k, v := range systemContext {
		clonedContext[k] = v
	}

	return &AuditTrailEntry{
		AuditID:       uuid.New(),
		Timestamp:     time.Now().UTC(),
		UserID:        userID,
		Action:        action,
		Details:       details,
		IPAddress:     ipAddress,
		SystemContext: clonedContext,
	}, nil
}
