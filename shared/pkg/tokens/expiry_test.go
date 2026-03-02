// Package tokens_test provides tests for the tokens package.
package tokens_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/meridianhub/meridian/shared/pkg/tokens"
)

func TestIsExpired(t *testing.T) {
	t.Run("returns false for token with future expiry", func(t *testing.T) {
		tok := tokens.TokenWithExpiry{
			Hash:      "somehash",
			ExpiresAt: time.Now().Add(1 * time.Hour),
		}
		assert.False(t, tokens.IsExpired(tok))
	})

	t.Run("returns true for token with past expiry", func(t *testing.T) {
		tok := tokens.TokenWithExpiry{
			Hash:      "somehash",
			ExpiresAt: time.Now().Add(-1 * time.Second),
		}
		assert.True(t, tokens.IsExpired(tok))
	})

	t.Run("returns true for token expiring exactly now (boundary)", func(t *testing.T) {
		tok := tokens.TokenWithExpiry{
			Hash:      "somehash",
			ExpiresAt: time.Now(),
		}
		// At-or-past expiry is expired
		assert.True(t, tokens.IsExpired(tok))
	})
}

func TestTimeUntilExpiry(t *testing.T) {
	t.Run("returns positive duration for future token", func(t *testing.T) {
		tok := tokens.TokenWithExpiry{
			Hash:      "somehash",
			ExpiresAt: time.Now().Add(1 * time.Hour),
		}
		d := tokens.TimeUntilExpiry(tok)
		assert.Greater(t, d, time.Duration(0))
	})

	t.Run("returns zero or negative for expired token", func(t *testing.T) {
		tok := tokens.TokenWithExpiry{
			Hash:      "somehash",
			ExpiresAt: time.Now().Add(-1 * time.Second),
		}
		d := tokens.TimeUntilExpiry(tok)
		assert.LessOrEqual(t, d, time.Duration(0))
	})

	t.Run("is approximately correct", func(t *testing.T) {
		future := time.Now().Add(72 * time.Hour)
		tok := tokens.TokenWithExpiry{
			Hash:      "somehash",
			ExpiresAt: future,
		}
		d := tokens.TimeUntilExpiry(tok)
		// Should be within 1 second of 72 hours
		assert.InDelta(t, (72 * time.Hour).Seconds(), d.Seconds(), 1.0)
	})
}

func TestConstants(t *testing.T) {
	t.Run("InvitationTokenLength is 32", func(t *testing.T) {
		assert.Equal(t, 32, tokens.InvitationTokenLength)
	})

	t.Run("PasswordResetTokenLength is 32", func(t *testing.T) {
		assert.Equal(t, 32, tokens.PasswordResetTokenLength)
	})

	t.Run("InvitationTokenTTL is 72 hours", func(t *testing.T) {
		assert.Equal(t, 72*time.Hour, tokens.InvitationTokenTTL)
	})

	t.Run("PasswordResetTokenTTL is 1 hour", func(t *testing.T) {
		assert.Equal(t, 1*time.Hour, tokens.PasswordResetTokenTTL)
	})
}
