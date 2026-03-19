package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

func TestDomainObservationToProto(t *testing.T) {
	now := time.Now()
	sourceID := uuid.New()

	obs, err := domain.NewMarketPriceObservation(
		"TEST_DS",
		sourceID,
		"EUR/USD",
		decimal.NewFromFloat(1.1234),
		"USD",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		domain.QualityLevelActual,
		80,
		domain.NewObservationContext(map[string]string{"pair": "EUR/USD"}),
	)
	assert.NoError(t, err)

	t.Run("converts basic fields", func(t *testing.T) {
		proto := domainObservationToProto(obs, nil, 3)

		assert.Equal(t, obs.ID().String(), proto.Id)
		assert.Equal(t, "TEST_DS", proto.DatasetCode)
		assert.Equal(t, int32(3), proto.DatasetVersion)
		assert.Equal(t, "EUR/USD", proto.ResolutionKeyValue)
		assert.Equal(t, "1.1234", proto.Value)
		assert.Equal(t, sourceID.String(), proto.SourceId)
		assert.NotNil(t, proto.ObservedAt)
		assert.NotNil(t, proto.ValidFrom)
		assert.NotNil(t, proto.ValidTo)
		assert.NotNil(t, proto.CreatedAt)
		assert.Nil(t, proto.SupersededAt)
		assert.Empty(t, proto.SupersededById)
		assert.Nil(t, proto.Attributes)
	})

	t.Run("includes attributes when provided", func(t *testing.T) {
		attrs := []*quantityv1.AttributeEntry{
			{Key: "pair", Value: "EUR/USD"},
		}
		proto := domainObservationToProto(obs, attrs, 1)
		assert.Len(t, proto.Attributes, 1)
		assert.Equal(t, "pair", proto.Attributes[0].Key)
	})

	t.Run("includes superseded fields when set", func(t *testing.T) {
		supersededByID := uuid.New()
		supersededAt := now.Add(-time.Hour)

		supersededObs := domain.NewMarketPriceObservationBuilder().
			WithID(uuid.New()).
			WithDataSetCode("DS").
			WithSourceID(sourceID).
			WithResolutionKey("key").
			WithValue(decimal.NewFromFloat(1.0)).
			WithObservedAt(now).
			WithValidFrom(now).
			WithValidTo(now.Add(time.Hour)).
			WithCreatedAt(now).
			WithQualityLevel(domain.QualityLevelEstimate).
			WithTrustLevel(50).
			WithSupersededBy(&supersededByID).
			WithSupersededAt(&supersededAt).
			Build()

		proto := domainObservationToProto(supersededObs, nil, 1)
		assert.NotNil(t, proto.SupersededAt)
		assert.Equal(t, supersededByID.String(), proto.SupersededById)
	})
}

