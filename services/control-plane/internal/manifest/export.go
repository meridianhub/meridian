package manifest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SectionName identifies a manifest section for filtering.
type SectionName string

// Manifest section names for filtering ExportManifest results.
const (
	SectionInstruments        SectionName = "instruments"
	SectionAccountTypes       SectionName = "account_types"
	SectionValuationRules     SectionName = "valuation_rules"
	SectionSagas              SectionName = "sagas"
	SectionMarketData         SectionName = "market_data"
	SectionOrganizations      SectionName = "organizations"
	SectionInternalAccounts   SectionName = "internal_accounts"
	SectionOperationalGateway SectionName = "operational_gateway"
	SectionMappings           SectionName = "mappings"
	SectionPartyTypes         SectionName = "party_types"
	SectionPaymentRails       SectionName = "payment_rails"
)

// allSections is the complete set of exportable sections.
var allSections = map[SectionName]bool{
	SectionInstruments:        true,
	SectionAccountTypes:       true,
	SectionValuationRules:     true,
	SectionSagas:              true,
	SectionMarketData:         true,
	SectionOrganizations:      true,
	SectionInternalAccounts:   true,
	SectionOperationalGateway: true,
	SectionMappings:           true,
	SectionPartyTypes:         true,
	SectionPaymentRails:       true,
}

// InstrumentCollector retrieves instruments from the live reference-data service.
type InstrumentCollector interface {
	ListInstruments(ctx context.Context) ([]*controlplanev1.InstrumentDefinition, error)
}

// AccountTypeCollector retrieves account types from the live reference-data service.
type AccountTypeCollector interface {
	ListAccountTypes(ctx context.Context) ([]*controlplanev1.AccountTypeDefinition, error)
}

// SagaCollector retrieves saga definitions from the live saga registry.
type SagaCollector interface {
	ListSagas(ctx context.Context) ([]*controlplanev1.SagaDefinition, error)
}

// MarketDataCollector retrieves market data sources and data sets from the live market-information service.
type MarketDataCollector interface {
	ListMarketDataSources(ctx context.Context) ([]*controlplanev1.MarketDataSourceDefinition, error)
	ListMarketDataSets(ctx context.Context) ([]*controlplanev1.MarketDataSetDefinition, error)
}

// OrganizationCollector retrieves organizations from the live party service.
type OrganizationCollector interface {
	ListOrganizations(ctx context.Context) ([]*controlplanev1.OrganizationDefinition, error)
}

// InternalAccountCollector retrieves internal accounts from the live internal-account service.
type InternalAccountCollector interface {
	ListInternalAccounts(ctx context.Context) ([]*controlplanev1.InternalAccountDefinition, error)
}

// OperationalGatewayCollector retrieves provider connections and routes from the live operational-gateway service.
type OperationalGatewayCollector interface {
	ListProviderConnections(ctx context.Context) ([]*controlplanev1.ProviderConnectionConfig, error)
	ListInstructionRoutes(ctx context.Context) ([]*controlplanev1.InstructionRouteConfig, error)
}

// ExportCollectors groups all collector interfaces for manifest export.
// All fields are optional — nil collectors result in fallback to the stored manifest
// for that section.
type ExportCollectors struct {
	Instruments        InstrumentCollector
	AccountTypes       AccountTypeCollector
	Sagas              SagaCollector
	MarketData         MarketDataCollector
	Organizations      OrganizationCollector
	InternalAccounts   InternalAccountCollector
	OperationalGateway OperationalGatewayCollector
}

// ExportService reconstructs manifests from live service state.
type ExportService struct {
	history    *HistoryService
	collectors *ExportCollectors
}

// NewExportService creates a new ExportService.
// history is required for fallback data; collectors may be nil (all sections fall back).
func NewExportService(history *HistoryService, collectors *ExportCollectors) (*ExportService, error) {
	if history == nil {
		return nil, ErrNilRepository
	}
	if collectors == nil {
		collectors = &ExportCollectors{}
	}
	return &ExportService{
		history:    history,
		collectors: collectors,
	}, nil
}

// ExportResult holds the output of an export operation.
type ExportResult struct {
	Manifest       *controlplanev1.Manifest
	ExportedAt     time.Time
	Checksum       string
	SectionSources map[string]string
	Warnings       []string
}

