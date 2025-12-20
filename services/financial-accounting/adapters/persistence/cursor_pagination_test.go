package persistence

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseCursorToken tests the cursor token parsing function.
func TestParseCursorToken(t *testing.T) {
	// Generate a valid UUID for testing
	validUUID := uuid.New()
	validTimestamp := time.Now().Unix()

	tests := []struct {
		name          string
		token         string
		wantErr       bool
		wantErrType   error
		wantTimestamp int64
		wantID        uuid.UUID
		rationale     string
	}{
		// Happy path tests
		{
			name:          "valid token with timestamp and UUID",
			token:         "1734567890_550e8400-e29b-41d4-a716-446655440000",
			wantErr:       false,
			wantTimestamp: 1734567890,
			wantID:        uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
			rationale:     "Standard valid token should parse correctly",
		},
		{
			name:          "valid token with generated UUID",
			token:         "1734567890_" + validUUID.String(),
			wantErr:       false,
			wantTimestamp: 1734567890,
			wantID:        validUUID,
			rationale:     "Token with any valid UUID should parse correctly",
		},
		{
			name:          "empty token returns zero values",
			token:         "",
			wantErr:       false,
			wantTimestamp: 0,
			wantID:        uuid.Nil,
			rationale:     "Empty token indicates first page - should return zero values without error",
		},
		{
			name:          "valid token with current timestamp",
			token:         formatTimestampUUID(validTimestamp, validUUID),
			wantErr:       false,
			wantTimestamp: validTimestamp,
			wantID:        validUUID,
			rationale:     "Token generated with current timestamp should be parseable",
		},
		{
			name:          "valid token with zero timestamp",
			token:         "0_550e8400-e29b-41d4-a716-446655440000",
			wantErr:       false,
			wantTimestamp: 0,
			wantID:        uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
			rationale:     "Zero timestamp is technically valid (Unix epoch)",
		},

		// Unhappy path tests - invalid format
		{
			name:        "invalid format - no underscore",
			token:       "1234567890",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
			rationale:   "Token without underscore separator is malformed",
		},
		{
			name:        "multiple underscores - UUID part is invalid",
			token:       "1234567890_550e8400_e29b-41d4-a716-446655440000",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
			rationale:   "With SplitN, second part includes extra underscore making it an invalid UUID",
		},
		{
			name:        "invalid format - empty timestamp",
			token:       "_550e8400-e29b-41d4-a716-446655440000",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
			rationale:   "Empty timestamp part is invalid",
		},
		{
			name:        "invalid format - empty UUID",
			token:       "1234567890_",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
			rationale:   "Empty UUID part is invalid",
		},

		// Unhappy path tests - invalid timestamp
		{
			name:        "invalid timestamp - not a number",
			token:       "notanumber_550e8400-e29b-41d4-a716-446655440000",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
			rationale:   "Non-numeric timestamp is invalid",
		},
		{
			name:        "invalid timestamp - floating point",
			token:       "123.456_550e8400-e29b-41d4-a716-446655440000",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
			rationale:   "Floating point timestamp is invalid - must be integer Unix timestamp",
		},
		{
			name:        "invalid timestamp - hex number",
			token:       "0x1234_550e8400-e29b-41d4-a716-446655440000",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
			rationale:   "Hex timestamp is invalid - must be decimal integer",
		},

		// Unhappy path tests - invalid UUID
		{
			name:        "invalid UUID - not a UUID",
			token:       "1234567890_not-a-uuid",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
			rationale:   "Non-UUID string is invalid",
		},
		{
			name:          "UUID without hyphens is accepted by google/uuid",
			token:         "1234567890_550e8400e29b41d4a716446655440000",
			wantErr:       false,
			wantTimestamp: 1234567890,
			wantID:        uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
			rationale:     "UUID library accepts UUIDs without hyphens",
		},
		{
			name:        "invalid UUID - truncated",
			token:       "1234567890_550e8400-e29b-41d4-a716",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
			rationale:   "Truncated UUID is invalid",
		},
		{
			name:          "nil UUID string is technically valid",
			token:         "1234567890_00000000-0000-0000-0000-000000000000",
			wantErr:       false,
			wantTimestamp: 1234567890,
			wantID:        uuid.Nil,
			rationale:     "Nil UUID is technically valid (though unusual)",
		},

		// Edge cases - timestamp bounds validation
		{
			name:        "very large timestamp exceeds year 2100",
			token:       "9223372036854775807_550e8400-e29b-41d4-a716-446655440000",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
			rationale:   "Timestamps beyond year 2100 are rejected as potential token manipulation",
		},
		{
			name:        "negative timestamp before Unix epoch",
			token:       "-1_550e8400-e29b-41d4-a716-446655440000",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
			rationale:   "Financial records before 1970 are unexpected and rejected",
		},
		{
			name:          "timestamp near year 2100 is valid",
			token:         "4102444799_550e8400-e29b-41d4-a716-446655440000", // 2099-12-31 23:59:59 UTC
			wantErr:       false,
			wantTimestamp: 4102444799,
			wantID:        uuid.MustParse("550e8400-e29b-41d4-a716-446655440000"),
			rationale:     "Timestamps just before year 2100 cutoff are valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Act
			timestamp, id, err := parseCursorToken(tt.token)

			// Assert
			if tt.wantErr {
				require.Error(t, err, tt.rationale)
				if tt.wantErrType != nil {
					assert.ErrorIs(t, err, tt.wantErrType, tt.rationale)
				}
			} else {
				require.NoError(t, err, tt.rationale)

				// Check timestamp
				if tt.wantTimestamp == 0 && tt.token == "" {
					assert.True(t, timestamp.IsZero(), "Empty token should return zero timestamp")
				} else {
					assert.Equal(t, tt.wantTimestamp, timestamp.Unix(), tt.rationale)
				}

				// Check UUID
				assert.Equal(t, tt.wantID, id, tt.rationale)
			}
		})
	}
}

