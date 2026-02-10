// Package stripe provides adapters for ingesting Stripe settlement data
// into the reconciliation service.
package stripe

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	stripego "github.com/stripe/stripe-go/v82"
)

// BalanceTransactionLister abstracts the Stripe Balance Transaction list API.
// This allows unit testing without a real Stripe client.
type BalanceTransactionLister interface {
	List(ctx context.Context, params *stripego.BalanceTransactionListParams) stripego.Seq2[*stripego.BalanceTransaction, error]
}

// BalanceTransactionClient fetches balance transactions from Stripe
// for a Connected Account with cursor-based pagination and date range filtering.
type BalanceTransactionClient struct {
	lister    BalanceTransactionLister
	accountID string
	pageSize  int64
	logger    *slog.Logger
}

// BalanceTransactionClientConfig holds configuration for the client.
type BalanceTransactionClientConfig struct {
	// AccountID is the Stripe Connected Account ID (acct_...).
	AccountID string
	// PageSize is the number of transactions per page. Defaults to 100.
	PageSize int64
}

// NewBalanceTransactionClient creates a new client that fetches balance transactions
// from the given Connected Account.
func NewBalanceTransactionClient(
	lister BalanceTransactionLister,
	cfg BalanceTransactionClientConfig,
	logger *slog.Logger,
) (*BalanceTransactionClient, error) {
	if lister == nil {
		return nil, ErrNilLister
	}
	if cfg.AccountID == "" {
		return nil, ErrEmptyAccountID
	}
	if logger == nil {
		logger = slog.Default()
	}
	pageSize := cfg.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return &BalanceTransactionClient{
		lister:    lister,
		accountID: cfg.AccountID,
		pageSize:  pageSize,
		logger:    logger,
	}, nil
}

// FetchTransactions retrieves all balance transactions for the Connected Account
// within the given date range [from, to). It handles Stripe's automatic pagination.
func (c *BalanceTransactionClient) FetchTransactions(
	ctx context.Context,
	from, to time.Time,
) ([]*stripego.BalanceTransaction, error) {
	params := &stripego.BalanceTransactionListParams{
		CreatedRange: &stripego.RangeQueryParams{
			GreaterThanOrEqual: from.Unix(),
			LesserThan:         to.Unix(),
		},
	}
	params.Limit = stripego.Int64(c.pageSize)
	params.SetStripeAccount(c.accountID)

	c.logger.Info("fetching stripe balance transactions",
		"account_id", c.accountID,
		"from", from.Format(time.RFC3339),
		"to", to.Format(time.RFC3339),
		"page_size", c.pageSize,
	)

	transactions := make([]*stripego.BalanceTransaction, 0, c.pageSize)
	var count int

	for bt, err := range c.lister.List(ctx, params) {
		if err != nil {
			return nil, fmt.Errorf("stripe balance transaction list failed after %d items: %w", count, err)
		}
		transactions = append(transactions, bt)
		count++

		if count >= maxTransactions {
			c.logger.Warn("reached maximum transaction limit",
				"account_id", c.accountID,
				"limit", maxTransactions,
			)
			break
		}
	}

	c.logger.Info("fetched stripe balance transactions",
		"account_id", c.accountID,
		"count", count,
	)

	return transactions, nil
}

const (
	// defaultPageSize is the number of balance transactions per Stripe API page.
	defaultPageSize int64 = 100

	// maxPageSize is the maximum allowed by the Stripe API.
	maxPageSize int64 = 100

	// maxTransactions is a safety cap to prevent unbounded fetches.
	maxTransactions = 100_000
)
