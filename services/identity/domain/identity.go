package domain

import (
	"regexp"
	"time"

	"github.com/google/uuid"
)

// IdentityStatus represents the lifecycle state of an identity
type IdentityStatus string

// Identity status constants
const (
	IdentityStatusPendingInvite IdentityStatus = "PENDING_INVITE"
	IdentityStatusActive        IdentityStatus = "ACTIVE"
	IdentityStatusSuspended     IdentityStatus = "SUSPENDED"
	IdentityStatusLocked        IdentityStatus = "LOCKED"
)

// maxFailedAttempts is the number of consecutive failed login attempts before lockout.
const maxFailedAttempts = 5

// emailRegex is a basic email validation pattern.
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// Identity represents a platform identity (user account) within a tenant.
//
// The Version field implements optimistic concurrency control to prevent lost updates.
// The persistence layer should use this field in UPDATE statements to detect conflicts.
type Identity struct {
	id             uuid.UUID
	email          string
	status         IdentityStatus
	passwordHash   string
	externalIDP    string
	externalSub    string
	failedAttempts int
	createdAt      time.Time
	updatedAt      time.Time
	version        int64
}

// NewIdentity creates a new identity in PENDING_INVITE status.
func NewIdentity(email string) (*Identity, error) {
	if !emailRegex.MatchString(email) {
		return nil, ErrInvalidEmail
	}

	now := time.Now()
	return &Identity{
		id:        uuid.New(),
		email:     email,
		status:    IdentityStatusPendingInvite,
		createdAt: now,
		updatedAt: now,
		version:   1,
	}, nil
}

// ReconstructIdentity recreates an Identity from persistence layer data.
// This should only be used by repositories when loading from the database.
func ReconstructIdentity(
	id uuid.UUID,
	email string,
	status IdentityStatus,
	passwordHash string,
	externalIDP string,
	externalSub string,
	failedAttempts int,
	createdAt time.Time,
	updatedAt time.Time,
	version int64,
) *Identity {
	return &Identity{
		id:             id,
		email:          email,
		status:         status,
		passwordHash:   passwordHash,
		externalIDP:    externalIDP,
		externalSub:    externalSub,
		failedAttempts: failedAttempts,
		createdAt:      createdAt,
		updatedAt:      updatedAt,
		version:        version,
	}
}

// ID returns the identity's unique identifier.
func (i *Identity) ID() uuid.UUID {
	return i.id
}

// Email returns the identity's email address.
func (i *Identity) Email() string {
	return i.email
}

// Status returns the identity's current lifecycle status.
func (i *Identity) Status() IdentityStatus {
	return i.status
}

// PasswordHash returns the stored bcrypt password hash.
func (i *Identity) PasswordHash() string {
	return i.passwordHash
}

// ExternalIDP returns the external identity provider name (e.g., "google").
func (i *Identity) ExternalIDP() string {
	return i.externalIDP
}

// ExternalSub returns the subject identifier from the external IDP.
func (i *Identity) ExternalSub() string {
	return i.externalSub
}

// FailedAttempts returns the count of consecutive failed login attempts.
func (i *Identity) FailedAttempts() int {
	return i.failedAttempts
}

// CreatedAt returns when the identity was created.
func (i *Identity) CreatedAt() time.Time {
	return i.createdAt
}

// UpdatedAt returns when the identity was last updated.
func (i *Identity) UpdatedAt() time.Time {
	return i.updatedAt
}

// Version returns the optimistic locking version.
func (i *Identity) Version() int64 {
	return i.version
}

// IsLocked returns true when the account is in LOCKED status.
func (i *Identity) IsLocked() bool {
	return i.status == IdentityStatusLocked
}

// Activate transitions the identity to ACTIVE status.
// Valid from PENDING_INVITE or SUSPENDED; invalid from LOCKED.
func (i *Identity) Activate() error {
	switch i.status {
	case IdentityStatusActive:
		return nil
	case IdentityStatusPendingInvite, IdentityStatusSuspended:
		i.status = IdentityStatusActive
		i.updatedAt = time.Now()
		i.version++
		return nil
	case IdentityStatusLocked:
		return ErrInvalidStatusTransition
	default:
		return ErrInvalidStatusTransition
	}
}

// Suspend transitions the identity to SUSPENDED status.
// Valid from ACTIVE; invalid from PENDING_INVITE or LOCKED.
func (i *Identity) Suspend() error {
	switch i.status {
	case IdentityStatusActive:
		i.status = IdentityStatusSuspended
		i.updatedAt = time.Now()
		i.version++
		return nil
	case IdentityStatusPendingInvite, IdentityStatusSuspended, IdentityStatusLocked:
		return ErrInvalidStatusTransition
	default:
		return ErrInvalidStatusTransition
	}
}

// Lock transitions the identity to LOCKED status.
// Valid from ACTIVE or SUSPENDED.
func (i *Identity) Lock() error {
	switch i.status {
	case IdentityStatusActive, IdentityStatusSuspended:
		i.status = IdentityStatusLocked
		i.updatedAt = time.Now()
		i.version++
		return nil
	case IdentityStatusPendingInvite, IdentityStatusLocked:
		return ErrInvalidStatusTransition
	default:
		return ErrInvalidStatusTransition
	}
}

// Unlock transitions the identity from LOCKED back to ACTIVE.
func (i *Identity) Unlock() error {
	if i.status != IdentityStatusLocked {
		return ErrInvalidStatusTransition
	}
	i.status = IdentityStatusActive
	i.failedAttempts = 0
	i.updatedAt = time.Now()
	i.version++
	return nil
}

// RecordLoginAttempt records a login attempt result. On success it resets the
// failed attempts counter. On failure it increments the counter and locks the
// account when the threshold is reached.
// Returns ErrAccountLocked if the identity is already locked, and
// ErrInvalidStatusTransition if the identity is not in ACTIVE status.
func (i *Identity) RecordLoginAttempt(success bool) error {
	switch i.status {
	case IdentityStatusLocked:
		return ErrAccountLocked
	case IdentityStatusPendingInvite, IdentityStatusSuspended:
		return ErrInvalidStatusTransition
	case IdentityStatusActive:
		// valid — proceed below
	default:
		return ErrInvalidStatusTransition
	}

	if success {
		i.failedAttempts = 0
		i.updatedAt = time.Now()
		i.version++
		return nil
	}

	i.failedAttempts++
	i.updatedAt = time.Now()
	i.version++

	if i.failedAttempts >= maxFailedAttempts {
		i.status = IdentityStatusLocked
	}
	return nil
}

// SetPassword stores a pre-computed bcrypt hash on the identity.
// The caller is responsible for hashing the plaintext before calling this method.
func (i *Identity) SetPassword(hash string) error {
	if hash == "" {
		return ErrPasswordHashEmpty
	}
	i.passwordHash = hash
	i.updatedAt = time.Now()
	i.version++
	return nil
}

// SetExternalIDP records the external identity provider and subject identifier.
func (i *Identity) SetExternalIDP(idp, sub string) error {
	if idp == "" || sub == "" {
		return ErrExternalIDPEmpty
	}
	i.externalIDP = idp
	i.externalSub = sub
	i.updatedAt = time.Now()
	i.version++
	return nil
}
