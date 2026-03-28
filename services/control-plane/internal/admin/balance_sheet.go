package admin

import (
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/client"
)

// BalanceSheetClassification categorizes accounts on the balance sheet.
type BalanceSheetClassification string

// Balance sheet classification constants.
const (
	ClassificationAssets      BalanceSheetClassification = "ASSETS"
	ClassificationLiabilities BalanceSheetClassification = "LIABILITIES"
	ClassificationEquity      BalanceSheetClassification = "EQUITY"
)

// NormalBalance indicates the normal balance direction for an account type.
type NormalBalance string

// Normal balance direction constants.
const (
	NormalBalanceDebit  NormalBalance = "DEBIT"
	NormalBalanceCredit NormalBalance = "CREDIT"
)

// instrumentUnknown is returned when the instrument code cannot be determined.
const instrumentUnknown = "UNKNOWN"

// equityAccountTypes contains account types classified as equity.
var equityAccountTypes = map[string]bool{
	"RETAINED_EARNINGS": true,
	"OWNER_EQUITY":      true,
	"CAPITAL":           true,
}

// LineItem represents a single line on the balance sheet.
type LineItem struct {
	AccountType   string
	Instrument    string
	Quantity      decimal.Decimal
	NormalBalance NormalBalance
	AccountCount  int32
}

// BalanceSheetSection represents a classified section of the balance sheet.
type BalanceSheetSection struct {
	Classification BalanceSheetClassification
	LineItems      []LineItem
	Totals         map[string]decimal.Decimal
}

// PositionDetail represents an individual position for drill-down.
type PositionDetail struct {
	AccountID   string
	Quantity    decimal.Decimal
	LogID       string
	LastUpdated time.Time
}

// BalanceSheet represents the complete balance sheet for a tenant.
type BalanceSheet struct {
	TenantID string
	AsOf     time.Time
	Sections []BalanceSheetSection
}

// PositionDetailsResult contains the drill-down result for a specific account type and instrument.
type PositionDetailsResult struct {
	TenantID    string
	AccountType string
	Instrument  string
	Positions   []PositionDetail
	Total       decimal.Decimal
}

// PositionKeepingClient abstracts the position-keeping gRPC client for testability.
type PositionKeepingClient interface {
	ListFinancialPositionLogs(ctx context.Context, req *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error)
	GetAccountBalance(ctx context.Context, req *positionkeepingv1.GetAccountBalanceRequest) (*positionkeepingv1.GetAccountBalanceResponse, error)
}

// Verify client.Client satisfies the interface at compile time.
var _ PositionKeepingClient = (*client.Client)(nil)

// BalanceSheetService aggregates positions across multiple instruments into a
// classified balance sheet with ASSETS, LIABILITIES, and EQUITY sections.
type BalanceSheetService struct {
	pkClient PositionKeepingClient
	logger   *slog.Logger
}

// NewBalanceSheetService creates a new BalanceSheetService.
func NewBalanceSheetService(pkClient PositionKeepingClient, logger *slog.Logger) *BalanceSheetService {
	if logger == nil {
		logger = slog.Default()
	}
	return &BalanceSheetService{
		pkClient: pkClient,
		logger:   logger,
	}
}

// GetBalanceSheet retrieves and aggregates positions into a classified balance sheet.
func (s *BalanceSheetService) GetBalanceSheet(ctx context.Context, tenantID string, asOf time.Time) (*BalanceSheet, error) {
	s.logger.Debug("generating balance sheet",
		"tenant_id", tenantID,
		"as_of", asOf,
	)

	// A nil proto Timestamp converts to Unix epoch (1970-01-01), not Go's zero time.
	if asOf.IsZero() || asOf.Unix() == 0 {
		asOf = time.Now().UTC()
	}

	// Fetch all position logs for the tenant via position-keeping service.
	// The tenant_id is used as an account_id prefix filter.
	logs, err := s.fetchPositionLogs(ctx, tenantID, asOf)
	if err != nil {
		return nil, fmt.Errorf("fetch position logs: %w", err)
	}

	// Aggregate by account type and instrument, classify into sections
	sections := s.aggregateAndClassify(logs)

	return &BalanceSheet{
		TenantID: tenantID,
		AsOf:     asOf,
		Sections: sections,
	}, nil
}

