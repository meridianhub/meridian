package client

import (
	"context"
	"strings"
	"testing"
	"time"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNew_WithTarget(t *testing.T) {
	client, cleanup, err := New(Config{
		Target:  "localhost:50051",
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	assert.NotNil(t, client.conn)
	assert.NotNil(t, client.currentAccount)
	assert.Equal(t, 10*time.Second, client.timeout)

	cleanup()
}

func TestNew_WithServiceName(t *testing.T) {
	client, cleanup, err := New(Config{
		ServiceName: "current-account",
		Namespace:   "default",
		Port:        50051,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	assert.NotNil(t, client.conn)
	assert.NotNil(t, client.currentAccount)
	assert.Equal(t, DefaultTimeout, client.timeout)

	cleanup()
}

func TestNew_Defaults(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50051",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	defer cleanup()

	assert.Equal(t, DefaultTimeout, client.timeout)
}

func TestNew_RequiresTargetOrServiceName(t *testing.T) {
	_, _, err := New(Config{})
	assert.ErrorIs(t, err, ErrTargetRequired)
}

func TestNew_DefaultsApplied(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		wantPort int
	}{
		{
			name:     "empty port defaults to 50051",
			cfg:      Config{ServiceName: "current-account"},
			wantPort: DefaultPort,
		},
		{
			name:     "custom port preserved",
			cfg:      Config{ServiceName: "current-account", Port: 9999},
			wantPort: 9999,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup, err := New(tt.cfg)
			require.NoError(t, err)
			defer cleanup()
			require.NotNil(t, client)
		})
	}
}

func TestClose(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50051",
	})
	require.NoError(t, err)
	defer cleanup()

	err = client.Close()
	assert.NoError(t, err)
}

func TestClose_NilConn(t *testing.T) {
	client := &Client{}
	err := client.Close()
	assert.NoError(t, err)
}

func TestConstants(t *testing.T) {
	assert.Equal(t, 50051, DefaultPort)
	assert.Equal(t, 30*time.Second, DefaultTimeout)
	assert.Equal(t, "default", DefaultNamespace)
	assert.Equal(t, "current-account", ServiceName)
}

func TestNew_WithResilience(t *testing.T) {
	resilienceConfig := clients.DefaultResilientClientConfig("current-account-client")
	client, cleanup, err := New(Config{
		Target:     "localhost:50051",
		Resilience: &resilienceConfig,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	defer cleanup()

	// Verify resilient client was created
	assert.NotNil(t, client.resilient)
}

func TestNew_WithoutResilience(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50051",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	defer cleanup()

	// Verify resilient client was not created
	assert.Nil(t, client.resilient)
}

// Account ID validation tests

func TestRetrieveCurrentAccount_InvalidAccountID(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50051",
	})
	require.NoError(t, err)
	defer cleanup()

	tests := []struct {
		name      string
		accountID string
	}{
		{"empty string", ""},
		{"contains space", "ACC 123"},
		{"contains at symbol", "ACC@123"},
		{"contains slash", "ACC/123"},
		{"exceeds max length", strings.Repeat("a", 101)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.RetrieveCurrentAccount(context.Background(), &currentaccountv1.RetrieveCurrentAccountRequest{
				AccountId: tt.accountID,
			})

			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, err.Error(), "invalid account_id format")
		})
	}
}

func TestExecuteDeposit_InvalidAccountID(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50051",
	})
	require.NoError(t, err)
	defer cleanup()

	tests := []struct {
		name      string
		accountID string
	}{
		{"empty string", ""},
		{"contains special char", "ACC#123"},
		{"exceeds max length", strings.Repeat("a", 101)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.ExecuteDeposit(context.Background(), &currentaccountv1.ExecuteDepositRequest{
				AccountId: tt.accountID,
			})

			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, err.Error(), "invalid account_id format")
		})
	}
}

func TestInitiateLien_InvalidAccountID(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50051",
	})
	require.NoError(t, err)
	defer cleanup()

	tests := []struct {
		name      string
		accountID string
	}{
		{"empty string", ""},
		{"contains dot", "ACC.123"},
		{"exceeds max length", strings.Repeat("a", 101)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.InitiateLien(context.Background(), &currentaccountv1.InitiateLienRequest{
				AccountId: tt.accountID,
			})

			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, err.Error(), "invalid account_id format")
		})
	}
}