// Export reconstructs a manifest from live service state.
// includeSections filters which sections to include (empty means all).
// manifestVersion specifies which stored manifest to use for fallback data.
func (s *ExportService) Export(ctx context.Context, includeSections []string, manifestVersion string) (*ExportResult, error) {
	// Determine which sections to include.
	sections := parseSections(includeSections)

	// Load fallback manifest from history.
	fallback, fallbackVersion, err := s.loadFallbackManifest(ctx, manifestVersion)
	if err != nil {
		return nil, fmt.Errorf("load fallback manifest: %w", err)
	}

	result := &ExportResult{
		Manifest:       &controlplanev1.Manifest{},
		ExportedAt:     time.Now().UTC(),
		SectionSources: make(map[string]string),
	}

	// Always include version and metadata from fallback.
	if fallback != nil {
		result.Manifest.Version = fallback.Version
		result.Manifest.Metadata = fallback.Metadata
	} else {
		result.Manifest.Version = "1.0"
		result.Manifest.Metadata = &controlplanev1.ManifestMetadata{
			Name:        "exported",
			Description: "Manifest reconstructed from live service state",
		}
	}

	// Collect each section.
	if sections[SectionInstruments] {
		s.collectInstruments(ctx, result, fallback, fallbackVersion)
	}
	if sections[SectionAccountTypes] {
		s.collectAccountTypes(ctx, result, fallback, fallbackVersion)
	}
	if sections[SectionValuationRules] {
		s.collectValuationRules(result, fallback, fallbackVersion)
	}
	if sections[SectionSagas] {
		s.collectSagas(ctx, result, fallback, fallbackVersion)
	}
	if sections[SectionMarketData] {
		s.collectMarketData(ctx, result, fallback, fallbackVersion)
	}
	if sections[SectionOrganizations] {
		s.collectOrganizations(ctx, result, fallback, fallbackVersion)
	}
	if sections[SectionInternalAccounts] {
		s.collectInternalAccounts(ctx, result, fallback, fallbackVersion)
	}
	if sections[SectionOperationalGateway] {
		s.collectOperationalGateway(ctx, result, fallback, fallbackVersion)
	}
	if sections[SectionMappings] {
		s.collectMappings(result, fallback, fallbackVersion)
	}
	if sections[SectionPartyTypes] {
		s.collectPartyTypes(result, fallback, fallbackVersion)
	}
	if sections[SectionPaymentRails] {
		s.collectPaymentRails(result, fallback, fallbackVersion)
	}

	// Compute checksum.
	checksum, err := manifestChecksum(result.Manifest)
	if err != nil {
		return nil, fmt.Errorf("compute checksum: %w", err)
	}
	result.Checksum = checksum

	return result, nil
}

// parseSections converts include_sections strings to a set of SectionNames.
// Returns all sections if the input is empty.
func parseSections(include []string) map[SectionName]bool {
	if len(include) == 0 {
		// Return all sections.
		result := make(map[SectionName]bool, len(allSections))
		for k, v := range allSections {
			result[k] = v
		}
		return result
	}
	result := make(map[SectionName]bool, len(include))
	for _, s := range include {
		name := SectionName(s)
		if allSections[name] {
			result[name] = true
		}
	}
	return result
}

// loadFallbackManifest loads the manifest used as fallback for sections without live collectors.
func (s *ExportService) loadFallbackManifest(ctx context.Context, version string) (*controlplanev1.Manifest, string, error) {
	var entity *VersionEntity
	var err error

	if version != "" {
		entity, err = s.history.GetManifestVersion(ctx, version)
	} else {
		entity, err = s.history.GetCurrentManifest(ctx)
	}

	if err != nil {
		if isNotFound(err) {
			return nil, "", nil
		}
		return nil, "", err
	}

	manifest, err := unmarshalManifest(entity.ManifestJSON)
	if err != nil {
		return nil, "", fmt.Errorf("unmarshal fallback manifest: %w", err)
	}
	return manifest, entity.Version, nil
}

// isNotFound checks if an error is a not-found condition.
func isNotFound(err error) bool {
	return err != nil && errors.Is(err, ErrVersionNotFound)
}