// GetPositionDetails returns drill-down details for a specific account type and instrument.
// When asOf is non-zero, only positions as of that time are included, consistent with GetBalanceSheet.
func (s *BalanceSheetService) GetPositionDetails(ctx context.Context, tenantID, accountType, instrument string, asOf time.Time) (*PositionDetailsResult, error) {
	s.logger.Debug("fetching position details",
		"tenant_id", tenantID,
		"account_type", accountType,
		"instrument", instrument,
		"as_of", asOf,
	)

	// A nil proto Timestamp converts to Unix epoch (1970-01-01), not Go's zero time.
	if asOf.IsZero() || asOf.Unix() == 0 {
		asOf = time.Now().UTC()
	}

	logs, err := s.fetchPositionLogs(ctx, tenantID, asOf)
	if err != nil {
		return nil, fmt.Errorf("fetch position logs: %w", err)
	}

	positions := make([]PositionDetail, 0, len(logs))
	total := decimal.Zero

	for _, log := range logs {
		at := extractAccountType(log.GetAccountId())
		if at != accountType {
			continue
		}

		logInstrument := extractInstrument(log)
		if logInstrument != instrument {
			continue
		}

		qty := computeLogBalance(log)
		total = total.Add(qty)

		positions = append(positions, PositionDetail{
			AccountID:   log.GetAccountId(),
			Quantity:    qty,
			LogID:       log.GetLogId(),
			LastUpdated: log.GetUpdatedAt().AsTime(),
		})
	}

	return &PositionDetailsResult{
		TenantID:    tenantID,
		AccountType: accountType,
		Instrument:  instrument,
		Positions:   positions,
		Total:       total,
	}, nil
}

// ExportBalanceSheetCSV exports the balance sheet as CSV.
func (s *BalanceSheetService) ExportBalanceSheetCSV(ctx context.Context, tenantID string, asOf time.Time) (string, error) {
	bs, err := s.GetBalanceSheet(ctx, tenantID, asOf)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	w := csv.NewWriter(&buf)

	if err := writeCSVMetadataHeader(w, bs); err != nil {
		return "", err
	}

	if err := writeCSVSectionRows(w, bs); err != nil {
		return "", err
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("flush CSV: %w", err)
	}

	return buf.String(), nil
}

// writeCSVMetadataHeader writes the metadata header and column headers to the CSV writer.
func writeCSVMetadataHeader(w *csv.Writer, bs *BalanceSheet) error {
	if err := w.Write([]string{"# Balance Sheet Export"}); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}
	if err := w.Write([]string{"# Tenant", sanitizeCSVCell(bs.TenantID)}); err != nil {
		return fmt.Errorf("write tenant: %w", err)
	}
	if err := w.Write([]string{"# Generated At", bs.AsOf.Format(time.RFC3339)}); err != nil {
		return fmt.Errorf("write timestamp: %w", err)
	}
	if err := w.Write([]string{""}); err != nil {
		return fmt.Errorf("write separator: %w", err)
	}
	if err := w.Write([]string{
		"classification", "account_type", "instrument", "quantity", "normal_balance", "account_count",
	}); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	return nil
}

// writeCSVSectionRows writes line items and section totals for all sections.
func writeCSVSectionRows(w *csv.Writer, bs *BalanceSheet) error {
	for _, section := range bs.Sections {
		for _, item := range section.LineItems {
			if err := w.Write([]string{
				sanitizeCSVCell(string(section.Classification)),
				sanitizeCSVCell(item.AccountType),
				sanitizeCSVCell(item.Instrument),
				item.Quantity.String(),
				sanitizeCSVCell(string(item.NormalBalance)),
				fmt.Sprintf("%d", item.AccountCount),
			}); err != nil {
				return fmt.Errorf("write line item: %w", err)
			}
		}

		instruments := make([]string, 0, len(section.Totals))
		for instrument := range section.Totals {
			instruments = append(instruments, instrument)
		}
		sort.Strings(instruments)

		for _, instrument := range instruments {
			if err := w.Write([]string{
				sanitizeCSVCell(string(section.Classification)),
				"TOTAL",
				sanitizeCSVCell(instrument),
				section.Totals[instrument].String(),
				"",
				"",
			}); err != nil {
				return fmt.Errorf("write total: %w", err)
			}
		}
	}
	return nil
}

