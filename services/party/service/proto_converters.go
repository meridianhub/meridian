package service

import (
	"encoding/json"
	"fmt"

	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fromJSONB extracts a string from JSONB storage.
// If the stored value is a JSON string, it's unmarshaled.
// Otherwise, the raw value is returned.
func fromJSONB(s string) string {
	var result string
	if err := json.Unmarshal([]byte(s), &result); err == nil {
		return result
	}
	return s
}

// protoToPartyStatus converts a proto PartyStatus to domain string.
// Returns an error for unknown enum values.
func protoToPartyStatus(s pb.PartyStatus) (string, error) {
	switch s {
	case pb.PartyStatus_PARTY_STATUS_ACTIVE:
		return string(domain.PartyStatusActive), nil
	case pb.PartyStatus_PARTY_STATUS_RESTRICTED:
		return string(domain.PartyStatusRestricted), nil
	case pb.PartyStatus_PARTY_STATUS_SUSPENDED:
		return string(domain.PartyStatusSuspended), nil
	case pb.PartyStatus_PARTY_STATUS_TERMINATED:
		return string(domain.PartyStatusTerminated), nil
	case pb.PartyStatus_PARTY_STATUS_UNSPECIFIED:
		return "", nil
	default:
		return "", fmt.Errorf("unknown party status: %v", s)
	}
}

// domainToProto converts a domain Party to a proto Party message.
// Returns nil if the input party is nil.
func domainToProto(party *domain.Party) *pb.Party {
	if party == nil {
		return nil
	}
	return &pb.Party{
		PartyId:               party.ID().String(),
		PartyType:             partyTypeToProto(party.PartyType()),
		LegalName:             party.LegalName(),
		DisplayName:           party.DisplayName(),
		Status:                partyStatusToProto(party.Status()),
		ExternalReference:     party.ExternalReference(),
		ExternalReferenceType: externalRefTypeToProto(party.ExternalReferenceType()),
		Attributes:            domainAttributesToProto(party.Attributes()),
		CreatedAt:             timestamppb.New(party.CreatedAt()),
		UpdatedAt:             timestamppb.New(party.UpdatedAt()),
		// #nosec G115 - Version is bounded by database constraints
		Version: int32(party.Version()),
	}
}

// domainAttributesToProto converts domain AttributeEntry slice to proto AttributeEntry slice.
func domainAttributesToProto(attrs []domain.AttributeEntry) []*quantityv1.AttributeEntry {
	result := make([]*quantityv1.AttributeEntry, len(attrs))
	for i, a := range attrs {
		result[i] = &quantityv1.AttributeEntry{Key: a.Key, Value: a.Value}
	}
	return result
}

// protoAttributesToDomain converts proto AttributeEntry slice to domain AttributeEntry slice.
// Nil proto entries are skipped.
func protoAttributesToDomain(attrs []*quantityv1.AttributeEntry) []domain.AttributeEntry {
	result := make([]domain.AttributeEntry, 0, len(attrs))
	for _, a := range attrs {
		if a != nil {
			result = append(result, domain.AttributeEntry{Key: a.Key, Value: a.Value})
		}
	}
	return result
}

// protoToPartyType converts a proto PartyType to domain PartyType
func protoToPartyType(pt pb.PartyType) (domain.PartyType, error) {
	switch pt {
	case pb.PartyType_PARTY_TYPE_PERSON:
		return domain.PartyTypePerson, nil
	case pb.PartyType_PARTY_TYPE_ORGANIZATION:
		return domain.PartyTypeOrganization, nil
	case pb.PartyType_PARTY_TYPE_UNSPECIFIED:
		return "", domain.ErrInvalidPartyType
	default:
		return "", domain.ErrInvalidPartyType
	}
}

// partyTypeToProto converts a domain PartyType to proto PartyType
func partyTypeToProto(pt domain.PartyType) pb.PartyType {
	switch pt {
	case domain.PartyTypePerson:
		return pb.PartyType_PARTY_TYPE_PERSON
	case domain.PartyTypeOrganization:
		return pb.PartyType_PARTY_TYPE_ORGANIZATION
	default:
		return pb.PartyType_PARTY_TYPE_UNSPECIFIED
	}
}

// partyStatusToProto converts a domain PartyStatus to proto PartyStatus
func partyStatusToProto(status domain.PartyStatus) pb.PartyStatus {
	switch status {
	case domain.PartyStatusActive:
		return pb.PartyStatus_PARTY_STATUS_ACTIVE
	case domain.PartyStatusRestricted:
		return pb.PartyStatus_PARTY_STATUS_RESTRICTED
	case domain.PartyStatusSuspended:
		return pb.PartyStatus_PARTY_STATUS_SUSPENDED
	case domain.PartyStatusTerminated:
		return pb.PartyStatus_PARTY_STATUS_TERMINATED
	default:
		return pb.PartyStatus_PARTY_STATUS_UNSPECIFIED
	}
}

// protoToExternalRefType converts a proto ExternalReferenceType to domain ExternalReferenceType
func protoToExternalRefType(rt pb.ExternalReferenceType) (domain.ExternalReferenceType, error) {
	switch rt {
	case pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE:
		return domain.ExternalReferenceTypeCompaniesHouse, nil
	case pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_NATIONAL_ID:
		return domain.ExternalReferenceTypeNationalID, nil
	case pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_LEI:
		return domain.ExternalReferenceTypeLEI, nil
	case pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_TAX_ID:
		return domain.ExternalReferenceTypeTaxID, nil
	case pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED:
		return "", ErrExternalRefTypeRequired
	default:
		return "", ErrUnknownExternalRefType
	}
}

// externalRefTypeToProto converts a domain ExternalReferenceType to proto ExternalReferenceType
func externalRefTypeToProto(rt domain.ExternalReferenceType) pb.ExternalReferenceType {
	switch rt {
	case domain.ExternalReferenceTypeCompaniesHouse:
		return pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE
	case domain.ExternalReferenceTypeNationalID:
		return pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_NATIONAL_ID
	case domain.ExternalReferenceTypeLEI:
		return pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_LEI
	case domain.ExternalReferenceTypeTaxID:
		return pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_TAX_ID
	default:
		return pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED
	}
}

// protoToControlAction converts proto ControlAction to domain ControlAction
func protoToControlAction(action pb.ControlAction) (domain.ControlAction, error) {
	switch action {
	case pb.ControlAction_CONTROL_ACTION_ACTIVATE:
		return domain.ControlActionActivate, nil
	case pb.ControlAction_CONTROL_ACTION_RESTRICT:
		return domain.ControlActionRestrict, nil
	case pb.ControlAction_CONTROL_ACTION_SUSPEND:
		return domain.ControlActionSuspend, nil
	case pb.ControlAction_CONTROL_ACTION_TERMINATE:
		return domain.ControlActionTerminate, nil
	case pb.ControlAction_CONTROL_ACTION_UNSPECIFIED:
		return "", ErrControlActionUnspecified
	default:
		return "", ErrUnknownControlAction
	}
}

// Association status string constants
const (
	associationStatusActive     = "ACTIVE"
	associationStatusSuspended  = "SUSPENDED"
	associationStatusTerminated = "TERMINATED"
)

// protoAssociationStatusToString converts proto AssociationStatus to string.
// Returns an error for unknown enum values.
func protoAssociationStatusToString(s pb.AssociationStatus) (string, error) {
	switch s {
	case pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE:
		return associationStatusActive, nil
	case pb.AssociationStatus_ASSOCIATION_STATUS_SUSPENDED:
		return associationStatusSuspended, nil
	case pb.AssociationStatus_ASSOCIATION_STATUS_TERMINATED:
		return associationStatusTerminated, nil
	case pb.AssociationStatus_ASSOCIATION_STATUS_UNSPECIFIED:
		return associationStatusActive, nil
	default:
		return "", fmt.Errorf("unknown association status: %v", s)
	}
}

// associationEntityToProto converts a persistence entity to a proto Association message.
func associationEntityToProto(entity *persistence.PartyAssociationEntity) *pb.Association {
	assoc := &pb.Association{
		AssociationId:    entity.ID.String(),
		PartyId:          entity.PartyID.String(),
		RelatedPartyId:   entity.RelatedPartyID.String(),
		RelationshipType: relationshipTypeToProto(entity.RelationshipType),
		CreatedAt:        timestamppb.New(entity.CreatedAt),
		UpdatedAt:        timestamppb.New(entity.UpdatedAt),
		Status:           associationStatusToProto(entity.Status),
		EffectiveFrom:    timestamppb.New(entity.EffectiveFrom),
	}

	if entity.EffectiveTo != nil {
		assoc.EffectiveTo = timestamppb.New(*entity.EffectiveTo)
	}

	if entity.Metadata != nil && *entity.Metadata != "" && *entity.Metadata != "{}" {
		var metadataMap map[string]interface{}
		if err := json.Unmarshal([]byte(*entity.Metadata), &metadataMap); err == nil {
			if pbStruct, err := structpb.NewStruct(metadataMap); err == nil {
				assoc.Metadata = pbStruct
			}
		}
	}

	return assoc
}

// associationStatusToProto converts a status string to proto AssociationStatus
func associationStatusToProto(status string) pb.AssociationStatus {
	switch status {
	case associationStatusActive:
		return pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE
	case associationStatusSuspended:
		return pb.AssociationStatus_ASSOCIATION_STATUS_SUSPENDED
	case associationStatusTerminated:
		return pb.AssociationStatus_ASSOCIATION_STATUS_TERMINATED
	default:
		return pb.AssociationStatus_ASSOCIATION_STATUS_UNSPECIFIED
	}
}

// protoToRelationshipType converts proto RelationshipType to domain string.
// Returns an error for unknown enum values.
func protoToRelationshipType(rt pb.RelationshipType) (string, error) {
	switch rt {
	case pb.RelationshipType_RELATIONSHIP_TYPE_UNSPECIFIED:
		return "UNSPECIFIED", nil
	case pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE:
		return string(domain.RelationshipTypeSpouse), nil
	case pb.RelationshipType_RELATIONSHIP_TYPE_DEPENDENT:
		return string(domain.RelationshipTypeDependent), nil
	case pb.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER:
		return string(domain.RelationshipTypeBusinessPartner), nil
	case pb.RelationshipType_RELATIONSHIP_TYPE_GUARANTOR:
		return string(domain.RelationshipTypeGuarantor), nil
	case pb.RelationshipType_RELATIONSHIP_TYPE_BENEFICIAL_OWNER:
		return string(domain.RelationshipTypeBeneficialOwner), nil
	case pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT:
		return string(domain.RelationshipTypeSyndicateParticipant), nil
	case pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_HOST:
		return string(domain.RelationshipTypeSyndicateHost), nil
	default:
		return "", fmt.Errorf("unknown relationship type: %v", rt)
	}
}

// relationshipTypeToProto converts domain string to proto RelationshipType
func relationshipTypeToProto(rt string) pb.RelationshipType {
	switch rt {
	case string(domain.RelationshipTypeSpouse):
		return pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE
	case string(domain.RelationshipTypeDependent):
		return pb.RelationshipType_RELATIONSHIP_TYPE_DEPENDENT
	case string(domain.RelationshipTypeBusinessPartner):
		return pb.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER
	case string(domain.RelationshipTypeGuarantor):
		return pb.RelationshipType_RELATIONSHIP_TYPE_GUARANTOR
	case string(domain.RelationshipTypeBeneficialOwner):
		return pb.RelationshipType_RELATIONSHIP_TYPE_BENEFICIAL_OWNER
	case string(domain.RelationshipTypeSyndicateParticipant):
		return pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT
	case string(domain.RelationshipTypeSyndicateHost):
		return pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_HOST
	default:
		return pb.RelationshipType_RELATIONSHIP_TYPE_UNSPECIFIED
	}
}
