package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtoToPartyStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       pb.PartyStatus
		expected    string
		expectError error
	}{
		{"active", pb.PartyStatus_PARTY_STATUS_ACTIVE, string(domain.PartyStatusActive), nil},
		{"restricted", pb.PartyStatus_PARTY_STATUS_RESTRICTED, string(domain.PartyStatusRestricted), nil},
		{"suspended", pb.PartyStatus_PARTY_STATUS_SUSPENDED, string(domain.PartyStatusSuspended), nil},
		{"terminated", pb.PartyStatus_PARTY_STATUS_TERMINATED, string(domain.PartyStatusTerminated), nil},
		{"unspecified returns empty", pb.PartyStatus_PARTY_STATUS_UNSPECIFIED, "", nil},
		{"unknown enum returns error", pb.PartyStatus(999), "", ErrUnknownPartyStatus},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := protoToPartyStatus(tt.input)
			if tt.expectError != nil {
				assert.ErrorIs(t, err, tt.expectError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestProtoToControlAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       pb.ControlAction
		expected    domain.ControlAction
		expectError error
	}{
		{"activate", pb.ControlAction_CONTROL_ACTION_ACTIVATE, domain.ControlActionActivate, nil},
		{"restrict", pb.ControlAction_CONTROL_ACTION_RESTRICT, domain.ControlActionRestrict, nil},
		{"suspend", pb.ControlAction_CONTROL_ACTION_SUSPEND, domain.ControlActionSuspend, nil},
		{"terminate", pb.ControlAction_CONTROL_ACTION_TERMINATE, domain.ControlActionTerminate, nil},
		{"unspecified returns error", pb.ControlAction_CONTROL_ACTION_UNSPECIFIED, "", ErrControlActionUnspecified},
		{"unknown enum returns error", pb.ControlAction(999), "", ErrUnknownControlAction},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := protoToControlAction(tt.input)
			if tt.expectError != nil {
				assert.ErrorIs(t, err, tt.expectError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestProtoAssociationStatusToString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       pb.AssociationStatus
		expected    string
		expectError error
	}{
		{"active", pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE, "ACTIVE", nil},
		{"suspended", pb.AssociationStatus_ASSOCIATION_STATUS_SUSPENDED, "SUSPENDED", nil},
		{"terminated", pb.AssociationStatus_ASSOCIATION_STATUS_TERMINATED, "TERMINATED", nil},
		{"unspecified defaults to active", pb.AssociationStatus_ASSOCIATION_STATUS_UNSPECIFIED, "ACTIVE", nil},
		{"unknown enum returns error", pb.AssociationStatus(999), "", ErrUnknownAssociationStatus},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := protoAssociationStatusToString(tt.input)
			if tt.expectError != nil {
				assert.ErrorIs(t, err, tt.expectError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestAssociationStatusToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected pb.AssociationStatus
	}{
		{"active", "ACTIVE", pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE},
		{"suspended", "SUSPENDED", pb.AssociationStatus_ASSOCIATION_STATUS_SUSPENDED},
		{"terminated", "TERMINATED", pb.AssociationStatus_ASSOCIATION_STATUS_TERMINATED},
		{"unknown defaults to unspecified", "UNKNOWN", pb.AssociationStatus_ASSOCIATION_STATUS_UNSPECIFIED},
		{"empty defaults to unspecified", "", pb.AssociationStatus_ASSOCIATION_STATUS_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := associationStatusToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestProtoToRelationshipType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       pb.RelationshipType
		expected    string
		expectError error
	}{
		{"unspecified", pb.RelationshipType_RELATIONSHIP_TYPE_UNSPECIFIED, "UNSPECIFIED", nil},
		{"spouse", pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE, string(domain.RelationshipTypeSpouse), nil},
		{"dependent", pb.RelationshipType_RELATIONSHIP_TYPE_DEPENDENT, string(domain.RelationshipTypeDependent), nil},
		{"business partner", pb.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER, string(domain.RelationshipTypeBusinessPartner), nil},
		{"guarantor", pb.RelationshipType_RELATIONSHIP_TYPE_GUARANTOR, string(domain.RelationshipTypeGuarantor), nil},
		{"beneficial owner", pb.RelationshipType_RELATIONSHIP_TYPE_BENEFICIAL_OWNER, string(domain.RelationshipTypeBeneficialOwner), nil},
		{"syndicate participant", pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT, string(domain.RelationshipTypeSyndicateParticipant), nil},
		{"syndicate host", pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_HOST, string(domain.RelationshipTypeSyndicateHost), nil},
		{"unknown enum returns error", pb.RelationshipType(999), "", ErrUnknownRelationshipType},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := protoToRelationshipType(tt.input)
			if tt.expectError != nil {
				assert.ErrorIs(t, err, tt.expectError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestRelationshipTypeToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected pb.RelationshipType
	}{
		{"spouse", string(domain.RelationshipTypeSpouse), pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE},
		{"dependent", string(domain.RelationshipTypeDependent), pb.RelationshipType_RELATIONSHIP_TYPE_DEPENDENT},
		{"business partner", string(domain.RelationshipTypeBusinessPartner), pb.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER},
		{"guarantor", string(domain.RelationshipTypeGuarantor), pb.RelationshipType_RELATIONSHIP_TYPE_GUARANTOR},
		{"beneficial owner", string(domain.RelationshipTypeBeneficialOwner), pb.RelationshipType_RELATIONSHIP_TYPE_BENEFICIAL_OWNER},
		{"syndicate participant", string(domain.RelationshipTypeSyndicateParticipant), pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT},
		{"syndicate host", string(domain.RelationshipTypeSyndicateHost), pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_HOST},
		{"unknown defaults to unspecified", "UNKNOWN", pb.RelationshipType_RELATIONSHIP_TYPE_UNSPECIFIED},
		{"empty defaults to unspecified", "", pb.RelationshipType_RELATIONSHIP_TYPE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := relationshipTypeToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFromJSONB(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"json quoted string", `"hello world"`, "hello world"},
		{"plain string not valid json", "hello world", "hello world"},
		{"empty string", "", ""},
		{"json number falls back to raw", "42", "42"},
		{"json object falls back to raw", `{"key":"val"}`, `{"key":"val"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := fromJSONB(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDomainAttributesToProto(t *testing.T) {
	t.Parallel()

	t.Run("empty slice returns empty slice", func(t *testing.T) {
		result := domainAttributesToProto([]domain.AttributeEntry{})
		assert.Empty(t, result)
		assert.NotNil(t, result)
	})

	t.Run("converts multiple entries", func(t *testing.T) {
		attrs := []domain.AttributeEntry{
			{Key: "color", Value: "blue"},
			{Key: "size", Value: "large"},
			{Key: "weight", Value: "10kg"},
		}

		result := domainAttributesToProto(attrs)
		require.Len(t, result, 3)

		assert.Equal(t, "color", result[0].Key)
		assert.Equal(t, "blue", result[0].Value)
		assert.Equal(t, "size", result[1].Key)
		assert.Equal(t, "large", result[1].Value)
		assert.Equal(t, "weight", result[2].Key)
		assert.Equal(t, "10kg", result[2].Value)
	})
}

func TestProtoAttributesToDomain(t *testing.T) {
	t.Parallel()

	t.Run("empty slice returns empty slice", func(t *testing.T) {
		result := protoAttributesToDomain([]*quantityv1.AttributeEntry{})
		assert.Empty(t, result)
		assert.NotNil(t, result)
	})

	t.Run("nil entries are skipped", func(t *testing.T) {
		attrs := []*quantityv1.AttributeEntry{
			{Key: "color", Value: "blue"},
			nil,
			{Key: "size", Value: "large"},
		}

		result := protoAttributesToDomain(attrs)
		require.Len(t, result, 2)

		assert.Equal(t, "color", result[0].Key)
		assert.Equal(t, "blue", result[0].Value)
		assert.Equal(t, "size", result[1].Key)
		assert.Equal(t, "large", result[1].Value)
	})

	t.Run("converts multiple entries", func(t *testing.T) {
		attrs := []*quantityv1.AttributeEntry{
			{Key: "a", Value: "1"},
			{Key: "b", Value: "2"},
		}

		result := protoAttributesToDomain(attrs)
		require.Len(t, result, 2)

		assert.Equal(t, domain.AttributeEntry{Key: "a", Value: "1"}, result[0])
		assert.Equal(t, domain.AttributeEntry{Key: "b", Value: "2"}, result[1])
	})
}

func TestAssociationEntityToProto(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	partyID := uuid.New()
	relatedID := uuid.New()
	assocID := uuid.New()

	t.Run("converts entity without EffectiveTo", func(t *testing.T) {
		entity := &persistence.PartyAssociationEntity{
			ID:               assocID,
			PartyID:          partyID,
			RelatedPartyID:   relatedID,
			RelationshipType: string(domain.RelationshipTypeSpouse),
			Status:           "ACTIVE",
			EffectiveFrom:    now,
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		result := associationEntityToProto(entity)

		assert.Equal(t, assocID.String(), result.AssociationId)
		assert.Equal(t, partyID.String(), result.PartyId)
		assert.Equal(t, relatedID.String(), result.RelatedPartyId)
		assert.Equal(t, pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE, result.RelationshipType)
		assert.Equal(t, pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE, result.Status)
		assert.NotNil(t, result.EffectiveFrom)
		assert.Nil(t, result.EffectiveTo)
		assert.NotNil(t, result.CreatedAt)
		assert.NotNil(t, result.UpdatedAt)
		assert.Nil(t, result.Metadata)
	})

	t.Run("converts entity with EffectiveTo", func(t *testing.T) {
		effectiveTo := now.Add(24 * time.Hour)
		entity := &persistence.PartyAssociationEntity{
			ID:               assocID,
			PartyID:          partyID,
			RelatedPartyID:   relatedID,
			RelationshipType: string(domain.RelationshipTypeGuarantor),
			Status:           "TERMINATED",
			EffectiveFrom:    now,
			EffectiveTo:      &effectiveTo,
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		result := associationEntityToProto(entity)

		assert.NotNil(t, result.EffectiveTo)
		assert.Equal(t, pb.AssociationStatus_ASSOCIATION_STATUS_TERMINATED, result.Status)
	})

	t.Run("converts entity with valid metadata", func(t *testing.T) {
		metadataJSON := `{"role":"primary","tier":"gold"}`
		entity := &persistence.PartyAssociationEntity{
			ID:               assocID,
			PartyID:          partyID,
			RelatedPartyID:   relatedID,
			RelationshipType: string(domain.RelationshipTypeBusinessPartner),
			Status:           "ACTIVE",
			EffectiveFrom:    now,
			Metadata:         &metadataJSON,
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		result := associationEntityToProto(entity)

		require.NotNil(t, result.Metadata)
		assert.Equal(t, "primary", result.Metadata.Fields["role"].GetStringValue())
		assert.Equal(t, "gold", result.Metadata.Fields["tier"].GetStringValue())
	})

	t.Run("ignores empty metadata object", func(t *testing.T) {
		emptyObj := "{}"
		entity := &persistence.PartyAssociationEntity{
			ID:               assocID,
			PartyID:          partyID,
			RelatedPartyID:   relatedID,
			RelationshipType: string(domain.RelationshipTypeSpouse),
			Status:           "ACTIVE",
			EffectiveFrom:    now,
			Metadata:         &emptyObj,
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		result := associationEntityToProto(entity)
		assert.Nil(t, result.Metadata)
	})

	t.Run("ignores empty string metadata", func(t *testing.T) {
		empty := ""
		entity := &persistence.PartyAssociationEntity{
			ID:               assocID,
			PartyID:          partyID,
			RelatedPartyID:   relatedID,
			RelationshipType: string(domain.RelationshipTypeSpouse),
			Status:           "ACTIVE",
			EffectiveFrom:    now,
			Metadata:         &empty,
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		result := associationEntityToProto(entity)
		assert.Nil(t, result.Metadata)
	})

	t.Run("ignores invalid json metadata", func(t *testing.T) {
		invalid := `{not valid json`
		entity := &persistence.PartyAssociationEntity{
			ID:               assocID,
			PartyID:          partyID,
			RelatedPartyID:   relatedID,
			RelationshipType: string(domain.RelationshipTypeSpouse),
			Status:           "ACTIVE",
			EffectiveFrom:    now,
			Metadata:         &invalid,
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		result := associationEntityToProto(entity)
		assert.Nil(t, result.Metadata)
	})

	t.Run("nil metadata pointer", func(t *testing.T) {
		entity := &persistence.PartyAssociationEntity{
			ID:               assocID,
			PartyID:          partyID,
			RelatedPartyID:   relatedID,
			RelationshipType: string(domain.RelationshipTypeSpouse),
			Status:           "ACTIVE",
			EffectiveFrom:    now,
			Metadata:         nil,
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		result := associationEntityToProto(entity)
		assert.Nil(t, result.Metadata)
	})
}
