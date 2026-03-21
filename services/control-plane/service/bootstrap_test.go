package service

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/control-plane/internal/generator"
	"github.com/meridianhub/meridian/services/control-plane/internal/manifest"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

// ---------------------------------------------------------------------------
// ValidateManifest tests
// ---------------------------------------------------------------------------

func TestValidateManifest_Valid(t *testing.T) {
	mf := &controlplanev1.Manifest{}
	err := protojson.Unmarshal([]byte(`{
		"version": "1.0",
		"metadata": {"name": "test", "industry": "platform"},
		"instruments": [{
			"code": "GBP",
			"name": "British Pound",
			"type": "INSTRUMENT_TYPE_FIAT",
			"dimensions": {"unit": "GBP", "precision": 2}
		}],
		"accountTypes": [{
			"code": "CLEARING",
			"name": "Clearing",
			"normalBalance": "NORMAL_BALANCE_DEBIT",
			"allowedInstruments": ["GBP"]
		}]
	}`), mf)
	require.NoError(t, err)

	result, err := ValidateManifest(mf, nil)
	require.NoError(t, err)
	assert.True(t, result.Valid)
	assert.Empty(t, result.Errors)
}

func TestValidateManifest_Invalid(t *testing.T) {
	// Empty manifest should have validation errors
	mf := &controlplanev1.Manifest{}

	result, err := ValidateManifest(mf, nil)
	require.NoError(t, err)
	assert.False(t, result.Valid)
	assert.NotEmpty(t, result.Errors)
}

func TestValidateManifest_WithWarnings(t *testing.T) {
	// A manifest with an event-triggered saga but no filter should produce
	// a MISSING_EVENT_FILTER warning while remaining valid.
	mf := &controlplanev1.Manifest{}
	err := protojson.Unmarshal([]byte(`{
		"version": "1.0",
		"metadata": {"name": "test-warnings", "industry": "platform"},
		"instruments": [{
			"code": "GBP",
			"name": "British Pound",
			"type": "INSTRUMENT_TYPE_FIAT",
			"dimensions": {"unit": "GBP", "precision": 2}
		}],
		"accountTypes": [{
			"code": "CLEARING",
			"name": "Clearing",
			"normalBalance": "NORMAL_BALANCE_DEBIT",
			"allowedInstruments": ["GBP"]
		}],
		"sagas": [{
			"name": "on_transaction_captured",
			"trigger": "event:position-keeping.transaction-captured.v1",
			"script": "def execute(ctx):\n    return {}\n"
		}]
	}`), mf)
	require.NoError(t, err)

	result, err := ValidateManifest(mf, nil)
	require.NoError(t, err)
	assert.True(t, result.Valid, "manifest with warnings should still be valid; errors: %v", result.Errors)
	assert.NotEmpty(t, result.Warnings, "expected at least one warning")

	found := false
	for _, w := range result.Warnings {
		if w.Code == "MISSING_EVENT_FILTER" {
			found = true
			break
		}
	}
	assert.True(t, found, "expected MISSING_EVENT_FILTER warning, got: %v", result.Warnings)
}

// ---------------------------------------------------------------------------
// RegisterApplyManifestService tests
// ---------------------------------------------------------------------------

func TestRegisterApplyManifestService_NilPool(t *testing.T) {
	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterApplyManifestService(server, ApplyManifestServiceConfig{
		Pool: nil,
	})
	require.ErrorIs(t, err, ErrPoolRequired)
}

