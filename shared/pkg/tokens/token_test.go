// Package tokens_test provides tests for the tokens package.
package tokens_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/tokens"
)

func TestGenerateToken(t *testing.T) {
	t.Run("returns plaintext and hash", func(t *testing.T) {
		plaintext, hash, err := tokens.GenerateToken(tokens.InvitationTokenLength)
		require.NoError(t, err)
		assert.NotEmpty(t, plaintext)
		assert.NotEmpty(t, hash)
	})

	t.Run("plaintext length matches requested bytes encoded as base64url", func(t *testing.T) {
		plaintext, _, err := tokens.GenerateToken(tokens.InvitationTokenLength)
		require.NoError(t, err)
		// base64url RawEncoding: ceil(n * 4/3), no padding
		// 32 bytes -> 43 chars
		assert.GreaterOrEqual(t, len(plaintext), 40)
	})

	t.Run("plaintext contains only URL-safe characters", func(t *testing.T) {
		plaintext, _, err := tokens.GenerateToken(tokens.InvitationTokenLength)
		require.NoError(t, err)
		for _, c := range plaintext {
			assert.True(t, isURLSafeChar(c), "unexpected char: %c", c)
		}
	})

	t.Run("hash matches HashToken of plaintext", func(t *testing.T) {
		plaintext, hash, err := tokens.GenerateToken(tokens.InvitationTokenLength)
		require.NoError(t, err)
		expected := tokens.HashToken(plaintext)
		assert.Equal(t, expected, hash)
	})

	t.Run("returns error for zero length", func(t *testing.T) {
		_, _, err := tokens.GenerateToken(0)
		assert.Error(t, err)
	})

	t.Run("returns error for negative length", func(t *testing.T) {
		_, _, err := tokens.GenerateToken(-1)
		assert.Error(t, err)
	})

	t.Run("generates unique tokens", func(t *testing.T) {
		const count = 10000
		seen := make(map[string]struct{}, count)
		for i := 0; i < count; i++ {
			plaintext, _, err := tokens.GenerateToken(tokens.InvitationTokenLength)
			require.NoError(t, err)
			_, exists := seen[plaintext]
			assert.False(t, exists, "duplicate token at iteration %d", i)
			seen[plaintext] = struct{}{}
		}
	})
}

func TestHashToken(t *testing.T) {
	t.Run("returns non-empty hex string", func(t *testing.T) {
		hash := tokens.HashToken("some-plaintext-token")
		assert.NotEmpty(t, hash)
		assert.True(t, isHex(hash), "hash should be hex: %s", hash)
	})

	t.Run("SHA256 produces 64-char hex string", func(t *testing.T) {
		hash := tokens.HashToken("some-plaintext-token")
		assert.Len(t, hash, 64)
	})

	t.Run("is deterministic", func(t *testing.T) {
		plaintext := "deterministic-token-value"
		hash1 := tokens.HashToken(plaintext)
		hash2 := tokens.HashToken(plaintext)
		assert.Equal(t, hash1, hash2)
	})

	t.Run("different inputs produce different hashes", func(t *testing.T) {
		hash1 := tokens.HashToken("token-a")
		hash2 := tokens.HashToken("token-b")
		assert.NotEqual(t, hash1, hash2)
	})
}

func TestValidateTokenHash(t *testing.T) {
	t.Run("returns true for matching plaintext and hash", func(t *testing.T) {
		plaintext, hash, err := tokens.GenerateToken(tokens.InvitationTokenLength)
		require.NoError(t, err)
		assert.True(t, tokens.ValidateTokenHash(plaintext, hash))
	})

	t.Run("returns false for wrong plaintext", func(t *testing.T) {
		_, hash, err := tokens.GenerateToken(tokens.InvitationTokenLength)
		require.NoError(t, err)
		assert.False(t, tokens.ValidateTokenHash("wrong-plaintext", hash))
	})

	t.Run("returns false for empty plaintext", func(t *testing.T) {
		_, hash, err := tokens.GenerateToken(tokens.InvitationTokenLength)
		require.NoError(t, err)
		assert.False(t, tokens.ValidateTokenHash("", hash))
	})

	t.Run("returns false for empty hash", func(t *testing.T) {
		plaintext, _, err := tokens.GenerateToken(tokens.InvitationTokenLength)
		require.NoError(t, err)
		assert.False(t, tokens.ValidateTokenHash(plaintext, ""))
	})
}

func isURLSafeChar(c rune) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_'
}

func isHex(s string) bool {
	for _, c := range strings.ToLower(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
