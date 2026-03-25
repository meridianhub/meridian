package persistence

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// --- Unit tests for toJSONB helper ---

func TestToJSONB_ValidJSONObject(t *testing.T) {
	input := `{"key":"value","nested":{"a":1}}`
	result := toJSONB(input)
	assert.Equal(t, input, result)
}

func TestToJSONB_ValidJSONArray(t *testing.T) {
	input := `[1,2,3]`
	result := toJSONB(input)
	assert.Equal(t, input, result)
}

func TestToJSONB_PlainString(t *testing.T) {
	result := toJSONB("hello world")
	assert.Equal(t, `"hello world"`, result)
}

func TestToJSONB_EmptyString(t *testing.T) {
	result := toJSONB("")
	assert.Equal(t, `""`, result)
}

func TestToJSONB_JSONPrimitiveNull(t *testing.T) {
	// "null" is valid JSON but not an object/array, should be stored as string
	result := toJSONB("null")
	assert.Equal(t, `"null"`, result)
}

func TestToJSONB_JSONPrimitiveNumber(t *testing.T) {
	result := toJSONB("42")
	assert.Equal(t, `"42"`, result)
}

func TestToJSONB_JSONPrimitiveBoolean(t *testing.T) {
	result := toJSONB("true")
	assert.Equal(t, `"true"`, result)
}

func TestToJSONB_WhitespaceAroundObject(t *testing.T) {
	input := `  {"key":"value"}  `
	result := toJSONB(input)
	// trimmed starts with '{' and input is valid JSON, so returned as-is
	assert.Equal(t, input, result)
}

func TestToJSONB_WhitespaceAroundArray(t *testing.T) {
	input := `  [1, 2, 3]  `
	result := toJSONB(input)
	assert.Equal(t, input, result)
}

func TestToJSONB_InvalidJSONStartingWithBrace(t *testing.T) {
	// Starts with '{' but is not valid JSON
	input := `{not valid json`
	result := toJSONB(input)
	// json.Valid returns false, so it's marshaled as a string
	assert.Equal(t, `"{not valid json"`, result)
}

func TestToJSONB_StringWithSpecialChars(t *testing.T) {
	result := toJSONB(`has "quotes" and \backslash`)
	// json.Marshal escapes these characters
	assert.Contains(t, result, `\"quotes\"`)
	assert.Contains(t, result, `\\backslash`)
}

func TestToJSONB_EmptyObject(t *testing.T) {
	result := toJSONB("{}")
	assert.Equal(t, "{}", result)
}

func TestToJSONB_EmptyArray(t *testing.T) {
	result := toJSONB("[]")
	assert.Equal(t, "[]", result)
}

// --- Unit tests for EncodePartyCursor / DecodePartyCursor round trip ---

func TestEncodeDecodePartyCursor_RoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Nanosecond)
	id := uuid.New()

	cursor := PartyCursor{CreatedAt: now, ID: id}
	encoded := EncodePartyCursor(cursor)

	decoded, err := DecodePartyCursor(encoded)
	require.NoError(t, err)
	assert.True(t, cursor.CreatedAt.Equal(decoded.CreatedAt))
	assert.Equal(t, cursor.ID, decoded.ID)
}

func TestDecodePartyCursor_EmptyString(t *testing.T) {
	cursor, err := DecodePartyCursor("")
	require.NoError(t, err)
	assert.True(t, cursor.CreatedAt.IsZero())
	assert.Equal(t, uuid.Nil, cursor.ID)
}

func TestDecodePartyCursor_RejectsTokenWithExtraPipes(t *testing.T) {
	// SplitN with N=2 means extra pipes end up in the UUID part, which should fail UUID parse
	ts := time.Now().Format(time.RFC3339Nano)
	data := ts + "|" + uuid.New().String() + "|extra"
	token := base64.URLEncoding.EncodeToString([]byte(data))
	_, err := DecodePartyCursor(token)
	assert.ErrorIs(t, err, ErrInvalidCursor)
}

// --- Unit tests for isDuplicateKeyError ---

func TestIsDuplicateKeyError_NilError(t *testing.T) {
	assert.False(t, isDuplicateKeyError(nil))
}

func TestIsDuplicateKeyError_DuplicateKeyMessage(t *testing.T) {
	err := &mockError{msg: "ERROR: duplicate key value violates unique constraint"}
	assert.True(t, isDuplicateKeyError(err))
}

