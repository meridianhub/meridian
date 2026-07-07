// Package service exposes the control-plane gRPC services for registration
// in the unified binary or standalone deployment.
package service

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/applier"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ/persistence"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/meridianhub/meridian/services/control-plane/internal/planner"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

// ApplyManifestServiceConfig holds the configuration for RegisterApplyManifestService.
type ApplyManifestServiceConfig struct {
	// Pool is the database connection pool (required).
	Pool *pgxpool.Pool

	// GormDB is the GORM handle for the control-plane database. When provided,
	// manifest history is recorded through the versioned HistoryService, giving
	// each apply a monotonically increasing sequence number with optimistic
	// concurrency control. When nil, history recording is disabled (suitable for
	// validate/dry-run-only deployments) and the legacy version store is used
	// as the single persistence path.
	GormDB *gorm.DB

	// Logger is the structured logger. Defaults to slog.Default() if nil.
	Logger *slog.Logger

	// HandlerDeps provides service clients for saga handler execution.
	// When nil, the handler validates, diffs, and plans manifests but does
	// not execute them (suitable for lightweight deployments).
	HandlerDeps *applier.HandlerDependencies

	// GRPCConn is a gRPC connection to downstream services used to build
	// the live-state provider for convergent manifest diffs. When nil, the
	// differ falls back to stored-manifest comparison only.
	GRPCConn *grpc.ClientConn
}

// ErrPoolRequired is returned when Pool is nil during service registration.
var ErrPoolRequired = errors.New("apply manifest service: pool is required")

// RegisterApplyManifestService creates and registers the ApplyManifestService
// on the given gRPC server. It wires together the validator, differ, planner,
// and optionally an executor for saga-based manifest application.
//
// When cfg.HandlerDeps is nil, the handler validates, diffs, and plans manifests
// but does not execute them (suitable for lightweight deployments).
func RegisterApplyManifestService(server *grpc.Server, cfg ApplyManifestServiceConfig) error {
	if cfg.Pool == nil {
		return ErrPoolRequired
	}

	v, err := validator.New()
	if err != nil {
		return fmt.Errorf("manifest validator: %w", err)
	}

	versionStore := persistence.NewPostgresManifestVersionStore(cfg.Pool)

	var historyService *manifest.HistoryService
	if cfg.GormDB != nil {
		historyService, err = buildManifestHistoryService(cfg.GormDB)
		if err != nil {
			return err
		}
	}

	var liveState differ.LiveStateProvider
	if cfg.GRPCConn != nil {
		var lsErr error
		liveState, lsErr = differ.NewLiveStateClients(cfg.GRPCConn)
		if lsErr != nil {
			return fmt.Errorf("live state provider: %w", lsErr)
		}
	}
	d := differ.New(nil, nil, liveState)
	p := planner.NewManifestPlanner()

	var executor *applier.ManifestExecutor
	if cfg.HandlerDeps != nil {
		executor, err = applier.NewManifestExecutorFromDeps(applier.ManifestExecutorDepsConfig{
			Pool:   cfg.Pool,
			Deps:   cfg.HandlerDeps,
			Logger: cfg.Logger,
		})
		if err != nil {
			return fmt.Errorf("manifest executor: %w", err)
		}
	}

	handler, err := applier.NewApplyManifestHandler(applier.ApplyManifestHandlerConfig{
		Validator:      v,
		Differ:         d,
		Planner:        p,
		Executor:       executor,
		VersionStore:   versionStore,
		HistoryService: historyService,
		Logger:         cfg.Logger,
	})
	if err != nil {
		return fmt.Errorf("apply manifest handler: %w", err)
	}

	controlplanev1.RegisterApplyManifestServiceServer(server, handler)
	return nil
}

// buildManifestHistoryService wires the versioned history service when a GORM
// handle is available. It is the single source of truth for manifest
// persistence: sequence numbers are assigned atomically and optimistic-
// concurrency checks prevent concurrent applies from clobbering each other or
// producing duplicate sequence-0 rows. Callers guard on a non-nil gormDB.
func buildManifestHistoryService(gormDB *gorm.DB) (*manifest.HistoryService, error) {
	repo, err := manifest.NewRepository(gormDB)
	if err != nil {
		return nil, fmt.Errorf("manifest repository: %w", err)
	}
	historyService, err := manifest.NewHistoryService(repo)
	if err != nil {
		return nil, fmt.Errorf("manifest history service: %w", err)
	}
	return historyService, nil
}

// NewHandlerDeps builds HandlerDependencies from a raw gRPC connection that can
// reach all downstream services (reference-data, internal-account, operational-gateway,
// market-information, party, saga-registry). In the unified binary, a single loopback
// connection suffices because all services share one gRPC server.
func NewHandlerDeps(conn *grpc.ClientConn) *applier.HandlerDependencies {
	return &applier.HandlerDependencies{
		ReferenceData:      applier.NewReferenceDataClient(conn, conn),
		InternalAccount:    applier.NewInternalAccountClient(conn),
		OperationalGateway: applier.NewOperationalGatewayClient(conn),
		MarketInformation:  applier.NewMarketInformationClient(conn),
		Party:              applier.NewPartyClient(conn),
	}
}
