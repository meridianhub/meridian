// Package credentials provides password hashing, validation, and policy enforcement.
//
// Passwords are hashed with bcrypt and compared using constant-time comparison
// to prevent timing attacks. The DefaultPasswordPolicy enforces a minimum length
// of 12 characters with uppercase, lowercase, and digit requirements, and tracks
// the last five passwords to prevent reuse.
//
// # Usage
//
//	hash, err := credentials.HashPassword("correct-horse-battery-staple")
//	ok, err  := credentials.VerifyPassword("correct-horse-battery-staple", hash)
package credentials