func TestIsDuplicateKeyError_PostgresCode23505(t *testing.T) {
	err := &mockError{msg: "pq: error code 23505"}
	assert.True(t, isDuplicateKeyError(err))
}

func TestIsDuplicateKeyError_UniqueConstraint(t *testing.T) {
	err := &mockError{msg: "unique constraint violation on column xyz"}
	assert.True(t, isDuplicateKeyError(err))
}

func TestIsDuplicateKeyError_UnrelatedError(t *testing.T) {
	err := &mockError{msg: "connection refused"}
	assert.False(t, isDuplicateKeyError(err))
}

func TestIsDuplicateKeyError_GormDuplicatedKey(t *testing.T) {
	assert.True(t, isDuplicateKeyError(gorm.ErrDuplicatedKey))
}

func TestIsDuplicateKeyError_WrappedGormDuplicatedKey(t *testing.T) {
	err := fmt.Errorf("save failed: %w", gorm.ErrDuplicatedKey)
	assert.True(t, isDuplicateKeyError(err))
}

type mockError struct {
	msg string
}

func (e *mockError) Error() string { return e.msg }

// --- Integration tests for ListParties ---

func TestListParties_BasicPagination(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	// Create 5 parties
	for i := 0; i < 5; i++ {
		p, err := domain.NewParty(domain.PartyTypePerson, "Person "+string(rune('A'+i)))
		require.NoError(t, err)
		err = repo.Save(ctx, p)
		require.NoError(t, err)
	}

	// First page: limit 3
	result, err := repo.ListParties(ctx, ListPartiesParams{Limit: 3})
	require.NoError(t, err)
	assert.Len(t, result.Parties, 3)
	assert.Equal(t, int64(5), result.TotalCount)
	assert.NotEmpty(t, result.NextCursor, "Should have next page cursor")

	// Second page
	cursor, err := DecodePartyCursor(result.NextCursor)
	require.NoError(t, err)
	result2, err := repo.ListParties(ctx, ListPartiesParams{Limit: 3, Cursor: cursor})
	require.NoError(t, err)
	assert.Len(t, result2.Parties, 2)
	assert.Equal(t, int64(5), result2.TotalCount)
	assert.Empty(t, result2.NextCursor, "No more pages")
}

func TestListParties_FilterByPartyType(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	p1, err := domain.NewParty(domain.PartyTypePerson, "Person One")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, p1))

	p2, err := domain.NewParty(domain.PartyTypeOrganization, "Org One")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, p2))

	result, err := repo.ListParties(ctx, ListPartiesParams{
		PartyType: string(domain.PartyTypePerson),
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.TotalCount)
	assert.Equal(t, domain.PartyTypePerson, result.Parties[0].PartyType())
}

func TestListParties_FilterByStatus(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	p1, err := domain.NewParty(domain.PartyTypePerson, "Active Person")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, p1))

	p2, err := domain.NewParty(domain.PartyTypePerson, "Another Person")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, p2))

	// Create a suspended party to ensure the filter actually excludes it
	p3, err := domain.NewParty(domain.PartyTypePerson, "Suspended Person")
	require.NoError(t, err)
	require.NoError(t, p3.Suspend())
	require.NoError(t, repo.Save(ctx, p3))

	result, err := repo.ListParties(ctx, ListPartiesParams{
		Status: string(domain.PartyStatusActive),
		Limit:  10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), result.TotalCount)
	for _, party := range result.Parties {
		assert.Equal(t, domain.PartyStatusActive, party.Status())
	}
}

func TestListParties_SearchQuery(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	p1, err := domain.NewParty(domain.PartyTypePerson, "Alice Wonderland")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, p1))

	p2, err := domain.NewParty(domain.PartyTypePerson, "Bob Builder")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, p2))

	// Search for "alice" (case-insensitive)
	result, err := repo.ListParties(ctx, ListPartiesParams{
		SearchQuery: "alice",
		Limit:       10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.TotalCount)
	assert.Equal(t, "Alice Wonderland", result.Parties[0].LegalName())
}

