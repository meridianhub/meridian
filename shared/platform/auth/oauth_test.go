package auth

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

// mockOAuthServer creates a test HTTP server that serves OAuth token responses
func mockOAuthServer(t *testing.T, token string, expiresIn int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request format
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))

		// Parse form data
		err := r.ParseForm()
		require.NoError(t, err)

		// Verify required parameters
		assert.Equal(t, "client_credentials", r.FormValue("grant_type"))

		// Return token response
		w.Header().Set("Content-Type", "application/json")
		resp := TokenResponse{
			AccessToken: token,
			TokenType:   "Bearer",
			ExpiresIn:   expiresIn,
			Scope:       r.FormValue("scope"),
		}
		err = json.NewEncoder(w).Encode(resp)
		require.NoError(t, err)
	}))
}

// mockIntrospectionServer creates a test HTTP server that serves token introspection responses
func mockIntrospectionServer(t *testing.T, active bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request format
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))

		// Parse form data
		err := r.ParseForm()
		require.NoError(t, err)

		// Return introspection response
		w.Header().Set("Content-Type", "application/json")
		resp := TokenIntrospection{
			Active:    active,
			ClientID:  "test-client",
			Username:  "test-user",
			Scope:     "read write",
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}
		err = json.NewEncoder(w).Encode(resp)
		require.NoError(t, err)
	}))
}

func TestNewOAuth2Client(t *testing.T) {
	t.Run("success with valid configuration", func(t *testing.T) {
		config := &OAuth2Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     "https://example.com/oauth/token",
			Scopes:       []string{"read", "write"},
			Client:       http.DefaultClient,
		}

		client, err := NewOAuth2Client(config)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "test-client", client.config.ClientID)
		assert.Equal(t, "https://example.com/oauth/token", client.config.TokenURL)
	})

	t.Run("uses default HTTP client when not provided", func(t *testing.T) {
		config := &OAuth2Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     "https://example.com/oauth/token",
		}

		client, err := NewOAuth2Client(config)

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.NotNil(t, client.config.Client)
		assert.Equal(t, 30*time.Second, client.config.Client.Timeout)
	})

	t.Run("error with nil configuration", func(t *testing.T) {
		client, err := NewOAuth2Client(nil)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrOAuthProviderNil)
		assert.Nil(t, client)
	})
}

