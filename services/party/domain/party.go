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
	ErrInvalidControlAction     = errors.New("invalid control action")
	ErrCircularAssociation      = errors.New("circular association detected")
	ErrInvalidUpdateField       = errors.New("invalid field for update")
	ErrInvalidRelationshipType  = errors.New("invalid relationship type")
	ErrInvalidPoliticalExposure = errors.New("invalid political exposure status")
	ErrInvalidReferenceType     = errors.New("invalid reference type")
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
	PartyStatusSuspended  PartyStatus = "SUSPENDED"
	PartyStatusTerminated PartyStatus = "TERMINATED"
)

// ControlAction represents an action to control the party lifecycle
type ControlAction string

// Control action constants
const (
	ControlActionRestrict  ControlAction = "RESTRICT"
	ControlActionSuspend   ControlAction = "SUSPEND"
	ControlActionActivate  ControlAction = "ACTIVATE"
	ControlActionTerminate ControlAction = "TERMINATE"
)

// IsValid checks if the control action is valid
func (ca ControlAction) IsValid() bool {
	switch ca {
	case ControlActionRestrict, ControlActionSuspend, ControlActionActivate, ControlActionTerminate:
		return true
	default:
		return false
	}
}

// RelationshipType represents the type of relationship between parties
type RelationshipType string

// Relationship type constants
const (
	RelationshipTypeSpouse               RelationshipType = "SPOUSE"
	RelationshipTypeDependent            RelationshipType = "DEPENDENT"
	RelationshipTypeBusinessPartner      RelationshipType = "BUSINESS_PARTNER"
	RelationshipTypeGuarantor            RelationshipType = "GUARANTOR"
	RelationshipTypeBeneficialOwner      RelationshipType = "BENEFICIAL_OWNER"
	RelationshipTypeSyndicateParticipant RelationshipType = "SYNDICATE_PARTICIPANT"
	RelationshipTypeSyndicateHost        RelationshipType = "SYNDICATE_HOST"
)

// IsValid checks if the relationship type is valid
func (rt RelationshipType) IsValid() bool {
	switch rt {
	case RelationshipTypeSpouse, RelationshipTypeDependent, RelationshipTypeBusinessPartner,
		RelationshipTypeGuarantor, RelationshipTypeBeneficialOwner,
		RelationshipTypeSyndicateParticipant, RelationshipTypeSyndicateHost:
		return true
	default:
		return false
	}
}

// PoliticalExposureStatus represents the political exposure classification
type PoliticalExposureStatus string

// Political exposure status constants
const (
	PoliticalExposureNone PoliticalExposureStatus = "NONE"
	PoliticalExposurePEP  PoliticalExposureStatus = "PEP" // Politically Exposed Person
	PoliticalExposureRCA  PoliticalExposureStatus = "RCA" // Relative or Close Associate
)

// IsValid checks if the political exposure status is valid
func (pes PoliticalExposureStatus) IsValid() bool {
	switch pes {
	case PoliticalExposureNone, PoliticalExposurePEP, PoliticalExposureRCA:
		return true
	default:
		return false
	}
}

// ReferenceType represents the type of reference document
type ReferenceType string

// Reference type constants
const (
	ReferenceTypeGovernmentID  ReferenceType = "GOVERNMENT_ID"
	ReferenceTypePassport      ReferenceType = "PASSPORT"
	ReferenceTypeDriverLicense ReferenceType = "DRIVER_LICENSE"
	ReferenceTypeUtilityBill   ReferenceType = "UTILITY_BILL"
)

// IsValid checks if the reference type is valid
func (rt ReferenceType) IsValid() bool {
	switch rt {
	case ReferenceTypeGovernmentID, ReferenceTypePassport, ReferenceTypeDriverLicense, ReferenceTypeUtilityBill:
		return true
	default:
		return false
	}
}

// ExternalReferenceType represents the type of external reference
type ExternalReferenceType string

// External reference type constants
const (
	ExternalReferenceTypeCompaniesHouse ExternalReferenceType = "COMPANIES_HOUSE"
	ExternalReferenceTypeNationalID     ExternalReferenceType = "NATIONAL_ID"
	ExternalReferenceTypeLEI            ExternalReferenceType = "LEI"
	ExternalReferenceTypeTaxID          ExternalReferenceType = "TAX_ID"
)

