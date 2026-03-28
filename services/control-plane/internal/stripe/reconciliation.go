package stripe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"
)

// Reconciliation errors.
var (
	ErrReconciliationVariance = errors.New("reconciliation variance detected")
	ErrNilStripeSource        = errors.New("stripe source cannot be nil")
	ErrNilLedgerSource        = errors.New("ledger source cannot be nil")
)

// varianceThresholdGBP is the absolute variance threshold in GBP.
var varianceThresholdGBP = decimal.NewFromInt(10) //nolint:gochecknoglobals // configurable threshold

// varianceThresholdPercent is the relative variance threshold as a percentage of daily volume.
var varianceThresholdPercent = decimal.NewFromFloat(0.01) // 1% //nolint:gochecknoglobals // configurable threshold

// ChargeSource provides Stripe charge data for reconciliation.
type ChargeSource interface {
	// ListCharges returns all successful charges for the given date range.
	ListCharges(ctx context.Context, from, to time.Time) ([]ChargeRecord, error)
}

// LedgerEntrySource provides ledger entry data for reconciliation.
type LedgerEntrySource interface {
	// ListEntriesByExternalRef returns ledger entries matching the external reference IDs.
	ListEntriesByExternalRef(ctx context.Context, externalRefIDs []string) ([]LedgerRecord, error)
}

// ChargeRecord represents a Stripe charge for reconciliation.
type ChargeRecord struct {
	ChargeID    string
	AmountCents int64
	Currency    string
	Created     time.Time
}

// LedgerRecord represents a ledger entry matched by external reference.
type LedgerRecord struct {
	LogID               string
	ExternalReferenceID string
	AmountCents         int64
	Currency            string
}

// ReconciliationReport is the output of a daily reconciliation run.
type ReconciliationReport struct {
	// Date is the reconciliation date.
	Date time.Time `json:"date"`
	// StripeChargeCount is the number of Stripe charges found.
	StripeChargeCount int `json:"stripe_charge_count"`
	// LedgerEntryCount is the number of matching ledger entries found.
	LedgerEntryCount int `json:"ledger_entry_count"`
	// StripeTotalCents is the total amount from Stripe charges.
	StripeTotalCents int64 `json:"stripe_total_cents"`
	// LedgerTotalCents is the total amount from ledger entries.
	LedgerTotalCents int64 `json:"ledger_total_cents"`
	// VarianceCents is the absolute difference between Stripe and ledger totals.
	VarianceCents int64 `json:"variance_cents"`
	// MissingInLedger contains charge IDs present in Stripe but not in the ledger.
	MissingInLedger []string `json:"missing_in_ledger,omitempty"`
	// MissingInStripe contains external reference IDs in ledger but not in Stripe.
	MissingInStripe []string `json:"missing_in_stripe,omitempty"`
	// VarianceExceedsThreshold indicates whether the variance exceeds alerting thresholds.
	VarianceExceedsThreshold bool `json:"variance_exceeds_threshold"`
	// AlertMessage is set when variance exceeds thresholds.
	AlertMessage string `json:"alert_message,omitempty"`
}

// ReconciliationService runs daily reconciliation between Stripe and the ledger.
type ReconciliationService struct {
	stripeSource ChargeSource
	ledgerSource LedgerEntrySource
	logger       *slog.Logger
}

// NewReconciliationService creates a new reconciliation service.
func NewReconciliationService(stripeSource ChargeSource, ledgerSource LedgerEntrySource, logger *slog.Logger) (*ReconciliationService, error) {
	if stripeSource == nil {
		return nil, ErrNilStripeSource
	}
	if ledgerSource == nil {
		return nil, ErrNilLedgerSource
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ReconciliationService{
		stripeSource: stripeSource,
		ledgerSource: ledgerSource,
		logger:       logger,
	}, nil
}

// RunDailyReconciliation compares Stripe charges with ledger entries for the given date.
// It produces a report identifying variances and missing entries.
func (s *ReconciliationService) RunDailyReconciliation(ctx context.Context, date time.Time) (*ReconciliationReport, error) {
	from := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)

	s.logger.Info("starting daily reconciliation",
		"date", from.Format("2006-01-02"),
	)

	charges, err := s.stripeSource.ListCharges(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch stripe charges: %w", err)
	}

	chargeMap, chargeIDs, stripeTotalCents := buildChargeIndex(charges)

	ledgerMap, ledgerTotalCents, err := s.fetchAndIndexLedgerEntries(ctx, chargeIDs)
	if err != nil {
		return nil, err
	}

	missingInLedger, missingInStripe := findMissingEntries(chargeIDs, chargeMap, ledgerMap)

	varianceCents := abs(stripeTotalCents - ledgerTotalCents)
	exceedsThreshold, alertMessage := checkVarianceThresholds(varianceCents, stripeTotalCents)

	report := &ReconciliationReport{
		Date:                     from,
		StripeChargeCount:        len(charges),
		LedgerEntryCount:         len(ledgerMap),
		StripeTotalCents:         stripeTotalCents,
		LedgerTotalCents:         ledgerTotalCents,
		VarianceCents:            varianceCents,
		MissingInLedger:          missingInLedger,
		MissingInStripe:          missingInStripe,
		VarianceExceedsThreshold: exceedsThreshold,
		AlertMessage:             alertMessage,
	}

	s.logReconciliationResult(from, report)

	return report, nil
}

