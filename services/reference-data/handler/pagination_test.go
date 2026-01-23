package handler

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCursorToken(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		wantErr     bool
		wantErrType error
	}{
		{
			name:    "empty token",
			token:   "",
			wantErr: false,
		},
		{
			name:    "valid token",
			token:   "1734567890_123e4567-e89b-12d3-a456-426614174000",
			wantErr: false,
		},
		{
			name:        "missing underscore",
			token:       "1734567890",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
		},
		{
			name:        "empty timestamp",
			token:       "_123e4567-e89b-12d3-a456-426614174000",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
		},
		{
			name:        "empty uuid",
			token:       "1734567890_",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
		},
		{
			name:        "invalid timestamp",
			token:       "invalid_123e4567-e89b-12d3-a456-426614174000",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
		},
		{
			name:        "invalid uuid",
			token:       "1734567890_not-a-uuid",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
		},
		{
			name:        "timestamp too old (negative)",
			token:       "-100_123e4567-e89b-12d3-a456-426614174000",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
		},
		{
			name:        "timestamp too far future",
			token:       "9999999999999_123e4567-e89b-12d3-a456-426614174000",
			wantErr:     true,
			wantErrType: ErrInvalidPageToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timestamp, id, err := parseCursorToken(tt.token)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrType != nil {
					assert.ErrorIs(t, err, tt.wantErrType)
				}
			} else {
				require.NoError(t, err)
				if tt.token == "" {
					assert.True(t, timestamp.IsZero())
					assert.Equal(t, uuid.Nil, id)
				} else {
					assert.False(t, timestamp.IsZero())
					assert.NotEqual(t, uuid.Nil, id)
				}
			}
		})
	}
}

func TestEncodeCursorToken(t *testing.T) {
	ts := time.Unix(1734567890, 0).UTC()
	id := uuid.MustParse("123e4567-e89b-12d3-a456-426614174000")

	token := encodeCursorToken(ts, id)

	assert.Equal(t, "1734567890_123e4567-e89b-12d3-a456-426614174000", token)
}

func TestParseCursorToken_RoundTrip(t *testing.T) {
	originalTime := time.Unix(1734567890, 0).UTC()
	originalID := uuid.New()

	token := encodeCursorToken(originalTime, originalID)

	parsedTime, parsedID, err := parseCursorToken(token)

	require.NoError(t, err)
	assert.Equal(t, originalTime, parsedTime)
	assert.Equal(t, originalID, parsedID)
}

func TestPaginationConstants(t *testing.T) {
	t.Run("default page size", func(t *testing.T) {
		assert.Equal(t, 50, DefaultPageSize)
	})

	t.Run("max page size", func(t *testing.T) {
		assert.Equal(t, 1000, MaxPageSize)
	})
}
