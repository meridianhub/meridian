package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient_RequiresBaseURL(t *testing.T) {
	_, err := NewClient(Config{})
	require.ErrorIs(t, err, ErrBaseURLRequired)
}

func TestNewClient_DefaultTimeout(t *testing.T) {
	c, err := NewClient(Config{BaseURL: "http://localhost:5556/dex"})
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, c.httpClient.Timeout)
}

func TestNewClient_CustomTimeout(t *testing.T) {
	c, err := NewClient(Config{BaseURL: "http://localhost:5556/dex", Timeout: 10 * time.Second})
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, c.httpClient.Timeout)
}

func TestDiscovery_Success(t *testing.T) {
	wellKnown := WellKnownConfig{
		Issuer:                "http://localhost:5556/dex",
		AuthorizationEndpoint: "http://localhost:5556/dex/auth",
		TokenEndpoint:         "http://localhost:5556/dex/token",
		JWKSURI:               "http://localhost:5556/dex/keys",
		UserinfoEndpoint:      "http://localhost:5556/dex/userinfo",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/.well-known/openid-configuration", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(wellKnown)
	}))
	defer srv.Close()

	c, err := NewClient(Config{BaseURL: srv.URL})
	require.NoError(t, err)

	got, err := c.Discovery(context.Background())
	require.NoError(t, err)
	assert.Equal(t, wellKnown.Issuer, got.Issuer)
	assert.Equal(t, wellKnown.JWKSURI, got.JWKSURI)
}

func TestDiscovery_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, err := NewClient(Config{BaseURL: srv.URL})
	require.NoError(t, err)

	_, err = c.Discovery(context.Background())
	require.ErrorIs(t, err, ErrUnexpectedStatus)
}

func TestHealthCheck_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/healthz", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, err := NewClient(Config{BaseURL: srv.URL})
	require.NoError(t, err)

	err = c.HealthCheck(context.Background())
	require.NoError(t, err)
}

func TestHealthCheck_Unavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c, err := NewClient(Config{BaseURL: srv.URL})
	require.NoError(t, err)

	err = c.HealthCheck(context.Background())
	require.ErrorIs(t, err, ErrUnexpectedStatus)
}
