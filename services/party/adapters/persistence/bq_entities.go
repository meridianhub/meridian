// Package persistence provides database persistence for the party domain
package persistence

import (
	"time"

	"github.com/google/uuid"
)

// PartyAssociationEntity represents a relationship between two parties
type PartyAssociationEntity struct {
	ID               uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	PartyID          uuid.UUID `gorm:"column:party_id;type:uuid;not null;index:idx_party_association_party_id"`
	RelatedPartyID   uuid.UUID `gorm:"column:related_party_id;type:uuid;not null;index:idx_party_association_related_party_id"`
	RelationshipType string    `gorm:"column:relationship_type;type:varchar(50);not null;index:idx_party_association_relationship_type"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null;default:now()"`
}

// TableName overrides the default table name
func (PartyAssociationEntity) TableName() string {
	return "party_association"
}

// PartyDemographicEntity represents demographic information for a party
type PartyDemographicEntity struct {
	ID                uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	PartyID           uuid.UUID `gorm:"column:party_id;type:uuid;not null;uniqueIndex:uq_party_demographic_party_id"`
	SocioEconomicData *string   `gorm:"column:socio_economic_data;type:jsonb"`
	EmploymentHistory *string   `gorm:"column:employment_history;type:jsonb"`
	IncomeLevel       *string   `gorm:"column:income_level;type:varchar(50)"`
	EducationLevel    *string   `gorm:"column:education_level;type:varchar(50)"`
	UpdatedAt         time.Time `gorm:"column:updated_at;not null;default:now()"`
}

// TableName overrides the default table name
func (PartyDemographicEntity) TableName() string {
	return "party_demographic"
}

// PartyReferenceEntity represents a reference document for a party
type PartyReferenceEntity struct {
	ID               uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	PartyID          uuid.UUID  `gorm:"column:party_id;type:uuid;not null;index:idx_party_reference_party_id"`
	ReferenceType    string     `gorm:"column:reference_type;type:varchar(50);not null;index:idx_party_reference_type_value"`
	ReferenceValue   string     `gorm:"column:reference_value;type:varchar(255);not null;index:idx_party_reference_type_value"`
	IssuingAuthority *string    `gorm:"column:issuing_authority;type:varchar(100)"`
	IssueDate        *time.Time `gorm:"column:issue_date;type:date"`
	ExpiryDate       *time.Time `gorm:"column:expiry_date;type:date;index:idx_party_reference_expiry_date,where:expiry_date IS NOT NULL"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null;default:now()"`
}

// TableName overrides the default table name
func (PartyReferenceEntity) TableName() string {
	return "party_reference"
}

// PartyBankRelationEntity represents bank relationship information
type PartyBankRelationEntity struct {
	ID                    uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	PartyID               uuid.UUID  `gorm:"column:party_id;type:uuid;not null;uniqueIndex:uq_party_bank_relation_party_id"`
	AccountOfficerID      *uuid.UUID `gorm:"column:account_officer_id;type:uuid;index:idx_party_bank_relation_account_officer,where:account_officer_id IS NOT NULL"`
	RelationshipManagerID *uuid.UUID `gorm:"column:relationship_manager_id;type:uuid;index:idx_party_bank_relation_relationship_manager,where:relationship_manager_id IS NOT NULL"`
	AssignedBranch        *string    `gorm:"column:assigned_branch;type:varchar(100)"`
	RelationshipStartDate *time.Time `gorm:"column:relationship_start_date;type:date"`
	UpdatedAt             time.Time  `gorm:"column:updated_at;not null;default:now()"`
}

// TableName overrides the default table name
func (PartyBankRelationEntity) TableName() string {
	return "party_bank_relation"
}