// TestParseCursorToken_RoundTrip tests that tokens can be generated and parsed back correctly.
func TestParseCursorToken_RoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		timestamp time.Time
		id        uuid.UUID
	}{
		{
			name:      "current time and new UUID",
			timestamp: time.Now().UTC(),
			id:        uuid.New(),
		},
		{
			name:      "past date",
			timestamp: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
			id:        uuid.New(),
		},
		{
			name:      "future date",
			timestamp: time.Date(2030, 12, 31, 23, 59, 59, 0, time.UTC),
			id:        uuid.New(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Generate token in the same format as the repository
			token := formatTimestampUUID(tt.timestamp.Unix(), tt.id)

			// Parse it back
			parsedTimestamp, parsedID, err := parseCursorToken(token)
			require.NoError(t, err)

			// Verify round-trip (note: timestamp loses sub-second precision)
			assert.Equal(t, tt.timestamp.Unix(), parsedTimestamp.Unix(),
				"Timestamp should round-trip correctly at second precision")
			assert.Equal(t, tt.id, parsedID, "UUID should round-trip exactly")
		})
	}
}

// formatTimestampUUID creates a cursor token in the same format used by the repository.
func formatTimestampUUID(timestamp int64, id uuid.UUID) string {
	return fmt.Sprintf("%d_%s", timestamp, id.String())
}

// TestListBookingLogsParams_Pagination tests that ListBookingLogsParams correctly validates page tokens.
func TestListBookingLogsParams_PaginationTokenValidation(t *testing.T) {
	// These tests verify that invalid page tokens are rejected at the repository level
	tests := []struct {
		name      string
		pageToken string
		wantErr   bool
		rationale string
	}{
		{
			name:      "empty page token is valid (first page)",
			pageToken: "",
			wantErr:   false,
			rationale: "Empty token indicates first page request",
		},
		{
			name:      "valid page token format",
			pageToken: "1734567890_550e8400-e29b-41d4-a716-446655440000",
			wantErr:   false,
			rationale: "Properly formatted token should be accepted",
		},
		{
			name:      "invalid page token format",
			pageToken: "invalid-token",
			wantErr:   true,
			rationale: "Malformed tokens should be rejected with error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseCursorToken(tt.pageToken)
			if tt.wantErr {
				assert.Error(t, err, tt.rationale)
			} else {
				assert.NoError(t, err, tt.rationale)
			}
		})
	}
}
