package tokens

import "time"

// TTL constants for different token types.
const (
	InvitationTokenTTL    = 72 * time.Hour
	PasswordResetTokenTTL = 1 * time.Hour
)

// TokenWithExpiry pairs a stored token hash with its expiry time.
type TokenWithExpiry struct {
	Hash      string
	ExpiresAt time.Time
}

// IsExpired returns true if the token's expiry time is at or before the current time.
func IsExpired(t TokenWithExpiry) bool {
	return !time.Now().Before(t.ExpiresAt)
}

// TimeUntilExpiry returns the duration remaining until the token expires.
// A negative or zero duration indicates the token has already expired.
func TimeUntilExpiry(t TokenWithExpiry) time.Duration {
	return time.Until(t.ExpiresAt)
}
