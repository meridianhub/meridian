package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	identitypersistence "github.com/meridianhub/meridian/services/identity/adapters/persistence"
	identitybootstrap "github.com/meridianhub/meridian/services/identity/bootstrap"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	refhandler "github.com/meridianhub/meridian/services/reference-data/handler"
	refregistry "github.com/meridianhub/meridian/services/reference-data/registry"
	refsaga "github.com/meridianhub/meridian/services/reference-data/saga"
	refvaluation "github.com/meridianhub/meridian/services/reference-data/valuation"
	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	tenantprovisioner "github.com/meridianhub/meridian/services/tenant/provisioner"
	tenantworker "github.com/meridianhub/meridian/services/tenant/worker"

	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/observability"

	financialaccountingservice "github.com/meridianhub/meridian/services/financial-accounting/service"
	pkdomain "github.com/meridianhub/meridian/services/position-keeping/domain"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"gorm.io/gorm"
)

// registerServices wires all gRPC services into the shared server in tier dependency order,
// then enables health checking and reflection. It returns the multi-tenant audit worker
// (if started) so that the caller can stop it during graceful shutdown.
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
) (*audit.MultiTenantWorker, error) {
	// refDataComps is populated by wireReferenceData and used by wireInternalAccount for the account type cache.
	var refDataComps *refDataComponents

	// auditWorker is populated by wireAudit and stopped during graceful shutdown.
	var auditWorker *audit.MultiTenantWorker

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
			return wireControlPlane(ctx, grpcServer, conns.pgxPool("control-plane"), conns.gormDB("control-plane"), loopback, logger)
		}},
		{"audit", func() error {
			var err error
			auditWorker, err = wireAudit(ctx, grpcServer, conns.gormDB("party"), logger)
			return err
		}},
		{"identity", func() error { return wireIdentity(grpcServer, conns.gormDB("identity"), logger) }},
	} {
		if err := wire.fn(); err != nil {
			return nil, fmt.Errorf("%s: %w", wire.name, err)
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
			return nil, fmt.Errorf("%s: %w", wire.name, err)
		}
	}
	logger.Info("Tier 1 services registered")

	// Tier 2: Depend on Tier 1 (current-account needs PK, FA loopback clients for saga orchestration)
	caOutboxRepo := events.NewPostgresOutboxRepository(conns.gormDB("current-account"))
	if err := wireCurrentAccount(grpcServer, conns.gormDB("current-account"), loopback.pk, loopback.fa, loopback.party, idempotencySvc, caOutboxRepo, refDataComps, tracer, logger); err != nil { //nolint:contextcheck // saga.StarlarkContext embeds context.Context; handler receives context at runtime
		return nil, fmt.Errorf("current-account: %w", err)
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
			return nil, fmt.Errorf("%s: %w", wire.name, err)
		}
	}
	logger.Info("Tier 3 services registered")

	// Health + Reflection
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	reflection.Register(grpcServer)

	return auditWorker, nil
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
//
// Registers all post-provisioning hooks in dependency order:
//  1. admin-identity: Provisions platform admin identity
//  2. instruments: Seeds platform instrument definitions (GBP, USD, EUR, etc.)
//  3. saga-definitions: Seeds platform default saga scripts into tenant schema
//  4. account-type-blueprints: Seeds canonical account type blueprints
//  5. valuation-defaults: Seeds system valuation methods and policies
//
// All hooks are fail-hard: any failure prevents tenant activation.
func startProvisioningWorker(ctx context.Context, prov *tenantprovisioner.PostgresProvisioner, conns *serviceConns, logger *slog.Logger) (*tenantworker.ProvisioningWorker, func(), error) {
	if prov == nil {
		return nil, nil, nil
	}

	repo := tenantpersistence.NewRepository(conns.gormDB("tenant"))
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

	// Register post-provisioning hooks in dependency order.
	// All hooks are fail-hard: failure prevents tenant activation.
	identityRepo := identitypersistence.NewRepository(conns.gormDB("identity"))
	w.RegisterPostProvisioningHook("admin-identity", identitybootstrap.AsPostProvisioningHook(identityRepo))

	refDataPool := conns.pgxPool("reference-data")
	instrumentSeeder := refregistry.NewInstrumentSeeder(refDataPool)
	w.RegisterPostProvisioningHook("instruments", instrumentSeeder.AsPostProvisioningHook())

	sagaSeeder := refsaga.NewSeeder(refDataPool)
	w.RegisterPostProvisioningHook("saga-definitions", sagaSeeder.AsPostProvisioningHook())

	accountTypeRegistry, atErr := accounttype.NewPostgresRegistry(refDataPool)
	if atErr != nil {
		_ = prov.Close()
		return nil, nil, fmt.Errorf("account type registry: %w", atErr)
	}
	blueprintSeeder := accounttype.NewBlueprintSeeder(accountTypeRegistry)
	w.RegisterPostProvisioningHook("account-type-blueprints", blueprintSeeder.AsPostProvisioningHook())

	valuationSeeder := refvaluation.NewSeeder(refDataPool)
	w.RegisterPostProvisioningHook("valuation-defaults", valuationSeeder.AsPostProvisioningHook())

	// Self-registered admin: creates the admin identity from registration metadata
	// stored by the registration handler. Must run after all reference data hooks
	// so the identity schema has instruments, account types, etc. available.
	selfRegHook, hookErr := identitybootstrap.NewSelfRegisteredAdminHook(identityRepo, repo, logger)
	if hookErr != nil {
		_ = prov.Close()
		return nil, nil, fmt.Errorf("self-registered admin hook: %w", hookErr)
	}
	w.RegisterPostProvisioningHook("self-registered-admin", selfRegHook.AsPostProvisioningHook())

	go w.Start(ctx)
	logger.Info("provisioning worker started")

	cleanup := func() {
		if closeErr := prov.Close(); closeErr != nil {
			logger.Error("failed to close provisioner connections", "error", closeErr)
		}
	}

	return w, cleanup, nil
}
