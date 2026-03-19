package audit

import (
	"context"
	"testing"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/stretchr/testify/assert"
)

func TestPublisher_Publish_EmptyRecordID(t *testing.T) {
	p := &Publisher{enabled: true}

	event := &auditv1.AuditEvent{
		RecordId: "",
	}

	err := p.Publish(context.Background(), event)
	assert.ErrorIs(t, err, ErrEmptyRecordID)
}

func TestPublisher_Close_NilProducer(t *testing.T) {
	p := &Publisher{
		producer: nil,
	}

	err := p.Close()
	assert.NoError(t, err)
}

func TestPublishToKafkaWithFallback_DisabledPublisher(t *testing.T) {
	original := GetGlobalPublisher()
	defer SetGlobalPublisher(original)

	// Set a disabled publisher
	p := &Publisher{enabled: false}
	SetGlobalPublisher(p)

	// Call publishToKafkaWithFallback - should take the "disabled" fallback path
	// We can't test the full path without a DB, but we verify the metric path is hit
	// by checking it doesn't panic. The actual DB write will fail, which is expected.
	db := setupHooksTestDB(t)

	err := publishToKafkaWithFallback(
		db,
		"test_table",
		"INSERT",
		"record-1",
		"",
		`{"key":"value"}`,
		"test-user",
		"test_schema",
	)

	// Should succeed - writes to outbox via fallback
	assert.NoError(t, err)

	// Verify outbox was written
	var count int64
	db.Model(&testAuditOutbox{}).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestPublishToKafkaWithFallback_NilPublisher(t *testing.T) {
	original := GetGlobalPublisher()
	defer SetGlobalPublisher(original)

	SetGlobalPublisher(nil)

	db := setupHooksTestDB(t)

	err := publishToKafkaWithFallback(
		db,
		"test_table",
		"UPDATE",
		"record-2",
		`{"old":"data"}`,
		`{"new":"data"}`,
		"",
		"test_schema",
	)

	assert.NoError(t, err)

	// Verify outbox entry created with nil ChangedBy
	var count int64
	db.Model(&testAuditOutbox{}).Count(&count)
	assert.Equal(t, int64(1), count)
}
