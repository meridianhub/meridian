package stripe

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripego "github.com/stripe/stripe-go/v82"
)

// mockRunRepo implements domain.SettlementRunRepository for testing.
type mockRunRepo struct {
	created   []*domain.SettlementRun
	updated   []*domain.SettlementRun
	runs      map[uuid.UUID]*domain.SettlementRun
	createErr error
	updateErr error
	findErr   error
}

func newMockRunRepo() *mockRunRepo {
	return &mockRunRepo{
		runs: make(map[uuid.UUID]*domain.SettlementRun),
	}
}

func (m *mockRunRepo) Create(_ context.Context, run *domain.SettlementRun) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.created = append(m.created, run)
	m.runs[run.RunID] = run
	return nil
}

func (m *mockRunRepo) FindByID(_ context.Context, runID uuid.UUID) (*domain.SettlementRun, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	run, ok := m.runs[runID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return run, nil
}

func (m *mockRunRepo) Update(_ context.Context, run *domain.SettlementRun) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updated = append(m.updated, run)
	m.runs[run.RunID] = run
	return nil
}

func (m *mockRunRepo) List(_ context.Context, _ domain.RunFilter) ([]*domain.SettlementRun, error) {
	return nil, nil
}

// mockSnapRepo implements domain.SettlementSnapshotRepository for testing.
type mockSnapRepo struct {
	snapshots   []*domain.SettlementSnapshot
	batchCount  int
	deleteCount int
	createErr   error
	batchErr    error
	deleteErr   error
}

func (m *mockSnapRepo) Create(_ context.Context, snapshot *domain.SettlementSnapshot) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.snapshots = append(m.snapshots, snapshot)
	return nil
}

func (m *mockSnapRepo) CreateBatch(_ context.Context, snapshots []*domain.SettlementSnapshot) error {
	if m.batchErr != nil {
		return m.batchErr
	}
	m.batchCount++
	m.snapshots = append(m.snapshots, snapshots...)
	return nil
}

func (m *mockSnapRepo) FindByID(_ context.Context, _ uuid.UUID) (*domain.SettlementSnapshot, error) {
	return nil, nil
}

func (m *mockSnapRepo) FindByRunID(_ context.Context, _ uuid.UUID) ([]*domain.SettlementSnapshot, error) {
	return m.snapshots, nil
}

func (m *mockSnapRepo) DeleteByRunID(_ context.Context, _ uuid.UUID) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deleteCount++
	return nil
}

func (m *mockSnapRepo) MarkRunSnapshotsFinal(_ context.Context, _ uuid.UUID) error {
	return nil
}

func newTestIngestor(t *testing.T, transactions []*stripego.BalanceTransaction, listerErr error) (*SettlementIngestor, *mockRunRepo, *mockSnapRepo) {
	t.Helper()

	lister := &mockLister{transactions: transactions, err: listerErr}
	client, err := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
		AccountID: "acct_test",
	}, slog.Default())
	require.NoError(t, err)

	transformer := NewSettlementTransformer()
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapRepo{}

	ingestor, err := NewSettlementIngestor(client, transformer, runRepo, snapRepo, SettlementIngestorConfig{
		AccountID:         "acct_test",
		InternalAccountID: "meridian-001",
		IngestionTimeout:  30 * time.Second,
	}, slog.Default())
	require.NoError(t, err)

	return ingestor, runRepo, snapRepo
}

