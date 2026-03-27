// Package tokens provides secure token generation, hashing, and validation utilities
// for single-use tokens such as invitations and password resets.
package tokens

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// ErrInvalidLength is returned when a non-positive token length is provided.
var ErrInvalidLength = errors.New("token length must be positive")

// Token length constants for different use cases.
const (
	InvitationTokenLength        = 32
	PasswordResetTokenLength     = 32
	EmailVerificationTokenLength = 32
)

// GenerateToken generates a cryptographically secure random token of the given byte length.
// Returns the URL-safe base64 plaintext (for delivery to the user) and its SHA256 hex hash
// (for storage). The plaintext must never be stored — only the hash is persisted.
func GenerateToken(length int) (plaintext, hash string, err error) {
	if length <= 0 {
		return "", "", ErrInvalidLength
	}

	b := make([]byte, length)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}

	plaintext = base64.RawURLEncoding.EncodeToString(b)
	hash = HashToken(plaintext)
	return plaintext, hash, nil
}

// HashToken returns the SHA256 hex digest of the given plaintext token.
// Use this to produce a storable hash; never store the plaintext.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ValidateTokenHash returns true if the SHA256 hash of plaintext matches the stored hash.
// Uses constant-time comparison to prevent timing attacks.
func ValidateTokenHash(plaintext, hash string) bool {
	if plaintext == "" || hash == "" {
		return false
	}
	computed := HashToken(plaintext)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(hash)) == 1
}
