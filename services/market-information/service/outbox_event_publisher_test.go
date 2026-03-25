package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupOutboxMITestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&events.EventOutbox{})
	require.NoError(t, err)

	return db
}

func validObservation(t *testing.T) domain.MarketPriceObservation {
	t.Helper()

	now := time.Now().UTC()
	obs, err := domain.NewMarketPriceObservation(
		"GBP-USD",
		uuid.New(),
		"resolution-key-1",
		decimal.NewFromFloat(1.25),
		"USD",
		now,
		now,
		now.Add(time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		90,
		domain.ObservationContext{},
	)
	require.NoError(t, err)
	return obs
}

// --- NewOutboxEventPublisher ---

func TestNewOutboxEventPublisher(t *testing.T) {
	publisher := events.NewOutboxPublisher("market-information")
	oep := NewOutboxEventPublisher(nil, publisher)

	require.NotNil(t, oep)
	assert.NotNil(t, oep.publisher)
}

// --- Publish unsupported type ---

func TestOutboxEventPublisher_Publish_UnsupportedType(t *testing.T) {
	publisher := events.NewOutboxPublisher("market-information")
	oep := NewOutboxEventPublisher(nil, publisher)

	err := oep.Publish(context.Background(), "unsupported-event")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedEventType)
}

// --- PublishObservationRecorded ---

func TestOutboxEventPublisher_PublishObservationRecorded_WritesEntry(t *testing.T) {
	db := setupOutboxMITestDB(t)
	publisher := events.NewOutboxPublisher("market-information")
	oep := NewOutboxEventPublisher(db, publisher)

	obs := validObservation(t)

	err := oep.PublishObservationRecorded(context.Background(), obs)
	require.NoError(t, err)

	var entries []events.EventOutbox
	db.Find(&entries)
	require.Len(t, entries, 1)

	entry := entries[0]
	assert.Equal(t, "market-information.observation-recorded.v1", entry.Topic)
	assert.Equal(t, "market_information.observation_recorded.v1", entry.EventType)
	assert.Equal(t, obs.ID().String(), entry.AggregateID)
	assert.Equal(t, "MarketPriceObservation", entry.AggregateType)
	assert.Equal(t, obs.DataSetCode(), entry.PartitionKey)
	assert.Equal(t, "market-information", entry.ServiceName)
	assert.Equal(t, events.StatusPending, entry.Status)
	assert.NotEmpty(t, entry.EventPayload)
}

func TestOutboxEventPublisher_PublishObservationRecorded_DBError(t *testing.T) {
	db := setupOutboxMITestDB(t)
	publisher := events.NewOutboxPublisher("market-information")
	oep := NewOutboxEventPublisher(db, publisher)

	db.Exec("DROP TABLE event_outbox")

	obs := validObservation(t)
	err := oep.PublishObservationRecorded(context.Background(), obs)
	require.Error(t, err)
}

// --- Publish with proto message ---

func TestOutboxEventPublisher_Publish_ObservationRecordedProto_WritesEntry(t *testing.T) {
	db := setupOutboxMITestDB(t)
	publisher := events.NewOutboxPublisher("market-information")
	oep := NewOutboxEventPublisher(db, publisher)

	event := &marketinformationv1.ObservationRecorded{
		ObservationId:      uuid.New().String(),
		DatasetCode:        "GBP-USD",
		ResolutionKeyValue: "res-key",
		ObservedAt:         timestamppb.Now(),
		Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
		Value:              "1.2500",
		SourceId:           uuid.New().String(),
		RecordedAt:         timestamppb.Now(),
	}

	err := oep.Publish(context.Background(), event)
	require.NoError(t, err)

	var count int64
	db.Model(&events.EventOutbox{}).Count(&count)
	assert.Equal(t, int64(1), count)
}

func TestOutboxEventPublisher_Publish_ObservationRecordedProto_SetsPartitionKey(t *testing.T) {
	db := setupOutboxMITestDB(t)
	publisher := events.NewOutboxPublisher("market-information")
	oep := NewOutboxEventPublisher(db, publisher)

	datasetCode := "NATURAL-GAS-UK"
	event := &marketinformationv1.ObservationRecorded{
		ObservationId:      uuid.New().String(),
		DatasetCode:        datasetCode,
		ResolutionKeyValue: "key",
		ObservedAt:         timestamppb.Now(),
		Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
		Value:              "3.14",
		SourceId:           uuid.New().String(),
		RecordedAt:         timestamppb.Now(),
	}

	err := oep.Publish(context.Background(), event)
	require.NoError(t, err)

	var entries []events.EventOutbox
	db.Find(&entries)
	require.Len(t, entries, 1)
	assert.Equal(t, datasetCode, entries[0].PartitionKey)
}

func TestOutboxEventPublisher_Publish_DBError(t *testing.T) {
	db := setupOutboxMITestDB(t)
	publisher := events.NewOutboxPublisher("market-information")
	oep := NewOutboxEventPublisher(db, publisher)

	db.Exec("DROP TABLE event_outbox")

	event := &marketinformationv1.ObservationRecorded{
		ObservationId:      uuid.New().String(),
		DatasetCode:        "GBP-USD",
		ResolutionKeyValue: "key",
		ObservedAt:         timestamppb.Now(),
		Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
		Value:              "1.00",
		SourceId:           uuid.New().String(),
		RecordedAt:         timestamppb.Now(),
	}

	err := oep.Publish(context.Background(), event)
	require.Error(t, err)
}
