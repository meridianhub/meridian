package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/shopspring/decimal"
)

// Invoice generation errors.
var (
	ErrPartialInvoiceGeneration = errors.New("invoice generation partially failed")
	ErrMixedCurrencies          = errors.New("mixed currencies for party accounts")
)

// PositionKeepingClient defines the interface for querying account balances
// and position logs from the position-keeping service.
type PositionKeepingClient interface {
	// GetAccountBalance retrieves the current balance for an account.
	// Returns (balanceCents, currency, error).
	GetAccountBalance(ctx context.Context, accountID string) (int64, string, error)

	// ListAccountsForTenant returns all account IDs belonging to a tenant.
	ListAccountsForTenant(ctx context.Context, tenantID string) ([]AccountInfo, error)

	// GetPositionLogEntries retrieves debit entries for an account within a period.
	GetPositionLogEntries(ctx context.Context, accountID string, periodStart, periodEnd time.Time) ([]PositionEntry, error)
}

// AccountInfo holds basic account information for billing queries.
type AccountInfo struct {
	AccountID string
	PartyID   string
	Currency  string
}

// PositionEntry represents a single position log entry used to build invoice line items.
type PositionEntry struct {
	Description       string
	AmountCents       int64
	Quantity          decimal.Decimal
	UnitPriceCents    int64
	ValuationAnalysis map[string]any
}

// InvoiceGenerator creates invoices from position-keeping data during a billing run.
type InvoiceGenerator struct {
	posClient PositionKeepingClient
	repo      persistence.BillingRepository
	metrics   *BillingMetrics
	logger    *slog.Logger
}

// NewInvoiceGenerator creates a new invoice generator.
func NewInvoiceGenerator(
	posClient PositionKeepingClient,
	repo persistence.BillingRepository,
	metrics *BillingMetrics,
	logger *slog.Logger,
) *InvoiceGenerator {
	return &InvoiceGenerator{
		posClient: posClient,
		repo:      repo,
		metrics:   metrics,
		logger:    logger.With("component", "invoice_generator"),
	}
}

// GenerateInvoices queries position-keeping for accounts with negative balances (amounts owed),
// groups by party, and creates invoices for the billing run. Returns partial results with an
// error if any party fails, allowing the caller to decide whether to mark the run as failed.
func (g *InvoiceGenerator) GenerateInvoices(ctx context.Context, billingRun *domain.BillingRun) ([]*domain.Invoice, error) {
	g.logger.Info("generating invoices",
		"billing_run_id", billingRun.ID,
		"tenant_id", billingRun.TenantID,
		"period_start", billingRun.CycleStart,
		"period_end", billingRun.CycleEnd)

	// List all accounts for the tenant
	accounts, err := g.posClient.ListAccountsForTenant(ctx, billingRun.TenantID)
	if err != nil {
		return nil, fmt.Errorf("list accounts for tenant: %w", err)
	}

	if len(accounts) == 0 {
		g.logger.Info("no accounts found for tenant", "tenant_id", billingRun.TenantID)
		return nil, nil
	}

	// Group accounts by party for invoice aggregation
	partyAccounts := make(map[string][]AccountInfo)
	for _, acct := range accounts {
		partyAccounts[acct.PartyID] = append(partyAccounts[acct.PartyID], acct)
	}

	// Get existing invoice count for sequence numbering
	existingInvoices, err := g.repo.FindInvoicesByBillingRunID(ctx, billingRun.ID)
	if err != nil {
		return nil, fmt.Errorf("find existing invoices: %w", err)
	}
	sequenceNum := len(existingInvoices)

	invoices := make([]*domain.Invoice, 0, len(partyAccounts))
	var errCount int

	for partyID, accts := range partyAccounts {
		lineItems, currency, lineErr := g.buildLineItemsForParty(ctx, accts, billingRun.CycleStart, billingRun.CycleEnd)
		if lineErr != nil {
			g.logger.Error("failed to build line items for party",
				"party_id", partyID,
				"error", lineErr)
			g.metrics.RecordError("build_line_items")
			errCount++
			continue
		}

		if len(lineItems) == 0 {
			continue
		}

		sequenceNum++
		invoiceNumber := formatInvoiceNumber(billingRun.TenantID, billingRun.CycleEnd, sequenceNum)

		inv, invErr := domain.NewInvoice(
			billingRun.ID,
			partyID,
			accts[0].AccountID, // primary account
			invoiceNumber,
			billingRun.CycleStart,
			billingRun.CycleEnd,
			lineItems,
			currency,
		)
		if invErr != nil {
			g.logger.Error("failed to create invoice",
				"party_id", partyID,
				"error", invErr)
			g.metrics.RecordError("create_invoice")
			errCount++
			continue
		}

		if persistErr := g.repo.CreateInvoice(ctx, inv); persistErr != nil {
			g.logger.Error("failed to persist invoice",
				"party_id", partyID,
				"invoice_number", invoiceNumber,
				"error", persistErr)
			g.metrics.RecordError("persist_invoice")
			errCount++
			continue
		}

		g.metrics.RecordInvoiceCreated()
		g.metrics.RecordAmountCollected(inv.SubtotalCents)
		invoices = append(invoices, inv)

		g.logger.Info("invoice created",
			"invoice_id", inv.ID,
			"party_id", partyID,
			"invoice_number", invoiceNumber,
			"subtotal_cents", inv.SubtotalCents,
			"line_items", len(lineItems))
	}

	g.logger.Info("invoice generation complete",
		"billing_run_id", billingRun.ID,
		"invoices_created", len(invoices),
		"errors", errCount)

	if errCount > 0 {
		return invoices, fmt.Errorf("%w: %d parties", ErrPartialInvoiceGeneration, errCount)
	}
	return invoices, nil
}

