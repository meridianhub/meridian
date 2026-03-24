package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meridianhub/meridian/internal/migrations"
	faclient "github.com/meridianhub/meridian/services/financial-accounting/client"
	financialaccountingservice "github.com/meridianhub/meridian/services/financial-accounting/service"
	misclient "github.com/meridianhub/meridian/services/market-information/client"
	partyclient "github.com/meridianhub/meridian/services/party/client"
	pkclient "github.com/meridianhub/meridian/services/position-keeping/client"
	tenantprovisioner "github.com/meridianhub/meridian/services/tenant/provisioner"

	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/gorm"
)

// ─── Per-Service Database Connections ────────────────────────────────────────

// DeriveProvisionerConfig returns a provisioner config with each service's DatabaseURL
// derived from baseDSN by replacing the database component. If baseDSN is empty,
// DefaultConfig() is returned unchanged (preserving env-var fallback behavior).
func DeriveProvisionerConfig(baseDSN string) (*tenantprovisioner.Config, error) {
	config := tenantprovisioner.DefaultConfig()
	if baseDSN == "" {
		return config, nil
	}
	for i := range config.Services {
		svc := &config.Services[i]
		if sdb, ok := migrations.ServiceDatabases[svc.Name]; ok {
			dsn, err := replaceDSNDatabase(baseDSN, sdb.Database)
			if err != nil {
				return nil, fmt.Errorf("derive DSN for %s: %w", svc.Name, err)
			}
			svc.DatabaseURL = dsn
		}
	}
	return config, nil
}

// replaceDSNDatabase replaces the database name in a PostgreSQL/CockroachDB DSN URL.
func replaceDSNDatabase(baseDSN, database string) (string, error) {
	parsed, err := url.Parse(baseDSN)
	if err != nil {
		return "", fmt.Errorf("parse base DSN: %w", err)
	}
	parsed.Path = "/" + database
	return parsed.String(), nil
}

// serviceConns holds per-database connections for the unified binary.
// Services sharing the same database (e.g., tenant and control-plane both use
// meridian_platform) share the same connection object.
type serviceConns struct {
	gormDBs  map[string]*gorm.DB
	pgxPools map[string]*pgxpool.Pool
}

// gormDB returns the GORM connection for the given service's database.
// Panics with a descriptive message if serviceName is not in ServiceDatabases.
func (c *serviceConns) gormDB(serviceName string) *gorm.DB {
	sdb, ok := migrations.ServiceDatabases[serviceName]
	if !ok {
		panic(fmt.Sprintf("unknown service %q: not found in ServiceDatabases", serviceName))
	}
	return c.gormDBs[sdb.Database]
}

// pgxPool returns the pgxpool connection for the given service's database.
// Panics with a descriptive message if serviceName is not in ServiceDatabases.
func (c *serviceConns) pgxPool(serviceName string) *pgxpool.Pool {
	sdb, ok := migrations.ServiceDatabases[serviceName]
	if !ok {
		panic(fmt.Sprintf("unknown service %q: not found in ServiceDatabases", serviceName))
	}
	return c.pgxPools[sdb.Database]
}

// closeAll closes all database connections.
func (c *serviceConns) closeAll(logger *slog.Logger) {
	for name, db := range c.gormDBs {
		bootstrap.CloseDatabase(db, logger)
		logger.Info("closed GORM connection", "database", name)
	}
	for name, pool := range c.pgxPools {
		pool.Close()
		logger.Info("closed pgxpool connection", "database", name)
	}
}

