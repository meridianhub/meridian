package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveProvisionerConfig_EmptyDSN(t *testing.T) {
	config, err := DeriveProvisionerConfig("")
	require.NoError(t, err)
	// Empty DSN returns DefaultConfig unchanged - all services present.
	assert.NotEmpty(t, config.Services)
	for _, svc := range config.Services {
		assert.NotEmpty(t, svc.Name)
	}
}

func TestDeriveProvisionerConfig_PostgresDSN(t *testing.T) {
	config, err := DeriveProvisionerConfig("postgres://root:pass@myhost:5432/defaultdb?sslmode=disable")
	require.NoError(t, err)
	require.NotEmpty(t, config.Services)
	for _, svc := range config.Services {
		assert.Contains(t, svc.DatabaseURL, "myhost:5432", "service %s should use provided host", svc.Name)
		assert.NotContains(t, svc.DatabaseURL, "defaultdb", "service %s should not use base database name", svc.Name)
	}
}

func TestDeriveProvisionerConfig_CockroachDBDSN(t *testing.T) {
	config, err := DeriveProvisionerConfig("postgres://root@cockroachdb:26257/defaultdb?sslmode=disable")
	require.NoError(t, err)
	require.NotEmpty(t, config.Services)
	for _, svc := range config.Services {
		assert.Contains(t, svc.DatabaseURL, "cockroachdb:26257", "service %s should use cockroachdb host", svc.Name)
	}
}

func TestDeriveProvisionerConfig_LocalhostDSN(t *testing.T) {
	config, err := DeriveProvisionerConfig("postgres://root@localhost:26257/defaultdb?sslmode=disable")
	require.NoError(t, err)
	require.NotEmpty(t, config.Services)

	// Verify a known service gets the correct derived database name.
	var partyURL string
	for _, svc := range config.Services {
		if svc.Name == "party" {
			partyURL = svc.DatabaseURL
		}
	}
	assert.Contains(t, partyURL, "localhost:26257")
	assert.Contains(t, partyURL, "/meridian_party")
}