func (s *ExportService) collectInstruments(ctx context.Context, result *ExportResult, fallback *controlplanev1.Manifest, fallbackVersion string) {
	if s.collectors.Instruments != nil {
		instruments, err := s.collectors.Instruments.ListInstruments(ctx)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("instruments: failed to query live state: %v", err))
			if fallback != nil {
				result.Manifest.Instruments = fallback.Instruments
				result.SectionSources["instruments"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
			}
			return
		}
		result.Manifest.Instruments = instruments
		result.SectionSources["instruments"] = "live:reference-data"
		return
	}
	if fallback != nil {
		result.Manifest.Instruments = fallback.Instruments
		result.SectionSources["instruments"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
	}
}

func (s *ExportService) collectAccountTypes(ctx context.Context, result *ExportResult, fallback *controlplanev1.Manifest, fallbackVersion string) {
	if s.collectors.AccountTypes != nil {
		accountTypes, err := s.collectors.AccountTypes.ListAccountTypes(ctx)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("account_types: failed to query live state: %v", err))
			if fallback != nil {
				result.Manifest.AccountTypes = fallback.AccountTypes
				result.SectionSources["account_types"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
			}
			return
		}
		result.Manifest.AccountTypes = accountTypes
		result.SectionSources["account_types"] = "live:reference-data"
		return
	}
	if fallback != nil {
		result.Manifest.AccountTypes = fallback.AccountTypes
		result.SectionSources["account_types"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
	}
}

