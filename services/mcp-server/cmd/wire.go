// Package main provides the wiring layer that connects gRPC clients to the
// MCP server's tool registry, resource provider, and prompt registry.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"google.golang.org/protobuf/encoding/protojson"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	mcpauth "github.com/meridianhub/meridian/services/mcp-server/internal/auth"
	"github.com/meridianhub/meridian/services/mcp-server/internal/clients"
	"github.com/meridianhub/meridian/services/mcp-server/internal/prompts"
	"github.com/meridianhub/meridian/services/mcp-server/internal/resources"
	"github.com/meridianhub/meridian/services/mcp-server/internal/server"
	"github.com/meridianhub/meridian/services/mcp-server/internal/session"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
)

// wireServer registers all MCP tools, resources, and prompts onto srv.
// If the Meridian backend (MERIDIAN_API_URL / MERIDIAN_API_KEY) is configured,
// it creates gRPC clients and wires all remote tools. If not, only local tools
// (validation, prompts, embedded docs) are registered.
//
// Returns a cleanup function to close the gRPC connection (nil when no
// connection was established).
func wireServer(srv *server.MCPServer, logger *slog.Logger) (func(), error) {
	// Prompts are always available (no external deps).
	srv.SetPromptRegistry(prompts.NewRegistry())

	// Tool registry collects all tools, then bridges them to the server.
	toolReg := tools.NewRegistry()

	// Validation tools use local CEL/Starlark libraries — no gRPC needed.
	if err := tools.RegisterValidationTools(toolReg); err != nil {
		return nil, fmt.Errorf("register validation tools: %w", err)
	}

	// Try to connect to the Meridian backend for remote tools.
	var cleanup func()

	authCfg, err := mcpauth.LoadFromEnv()
	if err != nil {
		logger.Warn("Meridian backend not configured — only local tools available", "error", err)
		bridgeToolsToServer(srv, toolReg)
		// Resource provider with nil manifest client returns a placeholder.
		srv.SetResourceProvider(resources.New(nil))
		return nil, nil //nolint:nilnil // partial availability is intentional
	}

	mc, err := clients.New(authCfg)
	if err != nil {
		logger.Warn("failed to create gRPC clients — only local tools available", "error", err)
		bridgeToolsToServer(srv, toolReg)
		srv.SetResourceProvider(resources.New(nil))
		return nil, nil //nolint:nilnil // partial availability is intentional
	}
	cleanup = func() { _ = mc.Close() }

	logger.Info("gRPC clients connected", "target", authCfg.APIUrl)

	// -- Resource provider (live manifest) --
	srv.SetResourceProvider(resources.New(&manifestResourceAdapter{c: mc.ManifestHistory}))

	// -- Reference data tools --
	mhAdapter := manifestHistoryAdapter{c: mc.ManifestHistory}
	rdAdapter := referenceDataAdapter{c: mc.ReferenceData}
	srAdapter := sagaRegistryAdapter{c: mc.SagaRegistry}
	miAdapter := marketInfoAdapter{c: mc.MarketInfo}

	if err := tools.RegisterReferenceDataTools(toolReg, tools.ReferenceDataDeps{
		ManifestHistory:   mhAdapter,
		ReferenceData:     rdAdapter,
		SagaRegistry:      srAdapter,
		MarketInformation: miAdapter,
	}); err != nil {
		logger.Warn("failed to register reference data tools", "error", err)
	}

	// -- Audit tools --
	tools.RegisterAuditTools(toolReg, tools.AuditClients{
		SagaAdmin:           sagaAdminAdapter{c: mc.SagaAdmin},
		PositionKeeping:     positionKeepingAdapter{c: mc.PositionKeeping},
		FinancialAccounting: postingAdapter{c: mc.Accounting},
		SagaRegistry:        srAdapter,
		Reconciliation:      reconciliationAdapter{c: mc.Reconciliation},
	})

	// -- Economy tools (manifest plan/apply/history) --
	sess := session.NewDefault()
	tools.RegisterEconomyTools(toolReg, sess, tools.EconomyDeps{
		Applier:   applyManifestAdapter{c: mc.ApplyManifest},
		Historian: mhAdapter,
	})

	// -- Simulation tools --
	// CELEvaluator, ManifestDiffer, ValuationSimulator, and SagaSimulator
	// require dedicated implementations that don't exist yet. Tools with
	// nil deps are silently skipped — they'll light up once implemented.
	tools.RegisterSimulationTools(toolReg, tools.SimulationDeps{})

	// Bridge all registered tools to the server.
	bridgeToolsToServer(srv, toolReg)

	return cleanup, nil
}

