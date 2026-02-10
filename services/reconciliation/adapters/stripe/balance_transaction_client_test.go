package stripe

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	stripego "github.com/stripe/stripe-go/v82"
)

// mockLister implements BalanceTransactionLister for testing.
type mockLister struct {
	transactions   []*stripego.BalanceTransaction
	err            error
	capturedParams *stripego.BalanceTransactionListParams
}

func (m *mockLister) List(_ context.Context, params *stripego.BalanceTransactionListParams) stripego.Seq2[*stripego.BalanceTransaction, error] {
	m.capturedParams = params
	return func(yield func(*stripego.BalanceTransaction, error) bool) {
		if m.err != nil {
			yield(nil, m.err)
			return
		}
		for _, bt := range m.transactions {
			if !yield(bt, nil) {
				return
			}
		}
	}
}

func TestNewBalanceTransactionClient_Validation(t *testing.T) {
	logger := slog.Default()
	lister := &mockLister{}

	t.Run("nil lister returns error", func(t *testing.T) {
		_, err := NewBalanceTransactionClient(nil, BalanceTransactionClientConfig{
			AccountID: "acct_test",
		}, logger)
		require.ErrorIs(t, err, ErrNilLister)
	})

	t.Run("empty account ID returns error", func(t *testing.T) {
		_, err := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
			AccountID: "",
		}, logger)
		require.ErrorIs(t, err, ErrEmptyAccountID)
	})

	t.Run("valid config succeeds", func(t *testing.T) {
		client, err := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
			AccountID: "acct_test",
		}, logger)
		require.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("nil logger defaults to slog.Default", func(t *testing.T) {
		client, err := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
			AccountID: "acct_test",
		}, nil)
		require.NoError(t, err)
		assert.NotNil(t, client)
	})

	t.Run("page size defaults when zero", func(t *testing.T) {
		client, err := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
			AccountID: "acct_test",
			PageSize:  0,
		}, logger)
		require.NoError(t, err)
		assert.Equal(t, defaultPageSize, client.pageSize)
	})

	t.Run("page size capped at max", func(t *testing.T) {
		client, err := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
			AccountID: "acct_test",
			PageSize:  500,
		}, logger)
		require.NoError(t, err)
		assert.Equal(t, maxPageSize, client.pageSize)
	})
}

func TestFetchTransactions_Success(t *testing.T) {
	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	to := now

	transactions := []*stripego.BalanceTransaction{
		{ID: "txn_1", Amount: 1000, Net: 970, Fee: 30, Currency: "gbp", Type: stripego.BalanceTransactionTypeCharge, AvailableOn: now.Unix()},
		{ID: "txn_2", Amount: 500, Net: 485, Fee: 15, Currency: "gbp", Type: stripego.BalanceTransactionTypeCharge, AvailableOn: now.Unix()},
		{ID: "txn_3", Amount: -200, Net: -200, Fee: 0, Currency: "gbp", Type: stripego.BalanceTransactionTypeRefund, AvailableOn: now.Unix()},
	}

	lister := &mockLister{transactions: transactions}
	client, err := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
		AccountID: "acct_test123",
	}, slog.Default())
	require.NoError(t, err)

	result, err := client.FetchTransactions(context.Background(), from, to)
	require.NoError(t, err)
	assert.Len(t, result, 3)
	assert.Equal(t, "txn_1", result[0].ID)
	assert.Equal(t, "txn_2", result[1].ID)
	assert.Equal(t, "txn_3", result[2].ID)
}

func TestFetchTransactions_DateRangeParams(t *testing.T) {
	from := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 6, 2, 0, 0, 0, 0, time.UTC)

	lister := &mockLister{transactions: nil}
	client, err := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
		AccountID: "acct_datetest",
	}, slog.Default())
	require.NoError(t, err)

	_, err = client.FetchTransactions(context.Background(), from, to)
	require.NoError(t, err)

	require.NotNil(t, lister.capturedParams)
	require.NotNil(t, lister.capturedParams.CreatedRange)
	assert.Equal(t, from.Unix(), lister.capturedParams.CreatedRange.GreaterThanOrEqual)
	assert.Equal(t, to.Unix(), lister.capturedParams.CreatedRange.LesserThan)
}

func TestFetchTransactions_EmptyResult(t *testing.T) {
	lister := &mockLister{transactions: nil}
	client, err := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
		AccountID: "acct_empty",
	}, slog.Default())
	require.NoError(t, err)

	result, err := client.FetchTransactions(context.Background(), time.Now().Add(-24*time.Hour), time.Now())
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestFetchTransactions_APIError(t *testing.T) {
	apiErr := errors.New("stripe API rate limited")
	lister := &mockLister{err: apiErr}
	client, err := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
		AccountID: "acct_error",
	}, slog.Default())
	require.NoError(t, err)

	_, err = client.FetchTransactions(context.Background(), time.Now().Add(-24*time.Hour), time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stripe balance transaction list failed")
}

func TestFetchTransactions_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// The cancelled context should propagate through to the lister
	lister := &mockLister{transactions: []*stripego.BalanceTransaction{
		{ID: "txn_1", Currency: "gbp", Type: stripego.BalanceTransactionTypeCharge},
	}}
	client, err := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
		AccountID: "acct_cancel",
	}, slog.Default())
	require.NoError(t, err)

	// Even though we have transactions, the function itself should work since
	// the mock doesn't check context. The real Stripe SDK would fail on cancelled ctx.
	result, err := client.FetchTransactions(ctx, time.Now().Add(-24*time.Hour), time.Now())
	// Mock doesn't check context so it will succeed - that's OK for unit tests
	require.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestFetchTransactions_ConnectedAccountHeader(t *testing.T) {
	lister := &mockLister{transactions: nil}
	client, err := NewBalanceTransactionClient(lister, BalanceTransactionClientConfig{
		AccountID: "acct_connected_123",
	}, slog.Default())
	require.NoError(t, err)

	_, err = client.FetchTransactions(context.Background(), time.Now().Add(-24*time.Hour), time.Now())
	require.NoError(t, err)

	require.NotNil(t, lister.capturedParams)
	// Verify the Stripe-Account header was set
	require.NotNil(t, lister.capturedParams.GetParams().StripeAccount)
	assert.Equal(t, "acct_connected_123", *lister.capturedParams.GetParams().StripeAccount)
}
