package service

import (
	"testing"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCreateLienResult_ValuationAnalysisKeyAlwaysPresent(t *testing.T) {
	t.Parallel()

	t.Run("nil basis includes valuation_analysis key with nil value", func(t *testing.T) {
		resp := &currentaccountv1.InitiateLienResponse{
			Lien: &currentaccountv1.Lien{LienId: "lien-123"},
		}

		result := buildCreateLienResult(resp, "bucket-1")

		require.Contains(t, result, "valuation_analysis", "valuation_analysis key must always be present")
		assert.Nil(t, result["valuation_analysis"])
		assert.Equal(t, "lien-123", result["lien_id"])
		assert.Equal(t, "bucket-1", result["bucket_id"])
		assert.Equal(t, "ACTIVE", result["status"])
	})

	t.Run("non-nil basis populates valuation_analysis", func(t *testing.T) {
		resp := &currentaccountv1.InitiateLienResponse{
			Lien: &currentaccountv1.Lien{LienId: "lien-456"},
			Basis: &currentaccountv1.ValuationAnalysis{
				MethodId: "spot-rate",
			},
		}

		result := buildCreateLienResult(resp, "")

		require.Contains(t, result, "valuation_analysis", "valuation_analysis key must always be present")
		assert.NotNil(t, result["valuation_analysis"])
		analysis, ok := result["valuation_analysis"].(map[string]any)
		require.True(t, ok, "valuation_analysis should be a map[string]any when basis is set")
		assert.Equal(t, "spot-rate", analysis["method_id"])
	})
}
