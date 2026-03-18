package ecb_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/market-information/adapters/external/ecb"
)

func TestClient_FetchDailyRates_Success(t *testing.T) {
	// Create mock ECB server
	mockCSV := `KEY,FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE
EXR.D.USD.EUR.SP00.A,D,USD,EUR,SP00,A,2024-01-15,1.0876`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "text/csv", r.Header.Get("Accept"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockCSV))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))

	body, err := client.FetchDailyRates(context.Background())
	require.NoError(t, err)
	defer body.Close()

	data, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Contains(t, string(data), "USD")
}

func TestClient_FetchDailyRates_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Intentional sleep: Delay response to test timeout handling
		time.Sleep(200 * time.Millisecond) //nolint:forbidigo // simulates slow HTTP server to trigger client timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	client := ecb.NewClient(ecb.Config{Timeout: 50 * time.Millisecond}, ecb.WithEndpoint(server.URL))

	_, err := client.FetchDailyRates(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

func TestClient_FetchDailyRates_Non200Status(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    error
	}{
		{"404 Not Found", http.StatusNotFound, ecb.ErrAPIError},
		{"500 Server Error", http.StatusInternalServerError, ecb.ErrAPIError},
		{"429 Rate Limited", http.StatusTooManyRequests, ecb.ErrRateLimited},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte("error"))
			}))
			defer server.Close()

			client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))

			_, err := client.FetchDailyRates(context.Background())
			require.Error(t, err)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestClient_FetchDailyRates_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Intentional sleep: Delay response to test context cancellation handling
		time.Sleep(1 * time.Second) //nolint:forbidigo // simulates slow HTTP server to ensure client context cancellation is tested
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))

	_, err := client.FetchDailyRates(ctx)
	require.Error(t, err)
}

func TestClient_NilClient(t *testing.T) {
	var client *ecb.Client
	_, err := client.FetchDailyRates(context.Background())
	require.ErrorIs(t, err, ecb.ErrNotConfigured)
}

func TestClient_DefaultEndpoint(t *testing.T) {
	client := ecb.NewClient(ecb.Config{})
	// We can't test the default endpoint directly without making a real request,
	// but we can verify the client is created successfully
	require.NotNil(t, client)
}

func TestClient_WithCustomHTTPClient(t *testing.T) {
	customClient := &http.Client{
		Timeout: 5 * time.Second,
	}

	client := ecb.NewClient(ecb.Config{}, ecb.WithHTTPClient(customClient))

	require.NotNil(t, client)
}

func TestClient_WithCustomEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test data"))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))

	body, err := client.FetchDailyRates(context.Background())
	require.NoError(t, err)
	defer body.Close()

	data, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, "test data", string(data))
}

func TestClient_ConfigEndpointTakesPrecedence(t *testing.T) {
	// When Config.Endpoint is set, it should be used
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("config endpoint"))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{Endpoint: server.URL})

	body, err := client.FetchDailyRates(context.Background())
	require.NoError(t, err)
	defer body.Close()

	data, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, "config endpoint", string(data))
}

func TestClient_OptionEndpointOverridesConfig(t *testing.T) {
	// WithEndpoint option should override Config.Endpoint
	configServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("config endpoint"))
	}))
	defer configServer.Close()

	optionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("option endpoint"))
	}))
	defer optionServer.Close()

	client := ecb.NewClient(ecb.Config{Endpoint: configServer.URL}, ecb.WithEndpoint(optionServer.URL))

	body, err := client.FetchDailyRates(context.Background())
	require.NoError(t, err)
	defer body.Close()

	data, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, "option endpoint", string(data))
}

func TestClient_ConfigTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Intentional sleep: Delay response to test timeout configuration
		time.Sleep(100 * time.Millisecond) //nolint:forbidigo // simulates slow HTTP server to trigger configured client timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Use a very short timeout that should trigger before response
	client := ecb.NewClient(ecb.Config{
		Endpoint: server.URL,
		Timeout:  10 * time.Millisecond,
	})

	_, err := client.FetchDailyRates(context.Background())
	require.Error(t, err)
}

func TestClient_FetchDailyRates_ErrorResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Invalid request parameters"))
	}))
	defer server.Close()

	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint(server.URL))

	_, err := client.FetchDailyRates(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, ecb.ErrAPIError)
	assert.Contains(t, err.Error(), "Invalid request parameters")
	assert.Contains(t, err.Error(), "400")
}

func TestClient_FetchDailyRates_NetworkError(t *testing.T) {
	// Use a URL that will fail to connect
	client := ecb.NewClient(ecb.Config{}, ecb.WithEndpoint("http://localhost:1"))

	_, err := client.FetchDailyRates(context.Background())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch ECB rates")
}