func TestRegisterApplyManifestService_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("requires testcontainer")
	}

	// Start a CockroachDB container and obtain a pgxpool.Pool via testdb.NewTestPool.
	pool := testdb.NewTestPool(t)

	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterApplyManifestService(server, ApplyManifestServiceConfig{
		Pool: pool,
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// RegisterManifestHistoryService tests
// ---------------------------------------------------------------------------

func TestRegisterManifestHistoryService_NilDB(t *testing.T) {
	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterManifestHistoryService(server, ManifestHistoryServiceConfig{
		DB: nil,
	})
	require.ErrorIs(t, err, ErrDBRequired)
}

func TestRegisterManifestHistoryService_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("requires testcontainer")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterManifestHistoryService(server, ManifestHistoryServiceConfig{
		DB: db,
	})
	require.NoError(t, err)
}

func TestRegisterManifestHistoryService_WithExportCollectors(t *testing.T) {
	if testing.Short() {
		t.Skip("requires testcontainer")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterManifestHistoryService(server, ManifestHistoryServiceConfig{
		DB:               db,
		ExportCollectors: &manifest.ExportCollectors{},
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// RegisterEconomyGeneratorService tests
// ---------------------------------------------------------------------------

func TestRegisterEconomyGeneratorService_NilRegistry(t *testing.T) {
	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterEconomyGeneratorService(server, EconomyGeneratorConfig{
		SchemaRegistry: nil,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema registry is required")
}

func TestRegisterEconomyGeneratorService_Success(t *testing.T) {
	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterEconomyGeneratorService(server, EconomyGeneratorConfig{
		SchemaRegistry: schema.NewRegistry(),
	})
	require.NoError(t, err)
}

func TestRegisterEconomyGeneratorService_WithOptions(t *testing.T) {
	server := grpc.NewServer()
	defer server.Stop()

	err := RegisterEconomyGeneratorService(server, EconomyGeneratorConfig{
		SchemaRegistry: schema.NewRegistry(),
		LLMClient:      &fakeLLMClient{},
		Validator:      &fakeManifestValidator{},
	})
	require.NoError(t, err)
}

// fakeLLMClient satisfies generator.LLMClient for testing.
type fakeLLMClient struct{}

func (f *fakeLLMClient) Generate(_ context.Context, _ string) (string, error) { return "", nil }
func (f *fakeLLMClient) Fix(_ context.Context, _ string, _ []generator.ValidationError) (string, error) {
	return "", nil
}

// fakeManifestValidator satisfies generator.ManifestValidator for testing.
type fakeManifestValidator struct{}

func (f *fakeManifestValidator) ValidateDryRun(_ context.Context, _ string) (*generator.ValidationResult, error) {
	return &generator.ValidationResult{Valid: true}, nil
}

// ---------------------------------------------------------------------------
// SeedManifestVersion tests
// ---------------------------------------------------------------------------

func TestSeedManifestVersion_NilDB(t *testing.T) {
	_, err := SeedManifestVersion(context.Background(), nil, &controlplanev1.Manifest{}, "test")
	require.ErrorIs(t, err, ErrDBRequired)
}

func TestSeedManifestVersion_StoresManifest(t *testing.T) {
	if testing.Short() {
		t.Skip("requires testcontainer")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	tc := testdb.SetupTenantSchema(t, db, "test_tenant")
	defer tc.Cleanup()
	testdb.CreateTable(t, tc.DB, tc.Tenant, manifestVersionsDDL)

	mf := &controlplanev1.Manifest{
		Version:  "1.0",
		Metadata: &controlplanev1.ManifestMetadata{Name: "Test", Industry: "platform"},
	}

	seeded, err := SeedManifestVersion(tc.Ctx, tc.DB, mf, "system:bootstrap")
	require.NoError(t, err)
	assert.True(t, seeded, "expected manifest to be seeded on first call")
}

func TestSeedManifestVersion_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("requires testcontainer")
	}

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	defer cleanup()

	tc := testdb.SetupTenantSchema(t, db, "test_tenant")
	defer tc.Cleanup()
	testdb.CreateTable(t, tc.DB, tc.Tenant, manifestVersionsDDL)

	mf := &controlplanev1.Manifest{
		Version:  "1.0",
		Metadata: &controlplanev1.ManifestMetadata{Name: "Test", Industry: "platform"},
	}

	seeded1, err := SeedManifestVersion(tc.Ctx, tc.DB, mf, "system:bootstrap")
	require.NoError(t, err)
	assert.True(t, seeded1)

	seeded2, err := SeedManifestVersion(tc.Ctx, tc.DB, mf, "system:bootstrap")
	require.NoError(t, err)
	assert.False(t, seeded2, "expected second call to skip seeding")
}

const manifestVersionsDDL = `CREATE TABLE IF NOT EXISTS %s.manifest_versions (
	id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	version VARCHAR(50) NOT NULL,
	manifest_json JSONB NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	applied_by VARCHAR(255) NOT NULL,
	apply_status VARCHAR(20) NOT NULL DEFAULT 'APPLIED',
	apply_job_id UUID,
	diff_summary TEXT,
	relationship_graph JSONB,
	sequence_number BIGINT NOT NULL DEFAULT 0,
	checksum VARCHAR(64),
	source VARCHAR(20),
	resource_path VARCHAR(255),
	phase_status JSONB,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	CONSTRAINT valid_apply_status CHECK (apply_status IN ('APPLIED', 'FAILED', 'ROLLED_BACK', 'PARTIAL'))
)`

// ---------------------------------------------------------------------------
// NewHandlerDeps test
// ---------------------------------------------------------------------------

func TestNewHandlerDeps_ReturnsNonNil(t *testing.T) {
	// NewHandlerDeps wraps gRPC client constructors. Passing a nil conn is
	// safe because the clients are lazy (they only dial on RPC calls).
	deps := NewHandlerDeps(nil)
	require.NotNil(t, deps)
}
