package service

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/stretchr/testify/assert"
)

// Minimal stubs for option tests.

type stubDisputeRepo struct{ domain.DisputeRepository }
type stubRunRepo struct{ domain.SettlementRunRepository }
type stubVarianceFinder struct{}

func (stubVarianceFinder) FindByID(_ context.Context, _ uuid.UUID) (*domain.Variance, error) {
	return nil, nil
}

func (stubVarianceFinder) UpdateStatus(_ context.Context, _ uuid.UUID, _ domain.VarianceStatus) error {
	return nil
}

type stubVarianceLister struct{}

func (stubVarianceLister) List(_ context.Context, _ domain.VarianceFilter) ([]*domain.Variance, error) {
	return nil, nil
}

type stubSagaRuntime struct{}

func (stubSagaRuntime) InvokeSaga(_ context.Context, _ string, _ map[string]interface{}) error {
	return nil
}

type stubEventPublisher struct{}

func (stubEventPublisher) Publish(_ context.Context, _ string, _ interface{}) error { return nil }

func TestWithDisputeRepository(t *testing.T) {
	repo := stubDisputeRepo{}
	svc := NewAccountReconciliationService(WithDisputeRepository(repo))
	assert.Equal(t, repo, svc.disputeRepo)
}

func TestWithSettlementRunRepository(t *testing.T) {
	repo := stubRunRepo{}
	svc := NewAccountReconciliationService(WithSettlementRunRepository(repo))
	assert.Equal(t, repo, svc.runRepo)
}

func TestWithVarianceRepository(t *testing.T) {
	repo := stubVarianceFinder{}
	svc := NewAccountReconciliationService(WithVarianceRepository(repo))
	assert.Equal(t, repo, svc.varianceRepo)
}

func TestWithVarianceListRepository(t *testing.T) {
	repo := stubVarianceLister{}
	svc := NewAccountReconciliationService(WithVarianceListRepository(repo))
	assert.Equal(t, repo, svc.varianceListRepo)
}

func TestWithSagaRuntime(t *testing.T) {
	rt := stubSagaRuntime{}
	svc := NewAccountReconciliationService(WithSagaRuntime(rt))
	assert.Equal(t, rt, svc.sagaRuntime)
}

func TestWithEventPublisher(t *testing.T) {
	pub := stubEventPublisher{}
	svc := NewAccountReconciliationService(WithEventPublisher(pub))
	assert.Equal(t, pub, svc.eventPublisher)
}

func TestWithBalanceAssertor(t *testing.T) {
	a := &BalanceAssertor{}
	svc := NewAccountReconciliationService(WithBalanceAssertor(a))
	assert.Equal(t, a, svc.assertor)
}

func TestWithLogger(t *testing.T) {
	l := slog.Default()
	svc := NewAccountReconciliationService(WithLogger(l))
	assert.Equal(t, l, svc.logger)
}

func TestWithSnapshotCapturer(t *testing.T) {
	fn := func(_ context.Context, _ uuid.UUID) error { return nil }
	svc := NewAccountReconciliationService(WithSnapshotCapturer(fn))
	assert.NotNil(t, svc.snapshotCapturer)
}

func TestWithVarianceDetector(t *testing.T) {
	fn := func(_ context.Context, _ uuid.UUID) ([]*domain.Variance, error) { return nil, nil }
	svc := NewAccountReconciliationService(WithVarianceDetector(fn))
	assert.NotNil(t, svc.varianceDetector)
}

func TestWithVarianceValuator(t *testing.T) {
	fn := func(_ context.Context, _ uuid.UUID) error { return nil }
	svc := NewAccountReconciliationService(WithVarianceValuator(fn))
	assert.NotNil(t, svc.varianceValuator)
}

func TestNewAccountReconciliationService_MultipleOptions(t *testing.T) {
	l := slog.Default()
	pub := stubEventPublisher{}
	svc := NewAccountReconciliationService(
		WithLogger(l),
		WithEventPublisher(pub),
	)
	assert.Equal(t, l, svc.logger)
	assert.Equal(t, pub, svc.eventPublisher)
	assert.NotNil(t, svc.pauseSignals)
}

func TestNewAccountReconciliationService_NoOptions(t *testing.T) {
	svc := NewAccountReconciliationService()
	assert.NotNil(t, svc)
	assert.NotNil(t, svc.pauseSignals)
}
