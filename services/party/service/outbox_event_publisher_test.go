package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupOutboxTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&events.EventOutbox{})
	require.NoError(t, err)

	return db
}

func validVerificationCompletedEvent() VerificationCompletedEvent {
	return VerificationCompletedEvent{
		EventID:        uuid.New().String(),
		PartyID:        uuid.New().String(),
		VerificationID: "verif-abc-123",
		Provider:       "jumio",
		Status:         "APPROVED",
		CompletedAt:    time.Now().UTC(),
		Metadata:       map[string]string{"source": "webhook"},
	}
}

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

// --- PublishVerificationCompleted ---

func TestPublishVerificationCompleted_WritesOutboxEntry(t *testing.T) {
	db := setupOutboxTestDB(t)
	publisher := events.NewOutboxPublisher("party")
	p := NewOutboxVerificationEventPublisher(publisher, db)

	e := validVerificationCompletedEvent()

	err := p.PublishVerificationCompleted(context.Background(), e)
	require.NoError(t, err)

	var entries []events.EventOutbox
	db.Find(&entries)
	require.Len(t, entries, 1)

	entry := entries[0]
	assert.Equal(t, "party.verification-completed.v1", entry.Topic)
	assert.Equal(t, "party.verification-completed.v1", entry.EventType)
	assert.Equal(t, e.PartyID, entry.AggregateID)
	assert.Equal(t, "Party", entry.AggregateType)
	assert.Equal(t, "party", entry.ServiceName)
	assert.Equal(t, events.StatusPending, entry.Status)
	assert.NotEmpty(t, entry.EventPayload)
}

func TestPublishVerificationCompleted_WithAllOptionalFields(t *testing.T) {
	db := setupOutboxTestDB(t)
	publisher := events.NewOutboxPublisher("party")
	p := NewOutboxVerificationEventPublisher(publisher, db)

	riskScore := 0.75
	reason := "high risk score"
	e := validVerificationCompletedEvent()
	e.Status = "REJECTED"
	e.RiskScore = &riskScore
	e.Reason = &reason

	err := p.PublishVerificationCompleted(context.Background(), e)
	require.NoError(t, err)

	var count int64
	db.Model(&events.EventOutbox{}).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestPublishVerificationCompleted_WithNilOptionalFields(t *testing.T) {
	db := setupOutboxTestDB(t)
	publisher := events.NewOutboxPublisher("party")
	p := NewOutboxVerificationEventPublisher(publisher, db)

	e := validVerificationCompletedEvent()
	e.RiskScore = nil
	e.Reason = nil
	e.Metadata = nil

	err := p.PublishVerificationCompleted(context.Background(), e)
	require.NoError(t, err)

	var count int64
	db.Model(&events.EventOutbox{}).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestPublishVerificationCompleted_ManualReviewStatus(t *testing.T) {
	db := setupOutboxTestDB(t)
	publisher := events.NewOutboxPublisher("party")
	p := NewOutboxVerificationEventPublisher(publisher, db)

	e := validVerificationCompletedEvent()
	e.Status = "MANUAL_REVIEW"

	err := p.PublishVerificationCompleted(context.Background(), e)
	require.NoError(t, err)
}

func TestPublishVerificationCompleted_RollbackOnDBError(t *testing.T) {
	db := setupOutboxTestDB(t)
	publisher := events.NewOutboxPublisher("party")
	p := NewOutboxVerificationEventPublisher(publisher, db)

	// Drop the table to force a DB error inside the transaction
	db.Exec("DROP TABLE event_outbox")

	e := validVerificationCompletedEvent()
	err := p.PublishVerificationCompleted(context.Background(), e)
	require.Error(t, err)
}

func TestPublishVerificationCompleted_InvalidEventFailsValidation(t *testing.T) {
	db := setupOutboxTestDB(t)
	publisher := events.NewOutboxPublisher("party")
	p := NewOutboxVerificationEventPublisher(publisher, db)

	e := validVerificationCompletedEvent()
	e.EventID = "not-a-uuid" // violates buf.validate uuid constraint

	err := p.PublishVerificationCompleted(context.Background(), e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event payload validation failed")

	// Nothing should be in the outbox
	var count int64
	db.Model(&events.EventOutbox{}).Count(&count)
	assert.Equal(t, int64(0), count)
}
