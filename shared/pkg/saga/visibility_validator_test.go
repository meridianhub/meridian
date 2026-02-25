package saga

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVisibilityValidator_Validate_AllVisible(t *testing.T) {
	partyA := uuid.New()
	partyB := uuid.New()
	partyC := uuid.New()

	scope := &PartyScope{
		PartyID:        partyA,
		PartyType:      PartyTypeOrganization,
		VisibleParties: []uuid.UUID{partyA, partyB, partyC},
		TenantID:       "tenant-1",
	}

	manifest := &VisibilityManifest{
		ReferencedParties: []uuid.UUID{partyA, partyB},
	}

	validator := NewVisibilityValidator()
	result := validator.Validate(scope, manifest)

	assert.True(t, result.Valid)
	assert.Empty(t, result.InvisibleParties)
}

func TestVisibilityValidator_Validate_SomeInvisible(t *testing.T) {
	partyA := uuid.New()
	partyB := uuid.New()
	invisibleParty := uuid.New()

	scope := &PartyScope{
		PartyID:        partyA,
		PartyType:      PartyTypeIndividual,
		VisibleParties: []uuid.UUID{partyA},
		TenantID:       "tenant-1",
	}

	manifest := &VisibilityManifest{
		ReferencedParties: []uuid.UUID{partyA, partyB, invisibleParty},
	}

	validator := NewVisibilityValidator()
	result := validator.Validate(scope, manifest)

	assert.False(t, result.Valid)
	assert.Len(t, result.InvisibleParties, 2)
	assert.Contains(t, result.InvisibleParties, partyB)
	assert.Contains(t, result.InvisibleParties, invisibleParty)
}

func TestVisibilityValidator_Validate_NilScope(t *testing.T) {
	partyA := uuid.New()

	manifest := &VisibilityManifest{
		ReferencedParties: []uuid.UUID{partyA},
	}

	validator := NewVisibilityValidator()
	result := validator.Validate(nil, manifest)

	// Nil scope means party isolation is disabled, should pass
	assert.True(t, result.Valid)
}

func TestVisibilityValidator_Validate_NilManifest(t *testing.T) {
	partyA := uuid.New()

	scope := &PartyScope{
		PartyID:        partyA,
		PartyType:      PartyTypeIndividual,
		VisibleParties: []uuid.UUID{partyA},
		TenantID:       "tenant-1",
	}

	validator := NewVisibilityValidator()
	result := validator.Validate(scope, nil)

	// Nil manifest means nothing to check, should pass
	assert.True(t, result.Valid)
}

func TestVisibilityValidator_Validate_EmptyManifest(t *testing.T) {
	partyA := uuid.New()

	scope := &PartyScope{
		PartyID:        partyA,
		PartyType:      PartyTypeIndividual,
		VisibleParties: []uuid.UUID{partyA},
		TenantID:       "tenant-1",
	}

	manifest := &VisibilityManifest{
		ReferencedParties: []uuid.UUID{},
	}

	validator := NewVisibilityValidator()
	result := validator.Validate(scope, manifest)

	// Empty manifest means nothing to check, should pass
	assert.True(t, result.Valid)
}

func TestVisibilityValidator_ValidateOrError(t *testing.T) {
	partyA := uuid.New()
	invisibleParty := uuid.New()

	scope := &PartyScope{
		PartyID:        partyA,
		PartyType:      PartyTypeIndividual,
		VisibleParties: []uuid.UUID{partyA},
		TenantID:       "tenant-1",
	}

	manifest := &VisibilityManifest{
		ReferencedParties: []uuid.UUID{invisibleParty},
	}

	validator := NewVisibilityValidator()
	err := validator.ValidateOrError(scope, manifest)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrVisibilityViolation)
	assert.Contains(t, err.Error(), partyA.String())
	assert.Contains(t, err.Error(), invisibleParty.String())
}

func TestExtractPartyReferencesFromInput_SinglePartyID(t *testing.T) {
	partyID := uuid.New()
	input := map[string]interface{}{
		"party_id": partyID.String(),
	}

	refs := ExtractPartyReferencesFromInput(input)

	assert.Len(t, refs, 1)
	assert.Equal(t, partyID, refs[0])
}