func TestOAuth2Client_GetToken(t *testing.T) {
	ctx := context.Background()

	t.Run("success fetching new token", func(t *testing.T) {
		server := mockOAuthServer(t, "test-access-token", 3600)
		defer server.Close()

		config := &OAuth2Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     server.URL,
			Scopes:       []string{"read", "write"},
			Client:       http.DefaultClient,
		}

		client, err := NewOAuth2Client(config)
		require.NoError(t, err)

		token, err := client.GetToken(ctx)

		assert.NoError(t, err)
		assert.Equal(t, "test-access-token", token)
	})

	t.Run("returns cached token when still valid", func(t *testing.T) {
		requestCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requestCount++
			w.Header().Set("Content-Type", "application/json")
			resp := TokenResponse{
				AccessToken: "cached-token",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		config := &OAuth2Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		}

		client, err := NewOAuth2Client(config)
		require.NoError(t, err)

		// First call should fetch new token
		token1, err := client.GetToken(ctx)
		require.NoError(t, err)
		assert.Equal(t, "cached-token", token1)
		assert.Equal(t, 1, requestCount)

		// Second call should return cached token without new request
		token2, err := client.GetToken(ctx)
		require.NoError(t, err)
		assert.Equal(t, "cached-token", token2)
		assert.Equal(t, 1, requestCount) // No additional request
	})

	t.Run("refreshes token when expired", func(t *testing.T) {
		requestCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requestCount++
			w.Header().Set("Content-Type", "application/json")
			resp := TokenResponse{
				AccessToken: "refreshed-token",
				TokenType:   "Bearer",
				ExpiresIn:   1, // Very short expiry
			}
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		config := &OAuth2Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		}

		client, err := NewOAuth2Client(config)
		require.NoError(t, err)

		// First call fetches token
		token1, err := client.GetToken(ctx)
		require.NoError(t, err)
		assert.Equal(t, "refreshed-token", token1)
		assert.Equal(t, 1, requestCount)

		//nolint:forbidigo // triggers OAuth token expiration; token TTL is time-based with no observable state change
		time.Sleep(2 * time.Second)

		// Second call should refresh token
		token2, err := client.GetToken(ctx)
		require.NoError(t, err)
		assert.Equal(t, "refreshed-token", token2)
		assert.Equal(t, 2, requestCount) // New request made
	})

	t.Run("error when OAuth server returns non-200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("invalid credentials"))
		}))
		defer server.Close()

		config := &OAuth2Config{
			ClientID:     "test-client",
			ClientSecret: "wrong-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		}

		client, err := NewOAuth2Client(config)
		require.NoError(t, err)

		token, err := client.GetToken(ctx)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrOAuthHTTPError)
		assert.Empty(t, token)
	})

	t.Run("error when response has invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("invalid json"))
		}))
		defer server.Close()

		config := &OAuth2Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		}

		client, err := NewOAuth2Client(config)
		require.NoError(t, err)

		token, err := client.GetToken(ctx)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidOAuthResponse)
		assert.Empty(t, token)
	})

	t.Run("error when access token is empty", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			resp := TokenResponse{
				AccessToken: "", // Empty token
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		config := &OAuth2Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		}

		client, err := NewOAuth2Client(config)
		require.NoError(t, err)

		token, err := client.GetToken(ctx)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidOAuthResponse)
		assert.Empty(t, token)
	})

	t.Run("handles short-lived tokens correctly", func(t *testing.T) {
		// Test token with ExpiresIn = 15s (less than 30s refresh lead)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			resp := TokenResponse{
				AccessToken: "short-lived-token",
				TokenType:   "Bearer",
				ExpiresIn:   15, // 15 seconds
			}
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		config := &OAuth2Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		}

		client, err := NewOAuth2Client(config)
		require.NoError(t, err)

		beforeFetch := time.Now()
		token, err := client.GetToken(ctx)
		afterFetch := time.Now()

		assert.NoError(t, err)
		assert.Equal(t, "short-lived-token", token)

		// Verify expiry is set to halfway through lifetime (7.5s)
		// Should be: now + (15s / 2) = now + 7.5s
		client.mu.RLock()
		expiresAt := client.expiresAt
		client.mu.RUnlock()

		// expiresAt should be between (beforeFetch + 7s) and (afterFetch + 8s)
		expectedMin := beforeFetch.Add(7 * time.Second)
		expectedMax := afterFetch.Add(8 * time.Second)

		assert.True(t, expiresAt.After(expectedMin), "expiresAt should be at least 7s from fetch start")
		assert.True(t, expiresAt.Before(expectedMax), "expiresAt should be at most 8s from fetch end")
	})

	t.Run("handles zero or negative ExpiresIn", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			resp := TokenResponse{
				AccessToken: "instant-expire-token",
				TokenType:   "Bearer",
				ExpiresIn:   0, // Already expired
			}
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		config := &OAuth2Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		}

		client, err := NewOAuth2Client(config)
		require.NoError(t, err)

		beforeFetch := time.Now()
		token, err := client.GetToken(ctx)
		afterFetch := time.Now()

		assert.NoError(t, err)
		assert.Equal(t, "instant-expire-token", token)

		// Verify expiry is set to now (not backdated)
		client.mu.RLock()
		expiresAt := client.expiresAt
		client.mu.RUnlock()

		assert.True(t, !expiresAt.Before(beforeFetch), "expiresAt should not be before fetch start")
		assert.True(t, !expiresAt.After(afterFetch.Add(time.Second)), "expiresAt should not be after fetch end + 1s")
	})
}