func TestListParties_SearchByDisplayName(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	p1, err := domain.NewParty(domain.PartyTypePerson, "John Smith")
	require.NoError(t, err)
	require.NoError(t, p1.SetDisplayName("Johnny"))
	require.NoError(t, repo.Save(ctx, p1))

	result, err := repo.ListParties(ctx, ListPartiesParams{
		SearchQuery: "johnny",
		Limit:       10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.TotalCount)
}

func TestListParties_EmptyResult(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	result, err := repo.ListParties(ctx, ListPartiesParams{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, result.Parties)
	assert.Equal(t, int64(0), result.TotalCount)
	assert.Empty(t, result.NextCursor)
}

func TestListParties_ExcludesSoftDeleted(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	p, err := domain.NewParty(domain.PartyTypePerson, "To Delete")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, p))

	require.NoError(t, repo.Delete(ctx, p.ID()))

	result, err := repo.ListParties(ctx, ListPartiesParams{Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(0), result.TotalCount)
}

func TestListParties_CombinedFilters(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	p1, err := domain.NewParty(domain.PartyTypePerson, "Alice Active")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, p1))

	p2, err := domain.NewParty(domain.PartyTypeOrganization, "Alice Corp")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, p2))

	// Search "alice" filtered by PERSON type
	result, err := repo.ListParties(ctx, ListPartiesParams{
		PartyType:   string(domain.PartyTypePerson),
		SearchQuery: "alice",
		Limit:       10,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.TotalCount)
	assert.Equal(t, "Alice Active", result.Parties[0].LegalName())
}

// --- Integration tests for Demographic operations ---

func TestSaveDemographic_CreateNew(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	// Need PartyDemographicEntity table
	require.NoError(t, db.AutoMigrate(&PartyDemographicEntity{}))

	partyID := uuid.New()
	err := repo.SaveDemographic(ctx, partyID, `{"income":"high"}`, `[{"company":"Acme"}]`)
	require.NoError(t, err)

	demo, err := repo.FindDemographic(ctx, partyID)
	require.NoError(t, err)
	require.NotNil(t, demo)
	assert.Equal(t, partyID, demo.PartyID)
	require.NotNil(t, demo.SocioEconomicData)
	assert.Contains(t, *demo.SocioEconomicData, "income")
}

func TestSaveDemographic_UpdateExisting(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	require.NoError(t, db.AutoMigrate(&PartyDemographicEntity{}))

	partyID := uuid.New()
	err := repo.SaveDemographic(ctx, partyID, `{"income":"low"}`, `[]`)
	require.NoError(t, err)

	// Update
	err = repo.SaveDemographic(ctx, partyID, `{"income":"high"}`, `[{"company":"NewCo"}]`)
	require.NoError(t, err)

	demo, err := repo.FindDemographic(ctx, partyID)
	require.NoError(t, err)
	require.NotNil(t, demo)
	assert.Contains(t, *demo.SocioEconomicData, "high")
}

func TestSaveDemographic_PlainStringValues(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	require.NoError(t, db.AutoMigrate(&PartyDemographicEntity{}))

	partyID := uuid.New()
	// Plain strings should be wrapped as JSON strings by toJSONB
	err := repo.SaveDemographic(ctx, partyID, "middle-class", "employed")
	require.NoError(t, err)

	demo, err := repo.FindDemographic(ctx, partyID)
	require.NoError(t, err)
	require.NotNil(t, demo)
	require.NotNil(t, demo.SocioEconomicData)
	assert.Equal(t, `"middle-class"`, *demo.SocioEconomicData)
}

func TestFindDemographic_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	require.NoError(t, db.AutoMigrate(&PartyDemographicEntity{}))

	demo, err := repo.FindDemographic(ctx, uuid.New())
	require.NoError(t, err)
	assert.Nil(t, demo)
}

// --- Integration tests for Reference operations ---

func TestSaveReference_SingleReference(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	require.NoError(t, db.AutoMigrate(&PartyReferenceEntity{}))

	partyID := uuid.New()
	err := repo.SaveReference(ctx, partyID, "PASSPORT", "AB123456", "UK Government", "2030-12-31")
	require.NoError(t, err)

	refs, err := repo.FindReferences(ctx, partyID)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, "PASSPORT", refs[0].ReferenceType)
	assert.Equal(t, "AB123456", refs[0].ReferenceValue)
	require.NotNil(t, refs[0].IssuingAuthority)
	assert.Equal(t, "UK Government", *refs[0].IssuingAuthority)
	require.NotNil(t, refs[0].ExpiryDate)
}

