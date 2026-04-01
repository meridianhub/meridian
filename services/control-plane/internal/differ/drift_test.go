package differ

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoOpDriftDetector_ReturnsEmpty(t *testing.T) {
	d := &NoOpDriftDetector{}
	warnings, err := d.DetectDrift(context.Background(), &controlplanev1.Manifest{})
	assert.NoError(t, err)
	assert.Nil(t, warnings)
}

func TestNoOpDriftDetector_NilManifest(t *testing.T) {
	d := &NoOpDriftDetector{}
	warnings, err := d.DetectDrift(context.Background(), nil)
	assert.NoError(t, err)
	assert.Nil(t, warnings)
}

func TestDriftWarning_Fields(t *testing.T) {
	w := DriftWarning{
		ResourceType: ResourceInstrument,
		ResourceCode: "GBP",
		Description:  "Instrument GBP was modified outside of manifest apply",
	}
	assert.Equal(t, ResourceInstrument, w.ResourceType)
	assert.Equal(t, "GBP", w.ResourceCode)
	assert.Contains(t, w.Description, "GBP")
}

func TestManifestDiffer_WithNoOpDriftDetector(t *testing.T) {
	drift := &NoOpDriftDetector{}
	d := New(nil, drift, nil)
	require.NotNil(t, d)

	plan, err := d.Diff(context.Background(), nil, &controlplanev1.Manifest{})
	require.NoError(t, err)
	assert.Empty(t, plan.DriftWarnings)
}

func TestValRuleKey_Direct(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want string
	}{
		{"KWH", "GBP", "KWH->GBP"},
		{"kwh", "gbp", "KWH->GBP"},
		{"EUR", "USD", "EUR->USD"},
	}
	for _, tc := range tests {
		t.Run(tc.from+"->"+tc.to, func(t *testing.T) {
			assert.Equal(t, tc.want, valRuleKey(tc.from, tc.to))
		})
	}
}
