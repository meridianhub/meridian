// Package tokens provides secure token generation, hashing, and validation utilities
// for single-use tokens such as invitations and password resets.
//
// Tokens are generated using crypto/rand, delivered in URL-safe base64, and stored
// only as their SHA-256 hex hash — the plaintext is never persisted. Comparison uses
// constant-time equality to prevent timing attacks.
//
// # Usage
//
//	plaintext, hash, err := tokens.GenerateToken(tokens.InvitationTokenLength)
//	// deliver plaintext to the user, store hash in the database
//
//	ok := tokens.ValidateToken(userSuppliedToken, storedHash)
package tokens
