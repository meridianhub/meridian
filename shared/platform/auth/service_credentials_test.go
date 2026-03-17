package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServiceCredentials(t *testing.T) {
	t.Run("success with valid OAuth2Client", func(t *testing.T) {
		server := mockOAuthServer(t, "test-token", 3600)
		defer server.Close()

		oauthClient, err := NewOAuth2Client(&OAuth2Config{
			ClientID:     "svc-client",
			ClientSecret: "svc-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		})
		require.NoError(t, err)

		creds, err := NewServiceCredentials(oauthClient)
		assert.NoError(t, err)
		assert.NotNil(t, creds)
	})

	t.Run("error with nil client", func(t *testing.T) {
		creds, err := NewServiceCredentials(nil)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrOAuthProviderNil)
		assert.Nil(t, creds)
	})
}

func TestServiceCredentials_GetRequestMetadata(t *testing.T) {
	ctx := context.Background()

	t.Run("returns bearer token in authorization header", func(t *testing.T) {
		server := mockOAuthServer(t, "svc-access-token", 3600)
		defer server.Close()

		oauthClient, err := NewOAuth2Client(&OAuth2Config{
			ClientID:     "svc-client",
			ClientSecret: "svc-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		})
		require.NoError(t, err)

		creds, err := NewServiceCredentials(oauthClient)
		require.NoError(t, err)

		md, err := creds.GetRequestMetadata(ctx)
		assert.NoError(t, err)
		assert.Equal(t, "Bearer svc-access-token", md["authorization"])
	})

	t.Run("returns cached token on subsequent calls", func(t *testing.T) {
		requestCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requestCount++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(TokenResponse{
				AccessToken: "cached-svc-token",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			})
		}))
		defer server.Close()

		oauthClient, err := NewOAuth2Client(&OAuth2Config{
			ClientID:     "svc-client",
			ClientSecret: "svc-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		})
		require.NoError(t, err)

		creds, err := NewServiceCredentials(oauthClient)
		require.NoError(t, err)

		md1, err := creds.GetRequestMetadata(ctx)
		require.NoError(t, err)
		assert.Equal(t, "Bearer cached-svc-token", md1["authorization"])

		md2, err := creds.GetRequestMetadata(ctx)
		require.NoError(t, err)
		assert.Equal(t, "Bearer cached-svc-token", md2["authorization"])

		assert.Equal(t, 1, requestCount, "should only fetch token once due to caching")
	})

	t.Run("propagates OAuth error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("invalid credentials"))
		}))
		defer server.Close()

		oauthClient, err := NewOAuth2Client(&OAuth2Config{
			ClientID:     "bad-client",
			ClientSecret: "bad-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		})
		require.NoError(t, err)

		creds, err := NewServiceCredentials(oauthClient)
		require.NoError(t, err)

		md, err := creds.GetRequestMetadata(ctx)
		assert.Error(t, err)
		assert.Nil(t, md)
	})
}

func TestServiceCredentials_RequireTransportSecurity(t *testing.T) {
	t.Run("returns false for now (TLS at service mesh level)", func(t *testing.T) {
		server := mockOAuthServer(t, "token", 3600)
		defer server.Close()

		oauthClient, err := NewOAuth2Client(&OAuth2Config{
			ClientID:     "svc-client",
			ClientSecret: "svc-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		})
		require.NoError(t, err)

		creds, err := NewServiceCredentials(oauthClient)
		require.NoError(t, err)

		assert.False(t, creds.RequireTransportSecurity())
	})
}
