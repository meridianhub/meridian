package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParty_StatusTransitions_Suspended tests the new SUSPENDED status
func TestParty_StatusTransitions_ActiveToSuspended(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)
	assert.Equal(t, PartyStatusActive, party.Status())

	err = party.Suspend()
	assert.NoError(t, err)
	assert.Equal(t, PartyStatusSuspended, party.Status())
}

func TestParty_StatusTransitions_SuspendedToActive(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.Suspend()
	require.NoError(t, err)
	assert.Equal(t, PartyStatusSuspended, party.Status())

	err = party.Activate()
	assert.NoError(t, err)
	assert.Equal(t, PartyStatusActive, party.Status())
}

func TestParty_StatusTransitions_SuspendedToTerminated(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.Suspend()
	require.NoError(t, err)

	err = party.Terminate()
	assert.NoError(t, err)
	assert.Equal(t, PartyStatusTerminated, party.Status())
}

func TestParty_StatusTransitions_TerminatedToSuspended_Invalid(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.Terminate()
	require.NoError(t, err)

	err = party.Suspend()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
	assert.Equal(t, PartyStatusTerminated, party.Status())
}

// TestControlAction_IsValid tests control action validation
func TestControlAction_IsValid(t *testing.T) {
	tests := []struct {
		action ControlAction
		want   bool
	}{
		{ControlActionActivate, true},
		{ControlActionRestrict, true},
		{ControlActionSuspend, true},
		{ControlActionTerminate, true},
		{ControlAction("INVALID"), false},
		{ControlAction(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.action.IsValid())
		})
	}
}

// TestParty_ControlParty tests the unified control method
func TestParty_ControlParty_Activate(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.Restrict()
	require.NoError(t, err)

	err = party.ControlParty(ControlActionActivate, "Restriction lifted")
	assert.NoError(t, err)
	assert.Equal(t, PartyStatusActive, party.Status())
}

func TestParty_ControlParty_Restrict(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.ControlParty(ControlActionRestrict, "Fraud investigation")
	assert.NoError(t, err)
	assert.Equal(t, PartyStatusRestricted, party.Status())
}

func TestParty_ControlParty_Suspend(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.ControlParty(ControlActionSuspend, "Temporary suspension")
	assert.NoError(t, err)
	assert.Equal(t, PartyStatusSuspended, party.Status())
}

func TestParty_ControlParty_Terminate(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.ControlParty(ControlActionTerminate, "Account closed")
	assert.NoError(t, err)
	assert.Equal(t, PartyStatusTerminated, party.Status())
}

func TestParty_ControlParty_InvalidAction(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.ControlParty(ControlAction("INVALID"), "Test")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidControlAction)
}

func TestParty_ControlParty_TerminalStateRestriction(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.ControlParty(ControlActionTerminate, "Closed")
	require.NoError(t, err)

	// Should not be able to activate from terminated
	err = party.ControlParty(ControlActionActivate, "Reopen")
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
	assert.Equal(t, PartyStatusTerminated, party.Status())
}

// TestParty_UpdateParty tests the update method
func TestParty_UpdateParty_DisplayName(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "John Smith")
	require.NoError(t, err)

	updates := map[string]interface{}{
		"displayName": "Johnny",
	}

	err = party.UpdateParty(updates)
	assert.NoError(t, err)
	assert.Equal(t, "Johnny", party.DisplayName())
}

func TestParty_UpdateParty_Demographics(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "John Smith")
	require.NoError(t, err)

	demographics := DemographicData{
		SocioEconomicData: map[string]interface{}{
			"maritalStatus": "MARRIED",
			"dependents":    2,
		},
		EmploymentHistory: []Employment{
			{
				Employer:  "Acme Corp",
				StartDate: time.Now().AddDate(-2, 0, 0),
				EndDate:   nil, // Current employment
				Position:  "Software Engineer",
			},
		},
		IncomeLevel:    "MEDIUM",
		EducationLevel: "BACHELOR",
	}

	updates := map[string]interface{}{
		"demographics": demographics,
	}

	initialVersion := party.Version()
	err = party.UpdateParty(updates)
	assert.NoError(t, err)
	assert.Equal(t, demographics, party.Demographics())
	assert.Equal(t, initialVersion+1, party.Version())
}

