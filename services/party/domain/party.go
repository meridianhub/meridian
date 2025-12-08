// Package domain contains the core business logic for party reference data
package domain

import (
	"errors"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Domain errors
var (
	ErrInvalidPartyType         = errors.New("invalid party type")
	ErrInvalidLegalName         = errors.New("invalid legal name: must be non-empty and max 255 characters")
	ErrInvalidDisplayName       = errors.New("invalid display name: max 255 characters")
	ErrInvalidStatusTransition  = errors.New("invalid status transition")
	ErrInvalidExternalReference = errors.New("invalid external reference format")
	ErrExternalReferenceExists  = errors.New("external reference already set")
)

// PartyType represents the type of party (person or organization)
type PartyType string

// Party type constants
const (
	PartyTypePerson       PartyType = "PERSON"
	PartyTypeOrganization PartyType = "ORGANIZATION"
)

// IsValid checks if the party type is valid
func (pt PartyType) IsValid() bool {
	switch pt {
	case PartyTypePerson, PartyTypeOrganization:
		return true
	default:
		return false
	}
}

// PartyStatus represents the lifecycle state of a party
type PartyStatus string

// Party status constants
const (
	PartyStatusActive     PartyStatus = "ACTIVE"
	PartyStatusRestricted PartyStatus = "RESTRICTED"
	PartyStatusTerminated PartyStatus = "TERMINATED"
)

// ExternalReferenceType represents the type of external reference
type ExternalReferenceType string

// External reference type constants
const (
	ExternalReferenceTypeCompaniesHouse ExternalReferenceType = "COMPANIES_HOUSE"
	ExternalReferenceTypeNationalID     ExternalReferenceType = "NATIONAL_ID"
	ExternalReferenceTypeLEI            ExternalReferenceType = "LEI"
	ExternalReferenceTypeTaxID          ExternalReferenceType = "TAX_ID"
)

// Party represents a BIAN Party Reference Data Directory domain model
type Party struct {
	id                    uuid.UUID
	partyType             PartyType
	legalName             string
	displayName           string
	status                PartyStatus
	externalReference     string
	externalReferenceType ExternalReferenceType
	createdAt             time.Time
	updatedAt             time.Time
	version               int64
}

// NewParty creates a new party with the given type and legal name
func NewParty(partyType PartyType, legalName string) (*Party, error) {
	if !partyType.IsValid() {
		return nil, ErrInvalidPartyType
	}

	if err := validateLegalName(legalName); err != nil {
		return nil, err
	}

	now := time.Now()
	return &Party{
		id:        uuid.New(),
		partyType: partyType,
		legalName: legalName,
		status:    PartyStatusActive,
		createdAt: now,
		updatedAt: now,
		version:   1,
	}, nil
}

// ReconstructParty recreates a Party from persistence layer data
// This should only be used by repositories when loading from database
func ReconstructParty(
	id uuid.UUID,
	partyType PartyType,
	legalName string,
	displayName string,
	status PartyStatus,
	externalReference string,
	externalReferenceType ExternalReferenceType,
	createdAt time.Time,
	updatedAt time.Time,
	version int64,
) *Party {
	return &Party{
		id:                    id,
		partyType:             partyType,
		legalName:             legalName,
		displayName:           displayName,
		status:                status,
		externalReference:     externalReference,
		externalReferenceType: externalReferenceType,
		createdAt:             createdAt,
		updatedAt:             updatedAt,
		version:               version,
	}
}

// ID returns the party's unique identifier (immutable after creation)
func (p *Party) ID() uuid.UUID {
	return p.id
}

// PartyType returns the type of party
func (p *Party) PartyType() PartyType {
	return p.partyType
}

// LegalName returns the party's legal name
func (p *Party) LegalName() string {
	return p.legalName
}

// DisplayName returns the party's display name
func (p *Party) DisplayName() string {
	return p.displayName
}

// Status returns the party's current status
func (p *Party) Status() PartyStatus {
	return p.status
}

// ExternalReference returns the external reference identifier
func (p *Party) ExternalReference() string {
	return p.externalReference
}

// ExternalReferenceType returns the type of external reference
func (p *Party) ExternalReferenceType() ExternalReferenceType {
	return p.externalReferenceType
}

// CreatedAt returns when the party was created
func (p *Party) CreatedAt() time.Time {
	return p.createdAt
}

// UpdatedAt returns when the party was last updated
func (p *Party) UpdatedAt() time.Time {
	return p.updatedAt
}

// Version returns the optimistic locking version
func (p *Party) Version() int64 {
	return p.version
}

// SetDisplayName sets the party's display name
func (p *Party) SetDisplayName(displayName string) error {
	if err := validateDisplayName(displayName); err != nil {
		return err
	}

	p.displayName = displayName
	p.updatedAt = time.Now()
	return nil
}

// SetExternalReference sets the external reference if not already set
func (p *Party) SetExternalReference(reference string, refType ExternalReferenceType) error {
	if p.externalReference != "" {
		return ErrExternalReferenceExists
	}

	if err := ValidateExternalReference(reference, refType); err != nil {
		return err
	}

	p.externalReference = reference
	p.externalReferenceType = refType
	p.updatedAt = time.Now()
	return nil
}

// Restrict transitions the party to restricted status
func (p *Party) Restrict() error {
	if p.status == PartyStatusTerminated {
		return ErrInvalidStatusTransition
	}

	p.status = PartyStatusRestricted
	p.updatedAt = time.Now()
	return nil
}

// Terminate transitions the party to terminated status
func (p *Party) Terminate() error {
	// Termination is allowed from any state
	p.status = PartyStatusTerminated
	p.updatedAt = time.Now()
	return nil
}

// Activate restores the party to active status
func (p *Party) Activate() error {
	// Cannot reactivate a terminated party
	if p.status == PartyStatusTerminated {
		return ErrInvalidStatusTransition
	}

	p.status = PartyStatusActive
	p.updatedAt = time.Now()
	return nil
}

// validateLegalName validates the legal name field
func validateLegalName(name string) error {
	if name == "" || utf8.RuneCountInString(name) > 255 {
		return ErrInvalidLegalName
	}
	return nil
}

// validateDisplayName validates the display name field
func validateDisplayName(name string) error {
	if utf8.RuneCountInString(name) > 255 {
		return ErrInvalidDisplayName
	}
	return nil
}

// External reference format validators
var (
	// Companies House number: 8 characters (may have leading letters for special types)
	companiesHouseRegex = regexp.MustCompile(`^[A-Z]{0,2}\d{6,8}$`)

	// LEI: 20 alphanumeric characters
	leiRegex = regexp.MustCompile(`^[A-Z0-9]{20}$`)

	// National ID: varies by country, using a general alphanumeric pattern
	nationalIDRegex = regexp.MustCompile(`^[A-Z0-9]{5,20}$`)

	// Tax ID: varies by country, using a general alphanumeric pattern
	taxIDRegex = regexp.MustCompile(`^[A-Z0-9]{5,20}$`)
)

// ValidateExternalReference validates the format of an external reference
func ValidateExternalReference(reference string, refType ExternalReferenceType) error {
	if reference == "" {
		return ErrInvalidExternalReference
	}

	var valid bool
	switch refType {
	case ExternalReferenceTypeCompaniesHouse:
		valid = companiesHouseRegex.MatchString(reference)
	case ExternalReferenceTypeLEI:
		valid = leiRegex.MatchString(reference)
	case ExternalReferenceTypeNationalID:
		valid = nationalIDRegex.MatchString(reference)
	case ExternalReferenceTypeTaxID:
		valid = taxIDRegex.MatchString(reference)
	default:
		return ErrInvalidExternalReference
	}

	if !valid {
		return ErrInvalidExternalReference
	}

	return nil
}
