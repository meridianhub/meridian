package domain_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDispute(t *testing.T) {
	varianceID := uuid.New()
	runID := uuid.New()

	tests := []struct {
		name       string
		varianceID uuid.UUID
		runID      uuid.UUID
		accountID  string
		reason     string
		raisedBy   string
		wantErr    error
	}{
		{
			name:       "valid dispute",
			varianceID: varianceID,
			runID:      runID,
			accountID:  "ACC-001",
			reason:     "Amount seems incorrect",
			raisedBy:   "user-1",
			wantErr:    nil,
		},
		{
			name:       "nil variance ID",
			varianceID: uuid.Nil,
			runID:      runID,
			accountID:  "ACC-001",
			reason:     "Reason",
			raisedBy:   "user-1",
			wantErr:    domain.ErrEmptyVarianceID,
		},
		{
			name:       "empty account ID",
			varianceID: varianceID,
			runID:      runID,
			accountID:  "",
			reason:     "Reason",
			raisedBy:   "user-1",
			wantErr:    domain.ErrEmptyAccountID,
		},
		{
			name:       "empty reason",
			varianceID: varianceID,
			runID:      runID,
			accountID:  "ACC-001",
			reason:     "",
			raisedBy:   "user-1",
			wantErr:    domain.ErrEmptyDisputeReason,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := domain.NewDispute(
				tt.varianceID, tt.runID, tt.accountID, tt.reason, tt.raisedBy,
			)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, d)
			} else {
				require.NoError(t, err)
				require.NotNil(t, d)

				assert.NotEqual(t, uuid.Nil, d.DisputeID)
				assert.Equal(t, tt.varianceID, d.VarianceID)
				assert.Equal(t, tt.runID, d.RunID)
				assert.Equal(t, tt.accountID, d.AccountID)
				assert.Equal(t, domain.DisputeStatusOpen, d.Status)
				assert.Equal(t, tt.reason, d.Reason)
				assert.Equal(t, tt.raisedBy, d.RaisedBy)
				assert.Nil(t, d.ResolvedAt)
			}
		})
	}
}

func TestDispute_Lifecycle(t *testing.T) {
	d := newTestDispute(t)

	// Review
	require.NoError(t, d.Review())
	assert.Equal(t, domain.DisputeStatusUnderReview, d.Status)

	// Escalate
	require.NoError(t, d.Escalate())
	assert.Equal(t, domain.DisputeStatusEscalated, d.Status)

	// Resolve
	require.NoError(t, d.Resolve("Adjustment posted", "admin"))
	assert.Equal(t, domain.DisputeStatusResolved, d.Status)
	assert.Equal(t, "Adjustment posted", d.Resolution)
	assert.Equal(t, "admin", d.ResolvedBy)
	assert.NotNil(t, d.ResolvedAt)
}

func TestDispute_RejectLifecycle(t *testing.T) {
	d := newTestDispute(t)

	require.NoError(t, d.Review())
	require.NoError(t, d.Reject("No evidence of error", "reviewer"))

	assert.Equal(t, domain.DisputeStatusRejected, d.Status)
	assert.Equal(t, "No evidence of error", d.Resolution)
	assert.Equal(t, "reviewer", d.ResolvedBy)
	assert.NotNil(t, d.ResolvedAt)
}

func TestDispute_InvalidTransitions(t *testing.T) {
	t.Run("cannot escalate from open", func(t *testing.T) {
		d := newTestDispute(t)
		err := d.Escalate()
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})

	t.Run("cannot review from resolved", func(t *testing.T) {
		d := newTestDispute(t)
		require.NoError(t, d.Resolve("done", "admin"))
		err := d.Review()
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})

	t.Run("cannot escalate from rejected", func(t *testing.T) {
		d := newTestDispute(t)
		require.NoError(t, d.Reject("rejected", "admin"))
		err := d.Escalate()
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})
}

func newTestDispute(t *testing.T) *domain.Dispute {
	t.Helper()
	d, err := domain.NewDispute(
		uuid.New(), uuid.New(), "ACC-001",
		"Amount discrepancy noted", "user-1",
	)
	require.NoError(t, err)
	return d
}
