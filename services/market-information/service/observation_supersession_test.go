package service

import (
	"context"
	"testing"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestRecordObservation_SupersedesOpenEndedPeriod is the DAT-2 regression test for
// open-ended validity: when valid_to is omitted, a higher-quality observation must
// still supersede a lower-quality one for the same period. Before the fix, each record
// defaulted valid_to to a non-deterministic now+100y, so the period-scoped supersession
// match failed and the stale ESTIMATE leaked into non-superseded listings.
func TestRecordObservation_SupersedesOpenEndedPeriod(t *testing.T) {
	server, _, cleanup := setupTestServerForObservation(t)
	defer cleanup()

	ctx := context.Background()
	datasetCode, sourceCode := setupTestDataSetAndSource(t, server, ctx)

	now := time.Now()
	attrs := []*quantityv1.AttributeEntry{{Key: "currency_pair", Value: "EUR/USD"}}

	// ESTIMATE with valid_to OMITTED (open-ended period).
	_, err := server.RecordObservation(ctx, &pb.RecordObservationRequest{
		DatasetCode: datasetCode,
		SourceCode:  sourceCode,
		Value:       "1.1000",
		ObservedAt:  timestamppb.New(now),
		ValidFrom:   timestamppb.New(now),
		Quality:     pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
		Attributes:  attrs,
	})
	require.NoError(t, err)

	// Higher-quality ACTUAL for the same open-ended period (valid_to also omitted).
	_, err = server.RecordObservation(ctx, &pb.RecordObservationRequest{
		DatasetCode: datasetCode,
		SourceCode:  sourceCode,
		Value:       "1.1050",
		ObservedAt:  timestamppb.New(now),
		ValidFrom:   timestamppb.New(now),
		Quality:     pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
		Attributes:  attrs,
	})
	require.NoError(t, err)

	// Non-superseded listing must contain only the ACTUAL: the ESTIMATE was superseded
	// despite the omitted (NULL) valid_to.
	nonSuperseded, err := server.ListObservations(ctx, &pb.ListObservationsRequest{
		DatasetCode:        datasetCode,
		ResolutionKeyValue: "EUR/USD",
		IncludeSuperseded:  false,
	})
	require.NoError(t, err)
	require.Len(t, nonSuperseded.Observations, 1, "stale ESTIMATE must be superseded for open-ended period")
	assert.Equal(t, pb.QualityLevel_QUALITY_LEVEL_ACTUAL, nonSuperseded.Observations[0].Quality)
	assert.Nil(t, nonSuperseded.Observations[0].ValidTo, "open-ended observation emits no valid_to")

	// Both rows are retained when superseded rows are included.
	all, err := server.ListObservations(ctx, &pb.ListObservationsRequest{
		DatasetCode:        datasetCode,
		ResolutionKeyValue: "EUR/USD",
		IncludeSuperseded:  true,
	})
	require.NoError(t, err)
	assert.Len(t, all.Observations, 2)
}