func TestParty_UpdateParty_ReferenceData(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "John Smith")
	require.NoError(t, err)

	expiryDate := time.Now().AddDate(5, 0, 0)
	referenceData := ReferenceData{
		References: []Reference{
			{
				Type:             ReferenceTypePassport,
				Value:            "AB1234567",
				IssuingAuthority: "UK Government",
				ExpiryDate:       &expiryDate,
			},
		},
		PoliticalExposureStatus: PoliticalExposureNone,
	}

	updates := map[string]interface{}{
		"referenceData": referenceData,
	}

	err = party.UpdateParty(updates)
	assert.NoError(t, err)
	assert.Equal(t, referenceData, party.ReferenceData())
}

func TestParty_UpdateParty_ReferenceData_InvalidPoliticalExposure(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "John Smith")
	require.NoError(t, err)

	referenceData := ReferenceData{
		References:              []Reference{},
		PoliticalExposureStatus: PoliticalExposureStatus("INVALID"),
	}

	updates := map[string]interface{}{
		"referenceData": referenceData,
	}

	err = party.UpdateParty(updates)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidPoliticalExposure)
}

func TestParty_UpdateParty_ReferenceData_InvalidReferenceType(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "John Smith")
	require.NoError(t, err)

	referenceData := ReferenceData{
		References: []Reference{
			{
				Type:             ReferenceType("INVALID"),
				Value:            "123456",
				IssuingAuthority: "Test",
				ExpiryDate:       nil,
			},
		},
		PoliticalExposureStatus: PoliticalExposureNone,
	}

	updates := map[string]interface{}{
		"referenceData": referenceData,
	}

	err = party.UpdateParty(updates)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidReferenceType)
}

func TestParty_UpdateParty_BankRelations(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "John Smith")
	require.NoError(t, err)

	bankRelations := BankRelationship{
		AccountOfficerID:      uuid.New(),
		RelationshipManagerID: uuid.New(),
		AssignedBranch:        "London Bridge",
		RelationshipStartDate: time.Now(),
	}

	updates := map[string]interface{}{
		"bankRelations": bankRelations,
	}

	err = party.UpdateParty(updates)
	assert.NoError(t, err)
	assert.Equal(t, bankRelations, party.BankRelations())
}

func TestParty_UpdateParty_Associations(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "John Smith")
	require.NoError(t, err)

	relatedPartyID := uuid.New()
	associations := []PartyAssociation{
		{
			RelatedPartyID:   relatedPartyID,
			RelationshipType: RelationshipTypeSpouse,
			CreatedAt:        time.Now(),
		},
	}

	updates := map[string]interface{}{
		"associations": associations,
	}

	err = party.UpdateParty(updates)
	assert.NoError(t, err)
	assert.Equal(t, associations, party.Associations())
}

func TestParty_UpdateParty_Associations_CircularReference(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "John Smith")
	require.NoError(t, err)

	// Attempt to create circular association (party associating with itself)
	associations := []PartyAssociation{
		{
			RelatedPartyID:   party.ID(),
			RelationshipType: RelationshipTypeSpouse,
			CreatedAt:        time.Now(),
		},
	}

	updates := map[string]interface{}{
		"associations": associations,
	}

	err = party.UpdateParty(updates)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrCircularAssociation)
}

func TestParty_UpdateParty_Associations_InvalidRelationshipType(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "John Smith")
	require.NoError(t, err)

	associations := []PartyAssociation{
		{
			RelatedPartyID:   uuid.New(),
			RelationshipType: RelationshipType("INVALID"),
			CreatedAt:        time.Now(),
		},
	}

	updates := map[string]interface{}{
		"associations": associations,
	}

	err = party.UpdateParty(updates)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidRelationshipType)
}

func TestParty_UpdateParty_MultipleFields(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "John Smith")
	require.NoError(t, err)

	demographics := DemographicData{
		IncomeLevel:    "HIGH",
		EducationLevel: "MASTER",
	}

	updates := map[string]interface{}{
		"displayName":  "Johnny",
		"demographics": demographics,
	}

	initialVersion := party.Version()
	err = party.UpdateParty(updates)
	assert.NoError(t, err)
	assert.Equal(t, "Johnny", party.DisplayName())
	assert.Equal(t, demographics, party.Demographics())
	// Version should be incremented for each field update
	assert.Greater(t, party.Version(), initialVersion)
}

func TestParty_UpdateParty_InvalidField(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "John Smith")
	require.NoError(t, err)

	updates := map[string]interface{}{
		"invalidField": "value",
	}

	err = party.UpdateParty(updates)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidUpdateField)
}

