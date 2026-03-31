package email_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPreferenceRepository implements email.PreferenceRepository for testing.
type mockPreferenceRepository struct {
	globalUnsubscribe bool
	globalUnsubErr    error
	preference        *email.CommunicationPreference
	preferenceErr     error
}

func (m *mockPreferenceRepository) GetGlobalUnsubscribe(_ context.Context, _, _ string) (bool, error) {
	return m.globalUnsubscribe, m.globalUnsubErr
}

func (m *mockPreferenceRepository) GetPreference(_ context.Context, _, _, _, _ string) (*email.CommunicationPreference, error) {
	return m.preference, m.preferenceErr
}

func testCtx() context.Context {
	tid, _ := tenant.NewTenantID("test-tenant")
	return tenant.WithTenant(context.Background(), tid)
}

func TestPreferenceEnforcer_TransactionalAlwaysAllowed(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{globalUnsubscribe: true}, // even with global unsub
		nil,
		slog.Default(),
	)

	allowed, reason, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "invoice", "TRANSACTIONAL")
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Empty(t, reason)
}

func TestPreferenceEnforcer_GlobalUnsubscribeBlocksOperational(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{globalUnsubscribe: true},
		nil,
		slog.Default(),
	)

	allowed, reason, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "maintenance", "OPERATIONAL")
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Contains(t, reason, "globally unsubscribed")
}

func TestPreferenceEnforcer_GlobalUnsubscribeBlocksMarketing(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{globalUnsubscribe: true},
		nil,
		slog.Default(),
	)

	allowed, reason, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "promo", "MARKETING")
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Contains(t, reason, "globally unsubscribed")
}

func TestPreferenceEnforcer_ExplicitOptOutBlocks(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{
			preference: &email.CommunicationPreference{OptedIn: false},
		},
		nil,
		slog.Default(),
	)

	allowed, reason, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "newsletter", "MARKETING")
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Contains(t, reason, "opted out")
}

func TestPreferenceEnforcer_ExplicitOptInAllows(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{
			preference: &email.CommunicationPreference{OptedIn: true},
		},
		nil,
		slog.Default(),
	)

	allowed, reason, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "newsletter", "MARKETING")
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Empty(t, reason)
}

func TestPreferenceEnforcer_DefaultOperationalAllowed(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{}, // no preference, no global unsub
		nil,
		slog.Default(),
	)

	allowed, reason, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "maintenance", "OPERATIONAL")
	require.NoError(t, err)
	assert.True(t, allowed)
	assert.Empty(t, reason)
}

func TestPreferenceEnforcer_DefaultMarketingSuppressed(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{}, // no explicit opt-in
		nil,
		slog.Default(),
	)

	allowed, reason, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "promo", "MARKETING")
	require.NoError(t, err)
	assert.False(t, allowed)
	assert.Contains(t, reason, "no explicit opt-in")
}

func TestPreferenceEnforcer_CategoryMismatchRejectsWithError(t *testing.T) {
	templateMap := map[string]string{
		"invoice": "TRANSACTIONAL",
	}
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{},
		templateMap,
		slog.Default(),
	)

	allowed, reason, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "invoice", "MARKETING")
	require.ErrorIs(t, err, email.ErrCategoryMismatch)
	assert.False(t, allowed)
	assert.Contains(t, reason, "category mismatch")
}

func TestPreferenceEnforcer_UnknownCategoryRejectsWithError(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{},
		nil,
		slog.Default(),
	)

	allowed, reason, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "foo", "INVALID")
	require.ErrorIs(t, err, email.ErrMissingCategory)
	assert.False(t, allowed)
	assert.Contains(t, reason, "unknown category")
}

func TestPreferenceEnforcer_RepositoryErrorPropagated(t *testing.T) {
	dbErr := errors.New("connection refused")
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{globalUnsubErr: dbErr},
		nil,
		slog.Default(),
	)

	_, _, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "maintenance", "OPERATIONAL")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestPreferenceEnforcer_PreferenceRepoErrorPropagated(t *testing.T) {
	dbErr := errors.New("timeout")
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{preferenceErr: dbErr},
		nil,
		slog.Default(),
	)

	_, _, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "newsletter", "MARKETING")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestPreferenceEnforcer_TemplateCategoryMapMatchAllows(t *testing.T) {
	templateMap := map[string]string{
		"invoice": "TRANSACTIONAL",
	}
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{},
		templateMap,
		slog.Default(),
	)

	allowed, _, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "invoice", "TRANSACTIONAL")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestPreferenceEnforcer_UnknownTemplateSkipsMapCheck(t *testing.T) {
	templateMap := map[string]string{
		"invoice": "TRANSACTIONAL",
	}
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{},
		templateMap,
		slog.Default(),
	)

	// "newsletter" not in map - should skip map check and proceed normally
	allowed, _, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "newsletter", "OPERATIONAL")
	require.NoError(t, err)
	assert.True(t, allowed)
}

func TestPreferenceEnforcer_NilLoggerUsesDefault(t *testing.T) {
	enforcer := email.NewPreferenceEnforcer(
		&mockPreferenceRepository{},
		nil,
		nil, // nil logger
	)

	allowed, _, err := enforcer.ShouldSend(testCtx(), "t1", "p1", "EMAIL", "test", "TRANSACTIONAL")
	require.NoError(t, err)
	assert.True(t, allowed)
}
