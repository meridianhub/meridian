package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewParty_ValidInputs(t *testing.T) {
	tests := []struct {
		name      string
		partyType PartyType
		legalName string
	}{
		{
			name:      "person with simple name",
			partyType: PartyTypePerson,
			legalName: "John Smith",
		},
		{
			name:      "organization with simple name",
			partyType: PartyTypeOrganization,
			legalName: "Acme Corporation",
		},
		{
			name:      "person with single character name",
			partyType: PartyTypePerson,
			legalName: "X",
		},
		{
			name:      "organization with max length name",
			partyType: PartyTypeOrganization,
			legalName: strings.Repeat("A", 255),
		},
		{
			name:      "person with unicode characters",
			partyType: PartyTypePerson,
			legalName: "José García",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			party, err := NewParty(tt.partyType, tt.legalName)
			require.NoError(t, err)

			assert.NotEqual(t, uuid.Nil, party.ID())
			assert.Equal(t, tt.partyType, party.PartyType())
			assert.Equal(t, tt.legalName, party.LegalName())
			assert.Equal(t, PartyStatusActive, party.Status())
			assert.Equal(t, int64(1), party.Version())
			assert.Empty(t, party.DisplayName())
			assert.Empty(t, party.ExternalReference())
			assert.NotZero(t, party.CreatedAt())
			assert.NotZero(t, party.UpdatedAt())
		})
	}
}

func TestNewParty_InvalidInputs(t *testing.T) {
	tests := []struct {
		name        string
		partyType   PartyType
		legalName   string
		expectedErr error
	}{
		{
			name:        "empty legal name",
			partyType:   PartyTypePerson,
			legalName:   "",
			expectedErr: ErrInvalidLegalName,
		},
		{
			name:        "legal name too long",
			partyType:   PartyTypePerson,
			legalName:   strings.Repeat("A", 256),
			expectedErr: ErrInvalidLegalName,
		},
		{
			name:        "invalid party type",
			partyType:   PartyType("INVALID"),
			legalName:   "Valid Name",
			expectedErr: ErrInvalidPartyType,
		},
		{
			name:        "empty party type",
			partyType:   PartyType(""),
			legalName:   "Valid Name",
			expectedErr: ErrInvalidPartyType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			party, err := NewParty(tt.partyType, tt.legalName)

			assert.Nil(t, party)
			assert.Error(t, err)
			assert.ErrorIs(t, err, tt.expectedErr)
		})
	}
}

func TestParty_IDImmutability(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	originalID := party.ID()

	// Attempt operations that might change state
	_ = party.SetDisplayName("New Display Name")
	_ = party.Restrict()

	// ID should remain unchanged
	assert.Equal(t, originalID, party.ID())
}

func TestParty_SetDisplayName(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		wantErr     bool
	}{
		{
			name:        "valid display name",
			displayName: "Johnny",
			wantErr:     false,
		},
		{
			name:        "empty display name is valid",
			displayName: "",
			wantErr:     false,
		},
		{
			name:        "max length display name",
			displayName: strings.Repeat("B", 255),
			wantErr:     false,
		},
		{
			name:        "display name too long",
			displayName: strings.Repeat("C", 256),
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			party, err := NewParty(PartyTypePerson, "John Smith")
			require.NoError(t, err)

			err = party.SetDisplayName(tt.displayName)

			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidDisplayName)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.displayName, party.DisplayName())
			}
		})
	}
}

func TestParty_StatusTransitions_ActiveToRestricted(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)
	assert.Equal(t, PartyStatusActive, party.Status())

	err = party.Restrict()
	assert.NoError(t, err)
	assert.Equal(t, PartyStatusRestricted, party.Status())
}

func TestParty_StatusTransitions_RestrictedToActive(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.Restrict()
	require.NoError(t, err)
	assert.Equal(t, PartyStatusRestricted, party.Status())

	err = party.Activate()
	assert.NoError(t, err)
	assert.Equal(t, PartyStatusActive, party.Status())
}

func TestParty_StatusTransitions_ActiveToTerminated(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.Terminate()
	assert.NoError(t, err)
	assert.Equal(t, PartyStatusTerminated, party.Status())
}

func TestParty_StatusTransitions_RestrictedToTerminated(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.Restrict()
	require.NoError(t, err)

	err = party.Terminate()
	assert.NoError(t, err)
	assert.Equal(t, PartyStatusTerminated, party.Status())
}

func TestParty_StatusTransitions_TerminatedToActive_Invalid(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.Terminate()
	require.NoError(t, err)

	err = party.Activate()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
	assert.Equal(t, PartyStatusTerminated, party.Status())
}

func TestParty_StatusTransitions_TerminatedToRestricted_Invalid(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	err = party.Terminate()
	require.NoError(t, err)

	err = party.Restrict()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidStatusTransition)
	assert.Equal(t, PartyStatusTerminated, party.Status())
}