func TestSaveReferences_Multiple(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	require.NoError(t, db.AutoMigrate(&PartyReferenceEntity{}))

	partyID := uuid.New()
	refs := []ReferenceInput{
		{RefType: "PASSPORT", RefValue: "AB123", IssuingAuthority: "UK", ExpiryDate: "2030-01-01"},
		{RefType: "DRIVING_LICENSE", RefValue: "DL456", IssuingAuthority: "", ExpiryDate: ""},
	}
	err := repo.SaveReferences(ctx, partyID, refs)
	require.NoError(t, err)

	found, err := repo.FindReferences(ctx, partyID)
	require.NoError(t, err)
	assert.Len(t, found, 2)
}

func TestSaveReferences_Empty(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	err := repo.SaveReferences(ctx, uuid.New(), []ReferenceInput{})
	require.NoError(t, err)
}

func TestSaveReference_NoOptionalFields(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	require.NoError(t, db.AutoMigrate(&PartyReferenceEntity{}))

	partyID := uuid.New()
	err := repo.SaveReference(ctx, partyID, "TAX_ID", "123-45-6789", "", "")
	require.NoError(t, err)

	refs, err := repo.FindReferences(ctx, partyID)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Nil(t, refs[0].IssuingAuthority)
	assert.Nil(t, refs[0].ExpiryDate)
}

func TestSaveReference_InvalidExpiryDate(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	require.NoError(t, db.AutoMigrate(&PartyReferenceEntity{}))

	partyID := uuid.New()
	// Invalid date format - should be silently ignored (no ExpiryDate set)
	err := repo.SaveReference(ctx, partyID, "PASSPORT", "XY999", "", "not-a-date")
	require.NoError(t, err)

	refs, err := repo.FindReferences(ctx, partyID)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Nil(t, refs[0].ExpiryDate)
}

func TestFindReferences_Empty(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	require.NoError(t, db.AutoMigrate(&PartyReferenceEntity{}))

	refs, err := repo.FindReferences(ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, refs)
}

// --- Integration tests for BankRelation operations ---

func TestSaveBankRelation_CreateNew(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	require.NoError(t, db.AutoMigrate(&PartyBankRelationEntity{}))

	partyID := uuid.New()
	err := repo.SaveBankRelation(ctx, partyID, "officer-1", "manager-1", "branch-london")
	require.NoError(t, err)

	br, err := repo.FindBankRelation(ctx, partyID)
	require.NoError(t, err)
	require.NotNil(t, br)
	assert.Equal(t, partyID, br.PartyID)
	require.NotNil(t, br.AccountOfficerID)
	assert.Equal(t, "officer-1", *br.AccountOfficerID)
	require.NotNil(t, br.RelationshipManagerID)
	assert.Equal(t, "manager-1", *br.RelationshipManagerID)
	require.NotNil(t, br.AssignedBranch)
	assert.Equal(t, "branch-london", *br.AssignedBranch)
}

func TestSaveBankRelation_UpdateExisting(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	require.NoError(t, db.AutoMigrate(&PartyBankRelationEntity{}))

	partyID := uuid.New()
	err := repo.SaveBankRelation(ctx, partyID, "officer-1", "manager-1", "branch-a")
	require.NoError(t, err)

	// Update
	err = repo.SaveBankRelation(ctx, partyID, "officer-2", "manager-2", "branch-b")
	require.NoError(t, err)

	br, err := repo.FindBankRelation(ctx, partyID)
	require.NoError(t, err)
	require.NotNil(t, br)
	assert.Equal(t, "officer-2", *br.AccountOfficerID)
	assert.Equal(t, "branch-b", *br.AssignedBranch)
}

func TestSaveBankRelation_EmptyOptionalFields(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	require.NoError(t, db.AutoMigrate(&PartyBankRelationEntity{}))

	partyID := uuid.New()
	err := repo.SaveBankRelation(ctx, partyID, "", "", "")
	require.NoError(t, err)

	br, err := repo.FindBankRelation(ctx, partyID)
	require.NoError(t, err)
	require.NotNil(t, br)
	assert.Nil(t, br.AccountOfficerID)
	assert.Nil(t, br.RelationshipManagerID)
	assert.Nil(t, br.AssignedBranch)
}

func TestFindBankRelation_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	require.NoError(t, db.AutoMigrate(&PartyBankRelationEntity{}))

	br, err := repo.FindBankRelation(ctx, uuid.New())
	require.NoError(t, err)
	assert.Nil(t, br)
}

// --- Integration tests for Association edge cases ---

