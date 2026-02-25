package client

import (
	"context"
	"strings"
	"testing"
	"time"

	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNew_WithTarget(t *testing.T) {
	client, cleanup, err := New(Config{
		Target:  "localhost:50057",
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	assert.NotNil(t, client.conn)
	assert.NotNil(t, client.internalAccount)
	assert.Equal(t, 10*time.Second, client.timeout)

	cleanup()
}

func TestNew_WithServiceName(t *testing.T) {
	client, cleanup, err := New(Config{
		ServiceName: "internal-account",
		Namespace:   "default",
		Port:        50057,
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, cleanup)

	assert.NotNil(t, client.conn)
	assert.NotNil(t, client.internalAccount)
	assert.Equal(t, DefaultTimeout, client.timeout)

	cleanup()
}

func TestNew_Defaults(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50057",
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
	// Port is used during connection setup but not stored in Client struct.
	// These tests verify defaults are applied during connection establishment.
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "empty port defaults to 50057",
			cfg:  Config{ServiceName: "internal-account"},
		},
		{
			name: "custom port preserved",
			cfg:  Config{ServiceName: "internal-account", Port: 9999},
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
		Target: "localhost:50057",
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
	assert.Equal(t, 50057, DefaultPort)
	assert.Equal(t, 30*time.Second, DefaultTimeout)
	assert.Equal(t, "default", DefaultNamespace)
	assert.Equal(t, "internal-account", ServiceName)
}

func TestNew_WithResilience(t *testing.T) {
	resilienceConfig := clients.DefaultResilientClientConfig("internal-account-client")
	client, cleanup, err := New(Config{
		Target:     "localhost:50057",
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
		Target: "localhost:50057",
	})
	require.NoError(t, err)
	require.NotNil(t, client)
	defer cleanup()

	// Verify resilient client was not created
	assert.Nil(t, client.resilient)
}

func TestConn(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50057",
	})
	require.NoError(t, err)
	defer cleanup()

	conn := client.Conn()
	assert.NotNil(t, conn)
	assert.Equal(t, client.conn, conn)
}

// Account ID validation tests

func TestRetrieveInternalAccount_InvalidAccountID(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50057",
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
			_, err := client.RetrieveInternalAccount(context.Background(), &internalaccountv1.RetrieveInternalAccountRequest{
				AccountId: tt.accountID,
			})

			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, err.Error(), "invalid account_id format")
		})
	}
}

func TestUpdateInternalAccount_InvalidAccountID(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50057",
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
			_, err := client.UpdateInternalAccount(context.Background(), &internalaccountv1.UpdateInternalAccountRequest{
				AccountId: tt.accountID,
			})

			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, err.Error(), "invalid account_id format")
		})
	}
}

func TestControlInternalAccount_InvalidAccountID(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50057",
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
			_, err := client.ControlInternalAccount(context.Background(), &internalaccountv1.ControlInternalAccountRequest{
				AccountId: tt.accountID,
			})

			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, err.Error(), "invalid account_id format")
		})
	}
}

func TestGetBalance_InvalidAccountID(t *testing.T) {
	client, cleanup, err := New(Config{
		Target: "localhost:50057",
	})
	require.NoError(t, err)
	defer cleanup()

	tests := []struct {
		name      string
		accountID string
	}{
		{"empty string", ""},
		{"contains newline", "ACC\n123"},
		{"exceeds max length", strings.Repeat("a", 101)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.GetBalance(context.Background(), &internalaccountv1.GetBalanceRequest{
				AccountId: tt.accountID,
			})

			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, err.Error(), "invalid account_id format")
		})
	}
}
