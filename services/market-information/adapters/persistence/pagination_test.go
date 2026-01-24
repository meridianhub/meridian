package persistence

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCursorToken(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		wantErr   bool
		rationale string
	}{
		{
			name:      "valid token",
			token:     "1734567890_550e8400-e29b-41d4-a716-446655440000",
			wantErr:   false,
			rationale: "Standard valid token",
		},
		{
			name:      "empty token",
			token:     "",
			wantErr:   false,
			rationale: "Empty token indicates first page",
		},
		{
			name:      "invalid format - no underscore",
			token:     "1234567890",
			wantErr:   true,
			rationale: "Malformed token missing separator",
		},
		{
			name:      "invalid format - empty timestamp",
			token:     "_550e8400-e29b-41d4-a716-446655440000",
			wantErr:   true,
			rationale: "Malformed token with empty timestamp",
		},
		{
			name:      "invalid format - empty uuid",
			token:     "1234567890_",
			wantErr:   true,
			rationale: "Malformed token with empty UUID",
		},
		{
			name:      "invalid timestamp",
			token:     "notanumber_550e8400-e29b-41d4-a716-446655440000",
			wantErr:   true,
			rationale: "Non-numeric timestamp",
		},
		{
			name:      "invalid UUID",
			token:     "1234567890_not-a-uuid",
			wantErr:   true,
			rationale: "Non-UUID string",
		},
		{
			name:      "timestamp beyond year 2100",
			token:     "9223372036854775807_550e8400-e29b-41d4-a716-446655440000",
			wantErr:   true,
			rationale: "Out of range timestamp",
		},
		{
			name:      "negative timestamp",
			token:     "-1_550e8400-e29b-41d4-a716-446655440000",
			wantErr:   true,
			rationale: "Before Unix epoch",
		},
		{
			name:      "valid token at boundary - unix epoch",
			token:     "0_550e8400-e29b-41d4-a716-446655440000",
			wantErr:   false,
			rationale: "Unix epoch is valid minimum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseCursorToken(tt.token)
			if tt.wantErr {
				require.Error(t, err, "expected error for: %s", tt.rationale)
				assert.ErrorIs(t, err, ErrInvalidPageToken)
			} else {
				require.NoError(t, err, "unexpected error for: %s", tt.rationale)
			}
		})
	}
}

func TestParseCursorToken_ParsesCorrectValues(t *testing.T) {
	expectedTime := time.Unix(1734567890, 0).UTC()
	expectedID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	token := "1734567890_550e8400-e29b-41d4-a716-446655440000"

	gotTime, gotID, err := parseCursorToken(token)

	require.NoError(t, err)
	assert.Equal(t, expectedTime, gotTime)
	assert.Equal(t, expectedID, gotID)
}

func TestParseCursorToken_EmptyReturnsZeroValues(t *testing.T) {
	gotTime, gotID, err := parseCursorToken("")

	require.NoError(t, err)
	assert.True(t, gotTime.IsZero(), "expected zero time for empty token")
	assert.Equal(t, uuid.Nil, gotID, "expected nil UUID for empty token")
}

func TestFormatCursorToken(t *testing.T) {
	timestamp := time.Unix(1734567890, 0).UTC()
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	token := formatCursorToken(timestamp, id)

	assert.Equal(t, "1734567890_550e8400-e29b-41d4-a716-446655440000", token)
}

func TestCursorToken_RoundTrip(t *testing.T) {
	// Use current time truncated to second precision (what we store)
	originalTime := time.Now().UTC().Truncate(time.Second)
	originalID := uuid.New()

	// Format then parse
	token := formatCursorToken(originalTime, originalID)
	parsedTime, parsedID, err := parseCursorToken(token)

	require.NoError(t, err)
	assert.Equal(t, originalTime.Unix(), parsedTime.Unix(), "timestamp should round-trip with second precision")
	assert.Equal(t, originalID, parsedID, "UUID should round-trip exactly")
}

func TestCursorToken_MultipleRoundTrips(t *testing.T) {
	// Test multiple round trips to ensure stability
	testCases := []struct {
		timestamp time.Time
		id        uuid.UUID
	}{
		{time.Unix(0, 0).UTC(), uuid.MustParse("00000000-0000-0000-0000-000000000001")},
		{time.Unix(1000000000, 0).UTC(), uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")},
		{time.Unix(2000000000, 0).UTC(), uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")},
	}

	for _, tc := range testCases {
		token := formatCursorToken(tc.timestamp, tc.id)
		parsedTime, parsedID, err := parseCursorToken(token)

		require.NoError(t, err)
		assert.Equal(t, tc.timestamp.Unix(), parsedTime.Unix())
		assert.Equal(t, tc.id, parsedID)
	}
}