func TestUpdateAssociation_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	partyID := uuid.New()
	relatedID := uuid.New()
	assocID, err := repo.SaveAssociation(ctx, partyID, relatedID, "PARENT_OF")
	require.NoError(t, err)

	updated, err := repo.UpdateAssociation(ctx, assocID, "GUARDIAN_OF")
	require.NoError(t, err)
	assert.Equal(t, "GUARDIAN_OF", updated.RelationshipType)
	assert.Equal(t, partyID, updated.PartyID)
	assert.Equal(t, relatedID, updated.RelatedPartyID)
}

func TestUpdateAssociation_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	_, err := repo.UpdateAssociation(ctx, uuid.New(), "PARENT_OF")
	assert.Error(t, err)
}

func TestCheckCircularAssociation_SameParty(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	id := uuid.New()
	isCircular, err := repo.CheckCircularAssociation(ctx, id, id)
	require.NoError(t, err)
	assert.True(t, isCircular)
}

func TestCheckCircularAssociation_ReverseExists(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	a := uuid.New()
	b := uuid.New()

	_, err := repo.SaveAssociation(ctx, b, a, "PARENT_OF")
	require.NoError(t, err)

	isCircular, err := repo.CheckCircularAssociation(ctx, a, b)
	require.NoError(t, err)
	assert.True(t, isCircular)
}

func TestCheckCircularAssociation_NoReverse(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	a := uuid.New()
	b := uuid.New()

	isCircular, err := repo.CheckCircularAssociation(ctx, a, b)
	require.NoError(t, err)
	assert.False(t, isCircular)
}

func TestSaveAssociation_DefaultValues(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	partyID := uuid.New()
	relatedID := uuid.New()
	assocID, err := repo.SaveAssociation(ctx, partyID, relatedID, "PARENT_OF")
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, assocID)

	associations, err := repo.FindAssociations(ctx, partyID)
	require.NoError(t, err)
	require.Len(t, associations, 1)
	assert.Equal(t, "ACTIVE", associations[0].Status)
}

func TestSaveAssociationWithInput_NilInput(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	partyID := uuid.New()
	relatedID := uuid.New()
	assocID, err := repo.SaveAssociationWithInput(ctx, partyID, relatedID, "AGENT", nil)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, assocID)

	associations, err := repo.FindAssociations(ctx, partyID)
	require.NoError(t, err)
	require.Len(t, associations, 1)
	assert.Equal(t, "ACTIVE", associations[0].Status)
}

func TestFindAssociations_MultipleAssociations(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	partyID := uuid.New()
	_, err := repo.SaveAssociation(ctx, partyID, uuid.New(), "PARENT_OF")
	require.NoError(t, err)
	_, err = repo.SaveAssociation(ctx, partyID, uuid.New(), "EMPLOYEE_OF")
	require.NoError(t, err)

	associations, err := repo.FindAssociations(ctx, partyID)
	require.NoError(t, err)
	assert.Len(t, associations, 2)
}

func TestFindAssociations_Empty(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	associations, err := repo.FindAssociations(ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, associations)
}

// --- Integration test for SaveInTx ---

func TestSaveInTx_UsesProvidedTransaction(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypePerson, "TxTest Person")
	require.NoError(t, err)

	// Force a rollback after SaveInTx to prove the write used the provided tx.
	// If SaveInTx ignored tx and wrote via repo.db, the row would persist despite rollback.
	rollbackErr := errors.New("rollback to verify tx wiring")
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		require.NoError(t, repo.SaveInTx(ctx, party, tx))
		return rollbackErr
	})
	require.ErrorIs(t, err, rollbackErr)

	// The row must NOT exist because the transaction was rolled back
	_, err = repo.FindByID(ctx, party.ID())
	assert.ErrorIs(t, err, ErrPartyNotFound)
}

// --- Encoding edge case ---

func TestEncodePartyCursor_Format(t *testing.T) {
	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	encoded := EncodePartyCursor(PartyCursor{CreatedAt: ts, ID: id})
	assert.NotEmpty(t, encoded)

	// Decode the base64 to verify format
	data, err := base64.URLEncoding.DecodeString(encoded)
	require.NoError(t, err)

	parts := strings.SplitN(string(data), "|", 2)
	require.Len(t, parts, 2)
	assert.Contains(t, parts[0], "2025-06-15")
	assert.Equal(t, id.String(), parts[1])
}