// bridgeToolsToServer iterates tools from the registry and registers each
// with the server, adapting the tools.ToolHandler signature to server.ToolHandler.
func bridgeToolsToServer(srv *server.MCPServer, reg *tools.Registry) {
	for _, t := range reg.List() {
		name := t.Name // capture for closure
		srv.RegisterTool(
			server.Tool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			},
			toolHandlerFor(reg, name),
		)
	}
}

// toolHandlerFor returns a server.ToolHandler that delegates to the registry.
// Tool-level errors (validation failures, gRPC errors) are returned as MCP
// error content blocks rather than Go errors, following MCP convention.
func toolHandlerFor(reg *tools.Registry, name string) server.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (*server.ToolCallResult, error) {
		result, callErr := reg.Call(ctx, name, args)
		if callErr != nil {
			// MCP convention: tool errors are returned as content blocks, not Go errors.
			// The MCP client displays the error text to the user/LLM.
			return toolErrorResult(callErr.Error()), nil
		}
		data, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("marshal tool result: %w", err)
		}
		return &server.ToolCallResult{
			Content: []server.ContentBlock{{Type: "text", Text: string(data)}},
		}, nil
	}
}

// toolErrorResult builds an MCP error content block from a message string.
func toolErrorResult(msg string) *server.ToolCallResult {
	return &server.ToolCallResult{
		Content: []server.ContentBlock{{Type: "text", Text: msg}},
		IsError: true,
	}
}

// ---------------------------------------------------------------------------
// gRPC → tool interface adapters
//
// The generated gRPC clients accept ...grpc.CallOption as the last parameter,
// but tool interfaces use a simpler (ctx, req) → (resp, error) signature.
// These thin adapters bridge the gap.
// ---------------------------------------------------------------------------

// manifestHistoryAdapter satisfies tools.ManifestHistoryClient and tools.ManifestHistorian.
type manifestHistoryAdapter struct {
	c controlplanev1.ManifestHistoryServiceClient
}

func (a manifestHistoryAdapter) GetCurrentManifest(ctx context.Context, req *controlplanev1.GetCurrentManifestRequest) (*controlplanev1.GetCurrentManifestResponse, error) {
	return a.c.GetCurrentManifest(ctx, req)
}

func (a manifestHistoryAdapter) ListManifestVersions(ctx context.Context, req *controlplanev1.ListManifestVersionsRequest) (*controlplanev1.ListManifestVersionsResponse, error) {
	return a.c.ListManifestVersions(ctx, req)
}

// referenceDataAdapter satisfies tools.ReferenceDataClient.
type referenceDataAdapter struct {
	c referencedatav1.ReferenceDataServiceClient
}

func (a referenceDataAdapter) ListInstruments(ctx context.Context, req *referencedatav1.ListInstrumentsRequest) (*referencedatav1.ListInstrumentsResponse, error) {
	return a.c.ListInstruments(ctx, req)
}

func (a referenceDataAdapter) RetrieveInstrument(ctx context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
	return a.c.RetrieveInstrument(ctx, req)
}

// sagaRegistryAdapter satisfies tools.SagaRegistryClient and tools.SagaExecutionQuerier.
type sagaRegistryAdapter struct {
	c sagav1.SagaRegistryServiceClient
}

func (a sagaRegistryAdapter) ListSagas(ctx context.Context, req *sagav1.ListSagasRequest) (*sagav1.ListSagasResponse, error) {
	return a.c.ListSagas(ctx, req)
}

func (a sagaRegistryAdapter) GetSaga(ctx context.Context, req *sagav1.GetSagaRequest) (*sagav1.GetSagaResponse, error) {
	return a.c.GetSaga(ctx, req)
}

