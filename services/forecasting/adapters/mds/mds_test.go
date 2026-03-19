package mds

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
)

// --- qualityToString tests ---

func TestQualityToString_Estimate(t *testing.T) {
	assert.Equal(t, "ESTIMATE", qualityToString(marketinformationv1.QualityLevel_QUALITY_LEVEL_ESTIMATE))
}

func TestQualityToString_Provisional(t *testing.T) {
	assert.Equal(t, "PROVISIONAL", qualityToString(marketinformationv1.QualityLevel_QUALITY_LEVEL_PROVISIONAL))
}

func TestQualityToString_Actual(t *testing.T) {
	assert.Equal(t, "ACTUAL", qualityToString(marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL))
}

func TestQualityToString_Revised(t *testing.T) {
	assert.Equal(t, "REVISED", qualityToString(marketinformationv1.QualityLevel_QUALITY_LEVEL_REVISED))
}

func TestQualityToString_Unspecified(t *testing.T) {
	assert.Equal(t, "UNSPECIFIED", qualityToString(marketinformationv1.QualityLevel_QUALITY_LEVEL_UNSPECIFIED))
}

func TestQualityToString_Unknown(t *testing.T) {
	// Default case: any value not in the switch
	assert.Equal(t, "UNSPECIFIED", qualityToString(marketinformationv1.QualityLevel(999)))
}

// --- Constructor tests ---

func TestNewMISAdapter(t *testing.T) {
	adapter := NewMISAdapter(nil)
	require.NotNil(t, adapter)
}

func TestNewPublisherAdapter(t *testing.T) {
	adapter := NewPublisherAdapter(nil)
	require.NotNil(t, adapter)
}

// --- NoOpRefDataClient tests ---

func TestNoOpRefDataClient_GetNodeByResolutionKey_ReturnsError(t *testing.T) {
	client := &NoOpRefDataClient{}
	result, err := client.GetNodeByResolutionKey(context.Background(), "tenant-1", "region:us-east-1")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, ErrRefDataNotConfigured)
	assert.Contains(t, err.Error(), "region:us-east-1")
}