func TestParty_UpdateParty_WrongType(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "John Smith")
	require.NoError(t, err)

	updates := map[string]interface{}{
		"demographics": "not a DemographicData struct",
	}

	err = party.UpdateParty(updates)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidUpdateField)
}

// TestRelationshipType_IsValid tests relationship type validation
func TestRelationshipType_IsValid(t *testing.T) {
	tests := []struct {
		relType RelationshipType
		want    bool
	}{
		{RelationshipTypeSpouse, true},
		{RelationshipTypeDependent, true},
		{RelationshipTypeBusinessPartner, true},
		{RelationshipTypeGuarantor, true},
		{RelationshipTypeBeneficialOwner, true},
		{RelationshipType("INVALID"), false},
		{RelationshipType(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.relType), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.relType.IsValid())
		})
	}
}

// TestPoliticalExposureStatus_IsValid tests political exposure validation
func TestPoliticalExposureStatus_IsValid(t *testing.T) {
	tests := []struct {
		status PoliticalExposureStatus
		want   bool
	}{
		{PoliticalExposureNone, true},
		{PoliticalExposurePEP, true},
		{PoliticalExposureRCA, true},
		{PoliticalExposureStatus("INVALID"), false},
		{PoliticalExposureStatus(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.IsValid())
		})
	}
}

// TestReferenceType_IsValid tests reference type validation
func TestReferenceType_IsValid(t *testing.T) {
	tests := []struct {
		refType ReferenceType
		want    bool
	}{
		{ReferenceTypeGovernmentID, true},
		{ReferenceTypePassport, true},
		{ReferenceTypeDriverLicense, true},
		{ReferenceTypeUtilityBill, true},
		{ReferenceType("INVALID"), false},
		{ReferenceType(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.refType), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.refType.IsValid())
		})
	}
}

// TestParty_VersionIncrementOnBQUpdates tests version increments for BQ updates
func TestParty_VersionIncrementOnBQUpdates(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	initialVersion := party.Version()

	// Update demographics
	updates := map[string]interface{}{
		"demographics": DemographicData{IncomeLevel: "HIGH"},
	}
	err = party.UpdateParty(updates)
	require.NoError(t, err)
	assert.Equal(t, initialVersion+1, party.Version())

	// Update associations
	updates = map[string]interface{}{
		"associations": []PartyAssociation{
			{
				RelatedPartyID:   uuid.New(),
				RelationshipType: RelationshipTypeSpouse,
				CreatedAt:        time.Now(),
			},
		},
	}
	err = party.UpdateParty(updates)
	require.NoError(t, err)
	assert.Equal(t, initialVersion+2, party.Version())

	// Update reference data
	updates = map[string]interface{}{
		"referenceData": ReferenceData{
			PoliticalExposureStatus: PoliticalExposureNone,
		},
	}
	err = party.UpdateParty(updates)
	require.NoError(t, err)
	assert.Equal(t, initialVersion+3, party.Version())
}

// TestReconstructParty_WithBQData tests reconstruction with new BQ fields
func TestReconstructParty_WithBQData(t *testing.T) {
	id := uuid.New()
	createdAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	demographics := DemographicData{
		IncomeLevel:    "HIGH",
		EducationLevel: "MASTER",
	}

	associations := []PartyAssociation{
		{
			RelatedPartyID:   uuid.New(),
			RelationshipType: RelationshipTypeSpouse,
			CreatedAt:        createdAt,
		},
	}

	referenceData := ReferenceData{
		PoliticalExposureStatus: PoliticalExposureNone,
	}

	bankRelations := BankRelationship{
		AccountOfficerID:      uuid.New(),
		RelationshipManagerID: uuid.New(),
		AssignedBranch:        "London",
		RelationshipStartDate: createdAt,
	}

	party := ReconstructParty(
		id,
		PartyTypeOrganization,
		"Test Corp",
		"Test",
		PartyStatusActive,
		"12345678",
		ExternalReferenceTypeCompaniesHouse,
		associations,
		demographics,
		referenceData,
		bankRelations,
		[]AttributeEntry{},
		createdAt,
		updatedAt,
		5,
	)

	assert.Equal(t, id, party.ID())
	assert.Equal(t, demographics, party.Demographics())
	assert.Equal(t, associations, party.Associations())
	assert.Equal(t, referenceData, party.ReferenceData())
	assert.Equal(t, bankRelations, party.BankRelations())
	assert.Equal(t, int64(5), party.Version())
}
