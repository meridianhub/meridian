// Package cmd implements the seed-dev CLI commands.
package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestCheckHealth_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	assert.True(t, checkHealth(srv.URL))
}

func TestCheckHealth_Unhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	assert.False(t, checkHealth(srv.URL))
}

func TestCheckHealth_ConnectionRefused(t *testing.T) {
	assert.False(t, checkHealth("http://127.0.0.1:1"))
}

func TestGetEnvOrDefault_ReturnsDefault(t *testing.T) {
	result := getEnvOrDefault("SEED_DEV_NONEXISTENT_VAR_99999", "my-default")
	assert.Equal(t, "my-default", result)
}

func TestGetEnvOrDefault_ReturnsEnvValue(t *testing.T) {
	t.Setenv("SEED_DEV_TEST_VAR", "from-env")
	result := getEnvOrDefault("SEED_DEV_TEST_VAR", "my-default")
	assert.Equal(t, "from-env", result)
}

func TestApplyManifest_InvalidFile(t *testing.T) {
	err := applyManifest(t.Context(), nil, "dev_tenant", "/nonexistent/path/manifest.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read manifest file")
}

func TestApplyManifest_InvalidJSON(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(tmp, []byte("not valid json {{"), 0o600))

	err := applyManifest(t.Context(), nil, "dev_tenant", tmp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse manifest JSON")
}

func TestApplyManifest_ParsesEnergyManifest(t *testing.T) {
	// Verify the example manifest parses as valid protobuf JSON without a live server.
	// We pass nil conn, so after successful parse it will fail at the gRPC call stage.
	manifestPath := "../../../examples/manifests/energy.json"
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		t.Skip("energy.json not found; skipping parse test")
	}

	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Unmarshal using the same path as production code to confirm it is valid.
	tmp := filepath.Join(t.TempDir(), "energy.json")
	require.NoError(t, os.WriteFile(tmp, data, 0o600))

	// applyManifest with nil conn will panic on the gRPC call; only test parse path.
	// We test parse separately via a helper to avoid a nil-conn panic.
	err = unmarshalManifestFile(tmp)
	assert.NoError(t, err)
}

func TestRootCmd_DefaultFlagValues(t *testing.T) {
	// Ensure the flags registered on rootCmd have the expected defaults.
	// These values must match what is documented and expected by docker-compose / scripts.
	f := rootCmd.Flags()

	gwURL, err := f.GetString("gateway-url")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8090", gwURL)

	grpc, err := f.GetString("grpc-addr")
	require.NoError(t, err)
	assert.Equal(t, "localhost:50051", grpc)

	manifest, err := f.GetString("manifest")
	require.NoError(t, err)
	assert.Equal(t, "examples/manifests/energy.json", manifest)

	tenantIDFlag, err := f.GetString("tenant-id")
	require.NoError(t, err)
	assert.Equal(t, "dev_tenant", tenantIDFlag)

	tenantSlugFlag, err := f.GetString("tenant-slug")
	require.NoError(t, err)
	assert.Equal(t, "dev-tenant", tenantSlugFlag)

	skip, err := f.GetBool("skip-manifest")
	require.NoError(t, err)
	assert.False(t, skip)
}

