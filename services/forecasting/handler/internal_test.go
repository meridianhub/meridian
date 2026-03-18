package handler_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	forecastingv1 "github.com/meridianhub/meridian/api/proto/meridian/forecasting/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/forecasting/domain"
	"github.com/meridianhub/meridian/services/forecasting/handler"
	"github.com/meridianhub/meridian/services/forecasting/starlark"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// --- ComputeForwardCurveInternal tests ---

func TestComputeForwardCurveInternal_Success_EmptyForecast(t *testing.T) {
	// Tests the success path including publishToMDS with empty points.
	// ComputeForwardCurveInternal uses time.Now() internally so scripts cannot
	// generate valid non-empty forecasts (granularity alignment would fail).
	// We verify the full code path through the empty list short-circuit in publishToMDS.
	strategyID := uuid.New()
	strategy := domain.NewForecastingStrategyBuilder().
		WithID(strategyID).
		WithTenantID("org_test_tenant").
		WithName("scheduled-strategy").
		WithStarlarkCode(`def compute_forecast(ctx): return []`).
		WithHorizonHours(24).
		WithGranularityHours(1).
		WithSchedule("0 * * * *").
		WithInputDatasetCodes([]string{}).
		WithOutputDatasetCode("FORECAST").
		WithStatus(domain.StrategyStatusActive).
		WithVersion(2).
		Build()

	repo := &mockStrategyRepo{
		findByIDFn: func(_ context.Context, _ uuid.UUID) (domain.ForecastingStrategy, error) {
			return strategy, nil
		},
	}

	svc := newTestService(t, repo, nil, nil)

	tid, _ := tenant.NewTenantID("org_test_tenant")
	ctx := tenant.WithTenant(context.Background(), tid)

	result, err := svc.ComputeForwardCurveInternal(ctx, strategyID)
	require.NoError(t, err)
	assert.Equal(t, int32(0), result.PointCount)
	assert.Equal(t, int64(2), result.StrategyVersion)
}

func TestComputeForwardCurveInternal_StrategyNotFound(t *testing.T) {
	repo := &mockStrategyRepo{
		findByIDFn: func(_ context.Context, _ uuid.UUID) (domain.ForecastingStrategy, error) {
			return domain.ForecastingStrategy{}, domain.ErrStrategyNotFound
		},
	}
	svc := newTestService(t, repo, nil, nil)

	tid, _ := tenant.NewTenantID("org_test_tenant")
	ctx := tenant.WithTenant(context.Background(), tid)

	_, err := svc.ComputeForwardCurveInternal(ctx, uuid.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "find strategy")
}

func TestComputeForwardCurveInternal_NotActiveStatus(t *testing.T) {
	strategyID := uuid.New()
	strategy := domain.NewForecastingStrategyBuilder().
		WithID(strategyID).
		WithTenantID("org_test_tenant").
		WithName("draft-strategy").
		WithStarlarkCode("ignored").
		WithHorizonHours(24).
		WithGranularityHours(1).
		WithSchedule("0 * * * *").
		WithInputDatasetCodes([]string{}).
		WithOutputDatasetCode("FORECAST").
		WithStatus(domain.StrategyStatusDraft).
		WithVersion(1).
		Build()

	repo := &mockStrategyRepo{
		findByIDFn: func(_ context.Context, _ uuid.UUID) (domain.ForecastingStrategy, error) {
			return strategy, nil
		},
	}
	svc := newTestService(t, repo, nil, nil)

	tid, _ := tenant.NewTenantID("org_test_tenant")
	ctx := tenant.WithTenant(context.Background(), tid)

	_, err := svc.ComputeForwardCurveInternal(ctx, strategyID)
	require.Error(t, err)
	assert.ErrorIs(t, err, handler.ErrNotActive)
}

