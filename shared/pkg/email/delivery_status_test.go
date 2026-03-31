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

// --- Test doubles for DeliveryStatusRecorder ---

type stubAuditRepo struct {
	recordByProviderIDErr   error
	recordCalls             int
	findByProviderIDResult  []email.AuditEntry
	findByProviderIDErr     error
	findByProviderIDCalls   int
}

func (s *stubAuditRepo) Record(_ context.Context, _ *email.AuditEntry) error { return nil }
func (s *stubAuditRepo) FindByOutboxID(_ context.Context, _ uuid.UUID) ([]email.AuditEntry, error) {
	return nil, nil
}
func (s *stubAuditRepo) FindByProviderID(_ context.Context, _ string) ([]email.AuditEntry, error) {
	s.findByProviderIDCalls++
	return s.findByProviderIDResult, s.findByProviderIDErr
}
func (s *stubAuditRepo) RecordByProviderID(_ context.Context, _ string, _ email.AuditStatus, _ map[string]any) error {
	s.recordCalls++
	return s.recordByProviderIDErr
}

type stubSuppressionRepo struct {
	addCalls   int
	addErr     error
	lastEntry  *email.SuppressionEntry
	allEntries []*email.SuppressionEntry
}

func (s *stubSuppressionRepo) IsSuppressed(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (s *stubSuppressionRepo) AddSuppression(_ context.Context, entry *email.SuppressionEntry) error {
	s.addCalls++
	s.lastEntry = entry
	s.allEntries = append(s.allEntries, entry)
	return s.addErr
}

// --- Tests ---

func TestDeliveryStatusRecorder_RecordDelivered_NoSuppression(t *testing.T) {
	audit := &stubAuditRepo{}
	supp := &stubSuppressionRepo{}
	recorder := email.NewDeliveryStatusRecorder(audit, supp, nil)

	err := recorder.RecordDeliveryStatus(context.Background(), "msg-1", email.AuditStatusDelivered, nil)

	require.NoError(t, err)
	assert.Equal(t, 1, audit.recordCalls)
	assert.Equal(t, 0, supp.addCalls, "delivered status should not suppress")
	assert.Equal(t, 0, audit.findByProviderIDCalls, "should not look up entries for non-bounce status")
}

func TestDeliveryStatusRecorder_RecordBounced_AddsSuppression(t *testing.T) {
	audit := &stubAuditRepo{
		findByProviderIDResult: []email.AuditEntry{
			{TenantID: "tenant-1", ToAddresses: []string{"bounced@example.com"}},
		},
	}
	supp := &stubSuppressionRepo{}
	recorder := email.NewDeliveryStatusRecorder(audit, supp, nil)

	err := recorder.RecordDeliveryStatus(context.Background(), "msg-2", email.AuditStatusBounced, nil)

	require.NoError(t, err)
	assert.Equal(t, 1, audit.recordCalls)
	assert.Equal(t, 1, supp.addCalls)
	assert.Equal(t, "bounced@example.com", supp.lastEntry.EmailAddress)
	assert.Equal(t, email.SuppressionBounce, supp.lastEntry.SuppressionType)
	assert.Equal(t, "msg-2", supp.lastEntry.ProviderID)
	assert.Equal(t, "tenant-1", supp.lastEntry.TenantID)
}

func TestDeliveryStatusRecorder_RecordComplained_AddsSuppression(t *testing.T) {
	audit := &stubAuditRepo{
		findByProviderIDResult: []email.AuditEntry{
			{TenantID: "tenant-1", ToAddresses: []string{"complainer@example.com"}},
		},
	}
	supp := &stubSuppressionRepo{}
	recorder := email.NewDeliveryStatusRecorder(audit, supp, nil)

	err := recorder.RecordDeliveryStatus(context.Background(), "msg-3", email.AuditStatusComplained, nil)

	require.NoError(t, err)
	assert.Equal(t, 1, supp.addCalls)
	assert.Equal(t, email.SuppressionComplaint, supp.lastEntry.SuppressionType)
}

func TestDeliveryStatusRecorder_MultipleRecipients(t *testing.T) {
	audit := &stubAuditRepo{
		findByProviderIDResult: []email.AuditEntry{
			{TenantID: "tenant-1", ToAddresses: []string{"a@example.com", "b@example.com"}},
		},
	}
	supp := &stubSuppressionRepo{}
	recorder := email.NewDeliveryStatusRecorder(audit, supp, nil)

	err := recorder.RecordDeliveryStatus(context.Background(), "msg-4", email.AuditStatusBounced, nil)

	require.NoError(t, err)
	assert.Equal(t, 2, supp.addCalls)
}

func TestDeliveryStatusRecorder_AuditError_PropagatesImmediately(t *testing.T) {
	audit := &stubAuditRepo{recordByProviderIDErr: email.ErrAuditEntryNotFound}
	supp := &stubSuppressionRepo{}
	recorder := email.NewDeliveryStatusRecorder(audit, supp, nil)

	err := recorder.RecordDeliveryStatus(context.Background(), "msg-5", email.AuditStatusBounced, nil)

	require.True(t, errors.Is(err, email.ErrAuditEntryNotFound))
	assert.Equal(t, 0, supp.addCalls, "should not attempt suppression when audit fails")
}

func TestDeliveryStatusRecorder_NilSuppressionRepo_SkipsSuppression(t *testing.T) {
	audit := &stubAuditRepo{}
	recorder := email.NewDeliveryStatusRecorder(audit, nil, nil)

	err := recorder.RecordDeliveryStatus(context.Background(), "msg-6", email.AuditStatusBounced, nil)

	require.NoError(t, err)
	assert.Equal(t, 1, audit.recordCalls)
	assert.Equal(t, 0, audit.findByProviderIDCalls, "should not look up entries when no suppression repo")
}

func TestDeliveryStatusRecorder_SuppressionError_DoesNotFail(t *testing.T) {
	audit := &stubAuditRepo{
		findByProviderIDResult: []email.AuditEntry{
			{TenantID: "tenant-1", ToAddresses: []string{"user@example.com"}},
		},
	}
	supp := &stubSuppressionRepo{addErr: errors.New("db error")}
	recorder := email.NewDeliveryStatusRecorder(audit, supp, nil)

	err := recorder.RecordDeliveryStatus(context.Background(), "msg-7", email.AuditStatusBounced, nil)

	require.NoError(t, err, "suppression error should not propagate")
	assert.Equal(t, 1, supp.addCalls, "suppression was attempted")
}

func TestDeliveryStatusRecorder_FindByProviderIDFails_LogsWarning(t *testing.T) {
	audit := &stubAuditRepo{
		findByProviderIDErr: errors.New("db error"),
	}
	supp := &stubSuppressionRepo{}
	recorder := email.NewDeliveryStatusRecorder(audit, supp, nil)

	err := recorder.RecordDeliveryStatus(context.Background(), "msg-8", email.AuditStatusBounced, nil)

	require.NoError(t, err, "suppression lookup failure should not propagate")
	assert.Equal(t, 0, supp.addCalls, "should not attempt suppression when lookup fails")
}

func TestDeliveryStatusRecorder_NoAuditEntries_SkipsSuppression(t *testing.T) {
	audit := &stubAuditRepo{
		findByProviderIDResult: []email.AuditEntry{},
	}
	supp := &stubSuppressionRepo{}
	recorder := email.NewDeliveryStatusRecorder(audit, supp, nil)

	err := recorder.RecordDeliveryStatus(context.Background(), "msg-9", email.AuditStatusBounced, nil)

	require.NoError(t, err)
	assert.Equal(t, 0, supp.addCalls)
}
