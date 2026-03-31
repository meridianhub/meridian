package email

import (
	"context"
	"fmt"
	"time"
)

// CommunicationPreference represents a party's opt-in/out preference for a
// specific channel and correspondence category.
type CommunicationPreference struct {
	TenantID         string
	PartyID          string
	Channel          string
	Category         string // "TRANSACTIONAL", "OPERATIONAL", "MARKETING"
	OptedIn          bool
	ConsentSource    string
	ConsentGrantedAt time.Time
	ConsentText      string
}

// PreferenceRepository reads communication preference state for enforcement.
type PreferenceRepository interface {
	// GetGlobalUnsubscribe returns whether the party has globally unsubscribed.
	// Returns false, nil when no row exists (not unsubscribed).
	GetGlobalUnsubscribe(ctx context.Context, tenantID, partyID string) (bool, error)

	// GetPreference returns the preference for a specific channel+category.
	// Returns nil, nil when no preference row exists (no explicit preference set).
	GetPreference(ctx context.Context, tenantID, partyID, channel, category string) (*CommunicationPreference, error)
}

// Sentinel errors for preference operations.
var (
	ErrPreferenceNotFound = fmt.Errorf("email: preference not found")
)