func TestComputeForwardCurveInternal_StarlarkFailure(t *testing.T) {
	strategyID := uuid.New()
	strategy := domain.NewForecastingStrategyBuilder().
		WithID(strategyID).
		WithTenantID("org_test_tenant").
		WithName("bad-strategy").
		WithStarlarkCode(`def compute_forecast(ctx): return "not a list"`).
		WithHorizonHours(24).
		WithGranularityHours(1).
		WithSchedule("0 * * * *").
		WithInputDatasetCodes([]string{}).
		WithOutputDatasetCode("FORECAST").
		WithStatus(domain.StrategyStatusActive).
		WithVersion(1).
		Build()

	repo := &mockStrategyRepo{
		findByIDFn: func(_ context.Context, _ uuid.UUID) (domain.ForecastingStrategy, error) {
			return strategy, nil
		},
	}

	svc := newTestService(t, repo, nil, nil)

	tid, _ := tenant.NewTenantID("org_test_tenant")
	ctx := tenant.WithTenant(context.Background(), tid)

	_, err := svc.ComputeForwardCurveInternal(ctx, strategyID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "starlark execution")
}

func TestComputeForwardCurveInternal_WithRefDataRequired_TenantPresent(t *testing.T) {
	// Tests the ref data branch when tenant is in context.
	// The ref data lookup will fail (mock returns error), covering the TenantID assignment line.
	strategyID := uuid.New()
	strategy := domain.NewForecastingStrategyBuilder().
		WithID(strategyID).
		WithTenantID("org_test_tenant").
		WithName("refdata-strategy").
		WithStarlarkCode(`def compute_forecast(ctx): return []`).
		WithHorizonHours(1).
		WithGranularityHours(1).
		WithSchedule("0 * * * *").
		WithInputDatasetCodes([]string{}).
		WithOutputDatasetCode("FORECAST").
		WithReferenceDataResolutionKey("region:us-east-1").
		WithStatus(domain.StrategyStatusActive).
		WithVersion(1).
		Build()

	repo := &mockStrategyRepo{
		findByIDFn: func(_ context.Context, _ uuid.UUID) (domain.ForecastingStrategy, error) {
			return strategy, nil
		},
	}

	// RefData client that returns an error - triggers coverage of input.TenantID assignment
	refClient := &mockRefDataClient{
		getFn: func(_ context.Context, _, _ string) (*starlark.ReferenceData, error) {
			return nil, errors.New("ref data service unavailable")
		},
	}
	misClient := &mockMISClient{}
	runner, err := starlark.NewForecastRunner(starlark.ForecastRunnerConfig{
		MISClient: misClient,
		RefData:   refClient,
	})
	require.NoError(t, err)

	svc, err := handler.NewService(repo, runner, nil, slog.Default())
	require.NoError(t, err)

	tid, _ := tenant.NewTenantID("org_test_tenant")
	ctx := tenant.WithTenant(context.Background(), tid)

	_, err = svc.ComputeForwardCurveInternal(ctx, strategyID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "starlark execution")
}

func TestComputeForwardCurveInternal_WithRefDataRequired_NoTenant(t *testing.T) {
	strategyID := uuid.New()
	strategy := domain.NewForecastingStrategyBuilder().
		WithID(strategyID).
		WithTenantID("org_test_tenant").
		WithName("refdata-strategy").
		WithStarlarkCode(`def compute_forecast(ctx): return []`).
		WithHorizonHours(1).
		WithGranularityHours(1).
		WithSchedule("0 * * * *").
		WithInputDatasetCodes([]string{}).
		WithOutputDatasetCode("FORECAST").
		WithReferenceDataResolutionKey("region:us-east-1").
		WithStatus(domain.StrategyStatusActive).
		WithVersion(1).
		Build()

	repo := &mockStrategyRepo{
		findByIDFn: func(_ context.Context, _ uuid.UUID) (domain.ForecastingStrategy, error) {
			return strategy, nil
		},
	}
	svc := newTestService(t, repo, nil, nil)

	// No tenant in context
	_, err := svc.ComputeForwardCurveInternal(context.Background(), strategyID)
	require.Error(t, err)
	assert.ErrorIs(t, err, handler.ErrTenantIDRequired)
}

