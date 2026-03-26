package email_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/email"
)

func TestNewSenderFromEnv_Disabled(t *testing.T) {
	t.Setenv("EMAIL_MODE", "disabled")
	s, err := email.NewSenderFromEnv(nil)
	require.NoError(t, err)
	assert.IsType(t, &email.NoopSender{}, s)
}

func TestNewSenderFromEnv_Log(t *testing.T) {
	t.Setenv("EMAIL_MODE", "log")
	s, err := email.NewSenderFromEnv(nil)
	require.NoError(t, err)
	assert.IsType(t, &email.LogSender{}, s)
}

func TestNewSenderFromEnv_Live_MissingKey(t *testing.T) {
	t.Setenv("EMAIL_MODE", "live")
	t.Setenv("RESEND_API_KEY", "")
	_, err := email.NewSenderFromEnv(nil)
	require.ErrorIs(t, err, email.ErrMissingResendAPIKey)
}

func TestNewSenderFromEnv_Live_WithKey(t *testing.T) {
	t.Setenv("EMAIL_MODE", "live")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	s, err := email.NewSenderFromEnv(nil)
	require.NoError(t, err)
	assert.IsType(t, &email.ResendSender{}, s)
}

func TestNewSenderFromEnv_Default_MissingKey(t *testing.T) {
	t.Setenv("EMAIL_MODE", "")
	t.Setenv("RESEND_API_KEY", "")
	_, err := email.NewSenderFromEnv(nil)
	require.ErrorIs(t, err, email.ErrMissingResendAPIKey)
}

func TestNewSenderFromEnv_Default_WithKey(t *testing.T) {
	t.Setenv("EMAIL_MODE", "")
	t.Setenv("RESEND_API_KEY", "re_test_key")
	s, err := email.NewSenderFromEnv(nil)
	require.NoError(t, err)
	assert.IsType(t, &email.ResendSender{}, s)
}

func TestNewSenderFromEnv_UnknownMode(t *testing.T) {
	t.Setenv("EMAIL_MODE", "invalid")
	_, err := email.NewSenderFromEnv(nil)
	require.ErrorIs(t, err, email.ErrUnknownEmailMode)
}
