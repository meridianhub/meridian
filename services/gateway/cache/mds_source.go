package cache

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	miclient "github.com/meridianhub/meridian/services/market-information/client"
)

// MDSSource implements Source by querying the Market Data Service via gRPC.
// It queries ESTIMATE quality forward curve observations using the ListObservations API.
//
// The unit field is set from the DataSetDefinition at construction time, since
// the MarketPriceObservation proto does not carry unit information directly.
type MDSSource struct {
	client      *miclient.Client
	datasetCode string
	unit        string
}

// NewMDSSource creates a new MDSSource that queries forward curve observations
// from the given MDS client for the specified dataset code.
// The unit parameter should come from the DataSetDefinition.unit field.
func NewMDSSource(client *miclient.Client, datasetCode string, unit string) *MDSSource {
	return &MDSSource{
		client:      client,
		datasetCode: datasetCode,
		unit:        unit,
	}
}

// GetForwardPrice queries MDS for a single ESTIMATE quality forward curve observation.
func (s *MDSSource) GetForwardPrice(ctx context.Context, resolutionKey string, ts time.Time) (*Observation, error) {
	resp, err := s.client.ListObservations(ctx, &marketinformationv1.ListObservationsRequest{
		DatasetCode:        s.datasetCode,
		ResolutionKeyValue: resolutionKey,
		ValidAt:            timestamppb.New(ts),
		QualityFilter:      marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
		PageSize:           1,
	})
	if err != nil {
		return nil, fmt.Errorf("query MDS for forward price: %w", err)
	}

	if len(resp.Observations) == 0 {
		return nil, ErrObservationNotFound
	}

	obs, err := protoToObservation(resp.Observations[0])
	if err != nil {
		return nil, err
	}
	obs.Unit = s.unit
	return obs, nil
}

// GetForwardPriceRange queries MDS for ESTIMATE quality observations in a time range.
func (s *MDSSource) GetForwardPriceRange(ctx context.Context, resolutionKey string, start, end time.Time) ([]*Observation, error) {
	resp, err := s.client.ListObservations(ctx, &marketinformationv1.ListObservationsRequest{
		DatasetCode:        s.datasetCode,
		ResolutionKeyValue: resolutionKey,
		ObservedFrom:       timestamppb.New(start),
		ObservedTo:         timestamppb.New(end),
		QualityFilter:      marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE,
		PageSize:           1000,
	})
	if err != nil {
		return nil, fmt.Errorf("query MDS for forward price range: %w", err)
	}

	observations := make([]*Observation, 0, len(resp.Observations))
	for _, proto := range resp.Observations {
		obs, err := protoToObservation(proto)
		if err != nil {
			slog.Warn("skipping malformed observation",
				"dataset_code", s.datasetCode,
				"observation_id", proto.GetId(),
				"error", err,
			)
			continue
		}
		obs.Unit = s.unit
		observations = append(observations, obs)
	}

	return observations, nil
}

// protoToObservation converts a proto MarketPriceObservation to a cache Observation.
func protoToObservation(proto *marketinformationv1.MarketPriceObservation) (*Observation, error) {
	value, err := decimal.NewFromString(proto.Value)
	if err != nil {
		return nil, fmt.Errorf("parse observation value %q: %w", proto.Value, err)
	}

	obs := &Observation{
		Value:       value,
		DataSetCode: proto.DatasetCode,
		SourceID:    proto.SourceId,
		Quality:     proto.Quality.String(),
	}

	if proto.ObservedAt != nil {
		obs.ObservedAt = proto.ObservedAt.AsTime()
	}
	if proto.ValidFrom != nil {
		obs.ValidFrom = proto.ValidFrom.AsTime()
	}
	if proto.ValidTo != nil {
		obs.ValidTo = proto.ValidTo.AsTime()
	}

	// Extract metadata from attributes
	if len(proto.Attributes) > 0 {
		obs.Metadata = make(map[string]string, len(proto.Attributes))
		for _, attr := range proto.Attributes {
			obs.Metadata[attr.Key] = attr.Value
		}
	}

	return obs, nil
}
