package credentials

import (
	"fmt"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

const (
	bcryptCost     = 12
	bcryptMaxBytes = 72
)

// HashPassword hashes the plaintext password using bcrypt with cost factor 12.
// Passwords longer than 72 bytes are truncated to the bcrypt limit before hashing.
// Returns an error if the password is empty or hashing fails.
func HashPassword(plaintext string) (string, error) {
	if plaintext == "" {
		return "", ErrPasswordEmpty
	}
	b := []byte(plaintext)
	if len(b) > bcryptMaxBytes {
		b = b[:bcryptMaxBytes]
	}
	hash, err := bcrypt.GenerateFromPassword(b, bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return string(hash), nil
}

// ValidatePassword performs a timing-safe comparison of the plaintext password
// against the stored bcrypt hash. Returns an error if the passwords do not match
// or if the hash is invalid. Passwords longer than 72 bytes are truncated before
// comparison to match the behavior of HashPassword.
func ValidatePassword(plaintext, hash string) error {
	b := []byte(plaintext)
	if len(b) > bcryptMaxBytes {
		b = b[:bcryptMaxBytes]
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), b); err != nil {
		return fmt.Errorf("invalid credentials: %w", err)
	}
	return nil
}

// ValidatePasswordPolicy checks that the plaintext password meets the platform's
// minimum requirements: at least 12 characters, one uppercase letter, one lowercase
// letter, and one digit. Length is checked first; if the password is too short
// ErrPasswordTooShort is returned before checking complexity.
func ValidatePasswordPolicy(plaintext string) error {
	if len([]rune(plaintext)) < 12 {
		return ErrPasswordTooShort
	}

	var hasUpper, hasLower, hasDigit bool
	for _, r := range plaintext {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}

	if !hasUpper || !hasLower || !hasDigit {
		return ErrPasswordTooWeak
	}
	return nil
}

// CheckPasswordHistory returns ErrPasswordInHistory if the plaintext matches any
// of the provided bcrypt hashes. The caller is responsible for limiting the
// history slice to the desired depth (e.g. last 5 hashes). Passwords longer than
// 72 bytes are truncated before comparison to match the behavior of HashPassword.
func CheckPasswordHistory(plaintext string, history []string) error {
	b := []byte(plaintext)
	if len(b) > bcryptMaxBytes {
		b = b[:bcryptMaxBytes]
	}
	for _, h := range history {
		if bcrypt.CompareHashAndPassword([]byte(h), b) == nil {
			return ErrPasswordInHistory
		}
	}
	return nil
}
