package email_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/email"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompositeAuditRepository_FindByProviderID_ReturnsFirstMatch(t *testing.T) {
	empty := &stubAuditRepo{findByProviderIDResult: []email.AuditEntry{}}
	match := &stubAuditRepo{findByProviderIDResult: []email.AuditEntry{
		{TenantID: "tenant-1", ToAddresses: []string{"a@example.com"}},
	}}
	repo := email.NewCompositeAuditRepository(empty, match)

	entries, err := repo.FindByProviderID(context.Background(), "provider-1")

	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tenant-1", entries[0].TenantID)
	assert.Equal(t, 1, empty.findByProviderIDCalls)
	assert.Equal(t, 1, match.findByProviderIDCalls)
}

func TestCompositeAuditRepository_FindByProviderID_SkipsErrors(t *testing.T) {
	failing := &stubAuditRepo{findByProviderIDErr: errors.New("db error")}
	match := &stubAuditRepo{findByProviderIDResult: []email.AuditEntry{
		{TenantID: "tenant-2"},
	}}
	repo := email.NewCompositeAuditRepository(failing, match)

	entries, err := repo.FindByProviderID(context.Background(), "provider-2")

	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tenant-2", entries[0].TenantID)
}

func TestCompositeAuditRepository_FindByProviderID_NoMatch(t *testing.T) {
	empty1 := &stubAuditRepo{findByProviderIDResult: []email.AuditEntry{}}
	empty2 := &stubAuditRepo{findByProviderIDResult: []email.AuditEntry{}}
	repo := email.NewCompositeAuditRepository(empty1, empty2)

	entries, err := repo.FindByProviderID(context.Background(), "provider-none")

	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestCompositeAuditRepository_RecordByProviderID_TriesUntilFound(t *testing.T) {
	notFound := &stubAuditRepo{recordByProviderIDErr: email.ErrAuditEntryNotFound}
	found := &stubAuditRepo{}
	repo := email.NewCompositeAuditRepository(notFound, found)

	err := repo.RecordByProviderID(context.Background(), "provider-3", email.AuditStatusComplained, nil)

	require.NoError(t, err)
	assert.Equal(t, 1, notFound.recordCalls)
	assert.Equal(t, 1, found.recordCalls)
}

func TestCompositeAuditRepository_RecordByProviderID_PropagatesNonNotFoundError(t *testing.T) {
	failing := &stubAuditRepo{recordByProviderIDErr: errors.New("connection refused")}
	repo := email.NewCompositeAuditRepository(failing)

	err := repo.RecordByProviderID(context.Background(), "provider-4", email.AuditStatusBounced, nil)

	require.Error(t, err)
	assert.Equal(t, "connection refused", err.Error())
}

func TestCompositeAuditRepository_RecordByProviderID_AllNotFound(t *testing.T) {
	r1 := &stubAuditRepo{recordByProviderIDErr: email.ErrAuditEntryNotFound}
	r2 := &stubAuditRepo{recordByProviderIDErr: email.ErrAuditEntryNotFound}
	repo := email.NewCompositeAuditRepository(r1, r2)

	err := repo.RecordByProviderID(context.Background(), "provider-5", email.AuditStatusBounced, nil)

	require.ErrorIs(t, err, email.ErrAuditEntryNotFound)
}

func TestCompositeAuditRepository_Record_Panics(t *testing.T) {
	repo := email.NewCompositeAuditRepository()

	assert.Panics(t, func() {
		_ = repo.Record(context.Background(), &email.AuditEntry{})
	})
}

func TestCompositeAuditRepository_FindByOutboxID_Panics(t *testing.T) {
	repo := email.NewCompositeAuditRepository()

	assert.Panics(t, func() {
		_, _ = repo.FindByOutboxID(context.Background(), uuid.New())
	})
}
