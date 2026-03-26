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
	"github.com/meridianhub/meridian/shared/pkg/email"
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

// PartyContact holds the contact details for a party needed for email delivery.
type PartyContact struct {
	Email string
	Name  string
}

// PartyClient defines the interface for retrieving party contact information.
type PartyClient interface {
	// GetPartyContact retrieves the contact email and display name for a party.
	GetPartyContact(ctx context.Context, partyID string) (PartyContact, error)
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
	posClient   PositionKeepingClient
	partyClient PartyClient
	emailRepo   email.OutboxRepository
	repo        persistence.BillingRepository
	metrics     *BillingMetrics
	shadowMode  bool
	logger      *slog.Logger
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

// WithEmailDelivery configures the invoice generator to queue invoice emails
// after successful invoice creation. When partyClient or emailRepo is nil,
// email delivery is disabled.
func (g *InvoiceGenerator) WithEmailDelivery(
	partyClient PartyClient,
	emailRepo email.OutboxRepository,
	shadowMode bool,
) *InvoiceGenerator {
	g.partyClient = partyClient
	g.emailRepo = emailRepo
	g.shadowMode = shadowMode
	return g
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

		g.queueInvoiceEmail(ctx, inv)

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

// invoiceEmailDueIn is the payment due period shown on invoice emails.
const invoiceEmailDueIn = 30 * 24 * time.Hour

// queueInvoiceEmail enqueues an invoice notification email to the outbox.
// Errors are non-fatal: the invoice was already created successfully, so
// a failure here only means the email won't be sent automatically.
func (g *InvoiceGenerator) queueInvoiceEmail(ctx context.Context, inv *domain.Invoice) {
	if g.shadowMode {
		g.logger.Debug("shadow mode: skipping invoice email", "invoice_id", inv.ID)
		return
	}
	if g.partyClient == nil || g.emailRepo == nil {
		return
	}

	contact, err := g.partyClient.GetPartyContact(ctx, inv.PartyID)
	if err != nil {
		g.logger.Warn("could not resolve party contact, skipping invoice email",
			"party_id", inv.PartyID,
			"invoice_id", inv.ID,
			"error", err)
		return
	}
	if contact.Email == "" {
		g.logger.Warn("party has no email address, skipping invoice email",
			"party_id", inv.PartyID,
			"invoice_id", inv.ID)
		return
	}

	lineItems := make([]email.LineItem, len(inv.LineItems))
	for i, li := range inv.LineItems {
		lineItems[i] = email.LineItem{
			Description: li.Description,
			Amount:      formatCents(li.TotalCents, inv.Currency),
		}
	}

	// Derive due date from invoice creation time so it is stable across retries.
	dueDate := inv.CreatedAt.UTC().Add(invoiceEmailDueIn)

	entry := &email.OutboxEntry{
		IdempotencyKey: "invoice-" + inv.ID.String(),
		ToAddresses:    []string{contact.Email},
		Subject:        "Invoice " + inv.InvoiceNumber,
		TemplateName:   "invoice",
		TemplateData: map[string]any{
			"CustomerName":  contact.Name,
			"InvoiceNumber": inv.InvoiceNumber,
			"LineItems":     lineItems,
			"Total":         formatCents(inv.SubtotalCents, inv.Currency),
			"DueDate":       dueDate.Format("2006-01-02"),
			"PaymentLink":   "",
		},
	}

	if enqueueErr := g.emailRepo.Enqueue(ctx, entry); enqueueErr != nil {
		g.logger.Error("failed to queue invoice email, invoice already created",
			"invoice_id", inv.ID,
			"party_id", inv.PartyID,
			"error", enqueueErr)
	}
}

// formatCents formats an integer cent amount as a human-readable string with currency code.
// Negative amounts are represented with a leading minus sign.
func formatCents(cents int64, currency string) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	major := cents / 100
	minor := cents % 100
	return fmt.Sprintf("%s%s %d.%02d", sign, currency, major, minor)
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