// fetchPositionLogs retrieves all position logs for a tenant from position-keeping,
// handling cursor-based pagination to collect all pages. When asOf is non-zero,
// a date_range filter is applied so only logs up to that date are returned.
func (s *BalanceSheetService) fetchPositionLogs(ctx context.Context, tenantID string, asOf time.Time) ([]*positionkeepingv1.FinancialPositionLog, error) {
	var allLogs []*positionkeepingv1.FinancialPositionLog
	var pageToken string

	for {
		req := &positionkeepingv1.ListFinancialPositionLogsRequest{
			AccountId: tenantID,
		}
		if !asOf.IsZero() {
			req.DateRange = &commonv1.DateRange{
				EndDate: asOf.Format("2006-01-02"),
			}
		}
		if pageToken != "" {
			req.Pagination = &commonv1.Pagination{
				PageToken: pageToken,
			}
		}

		resp, err := s.pkClient.ListFinancialPositionLogs(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("list position logs: %w", err)
		}

		allLogs = append(allLogs, resp.GetLogs()...)

		pageToken = resp.GetPagination().GetNextPageToken()
		if pageToken == "" {
			break
		}
	}

	// The API's date_range filter uses date-only granularity, so apply client-side
	// timestamp filtering for point-in-time accuracy.
	if !asOf.IsZero() {
		filtered := allLogs[:0]
		for _, log := range allLogs {
			if !log.GetUpdatedAt().AsTime().After(asOf) {
				filtered = append(filtered, log)
			}
		}
		allLogs = filtered
	}

	return allLogs, nil
}

// aggregateAndClassify groups position logs by account type and instrument,
// then classifies them into balance sheet sections.
// aggregateKey identifies a unique account type + instrument combination.
type aggregateKey struct {
	accountType string
	instrument  string
}

// aggregateValue holds the running totals for an aggregate key.
type aggregateValue struct {
	quantity   decimal.Decimal
	accountIDs map[string]bool
}

func (s *BalanceSheetService) aggregateAndClassify(logs []*positionkeepingv1.FinancialPositionLog) []BalanceSheetSection {
	aggregates := s.aggregatePositionLogs(logs)
	return buildBalanceSheetSections(aggregates)
}

// aggregatePositionLogs groups position logs by account type and instrument.
func (s *BalanceSheetService) aggregatePositionLogs(logs []*positionkeepingv1.FinancialPositionLog) map[aggregateKey]*aggregateValue {
	aggregates := make(map[aggregateKey]*aggregateValue)

	for _, log := range logs {
		accountType := extractAccountType(log.GetAccountId())
		instrument := extractInstrument(log)
		qty := computeLogBalance(log)

		key := aggregateKey{accountType: accountType, instrument: instrument}
		if agg, ok := aggregates[key]; ok {
			agg.quantity = agg.quantity.Add(qty)
			agg.accountIDs[log.GetAccountId()] = true
		} else {
			aggregates[key] = &aggregateValue{
				quantity:   qty,
				accountIDs: map[string]bool{log.GetAccountId(): true},
			}
		}
	}

	return aggregates
}

// buildBalanceSheetSections classifies aggregated positions into balance sheet sections.
func buildBalanceSheetSections(aggregates map[aggregateKey]*aggregateValue) []BalanceSheetSection {
	sectionMap := map[BalanceSheetClassification]*BalanceSheetSection{
		ClassificationAssets: {
			Classification: ClassificationAssets,
			Totals:         make(map[string]decimal.Decimal),
		},
		ClassificationLiabilities: {
			Classification: ClassificationLiabilities,
			Totals:         make(map[string]decimal.Decimal),
		},
		ClassificationEquity: {
			Classification: ClassificationEquity,
			Totals:         make(map[string]decimal.Decimal),
		},
	}

	for key, agg := range aggregates {
		normalBal := classifyNormalBalance(key.accountType)
		classification := classifyAccount(key.accountType, normalBal)

		item := LineItem{
			AccountType:   key.accountType,
			Instrument:    key.instrument,
			Quantity:      agg.quantity,
			NormalBalance: normalBal,
			AccountCount:  int32(len(agg.accountIDs)),
		}

		section := sectionMap[classification]
		section.LineItems = append(section.LineItems, item)

		existing := section.Totals[key.instrument]
		section.Totals[key.instrument] = existing.Add(agg.quantity)
	}

	// Sort line items within each section for deterministic output
	for _, section := range sectionMap {
		sort.Slice(section.LineItems, func(i, j int) bool {
			if section.LineItems[i].AccountType != section.LineItems[j].AccountType {
				return section.LineItems[i].AccountType < section.LineItems[j].AccountType
			}
			return section.LineItems[i].Instrument < section.LineItems[j].Instrument
		})
	}

	return []BalanceSheetSection{
		*sectionMap[ClassificationAssets],
		*sectionMap[ClassificationLiabilities],
		*sectionMap[ClassificationEquity],
	}
}