func (s *ExportService) collectValuationRules(result *ExportResult, fallback *controlplanev1.Manifest, fallbackVersion string) {
	// Valuation rules have no dedicated live service — always from fallback.
	if fallback != nil {
		result.Manifest.ValuationRules = fallback.ValuationRules
		result.SectionSources["valuation_rules"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
	}
}

func (s *ExportService) collectSagas(ctx context.Context, result *ExportResult, fallback *controlplanev1.Manifest, fallbackVersion string) {
	if s.collectors.Sagas != nil {
		sagas, err := s.collectors.Sagas.ListSagas(ctx)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("sagas: failed to query live state: %v", err))
			if fallback != nil {
				result.Manifest.Sagas = fallback.Sagas
				result.SectionSources["sagas"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
			}
			return
		}
		result.Manifest.Sagas = sagas
		result.SectionSources["sagas"] = "live:saga-registry"
		return
	}
	if fallback != nil {
		result.Manifest.Sagas = fallback.Sagas
		result.SectionSources["sagas"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
	}
}

func (s *ExportService) collectMarketData(ctx context.Context, result *ExportResult, fallback *controlplanev1.Manifest, fallbackVersion string) {
	if s.collectors.MarketData != nil {
		sources, srcErr := s.collectors.MarketData.ListMarketDataSources(ctx)
		datasets, dsErr := s.collectors.MarketData.ListMarketDataSets(ctx)

		if srcErr != nil || dsErr != nil {
			var warning string
			if srcErr != nil {
				warning = fmt.Sprintf("market_data.sources: %v", srcErr)
			}
			if dsErr != nil {
				if warning != "" {
					warning += "; "
				}
				warning += fmt.Sprintf("market_data.datasets: %v", dsErr)
			}
			result.Warnings = append(result.Warnings, warning)
			if fallback != nil {
				result.Manifest.MarketData = fallback.MarketData
				result.SectionSources["market_data"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
			}
			return
		}

		result.Manifest.MarketData = &controlplanev1.MarketDataConfig{
			Sources:  sources,
			Datasets: datasets,
		}
		result.SectionSources["market_data"] = "live:market-information"
		return
	}
	if fallback != nil {
		result.Manifest.MarketData = fallback.MarketData
		result.SectionSources["market_data"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
	}
}

func (s *ExportService) collectOrganizations(ctx context.Context, result *ExportResult, fallback *controlplanev1.Manifest, fallbackVersion string) {
	if s.collectors.Organizations != nil {
		orgs, err := s.collectors.Organizations.ListOrganizations(ctx)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("organizations: failed to query live state: %v", err))
			if fallback != nil {
				result.Manifest.Organizations = fallback.Organizations
				result.SectionSources["organizations"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
			}
			return
		}
		result.Manifest.Organizations = orgs
		result.SectionSources["organizations"] = "live:party"
		return
	}
	if fallback != nil {
		result.Manifest.Organizations = fallback.Organizations
		result.SectionSources["organizations"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
	}
}

func (s *ExportService) collectInternalAccounts(ctx context.Context, result *ExportResult, fallback *controlplanev1.Manifest, fallbackVersion string) {
	if s.collectors.InternalAccounts != nil {
		accounts, err := s.collectors.InternalAccounts.ListInternalAccounts(ctx)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("internal_accounts: failed to query live state: %v", err))
			if fallback != nil {
				result.Manifest.InternalAccounts = fallback.InternalAccounts
				result.SectionSources["internal_accounts"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
			}
			return
		}
		result.Manifest.InternalAccounts = accounts
		result.SectionSources["internal_accounts"] = "live:internal-account"
		return
	}
	if fallback != nil {
		result.Manifest.InternalAccounts = fallback.InternalAccounts
		result.SectionSources["internal_accounts"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
	}
}

func (s *ExportService) collectOperationalGateway(ctx context.Context, result *ExportResult, fallback *controlplanev1.Manifest, fallbackVersion string) {
	if s.collectors.OperationalGateway != nil {
		connections, connErr := s.collectors.OperationalGateway.ListProviderConnections(ctx)
		routes, routeErr := s.collectors.OperationalGateway.ListInstructionRoutes(ctx)

		if connErr != nil || routeErr != nil {
			var warning string
			if connErr != nil {
				warning = fmt.Sprintf("operational_gateway.connections: %v", connErr)
			}
			if routeErr != nil {
				if warning != "" {
					warning += "; "
				}
				warning += fmt.Sprintf("operational_gateway.routes: %v", routeErr)
			}
			result.Warnings = append(result.Warnings, warning)
			if fallback != nil {
				result.Manifest.OperationalGateway = fallback.OperationalGateway
				result.SectionSources["operational_gateway"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
			}
			return
		}

		result.Manifest.OperationalGateway = &controlplanev1.OperationalGatewayConfig{
			ProviderConnections: connections,
			InstructionRoutes:   routes,
		}
		result.SectionSources["operational_gateway"] = "live:operational-gateway"
		return
	}
	if fallback != nil {
		result.Manifest.OperationalGateway = fallback.OperationalGateway
		result.SectionSources["operational_gateway"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
	}
}

func (s *ExportService) collectMappings(result *ExportResult, fallback *controlplanev1.Manifest, fallbackVersion string) {
	// Mappings have no dedicated live service — always from fallback.
	if fallback != nil {
		result.Manifest.Mappings = fallback.Mappings
		result.SectionSources["mappings"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
	}
}

func (s *ExportService) collectPartyTypes(result *ExportResult, fallback *controlplanev1.Manifest, fallbackVersion string) {
	// Party types — always from fallback for now.
	if fallback != nil {
		result.Manifest.PartyTypes = fallback.PartyTypes
		result.SectionSources["party_types"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
	}
}

func (s *ExportService) collectPaymentRails(result *ExportResult, fallback *controlplanev1.Manifest, fallbackVersion string) {
	// Payment rails have no dedicated live service — always from fallback.
	if fallback != nil {
		result.Manifest.PaymentRails = fallback.PaymentRails
		result.SectionSources["payment_rails"] = fmt.Sprintf("fallback:manifest-v%s", fallbackVersion)
	}
}

// manifestChecksum computes a SHA-256 checksum of a Manifest proto.
func manifestChecksum(m *controlplanev1.Manifest) (string, error) {
	marshaler := protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: false,
	}
	b, err := marshaler.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("marshal manifest for checksum: %w", err)
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum), nil
}

// ToProtoResponse converts an ExportResult to the gRPC response.
func (r *ExportResult) ToProtoResponse() *controlplanev1.ExportManifestResponse {
	return &controlplanev1.ExportManifestResponse{
		Manifest:       r.Manifest,
		ExportedAt:     timestamppb.New(r.ExportedAt),
		Checksum:       r.Checksum,
		SectionSources: r.SectionSources,
		Warnings:       r.Warnings,
	}
}