// buildChargeIndex builds a lookup map, ordered ID list, and total from charges.
func buildChargeIndex(charges []ChargeRecord) (map[string]*ChargeRecord, []string, int64) {
	chargeMap := make(map[string]*ChargeRecord, len(charges))
	chargeIDs := make([]string, 0, len(charges))
	var totalCents int64
	for i := range charges {
		chargeMap[charges[i].ChargeID] = &charges[i]
		chargeIDs = append(chargeIDs, charges[i].ChargeID)
		totalCents += charges[i].AmountCents
	}
	return chargeMap, chargeIDs, totalCents
}

// fetchAndIndexLedgerEntries fetches ledger entries matching chargeIDs and builds a lookup map.
func (s *ReconciliationService) fetchAndIndexLedgerEntries(ctx context.Context, chargeIDs []string) (map[string]*LedgerRecord, int64, error) {
	if len(chargeIDs) == 0 {
		return make(map[string]*LedgerRecord), 0, nil
	}
	ledgerEntries, err := s.ledgerSource.ListEntriesByExternalRef(ctx, chargeIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to fetch ledger entries: %w", err)
	}
	ledgerMap := make(map[string]*LedgerRecord, len(ledgerEntries))
	var totalCents int64
	for i := range ledgerEntries {
		ledgerMap[ledgerEntries[i].ExternalReferenceID] = &ledgerEntries[i]
		totalCents += ledgerEntries[i].AmountCents
	}
	return ledgerMap, totalCents, nil
}

// findMissingEntries identifies charges missing in ledger and ledger entries missing in Stripe.
func findMissingEntries(chargeIDs []string, chargeMap map[string]*ChargeRecord, ledgerMap map[string]*LedgerRecord) ([]string, []string) {
	var missingInLedger []string
	for _, chargeID := range chargeIDs {
		if _, found := ledgerMap[chargeID]; !found {
			missingInLedger = append(missingInLedger, chargeID)
		}
	}
	var missingInStripe []string
	for refID := range ledgerMap {
		if _, found := chargeMap[refID]; !found {
			missingInStripe = append(missingInStripe, refID)
		}
	}
	return missingInLedger, missingInStripe
}

// checkVarianceThresholds evaluates whether the variance exceeds absolute or
// relative thresholds and returns the result with an alert message.
func checkVarianceThresholds(varianceCents, stripeTotalCents int64) (bool, string) {
	varianceDecimal := decimal.NewFromInt(varianceCents).Div(decimal.NewFromInt(100))
	stripeTotal := decimal.NewFromInt(stripeTotalCents).Div(decimal.NewFromInt(100))

	exceedsThreshold := false
	var alertMessage string

	if varianceDecimal.GreaterThan(varianceThresholdGBP) {
		exceedsThreshold = true
		alertMessage = fmt.Sprintf("absolute variance %s GBP exceeds threshold of %s GBP",
			varianceDecimal.StringFixed(2), varianceThresholdGBP.StringFixed(2))
	}

	if stripeTotal.GreaterThan(decimal.Zero) {
		pct := varianceDecimal.Div(stripeTotal)
		if pct.GreaterThan(varianceThresholdPercent) {
			exceedsThreshold = true
			pctStr := pct.Mul(decimal.NewFromInt(100)).StringFixed(2)
			if alertMessage != "" {
				alertMessage += "; "
			}
			alertMessage += fmt.Sprintf("relative variance %s%% exceeds threshold of %s%%",
				pctStr, varianceThresholdPercent.Mul(decimal.NewFromInt(100)).StringFixed(2))
		}
	}

	return exceedsThreshold, alertMessage
}

// logReconciliationResult logs the reconciliation outcome at the appropriate level.
func (s *ReconciliationService) logReconciliationResult(date time.Time, report *ReconciliationReport) {
	if report.VarianceExceedsThreshold {
		s.logger.Warn("reconciliation variance exceeds threshold",
			"date", date.Format("2006-01-02"),
			"variance_cents", report.VarianceCents,
			"alert", report.AlertMessage,
		)
	} else {
		s.logger.Info("reconciliation completed",
			"date", date.Format("2006-01-02"),
			"stripe_charges", report.StripeChargeCount,
			"ledger_entries", report.LedgerEntryCount,
			"variance_cents", report.VarianceCents,
		)
	}
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
