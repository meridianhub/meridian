package connector_test

import (
	"context"
	"strings"
	"testing"

	"github.com/meridianhub/meridian/services/identity/connector"
	"github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/pkg/credentials"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock outbox ---

type mockOutbox struct {
	entries []*email.OutboxEntry
	err     error
}

func (m *mockOutbox) Enqueue(_ context.Context, entry *email.OutboxEntry) error {
	if m.err != nil {
		return m.err
	}
	m.entries = append(m.entries, entry)
	return nil
}

func newConnectorWithOutbox(t *testing.T, repo domain.Repository, outbox connector.OutboxWriter) *connector.Connector {
	t.Helper()
	c, err := connector.New(repo, nil, connector.WithEmailOutbox(outbox))
	require.NoError(t, err)
	return c
}

func makeIdentityWithNFailures(t *testing.T, email string, n int) *domain.Identity {
	t.Helper()
	id, err := domain.NewIdentity(connTestTID, email)
	require.NoError(t, err)

	hash, err := credentials.HashPassword(testPassword)
	require.NoError(t, err)
	require.NoError(t, id.SetPassword(hash))
	require.NoError(t, id.Activate())

	for range n {
		err := id.RecordLoginAttempt(false)
		require.NoError(t, err)
	}
	return id
}

// --- Lockout email: triggered on exact 5th failure ---

func TestLogin_5thFailedAttempt_QueuesLockoutEmail(t *testing.T) {
	// Identity already has 4 failed attempts; 5th will lock it.
	identity := makeIdentityWithNFailures(t, "lockme@example.com", 4)
	require.False(t, identity.IsLocked())

	outbox := &mockOutbox{}
	repo := &mockRepo{identity: identity}
	c := newConnectorWithOutbox(t, repo, outbox)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Login(ctx, nil, "lockme@example.com", "WrongPassword999!")

	require.NoError(t, err)
	assert.False(t, valid)
	require.Len(t, outbox.entries, 1, "expected exactly one lockout email queued")

	entry := outbox.entries[0]
	assert.Equal(t, "account-lockout", entry.TemplateName)
	assert.Equal(t, []string{"lockme@example.com"}, entry.ToAddresses)
	assert.True(t, strings.HasPrefix(entry.IdempotencyKey, "account-lockout:"),
		"idempotency key should identify lockout event, got: %s", entry.IdempotencyKey)
	assert.Contains(t, entry.TemplateData, "TenantName")
	assert.Contains(t, entry.TemplateData, "SupportEmail")
	assert.Contains(t, entry.TemplateData, "LockoutTime")
}

func TestLogin_4thFailedAttempt_DoesNotQueueLockoutEmail(t *testing.T) {
	// Identity has 3 prior failures; 4th does not lock.
	identity := makeIdentityWithNFailures(t, "notlocked@example.com", 3)

	outbox := &mockOutbox{}
	repo := &mockRepo{identity: identity}
	c := newConnectorWithOutbox(t, repo, outbox)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Login(ctx, nil, "notlocked@example.com", "WrongPassword999!")

	require.NoError(t, err)
	assert.False(t, valid)
	assert.Empty(t, outbox.entries, "no lockout email should be queued before threshold")
}

func TestLogin_NilEmailOutbox_NoPanic(t *testing.T) {
	// When no outbox is wired, the 5th failure should still lock without panicking.
	identity := makeIdentityWithNFailures(t, "noemail@example.com", 4)
	repo := &mockRepo{identity: identity}

	// Use basic connector without email outbox option.
	c, err := connector.New(repo, nil)
	require.NoError(t, err)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, loginErr := c.Login(ctx, nil, "noemail@example.com", "WrongPassword999!")

	require.NoError(t, loginErr)
	assert.False(t, valid)
}

func TestLogin_IdempotentLockout_OutboxErrorIgnored(t *testing.T) {
	// If the outbox returns ErrDuplicateIdempotency (concurrent lockout), login still returns false/nil.
	identity := makeIdentityWithNFailures(t, "concurrent@example.com", 4)
	outbox := &mockOutbox{err: email.ErrDuplicateIdempotency}
	repo := &mockRepo{identity: identity}
	c := newConnectorWithOutbox(t, repo, outbox)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Login(ctx, nil, "concurrent@example.com", "WrongPassword999!")

	require.NoError(t, err)
	assert.False(t, valid)
}

func TestLogin_OutboxEnqueueError_DoesNotFailLogin(t *testing.T) {
	// A transient outbox failure must not bubble up as a login error.
	identity := makeIdentityWithNFailures(t, "outboxerr@example.com", 4)
	outbox := &mockOutbox{err: assert.AnError}
	repo := &mockRepo{identity: identity}
	c := newConnectorWithOutbox(t, repo, outbox)
	ctx := ctxWithTenant(t, "volterra")

	_, valid, err := c.Login(ctx, nil, "outboxerr@example.com", "WrongPassword999!")

	require.NoError(t, err)
	assert.False(t, valid)
}