func TestNewSettlementIngestor_Validation(t *testing.T) {
	lister := &mockLister{}
	client, _ := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
		AccountID: "acct_test",
	}, slog.Default())
	transformer := NewSettlementTransformer()
	runRepo := newMockRunRepo()
	snapRepo := &mockSnapRepo{}
	cfg := SettlementIngestorConfig{
		AccountID:         "acct_test",
		InternalAccountID: "meridian-001",
	}

	t.Run("nil client", func(t *testing.T) {
		_, err := NewSettlementIngestor(nil, transformer, runRepo, snapRepo, cfg, slog.Default())
		require.ErrorIs(t, err, ErrNilTransactionClient)
	})

	t.Run("nil transformer", func(t *testing.T) {
		_, err := NewSettlementIngestor(client, nil, runRepo, snapRepo, cfg, slog.Default())
		require.ErrorIs(t, err, ErrNilTransformer)
	})

	t.Run("nil run repo", func(t *testing.T) {
		_, err := NewSettlementIngestor(client, transformer, nil, snapRepo, cfg, slog.Default())
		require.ErrorIs(t, err, ErrNilRunRepo)
	})

	t.Run("nil snap repo", func(t *testing.T) {
		_, err := NewSettlementIngestor(client, transformer, runRepo, nil, cfg, slog.Default())
		require.ErrorIs(t, err, ErrNilSnapshotRepo)
	})

	t.Run("empty account ID", func(t *testing.T) {
		_, err := NewSettlementIngestor(client, transformer, runRepo, snapRepo, SettlementIngestorConfig{
			InternalAccountID: "meridian-001",
		}, slog.Default())
		require.ErrorIs(t, err, ErrEmptyAccountID)
	})

	t.Run("empty internal account ID", func(t *testing.T) {
		_, err := NewSettlementIngestor(client, transformer, runRepo, snapRepo, SettlementIngestorConfig{
			AccountID: "acct_test",
		}, slog.Default())
		require.ErrorIs(t, err, ErrEmptyInternalAccountID)
	})

	t.Run("valid config", func(t *testing.T) {
		ingestor, err := NewSettlementIngestor(client, transformer, runRepo, snapRepo, cfg, slog.Default())
		require.NoError(t, err)
		assert.NotNil(t, ingestor)
	})

	t.Run("default timeout when zero", func(t *testing.T) {
		ingestor, err := NewSettlementIngestor(client, transformer, runRepo, snapRepo, SettlementIngestorConfig{
			AccountID:         "acct_test",
			InternalAccountID: "meridian-001",
			IngestionTimeout:  0,
		}, slog.Default())
		require.NoError(t, err)
		assert.Equal(t, defaultIngestionTimeout, ingestor.config.IngestionTimeout)
	})
}

func TestIngestSettlement_Success(t *testing.T) {
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	transactions := []*stripego.BalanceTransaction{
		{ID: "txn_1", Amount: 1000, Net: 970, Fee: 30, Currency: "gbp", Type: stripego.BalanceTransactionTypeCharge, AvailableOn: now.Unix()},
		{ID: "txn_2", Amount: -200, Net: -200, Fee: 0, Currency: "gbp", Type: stripego.BalanceTransactionTypeRefund, AvailableOn: now.Unix()},
	}

	ingestor, runRepo, snapRepo := newTestIngestor(t, transactions, nil)

	err := ingestor.IngestSettlement(context.Background(), periodStart, periodEnd)
	require.NoError(t, err)

	// Verify run was created and completed
	require.Len(t, runRepo.created, 1)
	run := runRepo.created[0]
	assert.Equal(t, "meridian-001", run.AccountID)
	assert.Equal(t, domain.ReconciliationScopeAccount, run.Scope)
	assert.Equal(t, domain.SettlementTypeDaily, run.SettlementType)
	assert.Equal(t, "stripe-settlement-ingestor", run.InitiatedBy)

	// Verify run was updated to RUNNING then COMPLETED (2 updates for Start + Complete)
	assert.GreaterOrEqual(t, len(runRepo.updated), 2)
	lastUpdate := runRepo.updated[len(runRepo.updated)-1]
	assert.Equal(t, domain.RunStatusCompleted, lastUpdate.Status)

	// Verify snapshots were created
	assert.Len(t, snapRepo.snapshots, 2)
	assert.Equal(t, 1, snapRepo.batchCount)

	// Verify snapshot content
	snap1 := snapRepo.snapshots[0]
	assert.Equal(t, "meridian-001", snap1.AccountID)
	assert.Equal(t, "GBP", snap1.InstrumentCode)
	assert.True(t, decimal.NewFromFloat(9.70).Equal(snap1.ActualBalance))
	assert.Equal(t, "txn_1", snap1.Attributes["external_reference_id"])
	assert.Equal(t, "PAYMENT", snap1.Attributes["settlement_type"])

	snap2 := snapRepo.snapshots[1]
	assert.True(t, decimal.NewFromFloat(-2.00).Equal(snap2.ActualBalance))
	assert.Equal(t, "REFUND", snap2.Attributes["settlement_type"])

	// Verify idempotent cleanup was called
	assert.Equal(t, 1, snapRepo.deleteCount)
}