// newServiceConns creates per-database GORM and pgxpool connections for all services
// defined in the ServiceDatabases map. The baseDSN provides the host, port, and
// credentials; the database name is replaced for each service's target database.
func newServiceConns(ctx context.Context, baseDSN string, logger *slog.Logger) (*serviceConns, error) {
	conns := &serviceConns{
		gormDBs:  make(map[string]*gorm.DB),
		pgxPools: make(map[string]*pgxpool.Pool),
	}

	// Services requiring GORM connections.
	gormServices := []string{
		"party", "tenant", "internal-account",
		"financial-accounting", "current-account",
		"payment-order", "reconciliation", "identity",
	}

	// Services requiring pgxpool connections.
	pgxServices := []string{
		"control-plane", "reference-data", "market-information",
		"position-keeping", "forecasting",
	}

	// Create GORM connections (deduplicated by database name).
	for _, svc := range gormServices {
		sdb := migrations.ServiceDatabases[svc]
		if _, exists := conns.gormDBs[sdb.Database]; exists {
			continue
		}
		dsn, err := replaceDSNDatabase(baseDSN, sdb.Database)
		if err != nil {
			conns.closeAll(logger)
			return nil, fmt.Errorf("dsn for %s: %w", sdb.Database, err)
		}
		cfg := bootstrap.DatabaseConfig{
			DSN:             dsn,
			MaxOpenConns:    10,
			MaxIdleConns:    2,
			ConnMaxLifetime: 5 * time.Minute,
			ConnMaxIdleTime: 10 * time.Minute,
			Logger:          logger,
		}
		db, err := bootstrap.NewDatabase(ctx, cfg)
		if err != nil {
			conns.closeAll(logger)
			return nil, fmt.Errorf("gorm %s: %w", sdb.Database, err)
		}
		conns.gormDBs[sdb.Database] = db
	}

	// Create pgxpool connections (deduplicated by database name).
	for _, svc := range pgxServices {
		sdb := migrations.ServiceDatabases[svc]
		if _, exists := conns.pgxPools[sdb.Database]; exists {
			continue
		}
		dsn, err := replaceDSNDatabase(baseDSN, sdb.Database)
		if err != nil {
			conns.closeAll(logger)
			return nil, fmt.Errorf("dsn for %s: %w", sdb.Database, err)
		}
		pool, err := connectPgxPool(ctx, dsn, logger)
		if err != nil {
			conns.closeAll(logger)
			return nil, fmt.Errorf("pgxpool %s: %w", sdb.Database, err)
		}
		conns.pgxPools[sdb.Database] = pool
	}

	logger.Info("per-service database connections established",
		"gorm_databases", len(conns.gormDBs),
		"pgxpool_databases", len(conns.pgxPools))

	return conns, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// noopFAPublisher is a no-op event publisher for financial-accounting (no Kafka in dev).
type noopFAPublisher struct{}

func (p *noopFAPublisher) Publish(_ context.Context, _ financialaccountingservice.DomainEvent) error {
	return nil
}

func (p *noopFAPublisher) PublishBatch(_ context.Context, _ []financialaccountingservice.DomainEvent) error {
	return nil
}

// ─── Loopback gRPC Clients ──────────────────────────────────────────────────

// loopbackClients holds gRPC clients that connect back to the same unified binary
// for inter-service communication (e.g., current-account calling position-keeping).
type loopbackClients struct {
	mds        *misclient.Client
	mdsClose   func()
	pk         *pkclient.Client
	pkClose    func()
	fa         *faclient.Client
	faClose    func()
	party      *partyclient.Client
	partyClose func()
	// rawConn is a plain gRPC connection to the loopback server, used by
	// control-plane handler deps that create proto clients directly.
	rawConn      *grpc.ClientConn
	rawConnClose func()
}

// newLoopbackClients creates loopback gRPC clients targeting the local gRPC server.
// grpc.NewClient is lazy — it connects on first RPC, after the server is listening.
// When svcCreds is non-nil, bearer tokens are attached to every outbound RPC.
func newLoopbackClients(ctx context.Context, grpcPort int, svcCreds *platformauth.ServiceCredentials) (*loopbackClients, error) {
	target := fmt.Sprintf("localhost:%d", grpcPort)
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	if authOpt := platformauth.NewServiceCredentialsDialOption(svcCreds); authOpt != nil {
		opts = append(opts, authOpt)
	}

	mds, mdsClose, err := misclient.New(ctx, misclient.Config{Target: target, DialOptions: opts})
	if err != nil {
		return nil, fmt.Errorf("mds loopback: %w", err)
	}

	pk, pkClose, err := pkclient.New(ctx, pkclient.Config{Target: target, DialOptions: opts})
	if err != nil {
		_ = mdsClose()
		return nil, fmt.Errorf("pk loopback: %w", err)
	}

	fa, faClose, err := faclient.New(ctx, faclient.Config{Target: target, DialOptions: opts})
	if err != nil {
		_ = mdsClose()
		pkClose()
		return nil, fmt.Errorf("fa loopback: %w", err)
	}

	party, partyClose, err := partyclient.New(ctx, partyclient.Config{Target: target, DialOptions: opts})
	if err != nil {
		_ = mdsClose()
		pkClose()
		faClose()
		return nil, fmt.Errorf("party loopback: %w", err)
	}

	// Raw gRPC connection for control-plane handler deps (reference-data, internal-account, etc.)
	rawConn, err := grpc.NewClient(target, opts...)
	if err != nil {
		_ = mdsClose()
		pkClose()
		faClose()
		partyClose()
		return nil, fmt.Errorf("raw loopback: %w", err)
	}

	return &loopbackClients{
		mds: mds, mdsClose: func() { _ = mdsClose() },
		pk: pk, pkClose: pkClose,
		fa: fa, faClose: faClose,
		party: party, partyClose: partyClose,
		rawConn: rawConn, rawConnClose: func() { _ = rawConn.Close() },
	}, nil
}

// closeAll closes all loopback gRPC connections.
func (l *loopbackClients) closeAll() {
	if l.mdsClose != nil {
		l.mdsClose()
	}
	if l.pkClose != nil {
		l.pkClose()
	}
	if l.faClose != nil {
		l.faClose()
	}
	if l.partyClose != nil {
		l.partyClose()
	}
	if l.rawConnClose != nil {
		l.rawConnClose()
	}
}
