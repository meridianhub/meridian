package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	forecastingv1 "github.com/meridianhub/meridian/api/proto/meridian/forecasting/v1"
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
	auditservice "github.com/meridianhub/meridian/services/audit-worker/service"
	controlplaneservice "github.com/meridianhub/meridian/services/control-plane/service"
	currentaccountpersistence "github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	caconfig "github.com/meridianhub/meridian/services/current-account/config"
	currentaccountservice "github.com/meridianhub/meridian/services/current-account/service"
	financialaccountingpersistence "github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	financialaccountingservice "github.com/meridianhub/meridian/services/financial-accounting/service"
	forecastingmds "github.com/meridianhub/meridian/services/forecasting/adapters/mds"
	forecastingpersistence "github.com/meridianhub/meridian/services/forecasting/adapters/persistence"
	forecastinghandler "github.com/meridianhub/meridian/services/forecasting/handler"
	forecastingstarlark "github.com/meridianhub/meridian/services/forecasting/starlark"
	internalaccountpersistence "github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	internalaccountservice "github.com/meridianhub/meridian/services/internal-account/service"
	marketinformationpersistence "github.com/meridianhub/meridian/services/market-information/adapters/persistence"
	misclient "github.com/meridianhub/meridian/services/market-information/client"
	marketinformationservice "github.com/meridianhub/meridian/services/market-information/service"
	partypersistence "github.com/meridianhub/meridian/services/party/adapters/persistence"
	partyservice "github.com/meridianhub/meridian/services/party/service"
	paymentorderpersistence "github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	paymentorderservice "github.com/meridianhub/meridian/services/payment-order/service"
	pkmessaging "github.com/meridianhub/meridian/services/position-keeping/adapters/messaging"
	pkpersistence "github.com/meridianhub/meridian/services/position-keeping/adapters/persistence"
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

	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/observability"

	"google.golang.org/grpc"
	"gorm.io/gorm"

	faclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	partyclient "github.com/meridianhub/meridian/services/party/client"
	pkclient "github.com/meridianhub/meridian/services/position-keeping/client"
)

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

func wireTenant(server *grpc.Server, db *gorm.DB, prov tenantprovisioner.SchemaProvisioner, logger *slog.Logger) error {
	repo := tenantpersistence.NewRepository(db)
	svc := tenantservice.NewService(repo, prov, nil, nil, logger)
	tenantv1.RegisterTenantServiceServer(server, svc)
	logger.Info("registered tenant service", "provisioner_enabled", prov != nil)
	return nil
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

	// Load clearing account config from env vars (optional - deposits work
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

	// Wire notification.send saga handler so dunning sagas can enqueue emails.
	// Uses the current-account database for the email outbox (each service owns its outbox).
	caEmailOutboxRepo := email.NewPostgresOutboxRepository(db)
	emailResolver := newGRPCPartyEmailResolver(partyLoopback.Conn())
	prefRepo := email.NewPostgresPreferenceRepository(db)
	prefEnforcer := email.NewPreferenceEnforcer(prefRepo, email.DefaultTemplateCategoryMap(), logger.With("component", "preference-enforcer"))
	notifHandler := email.NewNotificationSendHandler(email.NotificationHandlerDeps{
		Outbox:             caEmailOutboxRepo,
		EmailResolver:      emailResolver,
		PreferenceEnforcer: prefEnforcer,
		Logger:             logger.With("component", "notification-handler"),
	})
	caOpts = append(caOpts, currentaccountservice.WithNotificationSagaHandler(notifHandler))

	svc, err := currentaccountservice.NewServiceWithExistingClients(
		repo, lienRepo, withdrawalRepo,
		outboxRepo, db,
		pkLoopback, faLoopback,
		partyWrapper,
		acctCfg,
		idempotencySvc, logger, tracer,
		nil, // accountResolver - no dynamic clearing account resolution
		nil, // fungibilityValidator - not needed for demo
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

func wireAudit(ctx context.Context, server *grpc.Server, db *gorm.DB, logger *slog.Logger) (*audit.MultiTenantWorker, error) {
	svc, err := auditservice.NewAuditService(db, logger)
	if err != nil {
		return nil, err
	}
	auditv1.RegisterAuditServiceServer(server, svc)

	// Start multi-tenant audit worker that discovers tenant schemas dynamically
	// and processes audit_outbox -> audit_log within each tenant schema.
	mtWorker := audit.NewMultiTenantWorker(db, "org_%", logger,
		audit.WithAdaptivePolling(100*time.Millisecond, 30*time.Second),
	)
	mtWorker.Start(ctx)

	logger.Info("registered audit service and started multi-tenant audit worker")
	return mtWorker, nil
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
		GRPCConn:    loopback.rawConn,
	}); err != nil {
		return err
	}
	logger.Info("registered control-plane service (ApplyManifestService)")

	// Ensure the apply_manifest saga definition is seeded in public.platform_saga_definition.
	// Uses the control-plane pool intentionally: this seeds a platform-level table distinct
	// from the tenant-scoped saga_definition table served by SagaRegistryService (reference-data).
	// The control-plane has its own database (meridian_control_plane).
	if err := controlplaneservice.EnsurePlatformSaga(ctx, pool); err != nil {
		logger.Warn("failed to seed platform saga (non-fatal, --bootstrap will retry)", "error", err)
	}

	// Register ManifestHistoryService with loopback applier for rollback support.
	applyClient := controlplanev1.NewApplyManifestServiceClient(loopback.rawConn)
	if err := controlplaneservice.RegisterManifestHistoryService(server, controlplaneservice.ManifestHistoryServiceConfig{
		DB:      db,
		Logger:  logger,
		Applier: loopbackApplyManifestAdapter{c: applyClient},
	}); err != nil {
		return err
	}
	logger.Info("registered control-plane service (ManifestHistoryService)")

	return nil
}
