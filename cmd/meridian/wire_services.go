package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	refhandler "github.com/meridianhub/meridian/services/reference-data/handler"
	refregistry "github.com/meridianhub/meridian/services/reference-data/registry"
	identitybootstrap "github.com/meridianhub/meridian/services/identity/bootstrap"
	identitypersistence "github.com/meridianhub/meridian/services/identity/adapters/persistence"
	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	tenantprovisioner "github.com/meridianhub/meridian/services/tenant/provisioner"
	tenantworker "github.com/meridianhub/meridian/services/tenant/worker"

	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/observability"

	pkdomain "github.com/meridianhub/meridian/services/position-keeping/domain"
	financialaccountingservice "github.com/meridianhub/meridian/services/financial-accounting/service"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"gorm.io/gorm"
)

// registerServices wires all gRPC services into the shared server in tier dependency order,
// then enables health checking and reflection.
func registerServices(
	ctx context.Context,
	grpcServer *grpc.Server,
	conns *serviceConns,
	idempotencySvc idempotency.Service,
	faEventPublisher financialaccountingservice.EventPublisher,
	pkEventPublisher pkdomain.EventPublisher,
	outboxPublisher *events.OutboxPublisher,
	outboxRepo *events.PostgresOutboxRepository,
	loopback *loopbackClients,
	schemaProvisioner tenantprovisioner.SchemaProvisioner,
	tracer *observability.Tracer,
	logger *slog.Logger,
) error {
	// refDataComps is populated by wireReferenceData and used by wireInternalAccount for the account type cache.
	var refDataComps *refDataComponents

	// Tier 0: No gRPC dependencies
	for _, wire := range []struct {
		name string
		fn   func() error
	}{
		{"party", func() error { return wireParty(grpcServer, conns.gormDB("party"), logger) }},
		{"reference-data", func() error {
			var err error
			refDataComps, err = wireReferenceData(grpcServer, conns.pgxPool("reference-data"), logger)
			return err
		}},
		{"market-information", func() error { return wireMarketInformation(grpcServer, conns.pgxPool("market-information"), logger) }},
		{"tenant", func() error { return wireTenant(grpcServer, conns.gormDB("tenant"), schemaProvisioner, logger) }},
		{"internal-account", func() error {
			return wireInternalAccount(grpcServer, conns.gormDB("internal-account"), refDataComps, logger)
		}},
		{"control-plane", func() error {
			return wireControlPlane(ctx, grpcServer, conns.pgxPool("control-plane"), conns.gormDB("tenant"), loopback, logger)
		}},
		{"audit", func() error { return wireAudit(grpcServer, conns.gormDB("tenant"), logger) }}, // audit uses platform DB
		{"identity", func() error { return wireIdentity(grpcServer, conns.gormDB("identity"), logger) }},
	} {
		if err := wire.fn(); err != nil {
			return fmt.Errorf("%s: %w", wire.name, err)
		}
	}
	logger.Info("Tier 0 services registered")

	// Run identity bootstrap: provisions the platform admin identity on first boot.
	// This is a no-op if the admin already exists or if env vars are not set.
	identityRepo := identitypersistence.NewRepository(conns.gormDB("identity"))
	if err := identitybootstrap.Run(ctx, identityRepo); err != nil {
		logger.Warn("identity bootstrap failed, service startup continues", "error", err)
	}

	// Seed demo users (operator, etc.) from environment variables.
	// No-op if env vars are not set. Idempotent on every boot.
	if err := identitybootstrap.SeedDemoUsers(ctx, identityRepo); err != nil {
		logger.Warn("demo user seeding failed, service startup continues", "error", err)
	}

	// Tier 1: Depend on Tier 0 via loopback
	for _, wire := range []struct {
		name string
		fn   func() error
	}{
		{"financial-accounting", func() error {
			return wireFinancialAccounting(grpcServer, conns.gormDB("financial-accounting"), idempotencySvc, faEventPublisher, outboxPublisher, outboxRepo, logger)
		}},
		{"position-keeping", func() error {
			return wirePositionKeeping(grpcServer, conns.pgxPool("position-keeping"), idempotencySvc, pkEventPublisher, logger)
		}},
		{"forecasting", func() error { return wireForecasting(grpcServer, conns.pgxPool("forecasting"), loopback.mds, logger) }},
	} {
		if err := wire.fn(); err != nil {
			return fmt.Errorf("%s: %w", wire.name, err)
		}
	}
	logger.Info("Tier 1 services registered")

	// Tier 2: Depend on Tier 1 (current-account needs PK, FA loopback clients for saga orchestration)
	caOutboxRepo := events.NewPostgresOutboxRepository(conns.gormDB("current-account"))
	if err := wireCurrentAccount(grpcServer, conns.gormDB("current-account"), loopback.pk, loopback.fa, loopback.party, idempotencySvc, caOutboxRepo, refDataComps, tracer, logger); err != nil {
		return fmt.Errorf("current-account: %w", err)
	}
	logger.Info("Tier 2 services registered")

	// Tier 3: Depend on Tier 2
	for _, wire := range []struct {
		name string
		fn   func() error
	}{
		{"payment-order", func() error {
			return wirePaymentOrder(grpcServer, conns.gormDB("payment-order"), idempotencySvc, logger)
		}},
		{"reconciliation", func() error { return wireReconciliation(grpcServer, conns.gormDB("reconciliation"), logger) }},
	} {
		if err := wire.fn(); err != nil {
			return fmt.Errorf("%s: %w", wire.name, err)
		}
	}
	logger.Info("Tier 3 services registered")

	// Health + Reflection
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	reflection.Register(grpcServer)

	return nil
}