// marketInfoAdapter satisfies tools.MarketInformationClient.
type marketInfoAdapter struct {
	c marketinformationv1.MarketInformationServiceClient
}

func (a marketInfoAdapter) ListDataSets(ctx context.Context, req *marketinformationv1.ListDataSetsRequest) (*marketinformationv1.ListDataSetsResponse, error) {
	return a.c.ListDataSets(ctx, req)
}

func (a marketInfoAdapter) ListObservations(ctx context.Context, req *marketinformationv1.ListObservationsRequest) (*marketinformationv1.ListObservationsResponse, error) {
	return a.c.ListObservations(ctx, req)
}

// sagaAdminAdapter satisfies tools.SagaAdminQuerier.
type sagaAdminAdapter struct {
	c sagav1.SagaAdminServiceClient
}

func (a sagaAdminAdapter) GetCausationTree(ctx context.Context, req *sagav1.GetCausationTreeRequest) (*sagav1.GetCausationTreeResponse, error) {
	return a.c.GetCausationTree(ctx, req)
}

// positionKeepingAdapter satisfies tools.PositionQuerier.
type positionKeepingAdapter struct {
	c positionkeepingv1.PositionKeepingServiceClient
}

func (a positionKeepingAdapter) ListFinancialPositionLogs(ctx context.Context, req *positionkeepingv1.ListFinancialPositionLogsRequest) (*positionkeepingv1.ListFinancialPositionLogsResponse, error) {
	return a.c.ListFinancialPositionLogs(ctx, req)
}

// postingAdapter satisfies tools.PostingQuerier.
type postingAdapter struct {
	c financialaccountingv1.FinancialAccountingServiceClient
}

func (a postingAdapter) ListLedgerPostings(ctx context.Context, req *financialaccountingv1.ListLedgerPostingsRequest) (*financialaccountingv1.ListLedgerPostingsResponse, error) {
	return a.c.ListLedgerPostings(ctx, req)
}

// reconciliationAdapter satisfies tools.ReconciliationQuerier.
type reconciliationAdapter struct {
	c reconciliationv1.AccountReconciliationServiceClient
}

func (a reconciliationAdapter) ListAccountReconciliations(ctx context.Context, req *reconciliationv1.ListAccountReconciliationsRequest) (*reconciliationv1.ListAccountReconciliationsResponse, error) {
	return a.c.ListAccountReconciliations(ctx, req)
}

func (a reconciliationAdapter) ListReconciliationResults(ctx context.Context, req *reconciliationv1.ListReconciliationResultsRequest) (*reconciliationv1.ListReconciliationResultsResponse, error) {
	return a.c.ListReconciliationResults(ctx, req)
}

// applyManifestAdapter satisfies tools.ManifestApplier.
type applyManifestAdapter struct {
	c controlplanev1.ApplyManifestServiceClient
}

func (a applyManifestAdapter) ApplyManifest(ctx context.Context, req *controlplanev1.ApplyManifestRequest) (*controlplanev1.ApplyManifestResponse, error) {
	return a.c.ApplyManifest(ctx, req)
}

// manifestResourceAdapter satisfies resources.ManifestClient.
// It wraps the gRPC ManifestHistoryServiceClient to produce a JSON representation
// of the current manifest (the resource MIME type is text/yaml but the content is
// JSON — LLM clients handle both formats equally well).
type manifestResourceAdapter struct {
	c controlplanev1.ManifestHistoryServiceClient
}

func (a *manifestResourceAdapter) GetCurrentManifestYAML(ctx context.Context) (string, error) {
	resp, err := a.c.GetCurrentManifest(ctx, &controlplanev1.GetCurrentManifestRequest{})
	if err != nil {
		return "", err
	}
	if resp.GetVersion() == nil || resp.GetVersion().GetManifest() == nil {
		return "# No manifest applied\n", nil
	}
	opts := protojson.MarshalOptions{Multiline: true, Indent: "  "}
	data, err := opts.Marshal(resp.GetVersion().GetManifest())
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}
	return string(data), nil
}
