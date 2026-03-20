package manifest

import (
	"context"
	"errors"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock collectors ---

type mockInstrumentCollector struct {
	instruments []*controlplanev1.InstrumentDefinition
	err         error
}

func (m *mockInstrumentCollector) ListInstruments(_ context.Context) ([]*controlplanev1.InstrumentDefinition, error) {
	return m.instruments, m.err
}

type mockAccountTypeCollector struct {
	accountTypes []*controlplanev1.AccountTypeDefinition
	err          error
}

func (m *mockAccountTypeCollector) ListAccountTypes(_ context.Context) ([]*controlplanev1.AccountTypeDefinition, error) {
	return m.accountTypes, m.err
}

type mockSagaCollector struct {
	sagas []*controlplanev1.SagaDefinition
	err   error
}

func (m *mockSagaCollector) ListSagas(_ context.Context) ([]*controlplanev1.SagaDefinition, error) {
	return m.sagas, m.err
}

type mockMarketDataCollector struct {
	sources  []*controlplanev1.MarketDataSourceDefinition
	datasets []*controlplanev1.MarketDataSetDefinition
	srcErr   error
	dsErr    error
}

func (m *mockMarketDataCollector) ListMarketDataSources(_ context.Context) ([]*controlplanev1.MarketDataSourceDefinition, error) {
	return m.sources, m.srcErr
}

func (m *mockMarketDataCollector) ListMarketDataSets(_ context.Context) ([]*controlplanev1.MarketDataSetDefinition, error) {
	return m.datasets, m.dsErr
}

type mockOrganizationCollector struct {
	orgs []*controlplanev1.OrganizationDefinition
	err  error
}

func (m *mockOrganizationCollector) ListOrganizations(_ context.Context) ([]*controlplanev1.OrganizationDefinition, error) {
	return m.orgs, m.err
}

type mockInternalAccountCollector struct {
	accounts []*controlplanev1.InternalAccountDefinition
	err      error
}

func (m *mockInternalAccountCollector) ListInternalAccounts(_ context.Context) ([]*controlplanev1.InternalAccountDefinition, error) {
	return m.accounts, m.err
}

type mockOperationalGatewayCollector struct {
	connections []*controlplanev1.ProviderConnectionConfig
	routes      []*controlplanev1.InstructionRouteConfig
	connErr     error
	routeErr    error
}

func (m *mockOperationalGatewayCollector) ListProviderConnections(_ context.Context) ([]*controlplanev1.ProviderConnectionConfig, error) {
	return m.connections, m.connErr
}

func (m *mockOperationalGatewayCollector) ListInstructionRoutes(_ context.Context) ([]*controlplanev1.InstructionRouteConfig, error) {
	return m.routes, m.routeErr
}

// --- Test helpers ---

func testFallbackManifest() *controlplanev1.Manifest {
	return &controlplanev1.Manifest{
		Version: "1.0",
		Metadata: &controlplanev1.ManifestMetadata{
			Name:     "Test Economy",
			Industry: "testing",
		},
		Instruments: []*controlplanev1.InstrumentDefinition{
			{
				Code: "GBP",
				Name: "British Pound",
				Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{
					Unit:      "GBP",
					Precision: 2,
				},
			},
		},
		AccountTypes: []*controlplanev1.AccountTypeDefinition{
			{
				Code:          "CURRENT",
				Name:          "Current Account",
				NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
			},
		},
		ValuationRules: []*controlplanev1.ValuationRule{
			{
				FromInstrument: "KWH",
				ToInstrument:   "GBP",
				Method:         controlplanev1.ValuationMethod_VALUATION_METHOD_SPOT_RATE,
				Source:         "nordpool",
			},
		},
		Sagas: []*controlplanev1.SagaDefinition{
			{
				Name:    "test_saga",
				Trigger: "api:/v1/test",
				Script:  "print('hello')",
			},
		},
	}
}

// --- Tests ---

func TestParseSections(t *testing.T) {
	t.Run("empty includes all sections", func(t *testing.T) {
		result := parseSections(nil)
		assert.Equal(t, len(allSections), len(result))
		for k := range allSections {
			assert.True(t, result[k], "expected section %s to be included", k)
		}
	})

	t.Run("specific sections", func(t *testing.T) {
		result := parseSections([]string{"instruments", "sagas"})
		assert.Len(t, result, 2)
		assert.True(t, result[SectionInstruments])
		assert.True(t, result[SectionSagas])
	})

	t.Run("unknown sections are ignored", func(t *testing.T) {
		result := parseSections([]string{"instruments", "nonexistent"})
		assert.Len(t, result, 1)
		assert.True(t, result[SectionInstruments])
	})
}

func TestManifestChecksum(t *testing.T) {
	t.Run("deterministic checksum", func(t *testing.T) {
		m := &controlplanev1.Manifest{
			Version: "1.0",
			Metadata: &controlplanev1.ManifestMetadata{
				Name: "Test",
			},
		}
		c1, err := manifestChecksum(m)
		require.NoError(t, err)
		c2, err := manifestChecksum(m)
		require.NoError(t, err)
		assert.Equal(t, c1, c2)
		assert.Len(t, c1, 64) // SHA-256 hex length
	})

	t.Run("different manifests produce different checksums", func(t *testing.T) {
		m1 := &controlplanev1.Manifest{Version: "1.0", Metadata: &controlplanev1.ManifestMetadata{Name: "A"}}
		m2 := &controlplanev1.Manifest{Version: "1.0", Metadata: &controlplanev1.ManifestMetadata{Name: "B"}}
		c1, _ := manifestChecksum(m1)
		c2, _ := manifestChecksum(m2)
		assert.NotEqual(t, c1, c2)
	})
}

func TestExportService_NewExportService(t *testing.T) {
	t.Run("nil history returns error", func(t *testing.T) {
		_, err := NewExportService(nil, nil)
		require.Error(t, err)
	})
}

func TestExportResult_ToProtoResponse(t *testing.T) {
	result := &ExportResult{
		Manifest: &controlplanev1.Manifest{Version: "1.0"},
		Checksum: "abc123",
		SectionSources: map[string]string{
			"instruments": "live:reference-data",
		},
		Warnings: []string{"test warning"},
	}
	resp := result.ToProtoResponse()
	assert.Equal(t, "1.0", resp.Manifest.Version)
	assert.Equal(t, "abc123", resp.Checksum)
	assert.Equal(t, "live:reference-data", resp.SectionSources["instruments"])
	assert.Equal(t, []string{"test warning"}, resp.Warnings)
	assert.NotNil(t, resp.ExportedAt)
}

func TestExportService_Export_AllFallback(t *testing.T) {
	// When no collectors are configured, all sections come from fallback.
	// We use a mock history service by creating one with a repository that has a stored manifest.
	// For unit testing without a DB, we'll test the export logic directly.

	t.Run("no collectors, no fallback returns empty manifest", func(t *testing.T) {
		// Create a minimal ExportService with a history that returns not-found.
		svc := &ExportService{
			history:    &HistoryService{repo: nil}, // will cause panic if called
			collectors: &ExportCollectors{},
		}

		// Call export with a custom loadFallbackManifest override.
		// Since we can't easily mock the history service in a unit test without a DB,
		// we test the collection functions directly.
		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{Version: "1.0"},
			SectionSources: make(map[string]string),
		}

		// With nil fallback, collect functions should be no-ops.
		svc.collectInstruments(context.Background(), result, nil, "")
		assert.Nil(t, result.Manifest.Instruments)
		assert.Empty(t, result.SectionSources["instruments"])
	})

	t.Run("no collectors, with fallback populates from fallback", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{},
		}

		fb := testFallbackManifest()
		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{Version: "1.0"},
			SectionSources: make(map[string]string),
		}

		svc.collectInstruments(context.Background(), result, fb, "1.0")
		assert.Equal(t, fb.Instruments, result.Manifest.Instruments)
		assert.Equal(t, "fallback:manifest-v1.0", result.SectionSources["instruments"])

		svc.collectAccountTypes(context.Background(), result, fb, "1.0")
		assert.Equal(t, fb.AccountTypes, result.Manifest.AccountTypes)
		assert.Equal(t, "fallback:manifest-v1.0", result.SectionSources["account_types"])

		svc.collectValuationRules(result, fb, "1.0")
		assert.Equal(t, fb.ValuationRules, result.Manifest.ValuationRules)
		assert.Equal(t, "fallback:manifest-v1.0", result.SectionSources["valuation_rules"])

		svc.collectSagas(context.Background(), result, fb, "1.0")
		assert.Equal(t, fb.Sagas, result.Manifest.Sagas)
		assert.Equal(t, "fallback:manifest-v1.0", result.SectionSources["sagas"])
	})
}