func TestComputeForwardCurveInternal_MDSPublishFailure(t *testing.T) {
	// Tests publishToMDS failure via the nil-mds "no publish" and then
	// an actual MDS publisher that fails. We create a service with a non-nil MDS
	// publisher that fails and a script that returns empty list.
	// Since empty list short-circuits publishToMDS before calling mds, we verify
	// that the function still returns nil (no error) for empty forecasts.
	// The full MDS error path is tested via ComputeForwardCurve tests (TestComputeForwardCurve_MDSPublishFailure).
	strategyID := uuid.New()
	strategy := domain.NewForecastingStrategyBuilder().
		WithID(strategyID).
		WithTenantID("org_test_tenant").
		WithName("mds-fail").
		WithStarlarkCode(`def compute_forecast(ctx): return []`).
		WithHorizonHours(24).
		WithGranularityHours(1).
		WithSchedule("0 * * * *").
		WithInputDatasetCodes([]string{}).
		WithOutputDatasetCode("FORECAST").
		WithStatus(domain.StrategyStatusActive).
		WithVersion(1).
		Build()

	repo := &mockStrategyRepo{
		findByIDFn: func(_ context.Context, _ uuid.UUID) (domain.ForecastingStrategy, error) {
			return strategy, nil
		},
	}

	mdsPublisher := &mockMDSPublisher{
		batchFn: func(_ context.Context, _ []*marketinformationv1.BatchObservationEntry) (*marketinformationv1.RecordObservationBatchResponse, error) {
			return nil, errors.New("MDS unavailable")
		},
	}

	misClient := &mockMISClient{}
	refClient := &mockRefDataClient{}
	runner, err := starlark.NewForecastRunner(starlark.ForecastRunnerConfig{
		MISClient: misClient,
		RefData:   refClient,
	})
	require.NoError(t, err)

	svc, err := handler.NewService(repo, runner, mdsPublisher, slog.Default())
	require.NoError(t, err)

	tid, _ := tenant.NewTenantID("org_test_tenant")
	ctx := tenant.WithTenant(context.Background(), tid)

	// Empty forecast -> publishToMDS skips batch call -> success
	result, err := svc.ComputeForwardCurveInternal(ctx, strategyID)
	require.NoError(t, err)
	assert.Equal(t, int32(0), result.PointCount)
}

// --- mapDomainError tests (via ComputeForwardCurve which calls mapDomainError) ---

func TestComputeForwardCurve_VersionMismatch(t *testing.T) {
	repo := &mockStrategyRepo{
		findByIDFn: func(_ context.Context, _ uuid.UUID) (domain.ForecastingStrategy, error) {
			return domain.ForecastingStrategy{}, domain.ErrVersionMismatch
		},
	}
	svc := newTestService(t, repo, nil, nil)

	ctx := tenantCtx("org_test_tenant")
	_, err := svc.ComputeForwardCurve(ctx, &forecastingv1.ComputeForwardCurveRequest{
		StrategyId: uuid.New().String(),
	})
	require.Error(t, err)
	s, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, s.Code())
	assert.Contains(t, s.Message(), "modified concurrently")
}

func TestComputeForwardCurve_UnknownDomainError(t *testing.T) {
	repo := &mockStrategyRepo{
		findByIDFn: func(_ context.Context, _ uuid.UUID) (domain.ForecastingStrategy, error) {
			return domain.ForecastingStrategy{}, errors.New("unexpected database failure")
		},
	}
	svc := newTestService(t, repo, nil, nil)

	ctx := tenantCtx("org_test_tenant")
	_, err := svc.ComputeForwardCurve(ctx, &forecastingv1.ComputeForwardCurveRequest{
		StrategyId: uuid.New().String(),
	})
	require.Error(t, err)
	s, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, s.Code())
	assert.Contains(t, s.Message(), "internal error")
}
