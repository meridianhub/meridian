package cache

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
)

func TestProtoToObservation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	validFrom := now.Add(-time.Hour)
	validTo := now

	proto := &marketinformationv1.MarketPriceObservation{
		Id:                 "obs-id",
		DatasetCode:        "ELEC_FORWARD",
		DatasetVersion:     1,
		ResolutionKeyValue: "PEAK_2026Q1",
		ObservedAt:         timestamppb.New(now),
		ValidFrom:          timestamppb.New(validFrom),
		ValidTo:            timestamppb.New(validTo),
		Value:              "45.50",
		Quality:            marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
		SourceId:           "source-123",
		Attributes: []*quantityv1.AttributeEntry{
			{Key: "region", Value: "UK"},
			{Key: "zone", Value: "North"},
		},
	}

	obs, err := protoToObservation(proto)
	require.NoError(t, err)

	assert.True(t, decimal.RequireFromString("45.50").Equal(obs.Value))
	assert.Equal(t, "ELEC_FORWARD", obs.DataSetCode)
	assert.Equal(t, "source-123", obs.SourceID)
	assert.Equal(t, "QUALITY_LEVEL_ESTIMATE", obs.Quality)
	assert.Equal(t, now, obs.ObservedAt)
	assert.Equal(t, validFrom, obs.ValidFrom)
	assert.Equal(t, validTo, obs.ValidTo)
	assert.Equal(t, "UK", obs.Metadata["region"])
	assert.Equal(t, "North", obs.Metadata["zone"])
}

func TestProtoToObservation_InvalidValue(t *testing.T) {
	proto := &marketinformationv1.MarketPriceObservation{
		Value: "not-a-number",
	}

	_, err := protoToObservation(proto)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse observation value")
}

func TestProtoToObservation_NoAttributes(t *testing.T) {
	proto := &marketinformationv1.MarketPriceObservation{
		Value:       "10.00",
		DatasetCode: "TEST",
		Quality:     marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
	}

	obs, err := protoToObservation(proto)
	require.NoError(t, err)
	assert.Nil(t, obs.Metadata)
}

func TestNewMDSSource(t *testing.T) {
	source := NewMDSSource(nil, "ELEC_FORWARD", "GBP/kWh")
	assert.Equal(t, "ELEC_FORWARD", source.datasetCode)
	assert.Equal(t, "GBP/kWh", source.unit)
}