func TestEnergyManifest_ContainsFixtureDependencies(t *testing.T) {
	// Verify energy.json contains all sections that fixtures.go depends on:
	// organizations (DNO party), internalAccounts (GSP KWH accounts), marketData (wholesale prices).
	manifestPath := "../../../examples/manifests/energy.json"
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	var manifest controlplanev1.Manifest
	require.NoError(t, protojson.Unmarshal(data, &manifest))

	// Organizations: fixtures resolve DNO party by external reference "UKPNDNO001".
	require.NotEmpty(t, manifest.GetOrganizations(), "manifest must define organizations for fixture DNO resolution")
	var foundDNO bool
	for _, org := range manifest.GetOrganizations() {
		if org.GetExternalReference() == "UKPNDNO001" {
			foundDNO = true
			break
		}
	}
	assert.True(t, foundDNO, "manifest must include UKPN organization with external_reference=UKPNDNO001")

	// Internal accounts: fixtures resolve GSP KWH accounts by code.
	require.NotEmpty(t, manifest.GetInternalAccounts(), "manifest must define internal accounts for GSP KWH resolution")
	manifestAccountCodes := make(map[string]bool)
	for _, ia := range manifest.GetInternalAccounts() {
		manifestAccountCodes[ia.GetCode()] = true
	}
	for _, gsp := range gspAccountCodes {
		assert.True(t, manifestAccountCodes[gsp.accountCode],
			"manifest missing internal account code %q required by fixtures (region %s)", gsp.accountCode, gsp.region)
	}

	// Market data: fixtures record observations to WHOLESALE_ENERGY_GBP_KWH dataset.
	require.NotNil(t, manifest.GetMarketData(), "manifest must define marketData for wholesale price seeding")
	var foundDataset bool
	for _, ds := range manifest.GetMarketData().GetDatasets() {
		if ds.GetCode() == "WHOLESALE_ENERGY_GBP_KWH" {
			foundDataset = true
			break
		}
	}
	assert.True(t, foundDataset, "manifest must include WHOLESALE_ENERGY_GBP_KWH dataset")
}

func TestEnergyManifest_ValidJSON(t *testing.T) {
	// Verify energy.json is valid JSON (catches syntax errors that protojson might tolerate).
	manifestPath := "../../../examples/manifests/energy.json"
	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw), "energy.json must be valid JSON")

	// Verify expected top-level keys exist.
	for _, key := range []string{"version", "metadata", "instruments", "accountTypes", "organizations", "internalAccounts", "marketData"} {
		assert.Contains(t, raw, key, "energy.json missing top-level key %q", key)
	}
}

func TestToMoney(t *testing.T) {
	m := toMoney(12.34, "GBP")
	require.NotNil(t, m)
	require.NotNil(t, m.GetAmount())
	assert.Equal(t, "GBP", m.GetAmount().GetCurrencyCode())
	assert.Equal(t, int64(12), m.GetAmount().GetUnits())
	assert.Equal(t, int32(340000000), m.GetAmount().GetNanos())
}

func TestToMoney_ZeroAmount(t *testing.T) {
	m := toMoney(0, "USD")
	require.NotNil(t, m)
	assert.Equal(t, int64(0), m.GetAmount().GetUnits())
	assert.Equal(t, int32(0), m.GetAmount().GetNanos())
}

func TestGSPAccountCodes_AllUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, gsp := range gspAccountCodes {
		assert.False(t, seen[gsp.accountCode], "duplicate GSP account code: %s", gsp.accountCode)
		seen[gsp.accountCode] = true
	}
}

func TestCustomerDefinitions_GSPIndexBounds(t *testing.T) {
	for _, cust := range customerDefinitions {
		assert.Less(t, cust.gspIndex, len(gspAccountCodes),
			"customer %s has gspIndex %d but only %d GSP codes defined", cust.legalName, cust.gspIndex, len(gspAccountCodes))
	}
}

func TestSkipManifestDoesNotBlockFixtures(t *testing.T) {
	// Verify that --skip-manifest and --with-fixtures can be set independently.
	// The bug: runSeed() returned early when skipManifest was true,
	// preventing withFixtures code from executing.
	origSkip, origFixtures := skipManifest, withFixtures
	t.Cleanup(func() {
		skipManifest = origSkip
		withFixtures = origFixtures
	})

	cmd := rootCmd

	// Parse flags without executing - verify both flags are accepted together.
	err := cmd.ParseFlags([]string{"--skip-manifest", "--with-fixtures"})
	assert.NoError(t, err)

	// After parsing, both package-level vars should be set.
	assert.True(t, skipManifest, "skipManifest flag should be true")
	assert.True(t, withFixtures, "withFixtures flag should be true")
}
