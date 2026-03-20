package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	// Proto registrations
	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	forecastingv1 "github.com/meridianhub/meridian/api/proto/meridian/forecasting/v1"
	identityv1 "github.com/meridianhub/meridian/api/proto/meridian/identity/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	mappingv1 "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	paymentorderv1 "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	sagav1 "github.com/meridianhub/meridian/api/proto/meridian/saga/v1"
	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"

	// Service packages
	auditservice "github.com/meridianhub/meridian/services/audit-worker/service"
	controlplaneservice "github.com/meridianhub/meridian/services/control-plane/service"
	currentaccountpersistence "github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	caconfig "github.com/meridianhub/meridian/services/current-account/config"
	currentaccountservice "github.com/meridianhub/meridian/services/current-account/service"
	financialaccountingpersistence "github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	faclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	financialaccountingservice "github.com/meridianhub/meridian/services/financial-accounting/service"
	forecastingmds "github.com/meridianhub/meridian/services/forecasting/adapters/mds"
	forecastingpersistence "github.com/meridianhub/meridian/services/forecasting/adapters/persistence"
	forecastinghandler "github.com/meridianhub/meridian/services/forecasting/handler"
	forecastingstarlark "github.com/meridianhub/meridian/services/forecasting/starlark"
	identitypersistence "github.com/meridianhub/meridian/services/identity/adapters/persistence"
	identitybootstrap "github.com/meridianhub/meridian/services/identity/bootstrap"
	identityconnector "github.com/meridianhub/meridian/services/identity/connector"
	identitydex "github.com/meridianhub/meridian/services/identity/dex"
	identityservice "github.com/meridianhub/meridian/services/identity/service"
	internalaccountpersistence "github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	internalaccountservice "github.com/meridianhub/meridian/services/internal-account/service"
	marketinformationpersistence "github.com/meridianhub/meridian/services/market-information/adapters/persistence"
	misclient "github.com/meridianhub/meridian/services/market-information/client"
	marketinformationservice "github.com/meridianhub/meridian/services/market-information/service"
	partypersistence "github.com/meridianhub/meridian/services/party/adapters/persistence"
	partyclient "github.com/meridianhub/meridian/services/party/client"
	partyservice "github.com/meridianhub/meridian/services/party/service"
	paymentorderpersistence "github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	paymentorderservice "github.com/meridianhub/meridian/services/payment-order/service"
	pkmessaging "github.com/meridianhub/meridian/services/position-keeping/adapters/messaging"
	pkpersistence "github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
	pkclient "github.com/meridianhub/meridian/services/position-keeping/client"
	pkdomain "github.com/meridianhub/meridian/services/position-keeping/domain"
	positionkeepingservice "github.com/meridianhub/meridian/services/position-keeping/service"
	reconciliationpersistence "github.com/meridianhub/meridian/services/reconciliation/adapters/persistence"
	reconciliationservice "github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	refcache "github.com/meridianhub/meridian/services/reference-data/cache"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	refhandler "github.com/meridianhub/meridian/services/reference-data/handler"
	refmapping "github.com/meridianhub/meridian/services/reference-data/mapping"
	refnode "github.com/meridianhub/meridian/services/reference-data/node"
	refregistry "github.com/meridianhub/meridian/services/reference-data/registry"
	refsaga "github.com/meridianhub/meridian/services/reference-data/saga"
	tenantpersistence "github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	tenantprovisioner "github.com/meridianhub/meridian/services/tenant/provisioner"
	tenantservice "github.com/meridianhub/meridian/services/tenant/service"
	tenantworker "github.com/meridianhub/meridian/services/tenant/worker"

	// Gateway
	gateway "github.com/meridianhub/meridian/services/api-gateway"

	// Shared platform
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/observability"

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
		{"tenant", func() error { return wireTenant(grpcServer, conns.gormDB("tenant"), logger) }},
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

// ─── Tier 0 Wiring ──────────────────────────────────────────────────────────

