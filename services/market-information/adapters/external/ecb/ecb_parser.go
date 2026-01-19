// Package ecb provides an HTTP client and parser for fetching daily FX rates from the European Central Bank.
package ecb

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/types/known/timestamppb"

	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
)

// ECB CSV column indices (based on SDMX format).
const (
	colKEY         = 0
	colFREQ        = 1
	colCurrency    = 2
	colCurrencyDen = 3
	colExrType     = 4
	colExrSuffix   = 5
	colTimePeriod  = 6
	colObsValue    = 7
	minColumns     = 8
)

// Expected header columns for ECB SDMX CSV format.
var expectedHeaders = []string{
	"KEY", "FREQ", "CURRENCY", "CURRENCY_DENOM",
	"EXR_TYPE", "EXR_SUFFIX", "TIME_PERIOD", "OBS_VALUE",
}

// Sentinel errors for parsing.
var (
	// ErrInvalidCSVFormat is returned when the CSV format is not recognized.
	ErrInvalidCSVFormat = errors.New("invalid ECB CSV format")
	// ErrNoData is returned when the CSV contains no data rows.
	ErrNoData = errors.New("no data in ECB CSV response")
	// ErrInvalidRate is returned when a rate value cannot be parsed.
	ErrInvalidRate = errors.New("invalid rate value")
	// ErrInvalidDate is returned when a date cannot be parsed.
	ErrInvalidDate = errors.New("invalid date format")
	// ErrTooFewColumns is returned when a row has fewer columns than required.
	ErrTooFewColumns = errors.New("row has too few columns")
)

// Rate represents a single exchange rate from the ECB feed.
type Rate struct {
	// BaseCurrency is the currency being quoted (e.g., USD, GBP).
	BaseCurrency string
	// QuoteCurrency is always EUR for ECB rates.
	QuoteCurrency string
	// Value is the exchange rate value with high precision.
	Value decimal.Decimal
	// ObservedDate is the date when the rate was observed.
	ObservedDate time.Time
	// Frequency is the data frequency (D=daily, M=monthly, etc.).
	Frequency string
	// ExchangeRateType is the rate type (SP00=spot).
	ExchangeRateType string
	// ExchangeRateSuffix is the series variation (A=average).
	ExchangeRateSuffix string
}

// Parser parses ECB SDMX CSV data into structured rates.
type Parser struct {
	logger *slog.Logger
}

// ParserOption configures the ECB parser.
type ParserOption func(*Parser)

// WithParserLogger sets a custom logger for the parser.
func WithParserLogger(logger *slog.Logger) ParserOption {
	return func(p *Parser) {
		p.logger = logger
	}
}

// NewParser creates a new ECB CSV parser.
func NewParser(opts ...ParserOption) *Parser {
	p := &Parser{
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(p)
	}
	p.logger = p.logger.With("component", "ecb_parser")
	return p
}

// ParseCSV parses ECB SDMX CSV data from the provided reader.
// It handles the standard ECB CSV format with headers and returns parsed rates.
// Malformed rows are logged and skipped rather than causing the entire parse to fail.
func (p *Parser) ParseCSV(r io.Reader) ([]Rate, error) {
	reader := csv.NewReader(bufio.NewReader(r))
	reader.FieldsPerRecord = -1 // Allow variable number of fields

	// Read and validate header
	header, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrNoData
		}
		return nil, fmt.Errorf("%w: failed to read header: %w", ErrInvalidCSVFormat, err)
	}

	if err := p.validateHeader(header); err != nil {
		return nil, err
	}

	// Parse data rows
	var rates []Rate
	lineNum := 1 // Header was line 1

	for {
		lineNum++
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			p.logger.Warn("skipping malformed CSV row", "line", lineNum, "error", err)
			continue
		}

		rate, err := p.parseRow(record)
		if err != nil {
			p.logger.Warn("skipping invalid row", "line", lineNum, "error", err)
			continue
		}

		rates = append(rates, rate)
	}

	if len(rates) == 0 {
		return nil, ErrNoData
	}

	p.logger.Debug("parsed ECB rates", "count", len(rates))
	return rates, nil
}

// validateHeader checks that the CSV has the expected ECB SDMX column headers.
func (p *Parser) validateHeader(header []string) error {
	if len(header) < minColumns {
		return fmt.Errorf("%w: expected at least %d columns, got %d",
			ErrInvalidCSVFormat, minColumns, len(header))
	}

	// Check that required columns are present (case-insensitive)
	for i, expected := range expectedHeaders {
		if i >= len(header) {
			break
		}
		if !strings.EqualFold(strings.TrimSpace(header[i]), expected) {
			return fmt.Errorf("%w: column %d expected '%s', got '%s'",
				ErrInvalidCSVFormat, i, expected, header[i])
		}
	}

	return nil
}