func TestExportService_Export_LiveCollectors(t *testing.T) {
	liveInstruments := []*controlplanev1.InstrumentDefinition{
		{
			Code: "USD",
			Name: "US Dollar",
			Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
			Dimensions: &controlplanev1.InstrumentDimensions{
				Unit:      "USD",
				Precision: 2,
			},
		},
	}

	liveAccountTypes := []*controlplanev1.AccountTypeDefinition{
		{
			Code:          "SAVINGS",
			Name:          "Savings Account",
			NormalBalance: controlplanev1.NormalBalance_NORMAL_BALANCE_DEBIT,
		},
	}

	liveSagas := []*controlplanev1.SagaDefinition{
		{
			Name:    "live_saga",
			Trigger: "api:/v1/live",
			Script:  "print('live')",
		},
	}

	t.Run("instruments from live collector", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{
				Instruments: &mockInstrumentCollector{instruments: liveInstruments},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		fb := testFallbackManifest()
		svc.collectInstruments(context.Background(), result, fb, "1.0")

		assert.Equal(t, liveInstruments, result.Manifest.Instruments)
		assert.Equal(t, "live:reference-data", result.SectionSources["instruments"])
	})

	t.Run("account types from live collector", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{
				AccountTypes: &mockAccountTypeCollector{accountTypes: liveAccountTypes},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectAccountTypes(context.Background(), result, nil, "")
		assert.Equal(t, liveAccountTypes, result.Manifest.AccountTypes)
		assert.Equal(t, "live:reference-data", result.SectionSources["account_types"])
	})

	t.Run("sagas from live collector", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{
				Sagas: &mockSagaCollector{sagas: liveSagas},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectSagas(context.Background(), result, nil, "")
		assert.Equal(t, liveSagas, result.Manifest.Sagas)
		assert.Equal(t, "live:saga-registry", result.SectionSources["sagas"])
	})
}

