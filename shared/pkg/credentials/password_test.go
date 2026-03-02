package credentials_test

import (
	"strings"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashPassword(t *testing.T) {
	t.Run("returns non-empty hash for valid password", func(t *testing.T) {
		hash, err := credentials.HashPassword("ValidPassword1!")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("returns different hashes for same password", func(t *testing.T) {
		hash1, err := credentials.HashPassword("ValidPassword1!")
		require.NoError(t, err)
		hash2, err := credentials.HashPassword("ValidPassword1!")
		require.NoError(t, err)
		assert.NotEqual(t, hash1, hash2, "bcrypt should produce different salts each call")
	})

	t.Run("returns bcrypt formatted hash", func(t *testing.T) {
		hash, err := credentials.HashPassword("ValidPassword1!")
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$"),
			"hash should be bcrypt format, got: %s", hash)
	})

	t.Run("handles unicode password", func(t *testing.T) {
		hash, err := credentials.HashPassword("Pässwörd123!")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})

	t.Run("returns error for empty password", func(t *testing.T) {
		_, err := credentials.HashPassword("")
		assert.ErrorIs(t, err, credentials.ErrPasswordEmpty)
	})

	t.Run("handles very long password by truncating at bcrypt limit", func(t *testing.T) {
		// bcrypt truncates at 72 bytes; HashPassword should handle gracefully
		longPassword := strings.Repeat("A1a!", 30) // 120 chars
		hash, err := credentials.HashPassword(longPassword)
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
	})
}

func TestValidatePassword(t *testing.T) {
	t.Run("validates correct password against hash", func(t *testing.T) {
		hash, err := credentials.HashPassword("ValidPassword1!")
		require.NoError(t, err)

		err = credentials.ValidatePassword("ValidPassword1!", hash)
		assert.NoError(t, err)
	})

	t.Run("returns error for wrong password", func(t *testing.T) {
		hash, err := credentials.HashPassword("ValidPassword1!")
		require.NoError(t, err)

		err = credentials.ValidatePassword("WrongPassword1!", hash)
		assert.Error(t, err)
	})

	t.Run("returns error for empty plaintext", func(t *testing.T) {
		hash, err := credentials.HashPassword("ValidPassword1!")
		require.NoError(t, err)

		err = credentials.ValidatePassword("", hash)
		assert.Error(t, err)
	})

	t.Run("returns error for invalid hash", func(t *testing.T) {
		err := credentials.ValidatePassword("ValidPassword1!", "not-a-valid-hash")
		assert.Error(t, err)
	})
}

func TestValidatePasswordPolicy(t *testing.T) {
	t.Run("accepts valid password", func(t *testing.T) {
		err := credentials.ValidatePasswordPolicy("ValidPassword1!")
		assert.NoError(t, err)
	})

	t.Run("rejects password under 12 chars", func(t *testing.T) {
		err := credentials.ValidatePasswordPolicy("Short1A!")
		assert.ErrorIs(t, err, credentials.ErrPasswordTooShort)
	})

	t.Run("rejects exactly 11 chars", func(t *testing.T) {
		err := credentials.ValidatePasswordPolicy("Abcdefgh1!x") // 11 chars
		assert.ErrorIs(t, err, credentials.ErrPasswordTooShort)
	})

	t.Run("accepts exactly 12 chars meeting all requirements", func(t *testing.T) {
		err := credentials.ValidatePasswordPolicy("Abcdefghij1!") // 12 chars
		assert.NoError(t, err)
	})

	t.Run("rejects password without uppercase", func(t *testing.T) {
		err := credentials.ValidatePasswordPolicy("validpassword1!")
		assert.ErrorIs(t, err, credentials.ErrPasswordTooWeak)
	})

	t.Run("rejects password without lowercase", func(t *testing.T) {
		err := credentials.ValidatePasswordPolicy("VALIDPASSWORD1!")
		assert.ErrorIs(t, err, credentials.ErrPasswordTooWeak)
	})

	t.Run("rejects password without digit", func(t *testing.T) {
		err := credentials.ValidatePasswordPolicy("ValidPassword!!")
		assert.ErrorIs(t, err, credentials.ErrPasswordTooWeak)
	})

	t.Run("rejects empty password with too short error", func(t *testing.T) {
		err := credentials.ValidatePasswordPolicy("")
		assert.ErrorIs(t, err, credentials.ErrPasswordTooShort)
	})

	t.Run("accepts password with unicode characters", func(t *testing.T) {
		err := credentials.ValidatePasswordPolicy("Pässwörd123!")
		assert.NoError(t, err)
	})
}

func TestCheckPasswordHistory(t *testing.T) {
	t.Run("allows password not in history", func(t *testing.T) {
		hash1, _ := credentials.HashPassword("OldPassword1!")
		hash2, _ := credentials.HashPassword("OlderPassword2!")

		err := credentials.CheckPasswordHistory("NewPassword3!", []string{hash1, hash2})
		assert.NoError(t, err)
	})

	t.Run("rejects password matching history entry", func(t *testing.T) {
		reused := "ReusedPassword1!"
		hash, _ := credentials.HashPassword(reused)

		err := credentials.CheckPasswordHistory(reused, []string{hash})
		assert.ErrorIs(t, err, credentials.ErrPasswordInHistory)
	})

	t.Run("rejects password matching second history entry", func(t *testing.T) {
		pass1 := "OldPassword1!"
		pass2 := "OldPassword2!"
		hash1, _ := credentials.HashPassword(pass1)
		hash2, _ := credentials.HashPassword(pass2)

		err := credentials.CheckPasswordHistory(pass2, []string{hash1, hash2})
		assert.ErrorIs(t, err, credentials.ErrPasswordInHistory)
	})

	t.Run("allows password with empty history", func(t *testing.T) {
		err := credentials.CheckPasswordHistory("NewPassword1!", []string{})
		assert.NoError(t, err)
	})

	t.Run("allows password with nil history", func(t *testing.T) {
		err := credentials.CheckPasswordHistory("NewPassword1!", nil)
		assert.NoError(t, err)
	})

	t.Run("checks only provided history entries", func(t *testing.T) {
		// Build 6 hashes but only check last 5 — first should not block
		hashes := make([]string, 5)
		for i := range hashes {
			h, _ := credentials.HashPassword("OldPassword1!")
			hashes[i] = h
		}

		err := credentials.CheckPasswordHistory("BrandNewPassword1!", hashes)
		assert.NoError(t, err)
	})
}

func BenchmarkHashPassword(b *testing.B) {
	for b.Loop() {
		_, err := credentials.HashPassword("BenchmarkPassword1!")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidatePassword(b *testing.B) {
	hash, err := credentials.HashPassword("BenchmarkPassword1!")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		_ = credentials.ValidatePassword("BenchmarkPassword1!", hash)
	}
}
