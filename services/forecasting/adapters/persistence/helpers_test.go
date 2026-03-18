package persistence

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/forecasting/domain"
)

// --- formatCursorToken / parseCursorToken tests ---

func TestFormatAndParseCursorToken_RoundTrip(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 30, 0, 0, time.UTC)
	id := uuid.New()

	token := formatCursorToken(ts, id)
	assert.NotEmpty(t, token)

	parsedTs, parsedID, err := parseCursorToken(token)
	require.NoError(t, err)
	assert.Equal(t, ts.UnixMicro(), parsedTs.UnixMicro())
	assert.Equal(t, id, parsedID)
}

func TestParseCursorToken_Empty(t *testing.T) {
	ts, id, err := parseCursorToken("")
	require.NoError(t, err)
	assert.True(t, ts.IsZero())
	assert.Equal(t, uuid.Nil, id)
}

func TestParseCursorToken_InvalidBase64(t *testing.T) {
	_, _, err := parseCursorToken("not-valid-base64!!!")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cursor token encoding")
}

func TestParseCursorToken_InvalidFormat(t *testing.T) {
	import64 := "aGVsbG8=" // "hello" in base64 - no | separator
	_, _, err := parseCursorToken(import64)
	require.Error(t, err)
}

func TestParseCursorToken_InvalidTimestamp(t *testing.T) {
	import64 := "bm90bnVtYmVyfHZhbGlkLXV1aWQ=" // "notnumber|valid-uuid"
	_, _, err := parseCursorToken(import64)
	require.Error(t, err)
}

func TestParseCursorToken_InvalidUUID(t *testing.T) {
	import64 := "MTIzNDU2fG5vdC1hLXV1aWQ=" // "123456|not-a-uuid"
	_, _, err := parseCursorToken(import64)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cursor UUID")
}

// --- nullStringPtr tests ---

func TestNullStringPtr_ValidString(t *testing.T) {
	ns := sql.NullString{String: "test", Valid: true}
	result := nullStringPtr(ns)
	assert.Equal(t, "test", result)
}

func TestNullStringPtr_InvalidString(t *testing.T) {
	ns := sql.NullString{Valid: false}
	result := nullStringPtr(ns)
	assert.Nil(t, result)
}

// --- parseStrategyStatus tests ---

func TestParseStrategyStatus_Draft(t *testing.T) {
	assert.Equal(t, domain.StrategyStatusDraft, parseStrategyStatus("DRAFT"))
}

func TestParseStrategyStatus_Active(t *testing.T) {
	assert.Equal(t, domain.StrategyStatusActive, parseStrategyStatus("ACTIVE"))
}

func TestParseStrategyStatus_Deprecated(t *testing.T) {
	assert.Equal(t, domain.StrategyStatusDeprecated, parseStrategyStatus("DEPRECATED"))
}

func TestParseStrategyStatus_Unknown(t *testing.T) {
	// Default case
	assert.Equal(t, domain.StrategyStatusDraft, parseStrategyStatus("UNKNOWN"))
}
