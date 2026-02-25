// Package clients creates typed gRPC client stubs for all Meridian Core
// services and manages the underlying connection lifecycle.
package clients

import (
	"errors"
	"fmt"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/mcp-server/internal/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// ErrNilAuthConfig is returned when New is called with a nil auth.Config.
var ErrNilAuthConfig = errors.New("auth config must not be nil")

// MeridianClients holds typed gRPC clients for all Meridian Core services.
// All clients share a single connection to the gateway.
type MeridianClients struct {
	// ApplyManifest submits and validates tenant manifests.
	ApplyManifest controlplanev1.ApplyManifestServiceClient
	// ManifestHistory retrieves previously applied manifest versions.
	ManifestHistory controlplanev1.ManifestHistoryServiceClient
	// ReferenceData provides instrument and node metadata.
	ReferenceData referencedatav1.ReferenceDataServiceClient
	// PositionKeeping logs and queries financial positions.
	PositionKeeping positionkeepingv1.PositionKeepingServiceClient
	// Accounting records and queries financial accounting entries.
	Accounting financialaccountingv1.FinancialAccountingServiceClient
	// Reconciliation queries account reconciliation state.
	Reconciliation reconciliationv1.AccountReconciliationServiceClient
	// MarketInfo retrieves market price and rate data.
	MarketInfo marketinformationv1.MarketInformationServiceClient
	// Health allows the MCP server to check gateway liveness.
	Health grpc_health_v1.HealthClient

	// conn is the underlying connection; callers must call Close when done.
	conn *grpc.ClientConn
}

// New dials the Meridian gateway and returns fully-wired MeridianClients.
// The auth.Config interceptor is applied to every outgoing unary RPC so that
// each call carries the API key as a Bearer token.
//
// The caller is responsible for calling Close() when the clients are no longer
// needed to release the underlying connection.
func New(cfg *auth.Config) (*MeridianClients, error) {
	if cfg == nil {
		return nil, fmt.Errorf("clients.New: %w", ErrNilAuthConfig)
	}
	if cfg.APIUrl == "" {
		return nil, fmt.Errorf("clients.New: %w", auth.ErrMissingAPIURL)
	}

	// insecure.NewCredentials() is intentional: TLS termination is handled at the
	// infrastructure layer (Kubernetes service mesh / Envoy sidecar). The Bearer
	// token carried by the auth interceptor is protected by the mesh mTLS tunnel
	// between pods, consistent with every other service client in this repository.
	conn, err := grpc.NewClient(
		cfg.APIUrl,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(cfg.UnaryInterceptor()),
	)
	if err != nil {
		return nil, fmt.Errorf("clients.New: dial %s: %w", cfg.APIUrl, err)
	}

	return &MeridianClients{
		ApplyManifest:   controlplanev1.NewApplyManifestServiceClient(conn),
		ManifestHistory: controlplanev1.NewManifestHistoryServiceClient(conn),
		ReferenceData:   referencedatav1.NewReferenceDataServiceClient(conn),
		PositionKeeping: positionkeepingv1.NewPositionKeepingServiceClient(conn),
		Accounting:      financialaccountingv1.NewFinancialAccountingServiceClient(conn),
		Reconciliation:  reconciliationv1.NewAccountReconciliationServiceClient(conn),
		MarketInfo:      marketinformationv1.NewMarketInformationServiceClient(conn),
		Health:          grpc_health_v1.NewHealthClient(conn),
		conn:            conn,
	}, nil
}

// Close releases the underlying gRPC connection.
func (m *MeridianClients) Close() error {
	return m.conn.Close()
}