// buildLineItemsForParty queries position entries for all accounts belonging to a party
// and constructs invoice line items from debit entries.
func (g *InvoiceGenerator) buildLineItemsForParty(
	ctx context.Context,
	accounts []AccountInfo,
	periodStart, periodEnd time.Time,
) ([]domain.InvoiceLineItem, string, error) {
	var lineItems []domain.InvoiceLineItem
	var currency string

	for _, acct := range accounts {
		entries, err := g.posClient.GetPositionLogEntries(ctx, acct.AccountID, periodStart, periodEnd)
		if err != nil {
			return nil, "", fmt.Errorf("get position entries for account %s: %w", acct.AccountID, err)
		}

		if currency == "" {
			currency = acct.Currency
		} else if acct.Currency != currency {
			return nil, "", fmt.Errorf("%w: %s vs %s", ErrMixedCurrencies, currency, acct.Currency)
		}

		for _, entry := range entries {
			if entry.AmountCents <= 0 {
				continue // only invoice positive amounts (debits owed)
			}

			lineItems = append(lineItems, domain.InvoiceLineItem{
				Description:       entry.Description,
				Quantity:          entry.Quantity,
				UnitPriceCents:    entry.UnitPriceCents,
				TotalCents:        entry.AmountCents,
				ValuationAnalysis: entry.ValuationAnalysis,
			})
		}
	}

	return lineItems, currency, nil
}

// formatInvoiceNumber generates an invoice number in the format INV-{tenant}-YYYY-MM-####.
// Includes tenant context to prevent cross-tenant collisions on the global unique index.
func formatInvoiceNumber(tenantID string, periodEnd time.Time, sequence int) string {
	// Use first 8 chars of tenant ID as prefix for brevity
	tenantPrefix := tenantID
	if len(tenantPrefix) > 8 {
		tenantPrefix = tenantPrefix[:8]
	}
	return fmt.Sprintf("INV-%s-%s-%s",
		tenantPrefix,
		periodEnd.Format("2006-01"),
		padSequence(sequence),
	)
}

// padSequence pads a sequence number to 4 digits.
func padSequence(seq int) string {
	s := strconv.Itoa(seq)
	if len(s) >= 4 {
		return s
	}
	return strings.Repeat("0", 4-len(s)) + s
}
