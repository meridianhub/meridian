package credentials

// PasswordPolicy defines the rules for password validation.
type PasswordPolicy struct {
	MinLength        int
	RequireUppercase bool
	RequireLowercase bool
	RequireDigit     bool
	HistoryDepth     int
}

// DefaultPasswordPolicy returns the default password policy used by the platform.
func DefaultPasswordPolicy() PasswordPolicy {
	return PasswordPolicy{
		MinLength:        12,
		RequireUppercase: true,
		RequireLowercase: true,
		RequireDigit:     true,
		HistoryDepth:     5,
	}
}

// PasswordHistoryChecker checks whether a password has been used recently.
type PasswordHistoryChecker interface {
	// CheckPasswordHistory returns ErrPasswordInHistory if the plaintext password
	// matches any of the provided bcrypt hashes.
	CheckPasswordHistory(plaintext string, history []string) error
}