// PartyAssociation represents a relationship between two parties
type PartyAssociation struct {
	RelatedPartyID   uuid.UUID
	RelationshipType RelationshipType
	CreatedAt        time.Time
}

// Employment represents employment history information
type Employment struct {
	Employer  string
	StartDate time.Time
	EndDate   *time.Time // nil for current employment
	Position  string
}

// DemographicData contains socio-economic and employment information
type DemographicData struct {
	SocioEconomicData map[string]interface{} // JSONB-friendly data
	EmploymentHistory []Employment
	IncomeLevel       string
	EducationLevel    string
}

// Reference represents a reference document
type Reference struct {
	Type             ReferenceType
	Value            string
	IssuingAuthority string
	ExpiryDate       *time.Time // nil for documents without expiry
}

// ReferenceData contains identity verification and political exposure information
type ReferenceData struct {
	References              []Reference
	PoliticalExposureStatus PoliticalExposureStatus
}

// BankRelationship contains information about the party's relationship with the bank
type BankRelationship struct {
	AccountOfficerID      uuid.UUID
	RelationshipManagerID uuid.UUID
	AssignedBranch        string
	RelationshipStartDate time.Time
}

// AttributeEntry represents a single key-value attribute for a party.
// Keys are snake_case identifiers validated against the party type's attribute_schema.
type AttributeEntry struct {
	Key   string
	Value string
}

