package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

// mockProtoPublisher implements protoPublisher for testing.
type mockProtoPublisher struct {
	publishedMessages []publishedMessage
	publishErr        error
	flushReturn       int
	closed            bool
}

type publishedMessage struct {
	topic string
	key   string
	msg   proto.Message
}

func (m *mockProtoPublisher) PublishWithTenant(_ context.Context, topic, key string, msg proto.Message) error {
	if m.publishErr != nil {
		return m.publishErr
	}
	m.publishedMessages = append(m.publishedMessages, publishedMessage{topic: topic, key: key, msg: msg})
	return nil
}

func (m *mockProtoPublisher) FlushWithTimeout(_ int) int {
	return m.flushReturn
}

func (m *mockProtoPublisher) Close() {
	m.closed = true
}

func TestNewKafkaObservationPublisher(t *testing.T) {
	t.Run("returns error for nil producer", func(t *testing.T) {
		_, err := NewKafkaObservationPublisher(nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNilProducer)
	})

	t.Run("creates publisher with valid producer", func(t *testing.T) {
		pub, err := NewKafkaObservationPublisher(&mockProtoPublisher{})
		require.NoError(t, err)
		assert.NotNil(t, pub)
		assert.Equal(t, ObservationRecordedTopic, pub.topic)
		assert.Equal(t, DeprecatedObservationRecordedTopic, pub.deprecatedTopic)
	})
}

func createTestObservation(t *testing.T) domain.MarketPriceObservation {
	t.Helper()
	now := time.Now()
	obs, err := domain.NewMarketPriceObservation(
		"TEST_DATASET",
		uuid.New(),
		"EUR/USD",
		decimal.NewFromFloat(1.1234),
		"Test Unit",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		80,
		domain.NewObservationContext(map[string]string{"currency_pair": "EUR/USD"}),
	)
	require.NoError(t, err)
	return obs
}

func TestKafkaObservationPublisher_PublishObservationRecorded(t *testing.T) {
	t.Run("publishes to both topics", func(t *testing.T) {
		mock := &mockProtoPublisher{}
		pub, err := NewKafkaObservationPublisher(mock)
		require.NoError(t, err)

		obs := createTestObservation(t)
		err = pub.PublishObservationRecorded(context.Background(), obs)
		require.NoError(t, err)

		assert.Len(t, mock.publishedMessages, 2)
		assert.Equal(t, ObservationRecordedTopic, mock.publishedMessages[0].topic)
		assert.Equal(t, "TEST_DATASET", mock.publishedMessages[0].key)
		assert.Equal(t, DeprecatedObservationRecordedTopic, mock.publishedMessages[1].topic)
	})

	t.Run("returns error when primary publish fails", func(t *testing.T) {
		mock := &mockProtoPublisher{publishErr: errors.New("kafka down")}
		pub, err := NewKafkaObservationPublisher(mock)
		require.NoError(t, err)

		obs := createTestObservation(t)
		err = pub.PublishObservationRecorded(context.Background(), obs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to publish ObservationRecorded event")
	})
}

func TestKafkaObservationPublisher_Close(t *testing.T) {
	mock := &mockProtoPublisher{}
	pub, err := NewKafkaObservationPublisher(mock)
	require.NoError(t, err)

	pub.Close()
	assert.True(t, mock.closed)
}

func TestKafkaObservationPublisher_FlushWithTimeout(t *testing.T) {
	mock := &mockProtoPublisher{flushReturn: 3}
	pub, err := NewKafkaObservationPublisher(mock)
	require.NoError(t, err)

	result := pub.FlushWithTimeout(5000)
	assert.Equal(t, 3, result)
}

func TestKafkaObservationPublisher_Publish(t *testing.T) {
	t.Run("publishes ObservationRecorded event", func(t *testing.T) {
		mock := &mockProtoPublisher{}
		pub, err := NewKafkaObservationPublisher(mock)
		require.NoError(t, err)

		event := &marketinformationv1.ObservationRecorded{
			ObservationId: uuid.New().String(),
			DatasetCode:   "TEST_DS",
			Value:         "1.23",
			RecordedAt:    timestamppb.Now(),
		}

		err = pub.Publish(context.Background(), event)
		require.NoError(t, err)
		assert.Len(t, mock.publishedMessages, 2)
		assert.Equal(t, "TEST_DS", mock.publishedMessages[0].key)
	})

	t.Run("returns error for unsupported event type", func(t *testing.T) {
		mock := &mockProtoPublisher{}
		pub, err := NewKafkaObservationPublisher(mock)
		require.NoError(t, err)

		err = pub.Publish(context.Background(), "unsupported")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrUnsupportedEventType)
	})

	t.Run("returns error when primary publish fails", func(t *testing.T) {
		mock := &mockProtoPublisher{publishErr: errors.New("kafka down")}
		pub, err := NewKafkaObservationPublisher(mock)
		require.NoError(t, err)

		event := &marketinformationv1.ObservationRecorded{
			DatasetCode: "TEST",
		}
		err = pub.Publish(context.Background(), event)
		require.Error(t, err)
	})
}

func TestMapObservationToProtoEvent(t *testing.T) {
	obs := createTestObservation(t)
	event := mapObservationToProtoEvent(obs)

	assert.Equal(t, obs.ID().String(), event.ObservationId)
	assert.Equal(t, obs.DataSetCode(), event.DatasetCode)
	assert.Equal(t, obs.ResolutionKey(), event.ResolutionKeyValue)
	assert.Equal(t, obs.Value().String(), event.Value)
	assert.Equal(t, obs.SourceID().String(), event.SourceId)
	assert.NotNil(t, event.ObservedAt)
	assert.NotNil(t, event.RecordedAt)
}

func TestMapQualityLevelToProto(t *testing.T) {
	tests := []struct {
		name     string
		input    domain.QualityLevel
		expected marketinformationv1.QualityLevel
	}{
		{"estimate", domain.QualityLevelEstimate, marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE},
		{"provisional", domain.QualityLevelProvisional, marketinformationv1.QualityLevel_QUALITY_LEVEL_PROVISIONAL},
		{"actual", domain.QualityLevelActual, marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL},
		// VERIFIED maps onto proto slot 4 (still spelled REVISED, semantically VERIFIED; rename pending task 14).
		{"verified maps to revised slot", domain.QualityLevelVerified, marketinformationv1.QualityLevel_QUALITY_LEVEL_REVISED},
		{"unknown maps to unspecified", domain.QualityLevel(99), marketinformationv1.QualityLevel_QUALITY_LEVEL_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapQualityLevelToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShouldPublishObservationEvent(t *testing.T) {
	tests := []struct {
		name     string
		quality  domain.QualityLevel
		expected bool
	}{
		{"actual publishes", domain.QualityLevelActual, true},
		{"verified publishes", domain.QualityLevelVerified, true},
		{"estimate does not publish", domain.QualityLevelEstimate, false},
		{"unknown does not publish", domain.QualityLevel(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, shouldPublishObservationEvent(tt.quality))
		})
	}
}
