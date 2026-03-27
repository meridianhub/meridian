package main

import (
	"log/slog"

	identityv1 "github.com/meridianhub/meridian/api/proto/meridian/identity/v1"
	identitypersistence "github.com/meridianhub/meridian/services/identity/adapters/persistence"
	identityservice "github.com/meridianhub/meridian/services/identity/service"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/platform/env"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

// wireIdentity wires the identity gRPC service into the shared server.
func wireIdentity(server *grpc.Server, db *gorm.DB, logger *slog.Logger) error {
	repo := identitypersistence.NewRepository(db)
	baseURL := "https://" + env.GetEnvOrDefault("BASE_DOMAIN", "app.meridianhub.cloud")
	svc, err := identityservice.NewService(repo, logger,
		identityservice.WithEmailOutbox(email.NewPostgresOutboxRepository(db)),
		identityservice.WithBaseURL(baseURL),
	)
	if err != nil {
		return err
	}
	identityv1.RegisterIdentityServiceServer(server, svc)
	logger.Info("registered identity service")
	return nil
}