func TestExportService_Export_CollectorError_FallsBack(t *testing.T) {
	fb := testFallbackManifest()

	t.Run("instrument collector error falls back with warning", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{
				Instruments: &mockInstrumentCollector{err: errors.New("connection refused")},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectInstruments(context.Background(), result, fb, "1.0")

		assert.Equal(t, fb.Instruments, result.Manifest.Instruments)
		assert.Equal(t, "fallback:manifest-v1.0", result.SectionSources["instruments"])
		require.Len(t, result.Warnings, 1)
		assert.Contains(t, result.Warnings[0], "connection refused")
	})

	t.Run("saga collector error falls back with warning", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{
				Sagas: &mockSagaCollector{err: errors.New("timeout")},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectSagas(context.Background(), result, fb, "1.0")

		assert.Equal(t, fb.Sagas, result.Manifest.Sagas)
		assert.Equal(t, "fallback:manifest-v1.0", result.SectionSources["sagas"])
		require.Len(t, result.Warnings, 1)
		assert.Contains(t, result.Warnings[0], "timeout")
	})

	t.Run("collector error without fallback produces warning only", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{
				Instruments: &mockInstrumentCollector{err: errors.New("fail")},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectInstruments(context.Background(), result, nil, "")

		assert.Nil(t, result.Manifest.Instruments)
		assert.Empty(t, result.SectionSources["instruments"])
		require.Len(t, result.Warnings, 1)
	})
}

