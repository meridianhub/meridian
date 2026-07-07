package applier

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ"
	"github.com/meridianhub/meridian/services/control-plane/internal/differ/persistence"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/meridianhub/meridian/services/control-plane/internal/planner"
	"github.com/meridianhub/meridian/services/control-plane/internal/validator"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// newHistoryBackedHandler builds an ApplyManifestHandler wired to a real
// DB-backed executor, the pgx version store, AND the versioned GORM-backed
// HistoryService, all sharing a single Postgres testcontainer. This mirrors the
// production wiring (RegisterApplyManifestService with a GormDB) so the full
// validate -> diff -> plan -> execute -> recordHistory path exercises sequence
// assignment and optimistic concurrency control. Returns the handler, a
// tenant-scoped context, the pool, and the tenant ID for row assertions.
func newHistoryBackedHandler(t *testing.T, tenantID string) (*ApplyManifestHandler, context.Context, *pgxpool.Pool, string) {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	pool := testdb.NewTestPool(t, testdb.WithMigrations("control-plane"))
	ctx, cleanup := testdb.SetupTenantSchemaForPgx(t, pool, tenantID, "control-plane")
	t.Cleanup(cleanup)

	script, version, err := loadEmbeddedApplyManifest()
	require.NoError(t, err)
	insertPlatformSaga(t, pool, version, script)

	deps := &HandlerDependencies{
		ReferenceData:      &noopReferenceData{},
		InternalAccount:    &noopInternalAccount{},
		MarketInformation:  &noopMarketInformation{},
		Party:              &noopParty{},
		OperationalGateway: &noopOperationalGateway{},
	}
	executor := newExecutorWithRunner(t, pool, deps)

	// Open a GORM handle over the SAME container so the history service and the
	// executor/version store observe one database and tenant schema.
	gormDB, err := gorm.Open(gormpostgres.Open(pool.Config().ConnString()), &gorm.Config{})
	require.NoError(t, err)
	repo, err := manifest.NewRepository(gormDB)
	require.NoError(t, err)
	historySvc, err := manifest.NewHistoryService(repo)
	require.NoError(t, err)

	v, err := validator.New()
	require.NoError(t, err)

	handler, err := NewApplyManifestHandler(ApplyManifestHandlerConfig{
		Validator:      v,
		Differ:         differ.New(nil, nil, nil),
		Planner:        planner.NewManifestPlanner(),
		Executor:       executor,
		VersionStore:   persistence.NewPostgresManifestVersionStore(pool),
		HistoryService: historySvc,
	})
	require.NoError(t, err)
	return handler, ctx, pool, tenantID
}

// seqTestManifest builds a manifest whose instrument set grows with each version
// so successive applies always produce a real (non-empty) CREATE diff.
func seqTestManifest(version string, instrumentCodes ...string) *controlplanev1.Manifest {
	m := &controlplanev1.Manifest{
		Version: version,
		Metadata: &controlplanev1.ManifestMetadata{
			Name:     "Sequence Test Manifest",
			Industry: "testing",
		},
	}
	for _, code := range instrumentCodes {
		m.Instruments = append(m.Instruments, &controlplanev1.InstrumentDefinition{
			Code: code,
			Name: code,
			Type: controlplanev1.InstrumentType_INSTRUMENT_TYPE_FIAT,
			Dimensions: &controlplanev1.InstrumentDimensions{
				Unit:      code,
				Precision: 2,
			},
		})
	}
	return m
}

// countManifestVersionRows returns the number of manifest_version rows in the
// tenant's schema, used to prove a single apply persists exactly one row.
func countManifestVersionRows(t *testing.T, pool *pgxpool.Pool, tenantID string) int {
	t.Helper()
	schema := pgx.Identifier{tenant.TenantID(tenantID).SchemaName()}.Sanitize()
	var count int
	err := pool.QueryRow(
		context.Background(),
		fmt.Sprintf("SELECT count(*) FROM %s.manifest_version", schema),
	).Scan(&count)
	require.NoError(t, err)
	return count
}

// TestApplyManifest_HistoryWiring_SequenceIncrements verifies that when the
// history service is wired into the handler, sequential applies through the full
// gRPC pipeline receive monotonically increasing sequence numbers (1, 2, 3) and
// that each apply persists exactly one manifest_version row (no duplicate
// sequence-0 rows from a second, unsequenced write).
func TestApplyManifest_HistoryWiring_SequenceIncrements(t *testing.T) {
	handler, ctx, pool, tenantID := newHistoryBackedHandler(t, "seqinc")

	applies := []*controlplanev1.Manifest{
		seqTestManifest("1.1", "GBP"),
		seqTestManifest("1.2", "GBP", "USD"),
		seqTestManifest("1.3", "GBP", "USD", "EUR"),
	}

	for i, m := range applies {
		resp, err := handler.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
			Manifest:  m,
			AppliedBy: "tester",
		})
		require.NoError(t, err)
		require.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED, resp.Status,
			"apply %d should succeed", i+1)
		require.NotNil(t, resp.Snapshot, "history service must attach a version snapshot")
		assert.Equal(t, int64(i+1), resp.SequenceNumber, "sequence number must increment per apply")
		assert.Equal(t, int64(i+1), resp.Snapshot.SequenceNumber, "snapshot sequence must match response")
	}

	// Exactly one row per apply: proves the legacy version store no longer writes
	// a duplicate, unsequenced row alongside the history service.
	assert.Equal(t, len(applies), countManifestVersionRows(t, pool, tenantID))
}

// TestApplyManifest_HistoryWiring_StaleSequenceAborted verifies that an apply
// carrying a stale expected_sequence_number is rejected with codes.Aborted via
// the optimistic concurrency check, so concurrent applies cannot clobber the
// version history by landing on the same sequence.
func TestApplyManifest_HistoryWiring_StaleSequenceAborted(t *testing.T) {
	handler, ctx, _, _ := newHistoryBackedHandler(t, "seqconflict")

	// First apply -> sequence 1.
	resp1, err := handler.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  seqTestManifest("1.1", "GBP"),
		AppliedBy: "tester",
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), resp1.SequenceNumber)

	// Second apply -> sequence 2.
	resp2, err := handler.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:  seqTestManifest("1.2", "GBP", "USD"),
		AppliedBy: "tester",
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), resp2.SequenceNumber)

	// Third apply supplies a stale expected sequence (1, but current is 2).
	// The optimistic-concurrency check must reject it with codes.Aborted.
	_, err = handler.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{
		Manifest:               seqTestManifest("1.3", "GBP", "USD", "EUR"),
		AppliedBy:              "tester",
		ExpectedSequenceNumber: 1,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Aborted, status.Code(err), "stale expected sequence must be Aborted")
}