func TestIngestSettlement_EmptyTransactions(t *testing.T) {
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	ingestor, runRepo, snapRepo := newTestIngestor(t, nil, nil)

	err := ingestor.IngestSettlement(context.Background(), periodStart, periodEnd)
	require.NoError(t, err)

	// Run should be created and completed with zero variance
	require.Len(t, runRepo.created, 1)
	lastUpdate := runRepo.updated[len(runRepo.updated)-1]
	assert.Equal(t, domain.RunStatusCompleted, lastUpdate.Status)
	assert.Equal(t, 0, lastUpdate.VarianceCount)

	// No snapshots should be created
	assert.Empty(t, snapRepo.snapshots)
}

func TestIngestSettlement_FetchError(t *testing.T) {
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	fetchErr := errors.New("stripe API unavailable")
	ingestor, runRepo, _ := newTestIngestor(t, nil, fetchErr)

	err := ingestor.IngestSettlement(context.Background(), periodStart, periodEnd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch stripe transactions")

	// Run should be marked as FAILED
	require.NotEmpty(t, runRepo.updated)
	lastUpdate := runRepo.updated[len(runRepo.updated)-1]
	assert.Equal(t, domain.RunStatusFailed, lastUpdate.Status)
	assert.Contains(t, lastUpdate.FailureReason, "stripe API unavailable")
}

func TestIngestSettlement_BatchInsertError(t *testing.T) {
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	transactions := []*stripego.BalanceTransaction{
		{ID: "txn_1", Amount: 1000, Net: 970, Fee: 30, Currency: "gbp", Type: stripego.BalanceTransactionTypeCharge, AvailableOn: now.Unix()},
	}

	ingestor, runRepo, snapRepo := newTestIngestor(t, transactions, nil)
	snapRepo.batchErr = errors.New("database write failed")

	err := ingestor.IngestSettlement(context.Background(), periodStart, periodEnd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to persist snapshots")

	// Run should be marked as FAILED
	var foundFailed bool
	for _, u := range runRepo.updated {
		if u.Status == domain.RunStatusFailed {
			foundFailed = true
			break
		}
	}
	assert.True(t, foundFailed, "expected run to be marked FAILED")
}

func TestIngestSettlement_CreateRunError(t *testing.T) {
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	ingestor, runRepo, _ := newTestIngestor(t, nil, nil)
	runRepo.createErr = errors.New("database conflict")

	err := ingestor.IngestSettlement(context.Background(), periodStart, periodEnd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to persist settlement run")
}

func TestIngestPreviousDay(t *testing.T) {
	transactions := []*stripego.BalanceTransaction{
		{ID: "txn_prev", Amount: 500, Net: 500, Currency: "gbp", Type: stripego.BalanceTransactionTypeCharge, AvailableOn: time.Now().Unix()},
	}

	ingestor, runRepo, _ := newTestIngestor(t, transactions, nil)

	err := ingestor.IngestPreviousDay(context.Background())
	require.NoError(t, err)

	// Verify the period covers yesterday midnight to today midnight UTC
	require.Len(t, runRepo.created, 1)
	run := runRepo.created[0]
	now := time.Now().UTC()
	expectedStart := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.UTC)
	expectedEnd := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	assert.Equal(t, expectedStart, run.PeriodStart)
	assert.Equal(t, expectedEnd, run.PeriodEnd)
}