func TestExportService_Export_MarketData(t *testing.T) {
	liveSources := []*controlplanev1.MarketDataSourceDefinition{
		{Code: "ECB", Name: "European Central Bank"},
	}
	liveDatasets := []*controlplanev1.MarketDataSetDefinition{
		{Code: "USD_EUR_FX", Unit: "USD/EUR"},
	}

	t.Run("market data from live collector", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{
				MarketData: &mockMarketDataCollector{
					sources:  liveSources,
					datasets: liveDatasets,
				},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectMarketData(context.Background(), result, nil, "")

		require.NotNil(t, result.Manifest.MarketData)
		assert.Equal(t, liveSources, result.Manifest.MarketData.Sources)
		assert.Equal(t, liveDatasets, result.Manifest.MarketData.Datasets)
		assert.Equal(t, "live:market-information", result.SectionSources["market_data"])
	})

	t.Run("market data source error falls back", func(t *testing.T) {
		fb := testFallbackManifest()
		fb.MarketData = &controlplanev1.MarketDataConfig{
			Sources: []*controlplanev1.MarketDataSourceDefinition{{Code: "FB"}},
		}

		svc := &ExportService{
			collectors: &ExportCollectors{
				MarketData: &mockMarketDataCollector{srcErr: errors.New("fail")},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectMarketData(context.Background(), result, fb, "1.0")

		assert.Equal(t, fb.MarketData, result.Manifest.MarketData)
		assert.Equal(t, "fallback:manifest-v1.0", result.SectionSources["market_data"])
		require.Len(t, result.Warnings, 1)
	})
}

func TestExportService_Export_OperationalGateway(t *testing.T) {
	liveConns := []*controlplanev1.ProviderConnectionConfig{
		{ConnectionId: "stripe-payments", ProviderName: "Stripe"},
	}
	liveRoutes := []*controlplanev1.InstructionRouteConfig{
		{InstructionType: "payment.initiate", ConnectionId: "stripe-payments"},
	}

	t.Run("operational gateway from live collector", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{
				OperationalGateway: &mockOperationalGatewayCollector{
					connections: liveConns,
					routes:      liveRoutes,
				},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectOperationalGateway(context.Background(), result, nil, "")

		require.NotNil(t, result.Manifest.OperationalGateway)
		assert.Equal(t, liveConns, result.Manifest.OperationalGateway.ProviderConnections)
		assert.Equal(t, liveRoutes, result.Manifest.OperationalGateway.InstructionRoutes)
		assert.Equal(t, "live:operational-gateway", result.SectionSources["operational_gateway"])
	})

	t.Run("operational gateway connection error falls back", func(t *testing.T) {
		fb := testFallbackManifest()
		fb.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
			ProviderConnections: []*controlplanev1.ProviderConnectionConfig{{ConnectionId: "fb"}},
		}

		svc := &ExportService{
			collectors: &ExportCollectors{
				OperationalGateway: &mockOperationalGatewayCollector{connErr: errors.New("fail")},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectOperationalGateway(context.Background(), result, fb, "1.0")

		assert.Equal(t, fb.OperationalGateway, result.Manifest.OperationalGateway)
		assert.Equal(t, "fallback:manifest-v1.0", result.SectionSources["operational_gateway"])
		require.Len(t, result.Warnings, 1)
	})
}

func TestExportService_Export_Organizations(t *testing.T) {
	liveOrgs := []*controlplanev1.OrganizationDefinition{
		{Code: "ACME", Name: "Acme Corp", PartyType: "ORGANIZATION"},
	}

	t.Run("organizations from live collector", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{
				Organizations: &mockOrganizationCollector{orgs: liveOrgs},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectOrganizations(context.Background(), result, nil, "")
		assert.Equal(t, liveOrgs, result.Manifest.Organizations)
		assert.Equal(t, "live:party", result.SectionSources["organizations"])
	})
}

func TestExportService_Export_InternalAccounts(t *testing.T) {
	liveAccounts := []*controlplanev1.InternalAccountDefinition{
		{Code: "REVENUE_GBP", AccountType: "REVENUE", Instrument: "GBP"},
	}

	t.Run("internal accounts from live collector", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{
				InternalAccounts: &mockInternalAccountCollector{accounts: liveAccounts},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectInternalAccounts(context.Background(), result, nil, "")
		assert.Equal(t, liveAccounts, result.Manifest.InternalAccounts)
		assert.Equal(t, "live:internal-account", result.SectionSources["internal_accounts"])
	})
}

func TestExportService_Export_FallbackOnlySections(t *testing.T) {
	fb := testFallbackManifest()

	t.Run("valuation rules always from fallback", func(t *testing.T) {
		svc := &ExportService{collectors: &ExportCollectors{}}
		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}
		svc.collectValuationRules(result, fb, "1.0")
		assert.Equal(t, fb.ValuationRules, result.Manifest.ValuationRules)
		assert.Equal(t, "fallback:manifest-v1.0", result.SectionSources["valuation_rules"])
	})

	t.Run("payment rails always from fallback", func(t *testing.T) {
		svc := &ExportService{collectors: &ExportCollectors{}}
		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}
		svc.collectPaymentRails(result, fb, "1.0")
		// fb has no payment rails, so should be nil
		assert.Nil(t, result.Manifest.PaymentRails)
	})

	t.Run("mappings always from fallback", func(t *testing.T) {
		svc := &ExportService{collectors: &ExportCollectors{}}
		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}
		svc.collectMappings(result, fb, "1.0")
		assert.Nil(t, result.Manifest.Mappings)
	})

	t.Run("party types always from fallback", func(t *testing.T) {
		svc := &ExportService{collectors: &ExportCollectors{}}
		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}
		svc.collectPartyTypes(result, fb, "1.0")
		assert.Nil(t, result.Manifest.PartyTypes)
	})
}