// parseRow parses a single CSV row into a Rate.
func (p *Parser) parseRow(record []string) (Rate, error) {
	if len(record) < minColumns {
		return Rate{}, fmt.Errorf("%w: has %d columns, expected at least %d",
			ErrTooFewColumns, len(record), minColumns)
	}

	// Parse the rate value
	rateStr := strings.TrimSpace(record[colObsValue])
	if rateStr == "" {
		return Rate{}, fmt.Errorf("%w: empty rate value", ErrInvalidRate)
	}

	rate, err := decimal.NewFromString(rateStr)
	if err != nil {
		return Rate{}, fmt.Errorf("%w: %w", ErrInvalidRate, err)
	}

	// Parse the date (format: YYYY-MM-DD)
	dateStr := strings.TrimSpace(record[colTimePeriod])
	observedDate, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return Rate{}, fmt.Errorf("%w: %w", ErrInvalidDate, err)
	}

	return Rate{
		BaseCurrency:       strings.TrimSpace(record[colCurrency]),
		QuoteCurrency:      strings.TrimSpace(record[colCurrencyDen]),
		Value:              rate,
		ObservedDate:       observedDate,
		Frequency:          strings.TrimSpace(record[colFREQ]),
		ExchangeRateType:   strings.TrimSpace(record[colExrType]),
		ExchangeRateSuffix: strings.TrimSpace(record[colExrSuffix]),
	}, nil
}

// TransformConfig holds configuration for transforming rates to observations.
type TransformConfig struct {
	// SourceCode is the data source code (e.g., "ECB").
	SourceCode string
	// DatasetCodeTemplate is a template for generating dataset codes.
	// Use %s placeholders for BaseCurrency and QuoteCurrency.
	// Default: "%s_%s_FX" (e.g., "USD_EUR_FX")
	DatasetCodeTemplate string
}

// DefaultTransformConfig returns the default transformation configuration.
func DefaultTransformConfig() TransformConfig {
	return TransformConfig{
		SourceCode:          "ECB",
		DatasetCodeTemplate: "%s_%s_FX",
	}
}

// TransformToObservations converts parsed ECB rates to RecordObservationRequest protobufs.
// The causation ID format is: ecb-feed-YYYY-MM-DD based on the observation date.
func TransformToObservations(rates []Rate, cfg TransformConfig) []*marketinformationv1.RecordObservationRequest {
	if cfg.SourceCode == "" {
		cfg.SourceCode = "ECB"
	}
	if cfg.DatasetCodeTemplate == "" {
		cfg.DatasetCodeTemplate = "%s_%s_FX"
	}

	requests := make([]*marketinformationv1.RecordObservationRequest, 0, len(rates))

	for _, rate := range rates {
		// Generate dataset code (e.g., "USD_EUR_FX")
		datasetCode := fmt.Sprintf(cfg.DatasetCodeTemplate,
			strings.ToUpper(rate.BaseCurrency),
			strings.ToUpper(rate.QuoteCurrency))

		// Generate causation ID: ecb-feed-YYYY-MM-DD
		causationID := fmt.Sprintf("ecb-feed-%s", rate.ObservedDate.Format("2006-01-02"))

		// Set observed_at to the observation date at midnight UTC
		observedAt := time.Date(
			rate.ObservedDate.Year(),
			rate.ObservedDate.Month(),
			rate.ObservedDate.Day(),
			0, 0, 0, 0, time.UTC,
		)

		// For spot rates, valid_from equals observed_at
		// ECB rates are typically published at 16:00 CET and valid for that business day
		validFrom := observedAt

		req := &marketinformationv1.RecordObservationRequest{
			DatasetCode:    datasetCode,
			DatasetVersion: 0, // Use latest version
			ObservedAt:     timestamppb.New(observedAt),
			ValidFrom:      timestamppb.New(validFrom),
			Value:          rate.Value.String(),
			Quality:        marketinformationv1.QualityLevel_QUALITY_LEVEL_ACTUAL,
			SourceCode:     cfg.SourceCode,
			Attributes:     buildAttributes(rate, causationID),
		}

		requests = append(requests, req)
	}

	return requests
}

// buildAttributes creates the attribute entries for an observation.
func buildAttributes(rate Rate, causationID string) []*quantityv1.AttributeEntry {
	return []*quantityv1.AttributeEntry{
		{Key: "causation_id", Value: causationID},
		{Key: "frequency", Value: rate.Frequency},
		{Key: "exchange_rate_type", Value: rate.ExchangeRateType},
		{Key: "exchange_rate_suffix", Value: rate.ExchangeRateSuffix},
		{Key: "base_currency", Value: rate.BaseCurrency},
		{Key: "quote_currency", Value: rate.QuoteCurrency},
	}
}

// ParseAndTransform is a convenience function that parses CSV data and transforms
// it to observation requests in a single call.
func (p *Parser) ParseAndTransform(r io.Reader, cfg TransformConfig) ([]*marketinformationv1.RecordObservationRequest, error) {
	rates, err := p.ParseCSV(r)
	if err != nil {
		return nil, err
	}
	return TransformToObservations(rates, cfg), nil
}
