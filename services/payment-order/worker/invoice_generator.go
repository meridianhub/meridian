package worker

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"github.com/shopspring/decimal"
)

// PositionKeepingClient defines the interface for querying account balances
// and position logs from the position-keeping service.
type PositionKeepingClient interface {
	// GetAccountBalance retrieves the current balance for an account.
	// Returns (accountID, balanceCents, currency, error).
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
// groups by party, and creates invoices for the billing run.
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

	for partyID, accts := range partyAccounts {
		lineItems, currency, err := g.buildLineItemsForParty(ctx, accts, billingRun.CycleStart, billingRun.CycleEnd)
		if err != nil {
			g.logger.Error("failed to build line items for party",
				"party_id", partyID,
				"error", err)
			g.metrics.RecordError("build_line_items")
			continue
		}

		if len(lineItems) == 0 {
			continue
		}

		sequenceNum++
		invoiceNumber := formatInvoiceNumber(billingRun.CycleEnd, sequenceNum)

		inv, err := domain.NewInvoice(
			billingRun.ID,
			partyID,
			accts[0].AccountID, // primary account
			invoiceNumber,
			billingRun.CycleStart,
			billingRun.CycleEnd,
			lineItems,
			currency,
		)
		if err != nil {
			g.logger.Error("failed to create invoice",
				"party_id", partyID,
				"error", err)
			g.metrics.RecordError("create_invoice")
			continue
		}

		if err := g.repo.CreateInvoice(ctx, inv); err != nil {
			g.logger.Error("failed to persist invoice",
				"party_id", partyID,
				"invoice_number", invoiceNumber,
				"error", err)
			g.metrics.RecordError("persist_invoice")
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
		"invoices_created", len(invoices))

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

// formatInvoiceNumber generates an invoice number in the format INV-YYYY-MM-####.
func formatInvoiceNumber(periodEnd time.Time, sequence int) string {
	return fmt.Sprintf("INV-%s-%s",
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
