package dex

import (
	"context"
	"errors"
	"testing"

	dexconnector "github.com/dexidp/dex/connector"
	meridianconnector "github.com/meridianhub/meridian/services/identity/connector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubConnector is a test double for meridianconnector.PasswordConnector.
type stubConnector struct {
	loginFn func(ctx context.Context, scopes []string, username, password string) (meridianconnector.Identity, bool, error)
}

func (s *stubConnector) Login(ctx context.Context, scopes []string, username, password string) (meridianconnector.Identity, bool, error) {
	return s.loginFn(ctx, scopes, username, password)
}

func TestConnectorAdapter_Prompt(t *testing.T) {
	adapter := NewConnectorAdapter(&stubConnector{})
	assert.Equal(t, "Email", adapter.Prompt())
}

func TestConnectorAdapter_Login_Success(t *testing.T) {
	expected := meridianconnector.Identity{
		UserID:        "user-123",
		Username:      "test@example.com",
		Email:         "test@example.com",
		EmailVerified: true,
		Groups:        []string{"admin", "user"},
		ConnectorData: []byte("opaque"),
	}

	stub := &stubConnector{
		loginFn: func(_ context.Context, _ []string, username, password string) (meridianconnector.Identity, bool, error) {
			assert.Equal(t, "test@example.com", username)
			assert.Equal(t, "secret", password)
			return expected, true, nil
		},
	}

	adapter := NewConnectorAdapter(stub)
	identity, valid, err := adapter.Login(context.Background(), dexconnector.Scopes{Groups: true}, "test@example.com", "secret")

	require.NoError(t, err)
	assert.True(t, valid)
	assert.Equal(t, "user-123", identity.UserID)
	assert.Equal(t, "test@example.com", identity.Username)
	assert.Equal(t, "test@example.com", identity.Email)
	assert.True(t, identity.EmailVerified)
	assert.Equal(t, []string{"admin", "user"}, identity.Groups)
	assert.Equal(t, []byte("opaque"), identity.ConnectorData)
}

func TestConnectorAdapter_Login_InvalidCredentials(t *testing.T) {
	stub := &stubConnector{
		loginFn: func(_ context.Context, _ []string, _, _ string) (meridianconnector.Identity, bool, error) {
			return meridianconnector.Identity{}, false, nil
		},
	}

	adapter := NewConnectorAdapter(stub)
	_, valid, err := adapter.Login(context.Background(), dexconnector.Scopes{}, "bad@example.com", "wrong")

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestConnectorAdapter_Login_Error(t *testing.T) {
	expectedErr := errors.New("repository failure")
	stub := &stubConnector{
		loginFn: func(_ context.Context, _ []string, _, _ string) (meridianconnector.Identity, bool, error) {
			return meridianconnector.Identity{}, false, expectedErr
		},
	}

	adapter := NewConnectorAdapter(stub)
	_, valid, err := adapter.Login(context.Background(), dexconnector.Scopes{}, "user@example.com", "pass")

	assert.False(t, valid)
	assert.ErrorIs(t, err, expectedErr)
}

func TestConnectorAdapter_Login_ScopesTranslation(t *testing.T) {
	var capturedScopes []string
	stub := &stubConnector{
		loginFn: func(_ context.Context, scopes []string, _, _ string) (meridianconnector.Identity, bool, error) {
			capturedScopes = scopes
			return meridianconnector.Identity{}, true, nil
		},
	}

	adapter := NewConnectorAdapter(stub)

	// Both scopes set.
	_, _, _ = adapter.Login(context.Background(), dexconnector.Scopes{
		OfflineAccess: true,
		Groups:        true,
	}, "user@example.com", "pass")

	assert.Contains(t, capturedScopes, "offline_access")
	assert.Contains(t, capturedScopes, "groups")

	// No scopes.
	capturedScopes = nil
	_, _, _ = adapter.Login(context.Background(), dexconnector.Scopes{}, "user@example.com", "pass")
	assert.Empty(t, capturedScopes)
}

func TestConnectorAdapter_ImplementsDexInterface(_ *testing.T) {
	// Compile-time check is in the source file, but verify at test time too.
	var _ dexconnector.PasswordConnector = (*ConnectorAdapter)(nil)
}
