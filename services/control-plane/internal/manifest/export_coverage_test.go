package manifest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewExportService_ValidInputs verifies the happy path constructor.
func TestNewExportService_ValidInputs(t *testing.T) {
	repo := &Repository{}
	history, err := NewHistoryService(repo)
	require.NoError(t, err)

	svc, err := NewExportService(history, &ExportCollectors{})
	require.NoError(t, err)
	require.NotNil(t, svc)
}

// TestNewExportService_NilCollectors_DefaultsToEmpty verifies that passing nil
// collectors is treated as an empty collectors set (no panic, no error).
func TestNewExportService_NilCollectors_DefaultsToEmpty(t *testing.T) {
	repo := &Repository{}
	history, err := NewHistoryService(repo)
	require.NoError(t, err)

	svc, err := NewExportService(history, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)

	// Verify that a collect call with nil collectors and nil fallback is a no-op.
	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{Version: "1.0"},
		SectionSources: make(map[string]string),
	}
	svc.collectInstruments(context.Background(), result, nil, "")
	assert.Nil(t, result.Manifest.Instruments)
	assert.Empty(t, result.SectionSources)
}

// TestIsNotFound_WrappedError verifies that isNotFound detects wrapped ErrVersionNotFound.
func TestIsNotFound_WrappedError(t *testing.T) {
	wrapped := errors.New("context: " + ErrVersionNotFound.Error())
	wrappedWithW := fmt.Errorf("wrapped: %w", ErrVersionNotFound)
	wrapErr := errors.Join(ErrVersionNotFound, errors.New("extra context"))
	assert.True(t, isNotFound(ErrVersionNotFound))
	assert.False(t, isNotFound(wrapped))
	assert.True(t, isNotFound(wrappedWithW))
	assert.True(t, isNotFound(wrapErr))
}

// TestExportService_CollectFallbackOnlySections_WithNilFallback verifies that
// all fallback-only collect functions are no-ops when fallback is nil.
func TestExportService_CollectFallbackOnlySections_WithNilFallback(t *testing.T) {
	svc := &ExportService{collectors: &ExportCollectors{}}

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectValuationRules(result, nil, "")
	assert.Nil(t, result.Manifest.ValuationRules)
	assert.Empty(t, result.SectionSources["valuation_rules"])

	svc.collectMappings(result, nil, "")
	assert.Nil(t, result.Manifest.Mappings)
	assert.Empty(t, result.SectionSources["mappings"])

	svc.collectPartyTypes(result, nil, "")
	assert.Nil(t, result.Manifest.PartyTypes)
	assert.Empty(t, result.SectionSources["party_types"])

	svc.collectPaymentRails(result, nil, "")
	assert.Nil(t, result.Manifest.PaymentRails)
	assert.Empty(t, result.SectionSources["payment_rails"])
}

// TestExportService_CollectOrganizations_WithNilFallback verifies no panic when
// the organizations collector errors and there is no fallback.
func TestExportService_CollectOrganizations_WithNilFallback(t *testing.T) {
	svc := &ExportService{
		collectors: &ExportCollectors{
			Organizations: &mockOrganizationCollector{err: errors.New("timeout")},
		},
	}
	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectOrganizations(context.Background(), result, nil, "")
	assert.Nil(t, result.Manifest.Organizations)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "timeout")
}

// TestExportService_CollectInternalAccounts_WithNilFallback verifies no panic
// when internal accounts collector errors without fallback.
func TestExportService_CollectInternalAccounts_WithNilFallback(t *testing.T) {
	svc := &ExportService{
		collectors: &ExportCollectors{
			InternalAccounts: &mockInternalAccountCollector{err: errors.New("unreachable")},
		},
	}
	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectInternalAccounts(context.Background(), result, nil, "")
	assert.Nil(t, result.Manifest.InternalAccounts)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "unreachable")
}

// TestExportService_CollectOperationalGateway_WithNilFallback verifies no panic
// when gateway collector errors without fallback.
func TestExportService_CollectOperationalGateway_WithNilFallback(t *testing.T) {
	svc := &ExportService{
		collectors: &ExportCollectors{
			OperationalGateway: &mockOperationalGatewayCollector{
				connErr:  errors.New("gateway down"),
				routeErr: errors.New("route fail"),
			},
		},
	}
	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectOperationalGateway(context.Background(), result, nil, "")
	assert.Nil(t, result.Manifest.OperationalGateway)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "gateway down")
	assert.Contains(t, result.Warnings[0], "route fail")
}

// TestExportService_CollectMarketData_WithNilFallback verifies no panic when
// market data collector errors without fallback.
func TestExportService_CollectMarketData_WithNilFallback(t *testing.T) {
	svc := &ExportService{
		collectors: &ExportCollectors{
			MarketData: &mockMarketDataCollector{srcErr: errors.New("market error")},
		},
	}
	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectMarketData(context.Background(), result, nil, "")
	assert.Nil(t, result.Manifest.MarketData)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "market error")
}