func TestExtractPartyReferencesFromInput_MultipleFields(t *testing.T) {
	fromParty := uuid.New()
	toParty := uuid.New()
	counterparty := uuid.New()

	input := map[string]interface{}{
		"from_party":      fromParty.String(),
		"to_party":        toParty.String(),
		"counterparty_id": counterparty.String(),
	}

	refs := ExtractPartyReferencesFromInput(input)

	assert.Len(t, refs, 3)
	assert.Contains(t, refs, fromParty)
	assert.Contains(t, refs, toParty)
	assert.Contains(t, refs, counterparty)
}

func TestExtractPartyReferencesFromInput_PartyIDsArray(t *testing.T) {
	party1 := uuid.New()
	party2 := uuid.New()

	input := map[string]interface{}{
		"party_ids": []interface{}{
			party1.String(),
			party2.String(),
		},
	}

	refs := ExtractPartyReferencesFromInput(input)

	assert.Len(t, refs, 2)
	assert.Contains(t, refs, party1)
	assert.Contains(t, refs, party2)
}

func TestExtractPartyReferencesFromInput_DeduplicatesParties(t *testing.T) {
	partyID := uuid.New()
	input := map[string]interface{}{
		"party_id":        partyID.String(),
		"from_party":      partyID.String(), // Same ID
		"counterparty_id": partyID.String(), // Same ID again
	}

	refs := ExtractPartyReferencesFromInput(input)

	// Should only contain the party once
	assert.Len(t, refs, 1)
	assert.Equal(t, partyID, refs[0])
}

func TestExtractPartyReferencesFromInput_IgnoresInvalidUUIDs(t *testing.T) {
	validParty := uuid.New()
	input := map[string]interface{}{
		"party_id":        validParty.String(),
		"counterparty_id": "not-a-valid-uuid",
		"from_party":      "", // Empty string
	}

	refs := ExtractPartyReferencesFromInput(input)

	// Should only contain the valid party
	assert.Len(t, refs, 1)
	assert.Equal(t, validParty, refs[0])
}

func TestExtractPartyReferencesFromInput_PartiesArrayWithObjects(t *testing.T) {
	party1 := uuid.New()
	party2 := uuid.New()

	input := map[string]interface{}{
		"parties": []interface{}{
			map[string]interface{}{
				"party_id": party1.String(),
				"name":     "Party One",
			},
			map[string]interface{}{
				"party_id": party2.String(),
				"name":     "Party Two",
			},
		},
	}

	refs := ExtractPartyReferencesFromInput(input)

	assert.Len(t, refs, 2)
	assert.Contains(t, refs, party1)
	assert.Contains(t, refs, party2)
}

func TestExtractPartyReferencesFromInput_NilInput(t *testing.T) {
	refs := ExtractPartyReferencesFromInput(nil)
	assert.Nil(t, refs)
}

func TestNewVisibilityManifestFromInput(t *testing.T) {
	partyID := uuid.New()
	counterpartyID := uuid.New()

	input := map[string]interface{}{
		"party_id":           partyID.String(),
		"counterparty_id":    counterpartyID.String(),
		"authorized_lookups": []interface{}{"resolve_account", "internal_account"},
	}

	manifest := NewVisibilityManifestFromInput(input)

	assert.Len(t, manifest.ReferencedParties, 2)
	assert.Contains(t, manifest.ReferencedParties, partyID)
	assert.Contains(t, manifest.ReferencedParties, counterpartyID)
	assert.Len(t, manifest.AuthorizedLookups, 2)
	assert.Contains(t, manifest.AuthorizedLookups, "resolve_account")
	assert.Contains(t, manifest.AuthorizedLookups, "internal_account")
}

func TestVisibilityManifest_isAuthorizedLookup(t *testing.T) {
	manifest := &VisibilityManifest{
		AuthorizedLookups: []string{"resolve_account"},
	}

	assert.True(t, manifest.isAuthorizedLookup("resolve_account"))
	assert.False(t, manifest.isAuthorizedLookup("internal_account"))
	assert.False(t, manifest.isAuthorizedLookup("unknown_lookup"))
}
