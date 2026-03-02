// Package domain contains the core business logic for identity and access management.
package domain

import "errors"

// Domain errors
var (
	ErrIdentityNotFound            = errors.New("identity not found")
	ErrEmailAlreadyExists          = errors.New("email already exists")
	ErrInvalidStatusTransition     = errors.New("invalid status transition")
	ErrAccountLocked               = errors.New("account is locked")
	ErrInvitationExpired           = errors.New("invitation has expired")
	ErrInvitationAlreadyAccepted   = errors.New("invitation has already been accepted")
	ErrRoleAlreadyRevoked          = errors.New("role assignment has already been revoked")
	ErrInvitationNotFound          = errors.New("invitation not found")
	ErrInvalidEmail                = errors.New("invalid email address")
	ErrPasswordHashEmpty           = errors.New("password hash must not be empty")
	ErrExternalIDPEmpty            = errors.New("external IDP provider and subject must not be empty")
	ErrInvalidRole                 = errors.New("invalid role")
	ErrInsufficientRolePermissions = errors.New("insufficient permissions to grant this role")
)