func TestOAuth2Client_ClearCache(t *testing.T) {
	t.Run("clears cached token", func(t *testing.T) {
		server := mockOAuthServer(t, "test-token", 3600)
		defer server.Close()

		config := &OAuth2Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     server.URL,
			Client:       http.DefaultClient,
		}

		client, err := NewOAuth2Client(config)
		require.NoError(t, err)

		// Fetch and cache token
		token, err := client.GetToken(context.Background())
		require.NoError(t, err)
		assert.NotEmpty(t, token)

		// Verify token is cached
		client.mu.RLock()
		assert.NotEmpty(t, client.cachedToken)
		client.mu.RUnlock()

		// Clear cache
		client.ClearCache()

		// Verify cache is cleared
		client.mu.RLock()
		assert.Empty(t, client.cachedToken)
		assert.True(t, client.expiresAt.IsZero())
		client.mu.RUnlock()
	})
}

func TestNewOAuth2Introspector(t *testing.T) {
	t.Run("success with valid configuration", func(t *testing.T) {
		introspector, err := NewOAuth2Introspector(
			"https://example.com/oauth/introspect",
			"test-client",
			"test-secret",
			http.DefaultClient,
		)

		assert.NoError(t, err)
		assert.NotNil(t, introspector)
		assert.Equal(t, "https://example.com/oauth/introspect", introspector.introspectionURL)
	})

	t.Run("uses default HTTP client when not provided", func(t *testing.T) {
		introspector, err := NewOAuth2Introspector(
			"https://example.com/oauth/introspect",
			"test-client",
			"test-secret",
			nil,
		)

		assert.NoError(t, err)
		assert.NotNil(t, introspector)
		assert.NotNil(t, introspector.client)
		assert.Equal(t, 30*time.Second, introspector.client.Timeout)
	})

	t.Run("error with empty introspection URL", func(t *testing.T) {
		introspector, err := NewOAuth2Introspector(
			"",
			"test-client",
			"test-secret",
			http.DefaultClient,
		)

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrIntrospectionURLEmpty)
		assert.Nil(t, introspector)
	})
}

func TestOAuth2Introspector_IntrospectToken(t *testing.T) {
	ctx := context.Background()

	t.Run("success with active token", func(t *testing.T) {
		server := mockIntrospectionServer(t, true)
		defer server.Close()

		introspector, err := NewOAuth2Introspector(
			server.URL,
			"test-client",
			"test-secret",
			http.DefaultClient,
		)
		require.NoError(t, err)

		result, err := introspector.IntrospectToken(ctx, "valid-token")

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.True(t, result.Active)
		assert.Equal(t, "test-client", result.ClientID)
	})

	t.Run("success with inactive token", func(t *testing.T) {
		server := mockIntrospectionServer(t, false)
		defer server.Close()

		introspector, err := NewOAuth2Introspector(
			server.URL,
			"test-client",
			"test-secret",
			http.DefaultClient,
		)
		require.NoError(t, err)

		result, err := introspector.IntrospectToken(ctx, "expired-token")

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.False(t, result.Active)
	})

	t.Run("error with empty token", func(t *testing.T) {
		server := mockIntrospectionServer(t, true)
		defer server.Close()

		introspector, err := NewOAuth2Introspector(
			server.URL,
			"test-client",
			"test-secret",
			http.DefaultClient,
		)
		require.NoError(t, err)

		result, err := introspector.IntrospectToken(ctx, "")

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrTokenEmpty)
		assert.Nil(t, result)
	})

	t.Run("error when introspection server returns non-200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("unauthorized"))
		}))
		defer server.Close()

		introspector, err := NewOAuth2Introspector(
			server.URL,
			"test-client",
			"wrong-secret",
			http.DefaultClient,
		)
		require.NoError(t, err)

		result, err := introspector.IntrospectToken(ctx, "some-token")

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrOAuthHTTPError)
		assert.Nil(t, result)
	})

	t.Run("error when response has invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("invalid json"))
		}))
		defer server.Close()

		introspector, err := NewOAuth2Introspector(
			server.URL,
			"test-client",
			"test-secret",
			http.DefaultClient,
		)
		require.NoError(t, err)

		result, err := introspector.IntrospectToken(ctx, "some-token")

		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInvalidOAuthResponse)
		assert.Nil(t, result)
	})
}
