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
	"github.com/meridianhub/meridian/services/control-plane/internal/planner"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
	"google.golang.org/grpc"
)

// ApplyManifestServiceConfig holds the configuration for RegisterApplyManifestService.
type ApplyManifestServiceConfig struct {
	// Pool is the database connection pool (required).
	Pool *pgxpool.Pool

	// Logger is the structured logger. Defaults to slog.Default() if nil.
	Logger *slog.Logger

	// HandlerDeps provides service clients for saga handler execution.
	// When nil, the handler validates, diffs, and plans manifests but does
	// not execute them (suitable for lightweight deployments).
	HandlerDeps *applier.HandlerDependencies
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
	d := differ.New(nil, nil) // NoOp safety checker and drift detector
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
		Validator:    v,
		Differ:       d,
		Planner:      p,
		Executor:     executor,
		VersionStore: versionStore,
		Logger:       cfg.Logger,
	})
	if err != nil {
		return fmt.Errorf("apply manifest handler: %w", err)
	}

	controlplanev1.RegisterApplyManifestServiceServer(server, handler)
	return nil
}