func TestExportService_Export_AccountTypeCollectorError(t *testing.T) {
	fb := testFallbackManifest()

	t.Run("account type collector error falls back with warning", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{
				AccountTypes: &mockAccountTypeCollector{err: errors.New("db connection lost")},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectAccountTypes(context.Background(), result, fb, "1.0")
		assert.Equal(t, fb.AccountTypes, result.Manifest.AccountTypes)
		assert.Equal(t, "fallback:manifest-v1.0", result.SectionSources["account_types"])
		require.Len(t, result.Warnings, 1)
		assert.Contains(t, result.Warnings[0], "db connection lost")
	})

	t.Run("account type collector error without fallback", func(t *testing.T) {
		svc := &ExportService{
			collectors: &ExportCollectors{
				AccountTypes: &mockAccountTypeCollector{err: errors.New("fail")},
			},
		}

		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{},
			SectionSources: make(map[string]string),
		}

		svc.collectAccountTypes(context.Background(), result, nil, "")
		assert.Nil(t, result.Manifest.AccountTypes)
		require.Len(t, result.Warnings, 1)
	})
}

func TestExportService_Export_OrganizationCollectorError(t *testing.T) {
	fb := testFallbackManifest()
	fb.Organizations = []*controlplanev1.OrganizationDefinition{
		{Code: "ACME", Name: "Acme Corp"},
	}

	svc := &ExportService{
		collectors: &ExportCollectors{
			Organizations: &mockOrganizationCollector{err: errors.New("timeout")},
		},
	}

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectOrganizations(context.Background(), result, fb, "1.0")
	assert.Equal(t, fb.Organizations, result.Manifest.Organizations)
	assert.Equal(t, "fallback:manifest-v1.0", result.SectionSources["organizations"])
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "timeout")
}

func TestExportService_Export_InternalAccountCollectorError(t *testing.T) {
	fb := testFallbackManifest()
	fb.InternalAccounts = []*controlplanev1.InternalAccountDefinition{
		{Code: "REVENUE_GBP", AccountType: "REVENUE"},
	}

	svc := &ExportService{
		collectors: &ExportCollectors{
			InternalAccounts: &mockInternalAccountCollector{err: errors.New("service unavailable")},
		},
	}

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectInternalAccounts(context.Background(), result, fb, "1.0")
	assert.Equal(t, fb.InternalAccounts, result.Manifest.InternalAccounts)
	assert.Equal(t, "fallback:manifest-v1.0", result.SectionSources["internal_accounts"])
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "service unavailable")
}

func TestExportService_Export_MarketDataDatasetError(t *testing.T) {
	fb := testFallbackManifest()
	fb.MarketData = &controlplanev1.MarketDataConfig{
		Datasets: []*controlplanev1.MarketDataSetDefinition{{Code: "FB_DS"}},
	}

	svc := &ExportService{
		collectors: &ExportCollectors{
			MarketData: &mockMarketDataCollector{dsErr: errors.New("dataset fail")},
		},
	}

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectMarketData(context.Background(), result, fb, "1.0")
	assert.Equal(t, fb.MarketData, result.Manifest.MarketData)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "dataset fail")
}