func TestParty_SetExternalReference_CompaniesHouse(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		wantErr   bool
	}{
		{
			name:      "valid 8 digit number",
			reference: "12345678",
			wantErr:   false,
		},
		{
			name:      "valid with leading letters",
			reference: "SC123456",
			wantErr:   false,
		},
		{
			name:      "valid NI company",
			reference: "NI654321",
			wantErr:   false,
		},
		{
			name:      "too short",
			reference: "12345",
			wantErr:   true,
		},
		{
			name:      "too long",
			reference: "123456789",
			wantErr:   true,
		},
		{
			name:      "invalid characters",
			reference: "1234-678",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			party, err := NewParty(PartyTypeOrganization, "Test Corp")
			require.NoError(t, err)

			err = party.SetExternalReference(tt.reference, ExternalReferenceTypeCompaniesHouse)

			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidExternalReference)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.reference, party.ExternalReference())
				assert.Equal(t, ExternalReferenceTypeCompaniesHouse, party.ExternalReferenceType())
			}
		})
	}
}

func TestParty_SetExternalReference_LEI(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		wantErr   bool
	}{
		{
			name:      "valid LEI",
			reference: "529900HNOAA1KXQJUQ27",
			wantErr:   false,
		},
		{
			name:      "all numbers",
			reference: "12345678901234567890",
			wantErr:   false,
		},
		{
			name:      "too short",
			reference: "529900HNOAA1KXQJUQ2",
			wantErr:   true,
		},
		{
			name:      "too long",
			reference: "529900HNOAA1KXQJUQ271",
			wantErr:   true,
		},
		{
			name:      "lowercase invalid",
			reference: "529900hnoaa1kxqjuq27",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			party, err := NewParty(PartyTypeOrganization, "Test Corp")
			require.NoError(t, err)

			err = party.SetExternalReference(tt.reference, ExternalReferenceTypeLEI)

			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidExternalReference)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.reference, party.ExternalReference())
				assert.Equal(t, ExternalReferenceTypeLEI, party.ExternalReferenceType())
			}
		})
	}
}

func TestParty_SetExternalReference_NationalID(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		wantErr   bool
	}{
		{
			name:      "valid national ID",
			reference: "AB123456789",
			wantErr:   false,
		},
		{
			name:      "minimum length",
			reference: "AB",
			wantErr:   false,
		},
		{
			name:      "maximum length",
			reference: "12345678901234567890",
			wantErr:   false,
		},
		{
			name:      "too short",
			reference: "A",
			wantErr:   true,
		},
		{
			name:      "too long",
			reference: "123456789012345678901",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			party, err := NewParty(PartyTypePerson, "Test Person")
			require.NoError(t, err)

			err = party.SetExternalReference(tt.reference, ExternalReferenceTypeNationalID)

			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidExternalReference)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.reference, party.ExternalReference())
				assert.Equal(t, ExternalReferenceTypeNationalID, party.ExternalReferenceType())
			}
		})
	}
}

func TestParty_SetExternalReference_TaxID(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		wantErr   bool
	}{
		{
			name:      "valid tax ID",
			reference: "GB123456789",
			wantErr:   false,
		},
		{
			name:      "minimum length",
			reference: "12345",
			wantErr:   false,
		},
		{
			name:      "maximum length",
			reference: "12345678901234567890",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			party, err := NewParty(PartyTypeOrganization, "Test Corp")
			require.NoError(t, err)

			err = party.SetExternalReference(tt.reference, ExternalReferenceTypeTaxID)

			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidExternalReference)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.reference, party.ExternalReference())
				assert.Equal(t, ExternalReferenceTypeTaxID, party.ExternalReferenceType())
			}
		})
	}
}

func TestParty_SetExternalReference_CannotOverwrite(t *testing.T) {
	party, err := NewParty(PartyTypeOrganization, "Test Corp")
	require.NoError(t, err)

	// Set initial reference
	err = party.SetExternalReference("12345678", ExternalReferenceTypeCompaniesHouse)
	require.NoError(t, err)

	// Attempt to set another reference
	err = party.SetExternalReference("529900HNOAA1KXQJUQ27", ExternalReferenceTypeLEI)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrExternalReferenceExists)

	// Original reference should remain
	assert.Equal(t, "12345678", party.ExternalReference())
	assert.Equal(t, ExternalReferenceTypeCompaniesHouse, party.ExternalReferenceType())
}

func TestParty_SetExternalReference_EmptyReference(t *testing.T) {
	party, err := NewParty(PartyTypeOrganization, "Test Corp")
	require.NoError(t, err)

	err = party.SetExternalReference("", ExternalReferenceTypeCompaniesHouse)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidExternalReference)
}

func TestParty_SetExternalReference_InvalidType(t *testing.T) {
	party, err := NewParty(PartyTypeOrganization, "Test Corp")
	require.NoError(t, err)

	err = party.SetExternalReference("12345678", ExternalReferenceType("INVALID"))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidExternalReference)
}