// extractAccountType derives the account type from an account ID.
// Account IDs follow the pattern: "tenant_ACCOUNT_TYPE_identifier"
// e.g., "acme_STRIPE_NOSTRO_001" -> "STRIPE_NOSTRO"
// The account type is the uppercase alphabetic segment(s) between the tenant
// prefix and the trailing numeric identifier.
func extractAccountType(accountID string) string {
	parts := strings.Split(accountID, "_")
	if len(parts) < 2 {
		return accountID
	}

	// Collect uppercase alphabetic segments between the tenant prefix (index 0)
	// and any trailing numeric identifiers.
	var typeParts []string
	for i := 1; i < len(parts); i++ {
		p := parts[i]
		if len(p) == 0 {
			continue
		}
		// A part is a "type" part if it contains at least one letter and is all uppercase
		if isUpperAlpha(p) {
			typeParts = append(typeParts, p)
		} else if len(typeParts) > 0 {
			// Once we hit a non-alpha-upper part after type parts, stop
			break
		}
	}

	if len(typeParts) > 0 {
		return strings.Join(typeParts, "_")
	}

	// Fallback: return everything after the first segment
	return strings.Join(parts[1:], "_")
}

// isUpperAlpha returns true if the string contains only uppercase letters.
func isUpperAlpha(s string) bool {
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return len(s) > 0
}

// extractInstrument determines the instrument code from a position log.
// It inspects the first transaction entry's amount for the instrument code.
func extractInstrument(log *positionkeepingv1.FinancialPositionLog) string {
	entries := log.GetTransactionLogEntries()
	if len(entries) == 0 {
		return instrumentUnknown
	}

	amount := entries[0].GetAmount()
	if amount == nil || amount.GetAmount() == nil {
		return instrumentUnknown
	}

	code := amount.GetAmount().GetCurrencyCode()
	if code == "" {
		return instrumentUnknown
	}
	return code
}

// computeLogBalance computes the net balance of a position log by summing
// all transaction entries, accounting for debit/credit direction.
func computeLogBalance(log *positionkeepingv1.FinancialPositionLog) decimal.Decimal {
	balance := decimal.Zero

	for _, entry := range log.GetTransactionLogEntries() {
		amount := entryAmount(entry)
		switch entry.GetDirection() {
		case commonv1.PostingDirection_POSTING_DIRECTION_DEBIT:
			balance = balance.Add(amount)
		case commonv1.PostingDirection_POSTING_DIRECTION_CREDIT:
			balance = balance.Sub(amount)
		case commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED:
			// Ignore entries with unspecified direction.
		}
	}

	return balance
}

// entryAmount extracts the decimal amount from a transaction log entry.
func entryAmount(entry *positionkeepingv1.TransactionLogEntry) decimal.Decimal {
	if entry.GetAmount() == nil || entry.GetAmount().GetAmount() == nil {
		return decimal.Zero
	}

	googleMoney := entry.GetAmount().GetAmount()
	units := decimal.NewFromInt(googleMoney.GetUnits())
	nanos := decimal.NewFromInt(int64(googleMoney.GetNanos())).Div(decimal.NewFromInt(1_000_000_000))
	return units.Add(nanos)
}

// classifyNormalBalance determines the normal balance direction for an account type.
// Asset and expense accounts normally carry DEBIT balances.
// Liability, equity, and revenue accounts normally carry CREDIT balances.
func classifyNormalBalance(accountType string) NormalBalance {
	upper := strings.ToUpper(accountType)

	// Credit-normal account types
	creditTypes := []string{
		"PAYABLE", "LIABILITY", "REVENUE", "INCOME",
		"CUSTOMER_DEPOSIT", "DEFERRED", "PROVISION",
		"RETAINED_EARNINGS", "OWNER_EQUITY", "CAPITAL",
	}
	for _, ct := range creditTypes {
		if strings.Contains(upper, ct) {
			return NormalBalanceCredit
		}
	}

	// Default: debit-normal (assets, expenses, etc.)
	return NormalBalanceDebit
}

// sanitizeCSVCell prevents CSV injection by escaping cells that begin with
// formula-triggering characters (=, +, -, @).
func sanitizeCSVCell(v string) string {
	if len(v) == 0 {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@':
		return "'" + v
	default:
		return v
	}
}

// classifyAccount determines the balance sheet classification for an account type.
func classifyAccount(accountType string, normalBalance NormalBalance) BalanceSheetClassification {
	upper := strings.ToUpper(accountType)

	// Check equity first
	if equityAccountTypes[upper] {
		return ClassificationEquity
	}
	for keyword := range equityAccountTypes {
		if strings.Contains(upper, keyword) {
			return ClassificationEquity
		}
	}

	// Classify by normal balance
	if normalBalance == NormalBalanceDebit {
		return ClassificationAssets
	}
	return ClassificationLiabilities
}