// Party represents a BIAN Party Reference Data Directory domain model.
//
// The Version field implements optimistic concurrency control to prevent lost updates
// in concurrent scenarios. The persistence layer should use this field in UPDATE
// statements (e.g., WHERE party_id = ? AND version = ?) to detect conflicts.
// Version is incremented on all state-modifying operations (status transitions,
// setting display name, and setting external references).
type Party struct {
	id                    uuid.UUID
	partyType             PartyType
	legalName             string
	displayName           string
	status                PartyStatus
	externalReference     string
	externalReferenceType ExternalReferenceType
	associations          []PartyAssociation
	demographics          DemographicData
	referenceData         ReferenceData
	bankRelations         BankRelationship
	attributes            []AttributeEntry
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
		id:         uuid.New(),
		partyType:  partyType,
		legalName:  legalName,
		status:     PartyStatusActive,
		attributes: []AttributeEntry{},
		createdAt:  now,
		updatedAt:  now,
		version:    1,
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
	associations []PartyAssociation,
	demographics DemographicData,
	referenceData ReferenceData,
	bankRelations BankRelationship,
	attributes []AttributeEntry,
	createdAt time.Time,
	updatedAt time.Time,
	version int64,
) *Party {
	if attributes == nil {
		attributes = []AttributeEntry{}
	}
	return &Party{
		id:                    id,
		partyType:             partyType,
		legalName:             legalName,
		displayName:           displayName,
		status:                status,
		externalReference:     externalReference,
		externalReferenceType: externalReferenceType,
		associations:          associations,
		demographics:          demographics,
		referenceData:         referenceData,
		bankRelations:         bankRelations,
		attributes:            attributes,
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

// Associations returns a copy of the party's associations to prevent external mutation.
func (p *Party) Associations() []PartyAssociation {
	result := make([]PartyAssociation, len(p.associations))
	copy(result, p.associations)
	return result
}

// Demographics returns the party's demographic data
func (p *Party) Demographics() DemographicData {
	return p.demographics
}

// ReferenceData returns the party's reference data
func (p *Party) ReferenceData() ReferenceData {
	return p.referenceData
}

// BankRelations returns the party's bank relationship information
func (p *Party) BankRelations() BankRelationship {
	return p.bankRelations
}

// Attributes returns a copy of the party's structured key-value attributes.
func (p *Party) Attributes() []AttributeEntry {
	result := make([]AttributeEntry, len(p.attributes))
	copy(result, p.attributes)
	return result
}

// SetAttributes replaces the party's attributes.
// The input slice is copied defensively so callers cannot mutate Party state after the call.
func (p *Party) SetAttributes(attrs []AttributeEntry) {
	if attrs == nil {
		attrs = []AttributeEntry{}
	}
	copied := make([]AttributeEntry, len(attrs))
	copy(copied, attrs)
	p.attributes = copied
	p.updatedAt = time.Now()
	p.version++
}

// SetDisplayName sets the party's display name
func (p *Party) SetDisplayName(displayName string) error {
	if err := validateDisplayName(displayName); err != nil {
		return err
	}

	p.displayName = displayName
	p.updatedAt = time.Now()
	p.version++
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
	p.version++
	return nil
}

// Restrict transitions the party to restricted status
func (p *Party) Restrict() error {
	if p.status == PartyStatusTerminated {
		return ErrInvalidStatusTransition
	}

	p.status = PartyStatusRestricted
	p.updatedAt = time.Now()
	p.version++
	return nil
}

// Suspend transitions the party to suspended status
func (p *Party) Suspend() error {
	if p.status == PartyStatusTerminated {
		return ErrInvalidStatusTransition
	}

	p.status = PartyStatusSuspended
	p.updatedAt = time.Now()
	p.version++
	return nil
}

// Terminate transitions the party to terminated status
func (p *Party) Terminate() error {
	// Termination is allowed from any state
	p.status = PartyStatusTerminated
	p.updatedAt = time.Now()
	p.version++
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
	p.version++
	return nil
}

// ControlParty applies a control action with state machine enforcement
// The reason parameter is reserved for future audit logging but not currently used in validation
func (p *Party) ControlParty(action ControlAction, _ string) error {
	if !action.IsValid() {
		return ErrInvalidControlAction
	}

	switch action {
	case ControlActionActivate:
		return p.Activate()
	case ControlActionRestrict:
		return p.Restrict()
	case ControlActionSuspend:
		return p.Suspend()
	case ControlActionTerminate:
		return p.Terminate()
	default:
		return ErrInvalidControlAction
	}
}

// UpdateParty updates party fields with validation
func (p *Party) UpdateParty(updates map[string]interface{}) error {
	for field, value := range updates {
		if err := p.updateField(field, value); err != nil {
			return err
		}
	}
	return nil
}

// updateField updates a single field with validation
func (p *Party) updateField(field string, value interface{}) error {
	switch field {
	case "displayName":
		return p.updateDisplayName(value)
	case "demographics":
		return p.updateDemographics(value)
	case "referenceData":
		return p.updateReferenceData(value)
	case "bankRelations":
		return p.updateBankRelations(value)
	case "associations":
		return p.updateAssociations(value)
	default:
		return ErrInvalidUpdateField
	}
}

func (p *Party) updateDisplayName(value interface{}) error {
	displayName, ok := value.(string)
	if !ok {
		return ErrInvalidUpdateField
	}
	return p.SetDisplayName(displayName)
}

func (p *Party) updateDemographics(value interface{}) error {
	demographics, ok := value.(DemographicData)
	if !ok {
		return ErrInvalidUpdateField
	}
	p.demographics = demographics
	p.updatedAt = time.Now()
	p.version++
	return nil
}

func (p *Party) updateReferenceData(value interface{}) error {
	referenceData, ok := value.(ReferenceData)
	if !ok {
		return ErrInvalidUpdateField
	}
	// Validate political exposure status
	if !referenceData.PoliticalExposureStatus.IsValid() {
		return ErrInvalidPoliticalExposure
	}
	// Validate reference types
	for _, ref := range referenceData.References {
		if !ref.Type.IsValid() {
			return ErrInvalidReferenceType
		}
	}
	p.referenceData = referenceData
	p.updatedAt = time.Now()
	p.version++
	return nil
}

func (p *Party) updateBankRelations(value interface{}) error {
	bankRelations, ok := value.(BankRelationship)
	if !ok {
		return ErrInvalidUpdateField
	}
	p.bankRelations = bankRelations
	p.updatedAt = time.Now()
	p.version++
	return nil
}

func (p *Party) updateAssociations(value interface{}) error {
	associations, ok := value.([]PartyAssociation)
	if !ok {
		return ErrInvalidUpdateField
	}
	// Validate relationship types and check for circular associations
	for _, assoc := range associations {
		if !assoc.RelationshipType.IsValid() {
			return ErrInvalidRelationshipType
		}
		if assoc.RelatedPartyID == p.id {
			return ErrCircularAssociation
		}
	}
	p.associations = associations
	p.updatedAt = time.Now()
	p.version++
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

	// National ID: varies by country, using a general alphanumeric pattern (min 2 for short codes like DCC)
	nationalIDRegex = regexp.MustCompile(`^[A-Z0-9]{2,20}$`)

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
