// Package credentials provides password hashing, validation, and policy enforcement.
package credentials

import "errors"

var (
	// ErrPasswordTooShort indicates the password does not meet the minimum length requirement.
	ErrPasswordTooShort = errors.New("password must be at least 12 characters")

	// ErrPasswordTooWeak indicates the password does not meet complexity requirements.
	ErrPasswordTooWeak = errors.New("password must contain at least one uppercase letter, one lowercase letter, and one digit")

	// ErrPasswordInHistory indicates the password was used recently and cannot be reused.
	ErrPasswordInHistory = errors.New("password has been used recently and cannot be reused")

	// ErrPasswordEmpty indicates that an empty password was provided.
	ErrPasswordEmpty = errors.New("password must not be empty")
)