func TestExportService_Export_MarketDataBothErrors(t *testing.T) {
	svc := &ExportService{
		collectors: &ExportCollectors{
			MarketData: &mockMarketDataCollector{
				srcErr: errors.New("src fail"),
				dsErr:  errors.New("ds fail"),
			},
		},
	}

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectMarketData(context.Background(), result, nil, "")
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "src fail")
	assert.Contains(t, result.Warnings[0], "ds fail")
}

func TestExportService_Export_OperationalGatewayRouteError(t *testing.T) {
	fb := testFallbackManifest()
	fb.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
		InstructionRoutes: []*controlplanev1.InstructionRouteConfig{{InstructionType: "pay"}},
	}

	svc := &ExportService{
		collectors: &ExportCollectors{
			OperationalGateway: &mockOperationalGatewayCollector{routeErr: errors.New("route fail")},
		},
	}

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectOperationalGateway(context.Background(), result, fb, "1.0")
	assert.Equal(t, fb.OperationalGateway, result.Manifest.OperationalGateway)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "route fail")
}

func TestExportService_Export_OperationalGatewayBothErrors(t *testing.T) {
	svc := &ExportService{
		collectors: &ExportCollectors{
			OperationalGateway: &mockOperationalGatewayCollector{
				connErr:  errors.New("conn fail"),
				routeErr: errors.New("route fail"),
			},
		},
	}

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		SectionSources: make(map[string]string),
	}

	svc.collectOperationalGateway(context.Background(), result, nil, "")
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "conn fail")
	assert.Contains(t, result.Warnings[0], "route fail")
}

func TestIsNotFound(t *testing.T) {
	assert.True(t, isNotFound(ErrVersionNotFound))
	assert.False(t, isNotFound(nil))
	assert.False(t, isNotFound(errors.New("other error")))
}

func TestExportService_Export_SectionFiltering(t *testing.T) {
	t.Run("include only instruments section", func(t *testing.T) {
		liveInstruments := []*controlplanev1.InstrumentDefinition{
			{
				Code: "EUR", Name: "Euro", Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
				Dimensions: &controlplanev1.InstrumentDimensions{Unit: "EUR", Precision: 2},
			},
		}

		svc := &ExportService{
			collectors: &ExportCollectors{
				Instruments: &mockInstrumentCollector{instruments: liveInstruments},
				Sagas:       &mockSagaCollector{sagas: []*controlplanev1.SagaDefinition{{Name: "should_not_appear"}}},
			},
		}

		fb := testFallbackManifest()
		result := &ExportResult{
			Manifest:       &controlplanev1.Manifest{Version: fb.Version, Metadata: fb.Metadata},
			SectionSources: make(map[string]string),
		}

		sections := parseSections([]string{"instruments"})
		// Only instruments section should be collected.
		if sections[SectionInstruments] {
			svc.collectInstruments(context.Background(), result, fb, "1.0")
		}
		if sections[SectionSagas] {
			svc.collectSagas(context.Background(), result, fb, "1.0")
		}

		assert.Equal(t, liveInstruments, result.Manifest.Instruments)
		assert.Nil(t, result.Manifest.Sagas) // Sagas not included.
	})
}

func TestParseSections_Empty(t *testing.T) {
	sections := parseSections(nil)
	assert.Len(t, sections, len(allSections))
	for name := range allSections {
		assert.True(t, sections[name], "expected %s in result", name)
	}
}

func TestParseSections_ValidSubset(t *testing.T) {
	sections := parseSections([]string{"instruments", "sagas"})
	assert.Len(t, sections, 2)
	assert.True(t, sections[SectionInstruments])
	assert.True(t, sections[SectionSagas])
}

func TestParseSections_UnknownIgnored(t *testing.T) {
	sections := parseSections([]string{"instruments", "nonexistent"})
	assert.Len(t, sections, 1)
	assert.True(t, sections[SectionInstruments])
}

func TestParseSections_AllUnknown(t *testing.T) {
	sections := parseSections([]string{"bogus", "fake"})
	assert.Empty(t, sections)
}

func TestParseSections_EmptySlice(t *testing.T) {
	sections := parseSections([]string{})
	assert.Len(t, sections, len(allSections))
}