// TestExportService_ManifestChecksumEmpty verifies checksum of empty manifest.
func TestExportService_ManifestChecksumEmpty(t *testing.T) {
	sum, err := manifestChecksum(&controlplanev1.Manifest{})
	require.NoError(t, err)
	assert.Len(t, sum, 64)
	assert.NotEmpty(t, sum)
}

// TestExportResult_ToProtoResponse_EmptyWarnings verifies nil warnings are
// preserved correctly in the proto response.
func TestExportResult_ToProtoResponse_EmptyWarnings(t *testing.T) {
	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{Version: "2.0"},
		Checksum:       "deadbeef",
		SectionSources: map[string]string{},
		Warnings:       nil,
	}
	resp := result.ToProtoResponse()
	assert.Equal(t, "2.0", resp.Manifest.Version)
	assert.Nil(t, resp.Warnings)
}

// TestExportService_CollectOrganizations_NilCollector_WithFallback verifies that
// when Organizations collector is nil, data is sourced from the fallback manifest.
func TestExportService_CollectOrganizations_NilCollector_WithFallback(t *testing.T) {
	svc := &ExportService{collectors: &ExportCollectors{}} // nil Organizations collector

	fb := testFallbackManifest()
	fb.Organizations = []*controlplanev1.OrganizationDefinition{
		{Code: "PLATFORM", Name: "Platform Org", PartyType: "ORGANIZATION"},
	}

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectOrganizations(context.Background(), result, fb, "1.5")
	assert.Equal(t, fb.Organizations, result.Manifest.Organizations)
	assert.Equal(t, "fallback:manifest-v1.5", result.SectionSources["organizations"])
}

// TestExportService_CollectInternalAccounts_NilCollector_WithFallback verifies that
// when InternalAccounts collector is nil, data is sourced from the fallback manifest.
func TestExportService_CollectInternalAccounts_NilCollector_WithFallback(t *testing.T) {
	svc := &ExportService{collectors: &ExportCollectors{}} // nil InternalAccounts collector

	fb := testFallbackManifest()
	fb.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "NOSTRO_GBP", AccountType: "NOSTRO", Instrument: "GBP"},
	}

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectInternalAccounts(context.Background(), result, fb, "1.5")
	assert.Equal(t, fb.InternalAccounts, result.Manifest.InternalAccounts)
	assert.Equal(t, "fallback:manifest-v1.5", result.SectionSources["internal_accounts"])
}

// TestExportService_CollectMarketData_NilCollector_WithFallback verifies that
// when MarketData collector is nil, data is sourced from the fallback manifest.
func TestExportService_CollectMarketData_NilCollector_WithFallback(t *testing.T) {
	svc := &ExportService{collectors: &ExportCollectors{}} // nil MarketData collector

	fb := testFallbackManifest()
	fb.MarketData = &controlplanev1.MarketDataConfig{
		Sources: []*controlplanev1.MarketDataSourceDefinition{
			{Code: "ECB", Name: "European Central Bank"},
		},
	}

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectMarketData(context.Background(), result, fb, "1.5")
	assert.Equal(t, fb.MarketData, result.Manifest.MarketData)
	assert.Equal(t, "fallback:manifest-v1.5", result.SectionSources["market_data"])
}

// TestExportService_CollectOperationalGateway_NilCollector_WithFallback verifies
// that when OperationalGateway collector is nil, data comes from the fallback.
func TestExportService_CollectOperationalGateway_NilCollector_WithFallback(t *testing.T) {
	svc := &ExportService{collectors: &ExportCollectors{}} // nil OperationalGateway collector

	fb := testFallbackManifest()
	fb.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		ProviderConnections: []*controlplanev1.ProviderConnectionConfig{
			{ConnectionId: "stripe", ProviderName: "Stripe"},
		},
	}

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectOperationalGateway(context.Background(), result, fb, "1.5")
	assert.Equal(t, fb.OperationalGateway, result.Manifest.OperationalGateway)
	assert.Equal(t, "fallback:manifest-v1.5", result.SectionSources["operational_gateway"])
}

// TestExportService_CollectAllFallbackSections_SourceLabels verifies that
// fallback-only sections use the correct version label in SectionSources.
func TestExportService_CollectAllFallbackSections_SourceLabels(t *testing.T) {
	svc := &ExportService{collectors: &ExportCollectors{}}

	// Use testFallbackManifest which has non-nil ValuationRules.
	fb := testFallbackManifest()

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectValuationRules(result, fb, "3.0")
	assert.Equal(t, "fallback:manifest-v3.0", result.SectionSources["valuation_rules"])

	svc.collectMappings(result, fb, "3.0")
	assert.Equal(t, "fallback:manifest-v3.0", result.SectionSources["mappings"])

	svc.collectPartyTypes(result, fb, "3.0")
	assert.Equal(t, "fallback:manifest-v3.0", result.SectionSources["party_types"])

	svc.collectPaymentRails(result, fb, "3.0")
	assert.Equal(t, "fallback:manifest-v3.0", result.SectionSources["payment_rails"])
}
