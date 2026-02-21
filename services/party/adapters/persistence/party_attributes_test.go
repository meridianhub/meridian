package persistence

import (
	"testing"

	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Unit tests for attribute serialization helpers ---

func TestSerializeAttributes_Empty(t *testing.T) {
	raw := serializeAttributes([]domain.AttributeEntry{})
	assert.JSONEq(t, "[]", string(raw))
}

func TestSerializeAttributes_Nil(t *testing.T) {
	raw := serializeAttributes(nil)
	assert.JSONEq(t, "[]", string(raw))
}

func TestSerializeAttributes_Single(t *testing.T) {
	attrs := []domain.AttributeEntry{{Key: "industry", Value: "finance"}}
	raw := serializeAttributes(attrs)
	assert.JSONEq(t, `[{"key":"industry","value":"finance"}]`, string(raw))
}

func TestSerializeAttributes_Multiple(t *testing.T) {
	attrs := []domain.AttributeEntry{
		{Key: "industry", Value: "finance"},
		{Key: "region", Value: "EMEA"},
	}
	raw := serializeAttributes(attrs)
	assert.JSONEq(t, `[{"key":"industry","value":"finance"},{"key":"region","value":"EMEA"}]`, string(raw))
}

func TestDeserializeAttributes_Empty(t *testing.T) {
	attrs := deserializeAttributes([]byte("[]"))
	assert.Empty(t, attrs)
}

func TestDeserializeAttributes_Nil(t *testing.T) {
	attrs := deserializeAttributes(nil)
	assert.Empty(t, attrs)
}

func TestDeserializeAttributes_Single(t *testing.T) {
	attrs := deserializeAttributes([]byte(`[{"key":"industry","value":"finance"}]`))
	require.Len(t, attrs, 1)
	assert.Equal(t, "industry", attrs[0].Key)
	assert.Equal(t, "finance", attrs[0].Value)
}

func TestDeserializeAttributes_Multiple(t *testing.T) {
	attrs := deserializeAttributes([]byte(`[{"key":"industry","value":"finance"},{"key":"region","value":"EMEA"}]`))
	require.Len(t, attrs, 2)
	assert.Equal(t, domain.AttributeEntry{Key: "industry", Value: "finance"}, attrs[0])
	assert.Equal(t, domain.AttributeEntry{Key: "region", Value: "EMEA"}, attrs[1])
}

func TestDeserializeAttributes_InvalidJSON(t *testing.T) {
	attrs := deserializeAttributes([]byte("not-json"))
	assert.Empty(t, attrs)
}

func TestSerializeDeserialize_RoundTrip(t *testing.T) {
	original := []domain.AttributeEntry{
		{Key: "industry", Value: "finance"},
		{Key: "region", Value: "EMEA"},
		{Key: "risk_tier", Value: "low"},
	}
	raw := serializeAttributes(original)
	restored := deserializeAttributes(raw)
	assert.Equal(t, original, restored)
}

// --- Integration tests for attribute persistence ---

func TestSaveAndLoadPartyWithAttributes(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypePerson, "Alice Smith")
	require.NoError(t, err)
	party.SetAttributes([]domain.AttributeEntry{
		{Key: "industry", Value: "finance"},
		{Key: "region", Value: "EMEA"},
	})

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	loaded, err := repo.FindByID(ctx, party.ID())
	require.NoError(t, err)

	attrs := loaded.Attributes()
	require.Len(t, attrs, 2)
	assert.Equal(t, "industry", attrs[0].Key)
	assert.Equal(t, "finance", attrs[0].Value)
	assert.Equal(t, "region", attrs[1].Key)
	assert.Equal(t, "EMEA", attrs[1].Value)
}

func TestUpdatePartyAttributes(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypePerson, "Bob Jones")
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	// Load and update attributes
	loaded, err := repo.FindByID(ctx, party.ID())
	require.NoError(t, err)
	assert.Empty(t, loaded.Attributes())

	loaded.SetAttributes([]domain.AttributeEntry{
		{Key: "risk_tier", Value: "high"},
	})
	err = repo.Save(ctx, loaded)
	require.NoError(t, err)

	updated, err := repo.FindByID(ctx, party.ID())
	require.NoError(t, err)
	attrs := updated.Attributes()
	require.Len(t, attrs, 1)
	assert.Equal(t, "risk_tier", attrs[0].Key)
	assert.Equal(t, "high", attrs[0].Value)
}

func TestSavePartyWithNoAttributes_DefaultsToEmpty(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)

	party, err := domain.NewParty(domain.PartyTypeOrganization, "Acme Corp")
	require.NoError(t, err)

	err = repo.Save(ctx, party)
	require.NoError(t, err)

	loaded, err := repo.FindByID(ctx, party.ID())
	require.NoError(t, err)
	assert.NotNil(t, loaded.Attributes())
	assert.Empty(t, loaded.Attributes())
}
