// Package service exposes the control-plane gRPC services for registration
// in the unified binary or standalone deployment.
package service

import (
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

// RegisterApplyManifestService creates and registers the ApplyManifestService
// on the given gRPC server. It wires together the validator, differ, planner,
// and optionally an executor for saga-based manifest application.
//
// When executor is nil, the handler validates, diffs, and plans manifests but
// does not execute them (suitable for lightweight deployments).
func RegisterApplyManifestService(server *grpc.Server, pool *pgxpool.Pool, executor *applier.ManifestExecutor, logger *slog.Logger) error {
	v, err := validator.New()
	if err != nil {
		return fmt.Errorf("manifest validator: %w", err)
	}

	versionStore := persistence.NewPostgresManifestVersionStore(pool)
	d := differ.New(nil, nil) // NoOp safety checker and drift detector
	p := planner.NewManifestPlanner()

	handler, err := applier.NewApplyManifestHandler(applier.ApplyManifestHandlerConfig{
		Validator:    v,
		Differ:       d,
		Planner:      p,
		Executor:     executor,
		VersionStore: versionStore,
		Logger:       logger,
	})
	if err != nil {
		return fmt.Errorf("apply manifest handler: %w", err)
	}

	controlplanev1.RegisterApplyManifestServiceServer(server, handler)
	return nil
}
