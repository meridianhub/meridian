package service

import (
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// --- NewOutboxVerificationEventPublisher ---

func TestNewOutboxVerificationEventPublisher_CreatesPublisher(t *testing.T) {
	publisher := events.NewOutboxPublisher("party")
	db := &gorm.DB{}

	p := NewOutboxVerificationEventPublisher(publisher, db)

	require.NotNil(t, p)
	assert.Equal(t, publisher, p.publisher)
	assert.Equal(t, db, p.db)
}

func TestNewOutboxVerificationEventPublisher_WithNilDB(t *testing.T) {
	publisher := events.NewOutboxPublisher("party")

	// Constructor does not enforce non-nil db - callers are responsible
	p := NewOutboxVerificationEventPublisher(publisher, nil)

	require.NotNil(t, p)
	assert.Nil(t, p.db)
}

// --- VerificationCompletedEvent field population ---

func TestVerificationCompletedEvent_AllFields(t *testing.T) {
	riskScore := 0.42
	reason := "address verified"

	e := VerificationCompletedEvent{
		EventID:        "evt-123",
		PartyID:        "party-456",
		VerificationID: "verif-789",
		Provider:       "jumio",
		Status:         "APPROVED",
		RiskScore:      &riskScore,
		Reason:         &reason,
		CompletedAt:    time.Now(),
		Metadata:       map[string]string{"key": "value"},
	}

	assert.Equal(t, "evt-123", e.EventID)
	assert.Equal(t, "party-456", e.PartyID)
	assert.Equal(t, "verif-789", e.VerificationID)
	assert.Equal(t, "jumio", e.Provider)
	assert.Equal(t, "APPROVED", e.Status)
	assert.Equal(t, &riskScore, e.RiskScore)
	assert.Equal(t, &reason, e.Reason)
	assert.Equal(t, map[string]string{"key": "value"}, e.Metadata)
}

func TestVerificationCompletedEvent_OptionalFieldsNil(t *testing.T) {
	e := VerificationCompletedEvent{
		EventID:        "evt-001",
		PartyID:        "party-001",
		VerificationID: "verif-001",
		Provider:       "provider",
		Status:         "REJECTED",
		RiskScore:      nil,
		Reason:         nil,
		CompletedAt:    time.Now(),
	}

	assert.Nil(t, e.RiskScore)
	assert.Nil(t, e.Reason)
}
