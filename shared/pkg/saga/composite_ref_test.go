package saga

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCompositeAccountRef(t *testing.T) {
	ref := BuildCompositeAccountRef("party-123", "org-456", "GBP")
	assert.Equal(t, "party:party-123:org:org-456:currency:GBP", ref)
}

func TestBuildCompositeAccountRef_UUIDs(t *testing.T) {
	ref := BuildCompositeAccountRef(
		"550e8400-e29b-41d4-a716-446655440000",
		"6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		"USD",
	)
	assert.Equal(t, "party:550e8400-e29b-41d4-a716-446655440000:org:6ba7b810-9dad-11d1-80b4-00c04fd430c8:currency:USD", ref)
}

func TestParseCompositeAccountRef_Success(t *testing.T) {
	ref, err := ParseCompositeAccountRef("party:party-123:org:org-456:currency:GBP")
	require.NoError(t, err)
	assert.Equal(t, "party-123", ref.PartyID)
	assert.Equal(t, "org-456", ref.OrgID)
	assert.Equal(t, "GBP", ref.Currency)
}

func TestParseCompositeAccountRef_Roundtrip(t *testing.T) {
	original := BuildCompositeAccountRef("my-party", "my-org", "EUR")
	parsed, err := ParseCompositeAccountRef(original)
	require.NoError(t, err)

	assert.Equal(t, "my-party", parsed.PartyID)
	assert.Equal(t, "my-org", parsed.OrgID)
	assert.Equal(t, "EUR", parsed.Currency)

	// Rebuild and verify equality
	rebuilt := BuildCompositeAccountRef(parsed.PartyID, parsed.OrgID, parsed.Currency)
	assert.Equal(t, original, rebuilt)
}

func TestParseCompositeAccountRef_TooFewSegments(t *testing.T) {
	_, err := ParseCompositeAccountRef("party:123:org:456")
	assert.ErrorIs(t, err, ErrMalformedCompositeRef)
	assert.Contains(t, err.Error(), "expected 6 segments, got 4")
}

func TestParseCompositeAccountRef_TooManySegments(t *testing.T) {
	_, err := ParseCompositeAccountRef("party:123:org:456:currency:GBP:extra:data")
	assert.ErrorIs(t, err, ErrMalformedCompositeRef)
	assert.Contains(t, err.Error(), "expected 6 segments, got 8")
}

func TestParseCompositeAccountRef_WrongFirstSegment(t *testing.T) {
	_, err := ParseCompositeAccountRef("account:123:org:456:currency:GBP")
	assert.ErrorIs(t, err, ErrMalformedCompositeRef)
	assert.Contains(t, err.Error(), "expected segment 1 to be \"party\"")
}

func TestParseCompositeAccountRef_WrongThirdSegment(t *testing.T) {
	_, err := ParseCompositeAccountRef("party:123:tenant:456:currency:GBP")
	assert.ErrorIs(t, err, ErrMalformedCompositeRef)
	assert.Contains(t, err.Error(), "expected segment 3 to be \"org\"")
}

func TestParseCompositeAccountRef_WrongFifthSegment(t *testing.T) {
	_, err := ParseCompositeAccountRef("party:123:org:456:amount:GBP")
	assert.ErrorIs(t, err, ErrMalformedCompositeRef)
	assert.Contains(t, err.Error(), "expected segment 5 to be \"currency\"")
}

func TestParseCompositeAccountRef_EmptyPartyID(t *testing.T) {
	_, err := ParseCompositeAccountRef("party::org:456:currency:GBP")
	assert.ErrorIs(t, err, ErrMalformedCompositeRef)
	assert.Contains(t, err.Error(), "party_id must not be empty")
}

func TestParseCompositeAccountRef_EmptyOrgID(t *testing.T) {
	_, err := ParseCompositeAccountRef("party:123:org::currency:GBP")
	assert.ErrorIs(t, err, ErrMalformedCompositeRef)
	assert.Contains(t, err.Error(), "org_id must not be empty")
}

func TestParseCompositeAccountRef_EmptyCurrency(t *testing.T) {
	_, err := ParseCompositeAccountRef("party:123:org:456:currency:")
	assert.ErrorIs(t, err, ErrMalformedCompositeRef)
	assert.Contains(t, err.Error(), "currency must not be empty")
}

func TestIsCompositeAccountRef(t *testing.T) {
	tests := []struct {
		ref      string
		expected bool
	}{
		{"party:123:org:456:currency:GBP", true},
		{"ACC-001", false},
		{"simple-ref", false},
		{"has:colons", true},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsCompositeAccountRef(tt.ref))
		})
	}
}
