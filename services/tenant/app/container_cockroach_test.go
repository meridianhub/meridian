package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// setupCockroachDBURL starts a CockroachDB testcontainer and returns a DSN
// suitable for use as the DATABASE_URL environment variable.
func setupCockroachDBURL(t *testing.T) string {
	t.Helper()

	container, cleanup := testdb.StartCockroachContainer(t, "tenant_test_db")
	t.Cleanup(cleanup)

	return testdb.CockroachDSN(t, container)
}

// configureMinimalContainerEnv sets environment toggles so NewContainer takes
// the fast/no-op paths: schema provisioning off, party client off, Redis off,
// auth disabled, and tracing disabled. Only the real CockroachDB connection
// (via DATABASE_URL) is exercised.
func configureMinimalContainerEnv(t *testing.T, dbURL string) {
	t.Helper()

	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("SCHEMA_PROVISIONING_ENABLED", "false")
	t.Setenv("PARTY_SERVICE_ENABLED", "false")
	t.Setenv("REDIS_ENABLED", "false")
	t.Setenv("AUTH_ENABLED", "false")
	t.Setenv("OTEL_TRACES_ENABLED", "false")
}

// TestNewContainer_RealDB constructs the full dependency container against a
// real CockroachDB testcontainer and verifies the key dependencies wired up by
// NewContainer are non-nil.
func TestNewContainer_RealDB(t *testing.T) {
	dbURL := setupCockroachDBURL(t)
	configureMinimalContainerEnv(t, dbURL)

	ctx := context.Background()
	c, err := NewContainer(ctx, testLogger(), "test-version")
	require.NoError(t, err)
	require.NotNil(t, c)
	defer c.Close()

	assert.NotNil(t, c.DB, "container.DB should not be nil")
	assert.NotNil(t, c.Repo, "container.Repo should not be nil")
	assert.NotNil(t, c.Tracer, "container.Tracer should not be nil (no-op tracer when disabled)")
	assert.NotNil(t, c.TenantService, "container.TenantService should not be nil")
	assert.NotNil(t, c.CachedRegistry, "container.CachedRegistry should not be nil")

	// Disabled paths leave their dependencies nil.
	assert.Nil(t, c.SchemaProvisioner, "schema provisioner should be nil when disabled")
	assert.Nil(t, c.PartyClient, "party client should be nil when disabled")
	assert.Nil(t, c.RedisClient, "redis client should be nil when disabled")
	assert.Nil(t, c.ProvisioningWorker, "provisioning worker should be nil without provisioner")
}

// TestContainer_Close_Idempotent_RealDB verifies that Close can be called
// multiple times on a fully-constructed container without error or panic.
func TestContainer_Close_Idempotent_RealDB(t *testing.T) {
	dbURL := setupCockroachDBURL(t)
	configureMinimalContainerEnv(t, dbURL)

	ctx := context.Background()
	c, err := NewContainer(ctx, testLogger(), "test-version")
	require.NoError(t, err)
	require.NotNil(t, c)

	assert.NotPanics(t, func() {
		c.Close()
		c.Close()
	})
}

// TestNewContainer_RealDB_PartyEnabled exercises the party-client wiring path
// (initPartyClient success branch and buildPartyResilienceConfig). The gRPC
// client is created lazily via grpc.NewClient, so construction succeeds without
// a live party service - the connection is only dialed on first RPC.
func TestNewContainer_RealDB_PartyEnabled(t *testing.T) {
	dbURL := setupCockroachDBURL(t)
	configureMinimalContainerEnv(t, dbURL)
	// Override the party toggle to enable the party-client path.
	t.Setenv("PARTY_SERVICE_ENABLED", "true")
	t.Setenv("K8S_NAMESPACE", "default")

	ctx := context.Background()
	c, err := NewContainer(ctx, testLogger(), "test-version")
	require.NoError(t, err)
	require.NotNil(t, c)
	defer c.Close()

	assert.NotNil(t, c.DB, "container.DB should not be nil")
	assert.NotNil(t, c.PartyClient, "party client should be wired when PARTY_SERVICE_ENABLED=true")
	assert.NotNil(t, c.TenantService, "container.TenantService should not be nil")
}

// TestNewContainer_RealDB_ProvisioningEnabled exercises the schema-provisioner
// and provisioning-worker wiring paths (initSchemaProvisioner,
// initProvisioningWorker, startProvisioningWorker). The provisioner opens its
// per-service GORM connections lazily (gorm.Open does not ping), so derived
// per-service DSNs against the same testcontainer succeed at construction time;
// no service migrations are run. The worker goroutine only begins polling, and
// the container is closed immediately.
func TestNewContainer_RealDB_ProvisioningEnabled(t *testing.T) {
	dbURL := setupCockroachDBURL(t)
	configureMinimalContainerEnv(t, dbURL)
	// Override the provisioning toggle. Per-service DSNs are derived from
	// DATABASE_URL by swapping the database name, reusing the same host/port.
	t.Setenv("SCHEMA_PROVISIONING_ENABLED", "true")

	ctx := context.Background()
	c, err := NewContainer(ctx, testLogger(), "test-version")
	require.NoError(t, err)
	require.NotNil(t, c)
	defer c.Close()

	assert.NotNil(t, c.DB, "container.DB should not be nil")
	assert.NotNil(t, c.SchemaProvisioner, "schema provisioner should be wired when enabled")
	assert.NotNil(t, c.ProvisioningWorker, "provisioning worker should be wired when provisioner present")
}