func wireParty(server *grpc.Server, db *gorm.DB, logger *slog.Logger) error {
	repo := partypersistence.NewRepository(db)
	svc, err := partyservice.NewService(repo, logger)
	if err != nil {
		return err
	}
	partyv1.RegisterPartyServiceServer(server, svc)
	logger.Info("registered party service")
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

func wireReferenceData(server *grpc.Server, pool *pgxpool.Pool, logger *slog.Logger) (*refDataComponents, error) {
	instrumentRegistry, err := refregistry.NewPostgresRegistry(pool)
	if err != nil {
		return nil, fmt.Errorf("instrument registry: %w", err)
	}

	compiler, err := refcel.NewCompiler()
	if err != nil {
		return nil, fmt.Errorf("CEL compiler: %w", err)
	}

	refDataSvc, err := refhandler.NewService(instrumentRegistry, compiler, logger)
	if err != nil {
		return nil, fmt.Errorf("ref data service: %w", err)
	}

	nodeRepo := refnode.NewPostgresRepository(pool)
	nodeSvc, err := refhandler.NewNodeService(nodeRepo, logger)
	if err != nil {
		return nil, fmt.Errorf("node service: %w", err)
	}

	sagaRegistry := refsaga.NewPostgresRegistry(pool, nil)
	sagaSvc := refsaga.NewRegistryHandler(sagaRegistry, nil, nil, logger)

	// Account type registry and gRPC service
	acctTypeReg, err := accounttype.NewPostgresRegistry(pool)
	if err != nil {
		return nil, fmt.Errorf("account type registry: %w", err)
	}

	acctTypeSvc, err := refhandler.NewAccountTypeService(acctTypeReg, instrumentRegistry, compiler, logger)
	if err != nil {
		return nil, fmt.Errorf("account type service: %w", err)
	}

	mappingRepo := refmapping.NewPostgresRepository(pool)
	mappingValidator, err := refmapping.NewValidator(compiler)
	if err != nil {
		return nil, fmt.Errorf("mapping validator: %w", err)
	}
	mappingSvc, err := refhandler.NewMappingService(mappingRepo, mappingValidator, logger)
	if err != nil {
		return nil, fmt.Errorf("mapping service: %w", err)
	}

	referencedatav1.RegisterReferenceDataServiceServer(server, refDataSvc)
	referencedatav1.RegisterNodeServiceServer(server, nodeSvc)
	referencedatav1.RegisterAccountTypeRegistryServiceServer(server, acctTypeSvc)
	sagav1.RegisterSagaRegistryServiceServer(server, sagaSvc)
	mappingv1.RegisterMappingServiceServer(server, mappingSvc)
	logger.Info("registered reference-data service")
	return &refDataComponents{
		accountTypeRegistry: acctTypeReg,
		celCompiler:         compiler,
		refDataService:      refDataSvc,
		instrumentRegistry:  instrumentRegistry,
	}, nil
}

func wireMarketInformation(server *grpc.Server, pool *pgxpool.Pool, logger *slog.Logger) error {
	masterTenantID := env.GetEnvOrDefault("MASTER_TENANT_ID", "meridian_master")
	repos := marketinformationpersistence.NewRepositories(pool, masterTenantID)

	miServer, err := marketinformationservice.NewServer(
		repos.DataSet,
		repos.Observation,
		repos.Source,
		marketinformationservice.WithLogger(logger),
	)
	if err != nil {
		return err
	}

	marketinformationv1.RegisterMarketInformationServiceServer(server, miServer)
	logger.Info("registered market-information service")
	return nil
}

func wireTenant(server *grpc.Server, db *gorm.DB, logger *slog.Logger) error {
	repo := tenantpersistence.NewRepository(db)
	svc := tenantservice.NewService(repo, nil, nil, nil, logger)
	tenantv1.RegisterTenantServiceServer(server, svc)
	logger.Info("registered tenant service")
	return nil
}

// startProvisioningWorker initializes the tenant schema provisioning worker when
// SCHEMA_PROVISIONING_ENABLED=true. It returns the worker (for graceful Stop)
// and a cleanup function that closes provisioner database connections.
// When provisioning is disabled both return values are nil.
func startProvisioningWorker(ctx context.Context, baseDSN string, platformDB *gorm.DB, identityDB *gorm.DB, logger *slog.Logger) (*tenantworker.ProvisioningWorker, func(), error) {
	if env.GetEnvOrDefault("SCHEMA_PROVISIONING_ENABLED", "false") != "true" {
		logger.Info("provisioning worker disabled",
			"hint", "set SCHEMA_PROVISIONING_ENABLED=true to enable background provisioning")
		return nil, nil, nil
	}

	// Create provisioner config and derive service DSNs from the resolved baseDSN.
	// DefaultConfig() falls back to cockroachdb:26257 when per-service env vars
	// are unset. The unified binary derives all connections from a single base DSN,
	// so we override each service's DatabaseURL to match.
	config, err := DeriveProvisionerConfig(baseDSN)
	if err != nil {
		return nil, nil, fmt.Errorf("provisioner config: %w", err)
	}
	prov, err := tenantprovisioner.NewPostgresProvisioner(platformDB, config)
	if err != nil {
		return nil, nil, fmt.Errorf("create schema provisioner: %w", err)
	}

	logger.Info("schema provisioner initialized",
		"services", len(config.Services),
		"provisioning_timeout", config.ProvisioningTimeout)

	// Build worker config from environment (mirrors standalone tenant service).
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
		// Close provisioner connections on worker creation failure.
		_ = prov.Close()
		return nil, nil, fmt.Errorf("create provisioning worker: %w", err)
	}

	// Register post-provisioning hooks before starting the worker.
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

// registryAccountTypeLoader adapts accounttype.PostgresRegistry to cache.AccountTypeLoader.
type registryAccountTypeLoader struct {
	registry *accounttype.PostgresRegistry
}

func (l *registryAccountTypeLoader) LoadAccountType(ctx context.Context, code string) (*accounttype.Definition, error) {
	return l.registry.GetActiveDefinition(ctx, code)
}

func (l *registryAccountTypeLoader) ListActiveAccountTypes(ctx context.Context) ([]*accounttype.Definition, error) {
	return l.registry.ListActive(ctx)
}

// inProcessRefDataClient adapts the in-process reference-data gRPC handler
// to the ReferenceDataClient interface used by internal-account service.
type inProcessRefDataClient struct {
	svc *refhandler.Service
}

func (c *inProcessRefDataClient) RetrieveInstrument(ctx context.Context, req *referencedatav1.RetrieveInstrumentRequest) (*referencedatav1.RetrieveInstrumentResponse, error) {
	return c.svc.RetrieveInstrument(ctx, req)
}

func (c *inProcessRefDataClient) Close() error { return nil }

// inProcessInstrumentGetter adapts the in-process instrument registry to
// the InstrumentGetter interface used by the current-account service for
// dimension resolution during account creation.
type inProcessInstrumentGetter struct {
	registry refregistry.InstrumentRegistry
}

func (g *inProcessInstrumentGetter) GetInstrument(ctx context.Context, code string, version int) (*refcache.CachedInstrument, error) {
	var def *refregistry.InstrumentDefinition
	var err error
	if version > 0 {
		def, err = g.registry.GetDefinition(ctx, code, version)
	} else {
		def, err = g.registry.GetActiveDefinition(ctx, code)
	}
	if err != nil {
		return nil, err
	}
	return &refcache.CachedInstrument{Definition: def}, nil
}

func wireInternalAccount(server *grpc.Server, db *gorm.DB, refData *refDataComponents, logger *slog.Logger) error {
	repo := internalaccountpersistence.NewRepository(db)

	var opts []internalaccountservice.Option
	var refDataClient internalaccountservice.ReferenceDataClient

	// Wire account type cache and reference data client if components are available
	if refData != nil {
		loader := &registryAccountTypeLoader{registry: refData.accountTypeRegistry}
		cache := refcache.NewLocalAccountTypeCache(loader, refData.celCompiler)
		opts = append(opts, internalaccountservice.WithAccountTypeCache(cache))

		if refData.refDataService != nil {
			refDataClient = &inProcessRefDataClient{svc: refData.refDataService}
		}
	}

	svc, err := internalaccountservice.NewServiceFull(repo, nil, refDataClient, logger, nil, opts...)
	if err != nil {
		return err
	}
	internalaccountv1.RegisterInternalAccountServiceServer(server, svc)
	logger.Info("registered internal-account service")
	return nil
}

// ─── Tier 1 Wiring ──────────────────────────────────────────────────────────

func wireFinancialAccounting(
	server *grpc.Server,
	db *gorm.DB,
	idempotencySvc idempotency.Service,
	eventPub financialaccountingservice.EventPublisher,
	outboxPub *events.OutboxPublisher,
	outboxRepo *events.PostgresOutboxRepository,
	logger *slog.Logger,
) error {
	svc, err := financialaccountingservice.NewFinancialAccountingService(
		financialaccountingpersistence.NewLedgerRepository(db),
		eventPub,
		idempotencySvc,
		outboxPub,
		outboxRepo,
	)
	if err != nil {
		return err
	}

	financialaccountingv1.RegisterFinancialAccountingServiceServer(server, svc)
	logger.Info("registered financial-accounting service")
	return nil
}

func wirePositionKeeping(
	server *grpc.Server,
	pool *pgxpool.Pool,
	idempotencySvc idempotency.Service,
	eventPub pkdomain.EventPublisher,
	logger *slog.Logger,
) error {
	outboxRepo := events.NewPgxOutboxRepository(pool)
	outboxPub, err := pkmessaging.NewOutboxEventPublisher(outboxRepo)
	if err != nil {
		return fmt.Errorf("failed to create position-keeping outbox publisher: %w", err)
	}

	svc, err := positionkeepingservice.NewPositionKeepingService(
		pkpersistence.NewPostgresRepository(pool),
		pkpersistence.NewMeasurementRepository(pool),
		eventPub,
		idempotencySvc,
		outboxPub,
	)
	if err != nil {
		return err
	}

	positionkeepingv1.RegisterPositionKeepingServiceServer(server, svc)
	logger.Info("registered position-keeping service")
	return nil
}

func wireForecasting(server *grpc.Server, pool *pgxpool.Pool, mdsClient *misclient.Client, logger *slog.Logger) error {
	repo := forecastingpersistence.NewStrategyRepository(pool)

	misAdapter := forecastingmds.NewMISAdapter(mdsClient)
	mdsPublisher := forecastingmds.NewPublisherAdapter(mdsClient)

	runner, err := forecastingstarlark.NewForecastRunner(forecastingstarlark.ForecastRunnerConfig{
		MISClient: misAdapter,
		RefData:   &forecastingmds.NoOpRefDataClient{},
		Logger:    logger,
	})
	if err != nil {
		return fmt.Errorf("forecast runner: %w", err)
	}

	svc, err := forecastinghandler.NewService(repo, runner, mdsPublisher, logger)
	if err != nil {
		return err
	}

	forecastingv1.RegisterForecastingServiceServer(server, svc)
	logger.Info("registered forecasting service")
	return nil
}

// ─── Tier 2 Wiring ──────────────────────────────────────────────────────────

func wireCurrentAccount(
	server *grpc.Server,
	db *gorm.DB,
	pkLoopback *pkclient.Client,
	faLoopback *faclient.Client,
	partyLoopback *partyclient.Client,
	idempotencySvc idempotency.Service,
	outboxRepo *events.PostgresOutboxRepository,
	refData *refDataComponents,
	tracer *observability.Tracer,
	logger *slog.Logger,
) error {
	repo := currentaccountpersistence.NewRepository(db)
	lienRepo := currentaccountpersistence.NewLienRepository(db)
	withdrawalRepo := currentaccountpersistence.NewWithdrawalRepository(db)

	partyWrapper := newPartyClientWrapper(partyLoopback)

	// Load clearing account config from env vars (optional — deposits work
	// without it but produce single-sided postings which FA may reject).
	var acctCfg *caconfig.AccountConfig
	if os.Getenv("DEPOSIT_CLEARING_ACCOUNT_ID") != "" {
		var err error
		acctCfg, err = caconfig.LoadAccountConfig()
		if err != nil {
			return fmt.Errorf("invalid clearing account config: %w", err)
		}
	} else {
		logger.Info("no clearing account config, deposits will skip debit posting")
	}

	var caOpts []currentaccountservice.Option

	// Wire in-process instrument getter for dimension resolution during account creation
	if refData != nil && refData.instrumentRegistry != nil {
		caOpts = append(caOpts, currentaccountservice.WithInstrumentGetter(
			&inProcessInstrumentGetter{registry: refData.instrumentRegistry}))
	}

	svc, err := currentaccountservice.NewServiceWithExistingClients(
		repo, lienRepo, withdrawalRepo,
		outboxRepo, db,
		pkLoopback, faLoopback,
		partyWrapper,
		acctCfg,
		idempotencySvc, logger, tracer,
		nil, // accountResolver — no dynamic clearing account resolution
		nil, // fungibilityValidator — not needed for demo
		caOpts...,
	)
	if err != nil {
		return err
	}

	currentaccountv1.RegisterCurrentAccountServiceServer(server, svc)
	logger.Info("registered current-account service")
	return nil
}

// ─── Tier 3 Wiring ──────────────────────────────────────────────────────────

func wirePaymentOrder(
	server *grpc.Server,
	db *gorm.DB,
	idempotencySvc idempotency.Service,
	logger *slog.Logger,
) error {
	repo := paymentorderpersistence.NewPaymentOrderRepository(db)
	svc, err := paymentorderservice.NewService(repo, idempotencySvc)
	if err != nil {
		return err
	}

	paymentorderv1.RegisterPaymentOrderServiceServer(server, svc)
	logger.Info("registered payment-order service")
	return nil
}

func wireReconciliation(
	server *grpc.Server,
	db *gorm.DB,
	logger *slog.Logger,
) error {
	svc := reconciliationservice.NewAccountReconciliationService(
		reconciliationservice.WithSettlementRunRepository(
			reconciliationpersistence.NewSettlementRunRepository(db),
		),
		reconciliationservice.WithDisputeRepository(
			reconciliationpersistence.NewDisputeRepository(db),
		),
		reconciliationservice.WithVarianceRepository(
			reconciliationpersistence.NewVarianceRepository(db),
		),
		reconciliationservice.WithVarianceListRepository(
			reconciliationpersistence.NewVarianceRepository(db),
		),
		reconciliationservice.WithLogger(logger),
	)

	reconciliationv1.RegisterAccountReconciliationServiceServer(server, svc)
	logger.Info("registered reconciliation service")
	return nil
}

// ─── Audit Wiring ────────────────────────────────────────────────────────────

func wireAudit(server *grpc.Server, db *gorm.DB, logger *slog.Logger) error {
	svc, err := auditservice.NewAuditService(db, logger)
	if err != nil {
		return err
	}
	auditv1.RegisterAuditServiceServer(server, svc)
	logger.Info("registered audit service")
	return nil
}

// ─── Identity Wiring ─────────────────────────────────────────────────────────

func wireIdentity(server *grpc.Server, db *gorm.DB, logger *slog.Logger) error {
	repo := identitypersistence.NewRepository(db)
	svc, err := identityservice.NewService(repo, logger)
	if err != nil {
		return err
	}
	identityv1.RegisterIdentityServiceServer(server, svc)
	logger.Info("registered identity service")
	return nil
}

// wireEmbeddedDex creates the embedded Dex OIDC server and returns a gateway
// ServerOption that mounts its HTTP handler at /dex/*. When DEX_ISSUER is not
// set, returns a no-op option (Dex is opt-in).
func wireEmbeddedDex(ctx context.Context, identityDB *gorm.DB, logger *slog.Logger) (gateway.ServerOption, error) {
	noopOption := func(*gateway.Server) {}

	issuer := os.Getenv("DEX_ISSUER")
	if issuer == "" {
		logger.Info("DEX_ISSUER not set, embedded Dex disabled")
		return noopOption, nil
	}

	baseDomain := env.GetEnvOrDefault("DEX_BASE_DOMAIN", env.GetEnvOrDefault("BASE_DOMAIN", "localhost"))

	// Create identity repository and connector.
	repo := identitypersistence.NewRepository(identityDB)
	conn, err := identityconnector.New(repo, logger)
	if err != nil {
		return nil, fmt.Errorf("dex connector: %w", err)
	}

	// Build client list: default demo client + MCP OAuth callback.
	clients := []identitydex.ClientConfig{identitydex.DefaultDemoClient(baseDomain)}

	// Create embedded Dex.
	embedded, err := identitydex.New(ctx, identitydex.Config{
		Issuer:    issuer,
		Connector: conn,
		Logger:    logger,
		Clients:   clients,
	})
	if err != nil {
		return nil, fmt.Errorf("embedded dex: %w", err)
	}

	// Start the Dex OIDC HTTP server (creates signing keys, mounts endpoints).
	if err := embedded.StartServer(ctx, issuer, true); err != nil {
		return nil, fmt.Errorf("dex server start: %w", err)
	}

	logger.Info("embedded Dex wired", "issuer", issuer, "base_domain", baseDomain)
	return gateway.WithDexHandler(embedded.Handler()), nil
}

// wireBFFAuth creates the BFF auth handler for direct password login.
// The handler validates credentials via the identity connector and signs
// JWTs with Meridian's own RSA key. This bypasses Dex for password auth.
//
// Environment variables:
//   - JWT_SIGNING_KEY: RSA private key in PEM format (auto-generated if unset)
//   - JWT_SIGNING_KEY_ID: kid header value (default: "meridian-1")
//   - JWT_SIGNING_ISSUER: iss claim value (default: "meridian")
//   - JWT_TOKEN_TTL: token lifetime (default: "1h")
func wireBFFAuth(identityDB *gorm.DB, logger *slog.Logger) (*platformauth.JWTSigner, []gateway.ServerOption) {
	signer, err := platformauth.NewJWTSigner(platformauth.JWTSignerConfig{
		PrivateKeyFile: os.Getenv("JWT_SIGNING_KEY_FILE"),
		PrivateKeyPEM:  os.Getenv("JWT_SIGNING_KEY"),
		KeyID:          env.GetEnvOrDefault("JWT_SIGNING_KEY_ID", "meridian-1"),
		Issuer:         env.GetEnvOrDefault("JWT_SIGNING_ISSUER", "meridian"),
	})
	if err != nil {
		logger.Error("failed to create JWT signer, BFF auth disabled", "error", err)
		return nil, nil
	}

	identityRepo := identitypersistence.NewRepository(identityDB)
	conn, err := identityconnector.New(identityRepo, logger)
	if err != nil {
		logger.Error("failed to create identity connector, BFF auth disabled", "error", err)
		return nil, nil
	}

	tokenTTL := env.GetEnvAsDuration("JWT_TOKEN_TTL", time.Hour)

	handler, err := gateway.NewAuthHandler(gateway.AuthHandlerConfig{
		Connector: conn,
		Signer:    signer,
		TokenTTL:  tokenTTL,
		Logger:    logger,
	})
	if err != nil {
		logger.Error("failed to create auth handler, BFF auth disabled", "error", err)
		return nil, nil
	}

	logger.Info("BFF auth handler initialized",
		"issuer", signer.Issuer(),
		"key_id", signer.KeyID(),
		"token_ttl", tokenTTL)

	return signer, []gateway.ServerOption{gateway.WithAuthHandler(handler)}
}

// ─── Control Plane Wiring ────────────────────────────────────────────────────

func wireControlPlane(ctx context.Context, server *grpc.Server, pool *pgxpool.Pool, db *gorm.DB, loopback *loopbackClients, logger *slog.Logger) error {
	// Build handler dependencies from loopback connections.
	// All downstream services are accessible via the shared loopback connection.
	deps := controlplaneservice.NewHandlerDeps(loopback.rawConn)

	if err := controlplaneservice.RegisterApplyManifestService(server, controlplaneservice.ApplyManifestServiceConfig{
		Pool:        pool,
		Logger:      logger,
		HandlerDeps: deps,
	}); err != nil {
		return err
	}
	logger.Info("registered control-plane service (ApplyManifestService)")

	// Ensure the apply_manifest saga definition is seeded in the platform table.
	// This is idempotent and runs on every startup so the saga is always available
	// even when --bootstrap was not invoked (e.g., E2E and local dev).
	if err := controlplaneservice.EnsurePlatformSaga(ctx, pool); err != nil {
		logger.Warn("failed to seed platform saga (non-fatal, --bootstrap will retry)", "error", err)
	}

	// Register ManifestHistoryService for manifest version history queries.
	if err := controlplaneservice.RegisterManifestHistoryService(server, controlplaneservice.ManifestHistoryServiceConfig{
		DB:     db,
		Logger: logger,
	}); err != nil {
		return err
	}
	logger.Info("registered control-plane service (ManifestHistoryService)")

	return nil
}
