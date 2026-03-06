package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/shopspring/decimal"
)

// ErrNoValuationMethod is returned when no valuation method can be resolved for an instrument.
var ErrNoValuationMethod = errors.New("no valuation method found")

// GRPCReferenceDataProvider implements ReferenceDataProvider using the Reference Data
// gRPC service. It retrieves instrument metadata for materiality thresholds and
// resolves valuation method IDs via account type definitions.
type GRPCReferenceDataProvider struct {
	instrumentClient  referencedatav1.ReferenceDataServiceClient
	accountTypeClient referencedatav1.AccountTypeRegistryServiceClient
	defaultMethodID   uuid.UUID
	logger            *slog.Logger
}

// GRPCReferenceDataProviderConfig holds configuration for the provider.
type GRPCReferenceDataProviderConfig struct {
	// InstrumentClient provides instrument metadata lookups.
	InstrumentClient referencedatav1.ReferenceDataServiceClient

	// AccountTypeClient provides account type lookups for valuation method resolution.
	AccountTypeClient referencedatav1.AccountTypeRegistryServiceClient

	// DefaultMethodID is used as fallback when no valuation method is found
	// for an instrument in the account type configuration.
	DefaultMethodID uuid.UUID

	// Logger for structured logging.
	Logger *slog.Logger
}

// NewGRPCReferenceDataProvider creates a new provider backed by Reference Data gRPC services.
func NewGRPCReferenceDataProvider(cfg GRPCReferenceDataProviderConfig) *GRPCReferenceDataProvider {
	return &GRPCReferenceDataProvider{
		instrumentClient:  cfg.InstrumentClient,
		accountTypeClient: cfg.AccountTypeClient,
		defaultMethodID:   cfg.DefaultMethodID,
		logger:            cfg.Logger,
	}
}

// GetValuationMethodID resolves the valuation method ID for an instrument code.
//
// Resolution strategy:
//  1. If an account type client is available, list active account types and find a
//     valuation method template matching the instrument code.
//  2. Fall back to the configured default method ID.
func (p *GRPCReferenceDataProvider) GetValuationMethodID(ctx context.Context, instrumentCode string) (uuid.UUID, error) {
	if p.accountTypeClient != nil {
		methodID, err := p.resolveFromAccountTypes(ctx, instrumentCode)
		if err != nil {
			p.logger.WarnContext(ctx, "account type method resolution failed, using default",
				"instrument_code", instrumentCode,
				"error", err,
			)
		} else if methodID != uuid.Nil {
			return methodID, nil
		}
	}

	if p.defaultMethodID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w for instrument %s and no default configured", ErrNoValuationMethod, instrumentCode)
	}

	p.logger.DebugContext(ctx, "using default valuation method",
		"instrument_code", instrumentCode,
		"method_id", p.defaultMethodID,
	)
	return p.defaultMethodID, nil
}

// GetMaterialityThreshold derives the materiality threshold from instrument precision.
//
// The threshold is 10^(-precision), so an instrument with precision 2 (e.g., GBP)
// gets a threshold of 0.01, and precision 4 gets 0.0001.
// Falls back to 0.01 if the instrument lookup fails.
func (p *GRPCReferenceDataProvider) GetMaterialityThreshold(ctx context.Context, instrumentCode string) (decimal.Decimal, error) {
	defaultThreshold := decimal.NewFromFloat(0.01)

	if p.instrumentClient == nil {
		return defaultThreshold, nil
	}

	resp, err := p.instrumentClient.RetrieveInstrument(ctx, &referencedatav1.RetrieveInstrumentRequest{
		Code: instrumentCode,
	})
	if err != nil {
		p.logger.WarnContext(ctx, "instrument lookup failed for materiality threshold, using default",
			"instrument_code", instrumentCode,
			"error", err,
		)
		return defaultThreshold, nil
	}

	if resp.GetInstrument() == nil {
		return defaultThreshold, nil
	}

	precision := resp.GetInstrument().GetPrecision()
	if precision <= 0 {
		return defaultThreshold, nil
	}

	// Materiality threshold = 10^(-precision)
	// e.g., precision 2 → 0.01, precision 4 → 0.0001
	threshold := decimal.New(1, -precision)

	p.logger.DebugContext(ctx, "materiality threshold from instrument precision",
		"instrument_code", instrumentCode,
		"precision", precision,
		"threshold", threshold.String(),
	)

	return threshold, nil
}

// resolveFromAccountTypes searches active account type definitions for a valuation
// method template matching the given instrument code.
func (p *GRPCReferenceDataProvider) resolveFromAccountTypes(ctx context.Context, instrumentCode string) (uuid.UUID, error) {
	resp, err := p.accountTypeClient.ListActive(ctx, &referencedatav1.ListActiveRequest{
		PageSize: 100,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to list active account types: %w", err)
	}

	for _, def := range resp.GetDefinitions() {
		for _, vm := range def.GetValuationMethods() {
			if vm.GetInputInstrument() == instrumentCode {
				methodID, err := uuid.Parse(vm.GetValuationMethodId())
				if err != nil {
					continue
				}
				return methodID, nil
			}
		}

		// Check default conversion method as fallback
		if convID := def.GetDefaultConversionMethodId(); convID != "" {
			methodID, err := uuid.Parse(convID)
			if err == nil {
				return methodID, nil
			}
		}
	}

	return uuid.Nil, nil
}