// refDataComponents holds objects created during wireReferenceData that other
// services need (e.g., the account type cache loader for internal-account).
type refDataComponents struct {
	accountTypeRegistry *accounttype.PostgresRegistry
	celCompiler         *refcel.Compiler
	refDataService      *refhandler.Service
	instrumentRegistry  refregistry.InstrumentRegistry
}

// createSchemaProvisioner creates the provisioner when SCHEMA_PROVISIONING_ENABLED=true.
// Returns nil when provisioning is disabled.
func createSchemaProvisioner(baseDSN string, platformDB *gorm.DB, logger *slog.Logger) (*tenantprovisioner.PostgresProvisioner, error) {
	if env.GetEnvOrDefault("SCHEMA_PROVISIONING_ENABLED", "false") != "true" {
		logger.Info("schema provisioning disabled",
			"hint", "set SCHEMA_PROVISIONING_ENABLED=true to enable tenant schema isolation")
		return nil, nil //nolint:nilnil // nil provisioner is the expected "disabled" signal
	}

	config, err := DeriveProvisionerConfig(baseDSN)
	if err != nil {
		return nil, fmt.Errorf("provisioner config: %w", err)
	}
	prov, err := tenantprovisioner.NewPostgresProvisioner(platformDB, config)
	if err != nil {
		return nil, fmt.Errorf("create schema provisioner: %w", err)
	}
	logger.Info("schema provisioner initialized",
		"services", len(config.Services),
		"provisioning_timeout", config.ProvisioningTimeout)
	return prov, nil
}

// startProvisioningWorker starts the background worker using an initialized provisioner.
// When prov is nil (provisioning disabled) both return values are nil.
func startProvisioningWorker(ctx context.Context, prov *tenantprovisioner.PostgresProvisioner, platformDB *gorm.DB, identityDB *gorm.DB, logger *slog.Logger) (*tenantworker.ProvisioningWorker, func(), error) {
	if prov == nil {
		return nil, nil, nil
	}

	repo := tenantpersistence.NewRepository(platformDB)
	w, err := tenantworker.NewProvisioningWorker(
		repo,
		prov,
		tenantworker.Config{
			PollInterval:   env.GetEnvAsDuration("PROVISIONING_WORKER_POLL_INTERVAL", 10*time.Second),
			MaxRetries:     env.GetEnvAsInt("PROVISIONING_MAX_RETRIES", 5),
			RetryBaseDelay: env.GetEnvAsDuration("PROVISIONING_RETRY_BASE_DELAY", 2*time.Second),
			RetryMaxDelay:  env.GetEnvAsDuration("PROVISIONING_RETRY_MAX_DELAY", defaults.DefaultMaxRetryInterval),
			MaxConcurrent:  env.GetEnvAsInt("PROVISIONING_MAX_CONCURRENT", 5),
		},
		logger,
	)
	if err != nil {
		_ = prov.Close()
		return nil, nil, fmt.Errorf("create provisioning worker: %w", err)
	}

	identityRepo := identitypersistence.NewRepository(identityDB)
	w.RegisterPostProvisioningHook("admin-identity", identitybootstrap.AsPostProvisioningHook(identityRepo))

	go w.Start(ctx)
	logger.Info("provisioning worker started")

	cleanup := func() {
		if closeErr := prov.Close(); closeErr != nil {
			logger.Error("failed to close provisioner connections", "error", closeErr)
		}
	}

	return w, cleanup, nil
}
