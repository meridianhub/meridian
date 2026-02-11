// Package mds provides adapter implementations that bridge the forecasting
// service's internal interfaces to the Market Data Service gRPC client.
package mds

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/forecasting/starlark"
	misclient "github.com/meridianhub/meridian/services/market-information/client"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MISAdapter wraps the Market Information Service client to implement
// the starlark.MISClient interface for fetching historical observations.
type MISAdapter struct {
	client *misclient.Client
}

// NewMISAdapter creates a new MIS adapter.
func NewMISAdapter(client *misclient.Client) *MISAdapter {
	return &MISAdapter{client: client}
}

// FetchObservations retrieves historical observations for a dataset code from MDS.
func (a *MISAdapter) FetchObservations(ctx context.Context, datasetCode string, before time.Time) ([]starlark.Observation, error) {
	resp, err := a.client.ListObservations(ctx, &marketinformationv1.ListObservationsRequest{
		DatasetCode: datasetCode,
		ObservedTo:  timestamppb.New(before),
		PageSize:    1000,
	})
	if err != nil {
		return nil, err
	}

	observations := make([]starlark.Observation, 0, len(resp.GetObservations()))
	for _, obs := range resp.GetObservations() {
		val, err := decimal.NewFromString(obs.GetValue())
		if err != nil {
			continue
		}
		observations = append(observations, starlark.Observation{
			Timestamp: obs.GetValidFrom().AsTime(),
			Value:     val,
			Quality:   qualityToString(obs.GetQuality()),
		})
	}

	return observations, nil
}

// PublisherAdapter wraps the Market Information Service client to implement
// the handler.MDSPublisher interface for publishing forecast observations.
type PublisherAdapter struct {
	client *misclient.Client
}

// NewPublisherAdapter creates a new publisher adapter.
func NewPublisherAdapter(client *misclient.Client) *PublisherAdapter {
	return &PublisherAdapter{client: client}
}

// RecordObservationBatch publishes a batch of observations to MDS.
func (a *PublisherAdapter) RecordObservationBatch(
	ctx context.Context,
	observations []*marketinformationv1.BatchObservationEntry,
) (*marketinformationv1.RecordObservationBatchResponse, error) {
	return a.client.RecordObservationBatch(ctx, observations)
}

func qualityToString(q marketinformationv1.QualityLevel) string {
	switch q {
	case marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE:
		return "ESTIMATE"
	case marketinformationv1.QualityLevel_QUALITY_LEVEL_PROVISIONAL:
		return "PROVISIONAL"
	case marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL:
		return "ACTUAL"
	case marketinformationv1.QualityLevel_QUALITY_LEVEL_REVISED:
		return "REVISED"
	case marketinformationv1.QualityLevel_QUALITY_LEVEL_UNSPECIFIED:
		return "UNSPECIFIED"
	default:
		return "UNSPECIFIED"
	}
}