func TestPartyType_IsValid(t *testing.T) {
	tests := []struct {
		partyType PartyType
		want      bool
	}{
		{PartyTypePerson, true},
		{PartyTypeOrganization, true},
		{PartyType("INVALID"), false},
		{PartyType(""), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.partyType), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.partyType.IsValid())
		})
	}
}

func TestValidateExternalReference(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		refType   ExternalReferenceType
		wantErr   bool
	}{
		{
			name:      "valid companies house",
			reference: "12345678",
			refType:   ExternalReferenceTypeCompaniesHouse,
			wantErr:   false,
		},
		{
			name:      "valid LEI",
			reference: "529900HNOAA1KXQJUQ27",
			refType:   ExternalReferenceTypeLEI,
			wantErr:   false,
		},
		{
			name:      "valid national ID",
			reference: "AB123456789",
			refType:   ExternalReferenceTypeNationalID,
			wantErr:   false,
		},
		{
			name:      "valid short national ID",
			reference: "DCC",
			refType:   ExternalReferenceTypeNationalID,
			wantErr:   false,
		},
		{
			name:      "valid tax ID",
			reference: "GB123456789",
			refType:   ExternalReferenceTypeTaxID,
			wantErr:   false,
		},
		{
			name:      "empty reference",
			reference: "",
			refType:   ExternalReferenceTypeCompaniesHouse,
			wantErr:   true,
		},
		{
			name:      "invalid type",
			reference: "12345678",
			refType:   ExternalReferenceType("UNKNOWN"),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateExternalReference(tt.reference, tt.refType)

			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidExternalReference)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestReconstructParty(t *testing.T) {
	id := uuid.New()
	createdAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	party := ReconstructParty(
		id,
		PartyTypeOrganization,
		"Test Corp",
		"Test",
		PartyStatusRestricted,
		"12345678",
		ExternalReferenceTypeCompaniesHouse,
		[]PartyAssociation{},
		DemographicData{},
		ReferenceData{},
		BankRelationship{},
		[]AttributeEntry{},
		createdAt,
		updatedAt,
		5,
	)

	assert.Equal(t, id, party.ID())
	assert.Equal(t, PartyTypeOrganization, party.PartyType())
	assert.Equal(t, "Test Corp", party.LegalName())
	assert.Equal(t, "Test", party.DisplayName())
	assert.Equal(t, PartyStatusRestricted, party.Status())
	assert.Equal(t, "12345678", party.ExternalReference())
	assert.Equal(t, ExternalReferenceTypeCompaniesHouse, party.ExternalReferenceType())
	assert.Equal(t, createdAt, party.CreatedAt())
	assert.Equal(t, updatedAt, party.UpdatedAt())
	assert.Equal(t, int64(5), party.Version())
}

func TestParty_UpdatedAtChangesOnMutation(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	originalUpdatedAt := party.UpdatedAt()

	// Small delay to ensure time difference
	err = party.SetDisplayName("New Name")
	require.NoError(t, err)

	// UpdatedAt should be same or later
	assert.True(t, !party.UpdatedAt().Before(originalUpdatedAt))
}

func TestParty_CreatedAtDoesNotChange(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	originalCreatedAt := party.CreatedAt()

	// Perform mutations
	_ = party.SetDisplayName("New Name")
	_ = party.Restrict()

	// CreatedAt should remain unchanged
	assert.Equal(t, originalCreatedAt, party.CreatedAt())
}

func TestParty_VersionIncrementsOnMutation(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	// Initial version should be 1
	assert.Equal(t, int64(1), party.Version())

	// SetDisplayName should increment version
	err = party.SetDisplayName("New Name")
	require.NoError(t, err)
	assert.Equal(t, int64(2), party.Version())

	// Restrict should increment version
	err = party.Restrict()
	require.NoError(t, err)
	assert.Equal(t, int64(3), party.Version())

	// Activate should increment version
	err = party.Activate()
	require.NoError(t, err)
	assert.Equal(t, int64(4), party.Version())

	// Terminate should increment version
	err = party.Terminate()
	require.NoError(t, err)
	assert.Equal(t, int64(5), party.Version())
}

func TestParty_VersionIncrementsOnSetExternalReference(t *testing.T) {
	party, err := NewParty(PartyTypeOrganization, "Test Corp")
	require.NoError(t, err)

	assert.Equal(t, int64(1), party.Version())

	err = party.SetExternalReference("12345678", ExternalReferenceTypeCompaniesHouse)
	require.NoError(t, err)
	assert.Equal(t, int64(2), party.Version())
}

func TestParty_VersionDoesNotIncrementOnFailedMutation(t *testing.T) {
	party, err := NewParty(PartyTypePerson, "Test Person")
	require.NoError(t, err)

	// Terminate the party
	err = party.Terminate()
	require.NoError(t, err)
	versionAfterTerminate := party.Version()

	// Attempting to activate a terminated party should fail
	err = party.Activate()
	assert.Error(t, err)

	// Version should not have changed
	assert.Equal(t, versionAfterTerminate, party.Version())
}
