// Package cmd implements the seed-dev CLI commands.
package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
