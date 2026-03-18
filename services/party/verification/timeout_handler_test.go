package verification

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTimeoutRepo implements TimeoutVerificationRepository for testing.
type mockTimeoutRepo struct {
	mu             sync.Mutex
	verifications  []persistence.PartyVerificationEntity
	updatedCalls   []statusUpdateCall
	updateErr      error
	listPendingErr error
}

type statusUpdateCall struct {
	VerificationID uuid.UUID
	Status         string
	RiskScore      *float64
	Reason         *string
	CompletedAt    *time.Time
	Version        int64
}

func (m *mockTimeoutRepo) ListPendingVerifications(_ context.Context) ([]persistence.PartyVerificationEntity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listPendingErr != nil {
		return nil, m.listPendingErr
	}
	var pending []persistence.PartyVerificationEntity
	for _, v := range m.verifications {
		if v.Status == "PENDING" {
			pending = append(pending, v)
		}
	}
	return pending, nil
}

func (m *mockTimeoutRepo) UpdateVerificationStatus(
	_ context.Context,
	verificationID uuid.UUID,
	status string,
	riskScore *float64,
	reason *string,
	completedAt *time.Time,
	currentVersion int64,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updateErr != nil {
		return m.updateErr
	}
	m.updatedCalls = append(m.updatedCalls, statusUpdateCall{
		VerificationID: verificationID,
		Status:         status,
		RiskScore:      riskScore,
		Reason:         reason,
		CompletedAt:    completedAt,
		Version:        currentVersion,
	})
	// Update in-memory state
	for i := range m.verifications {
		if m.verifications[i].ID == verificationID {
			m.verifications[i].Status = status
			m.verifications[i].Version = currentVersion + 1
		}
	}
	return nil
}

func (m *mockTimeoutRepo) getUpdatedCalls() []statusUpdateCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]statusUpdateCall, len(m.updatedCalls))
	copy(result, m.updatedCalls)
	return result
}

// statusCheckProvider is a mock provider that returns configured statuses per verification ID.
type statusCheckProvider struct {
	mu       sync.Mutex
	statuses map[string]Result
	err      error
	calls    []string
}

func (p *statusCheckProvider) GetVerificationStatus(_ context.Context, verificationID string) (Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, verificationID)
	if p.err != nil {
		return Result{}, p.err
	}
	if result, ok := p.statuses[verificationID]; ok {
		return result, nil
	}
	return Result{
		VerificationID: verificationID,
		Status:         StatusPending,
	}, nil
}

func (p *statusCheckProvider) getCalls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]string, len(p.calls))
	copy(result, p.calls)
	return result
}

func TestTimeoutHandler_DetectsTimedOutVerifications(t *testing.T) {
	repo := &mockTimeoutRepo{
		verifications: []persistence.PartyVerificationEntity{
			{
				ID:             uuid.New(),
				PartyID:        uuid.New(),
				VerificationID: "provider-old",
				Status:         "PENDING",
				Version:        1,
				CreatedAt:      time.Now().Add(-48 * time.Hour), // 48 hours ago - timed out
			},
			{
				ID:             uuid.New(),
				PartyID:        uuid.New(),
				VerificationID: "provider-recent",
				Status:         "PENDING",
				Version:        1,
				CreatedAt:      time.Now().Add(-1 * time.Hour), // 1 hour ago - not timed out
			},
		},
	}

	provider := &statusCheckProvider{
		statuses: map[string]Result{
			"provider-old": {
				VerificationID: "provider-old",
				Status:         StatusPending,
			},
		},
	}

	handler, err := NewTimeoutHandler(TimeoutHandlerConfig{
		VerificationRepo: repo,
		Provider:         provider,
		Timeout:          24 * time.Hour,
		PollInterval:     50 * time.Millisecond,
		Logger:           newTestLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	// Run handler in background
	done := make(chan struct{})
	go func() {
		handler.Run(ctx)
		close(done)
	}()

	// Wait for the handler to process the timed-out verification
	require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
		return len(repo.getUpdatedCalls()) >= 1
	}), "handler did not process timed-out verification within timeout")
	cancel()
	<-done

	// Only the old verification should have been checked with the provider
	calls := provider.getCalls()
	assert.Contains(t, calls, "provider-old")
	assert.NotContains(t, calls, "provider-recent")

	// Old verification should be marked as MANUAL_REVIEW (provider returned PENDING)
	updated := repo.getUpdatedCalls()
	require.Len(t, updated, 1)
	assert.Equal(t, "MANUAL_REVIEW", updated[0].Status)
	assert.NotNil(t, updated[0].Reason)
	assert.Contains(t, *updated[0].Reason, "timed out")
}

func TestTimeoutHandler_ProviderReturnsApproved(t *testing.T) {
	verificationID := uuid.New()
	now := time.Now()

	repo := &mockTimeoutRepo{
		verifications: []persistence.PartyVerificationEntity{
			{
				ID:             verificationID,
				PartyID:        uuid.New(),
				VerificationID: "provider-123",
				Status:         "PENDING",
				Version:        1,
				CreatedAt:      time.Now().Add(-48 * time.Hour),
			},
		},
	}

	completedAt := now.Add(-1 * time.Hour)
	riskScore := 0.15
	provider := &statusCheckProvider{
		statuses: map[string]Result{
			"provider-123": {
				VerificationID: "provider-123",
				Status:         StatusApproved,
				RiskScore:      riskScore,
				Reason:         "Identity verified successfully",
				CompletedAt:    &completedAt,
			},
		},
	}

	handler, err := NewTimeoutHandler(TimeoutHandlerConfig{
		VerificationRepo: repo,
		Provider:         provider,
		Timeout:          24 * time.Hour,
		PollInterval:     50 * time.Millisecond,
		Logger:           newTestLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		handler.Run(ctx)
		close(done)
	}()

	require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
		return len(repo.getUpdatedCalls()) >= 1
	}), "handler did not process verification within timeout")
	cancel()
	<-done

	updated := repo.getUpdatedCalls()
	require.Len(t, updated, 1)
	assert.Equal(t, verificationID, updated[0].VerificationID)
	assert.Equal(t, "APPROVED", updated[0].Status)
	assert.NotNil(t, updated[0].RiskScore)
	assert.InDelta(t, 0.15, *updated[0].RiskScore, 0.001)
}

func TestTimeoutHandler_ProviderReturnsRejected(t *testing.T) {
	verificationID := uuid.New()

	repo := &mockTimeoutRepo{
		verifications: []persistence.PartyVerificationEntity{
			{
				ID:             verificationID,
				PartyID:        uuid.New(),
				VerificationID: "provider-456",
				Status:         "PENDING",
				Version:        1,
				CreatedAt:      time.Now().Add(-48 * time.Hour),
			},
		},
	}

	completedAt := time.Now()
	riskScore := 0.9
	reason := "Document mismatch"
	provider := &statusCheckProvider{
		statuses: map[string]Result{
			"provider-456": {
				VerificationID: "provider-456",
				Status:         StatusRejected,
				RiskScore:      riskScore,
				Reason:         reason,
				CompletedAt:    &completedAt,
			},
		},
	}

	handler, err := NewTimeoutHandler(TimeoutHandlerConfig{
		VerificationRepo: repo,
		Provider:         provider,
		Timeout:          24 * time.Hour,
		PollInterval:     50 * time.Millisecond,
		Logger:           newTestLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		handler.Run(ctx)
		close(done)
	}()

	require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
		return len(repo.getUpdatedCalls()) >= 1
	}), "handler did not process verification within timeout")
	cancel()
	<-done

	updated := repo.getUpdatedCalls()
	require.Len(t, updated, 1)
	assert.Equal(t, "REJECTED", updated[0].Status)
	assert.NotNil(t, updated[0].Reason)
	assert.Equal(t, "Document mismatch", *updated[0].Reason)
}

func TestTimeoutHandler_IgnoresCompletedVerifications(t *testing.T) {
	// Only PENDING verifications are returned by ListPendingVerifications,
	// but we also need to filter by timeout. Here we ensure the handler
	// does NOT process PENDING verifications that are within the timeout window.
	repo := &mockTimeoutRepo{
		verifications: []persistence.PartyVerificationEntity{
			{
				ID:             uuid.New(),
				PartyID:        uuid.New(),
				VerificationID: "provider-new",
				Status:         "PENDING",
				Version:        1,
				CreatedAt:      time.Now().Add(-30 * time.Minute), // Only 30 min old
			},
		},
	}

	provider := &statusCheckProvider{
		statuses: make(map[string]Result),
	}

	handler, err := NewTimeoutHandler(TimeoutHandlerConfig{
		VerificationRepo: repo,
		Provider:         provider,
		Timeout:          24 * time.Hour,
		PollInterval:     50 * time.Millisecond,
		Logger:           newTestLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		handler.Run(ctx)
		close(done)
	}()

	time.Sleep(200 * time.Millisecond) //nolint:forbidigo // gives handler time to run at least one poll cycle (asserting absence of processing)
	cancel()
	<-done

	// Provider should NOT have been called
	calls := provider.getCalls()
	assert.Empty(t, calls)

	// No updates should have been made
	updated := repo.getUpdatedCalls()
	assert.Empty(t, updated)
}

func TestTimeoutHandler_RespectsContextCancellation(t *testing.T) {
	repo := &mockTimeoutRepo{
		verifications: []persistence.PartyVerificationEntity{},
	}

	provider := &statusCheckProvider{
		statuses: make(map[string]Result),
	}

	handler, err := NewTimeoutHandler(TimeoutHandlerConfig{
		VerificationRepo: repo,
		Provider:         provider,
		Timeout:          24 * time.Hour,
		PollInterval:     1 * time.Hour, // Long poll interval
		Logger:           newTestLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		handler.Run(ctx)
		close(done)
	}()

	// Cancel immediately
	cancel()

	// Handler should stop quickly
	select {
	case <-done:
		// Success - handler stopped
	case <-time.After(2 * time.Second):
		t.Fatal("timeout handler did not stop after context cancellation")
	}
}

func TestTimeoutHandler_HandlesProviderErrorsGracefully(t *testing.T) {
	v1 := uuid.New()
	v2 := uuid.New()

	repo := &mockTimeoutRepo{
		verifications: []persistence.PartyVerificationEntity{
			{
				ID:             v1,
				PartyID:        uuid.New(),
				VerificationID: "provider-err",
				Status:         "PENDING",
				Version:        1,
				CreatedAt:      time.Now().Add(-48 * time.Hour),
			},
			{
				ID:             v2,
				PartyID:        uuid.New(),
				VerificationID: "provider-ok",
				Status:         "PENDING",
				Version:        1,
				CreatedAt:      time.Now().Add(-48 * time.Hour),
			},
		},
	}

	provider := &statusCheckProvider{
		statuses: map[string]Result{
			// provider-err will return the default error
			"provider-ok": {
				VerificationID: "provider-ok",
				Status:         StatusPending,
			},
		},
	}

	// Make the first call fail, but second succeed
	errProvider := &failFirstProvider{
		failOnIDs: map[string]bool{"provider-err": true},
		fallback:  provider,
	}

	handler, err := NewTimeoutHandler(TimeoutHandlerConfig{
		VerificationRepo: repo,
		Provider:         errProvider,
		Timeout:          24 * time.Hour,
		PollInterval:     50 * time.Millisecond,
		Logger:           newTestLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		handler.Run(ctx)
		close(done)
	}()

	require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
		for _, u := range repo.getUpdatedCalls() {
			if u.VerificationID == v2 {
				return true
			}
		}
		return false
	}), "handler did not process provider-ok verification within timeout")
	cancel()
	<-done

	// provider-ok should still have been processed (marked MANUAL_REVIEW)
	updated := repo.getUpdatedCalls()
	var foundOK bool
	for _, u := range updated {
		if u.VerificationID == v2 {
			foundOK = true
			assert.Equal(t, "MANUAL_REVIEW", u.Status)
		}
	}
	assert.True(t, foundOK, "provider-ok verification should have been updated despite provider-err failure")

	// provider-err should NOT have been updated (provider error skipped it)
	for _, u := range updated {
		assert.NotEqual(t, v1, u.VerificationID, "provider-err verification should not be updated when provider fails")
	}
}

// failFirstProvider returns an error for specific verification IDs.
type failFirstProvider struct {
	failOnIDs map[string]bool
	fallback  *statusCheckProvider
}

func (p *failFirstProvider) GetVerificationStatus(ctx context.Context, verificationID string) (Result, error) {
	if p.failOnIDs[verificationID] {
		return Result{}, assert.AnError
	}
	return p.fallback.GetVerificationStatus(ctx, verificationID)
}

func TestTimeoutHandler_ProviderReturnsManualReview(t *testing.T) {
	verificationID := uuid.New()

	repo := &mockTimeoutRepo{
		verifications: []persistence.PartyVerificationEntity{
			{
				ID:             verificationID,
				PartyID:        uuid.New(),
				VerificationID: "provider-mr",
				Status:         "PENDING",
				Version:        1,
				CreatedAt:      time.Now().Add(-48 * time.Hour),
			},
		},
	}

	completedAt := time.Now()
	reason := "Needs human review"
	provider := &statusCheckProvider{
		statuses: map[string]Result{
			"provider-mr": {
				VerificationID: "provider-mr",
				Status:         StatusManualReview,
				Reason:         reason,
				CompletedAt:    &completedAt,
			},
		},
	}

	handler, err := NewTimeoutHandler(TimeoutHandlerConfig{
		VerificationRepo: repo,
		Provider:         provider,
		Timeout:          24 * time.Hour,
		PollInterval:     50 * time.Millisecond,
		Logger:           newTestLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		handler.Run(ctx)
		close(done)
	}()

	require.NoError(t, await.New().AtMost(5*time.Second).PollInterval(10*time.Millisecond).Until(func() bool {
		return len(repo.getUpdatedCalls()) >= 1
	}), "handler did not process verification within timeout")
	cancel()
	<-done

	updated := repo.getUpdatedCalls()
	require.Len(t, updated, 1)
	assert.Equal(t, "MANUAL_REVIEW", updated[0].Status)
	assert.NotNil(t, updated[0].Reason)
	assert.Equal(t, "Needs human review", *updated[0].Reason)
}

func TestTimeoutHandler_NilRepoReturnsError(t *testing.T) {
	_, err := NewTimeoutHandler(TimeoutHandlerConfig{
		VerificationRepo: nil,
		Provider:         &statusCheckProvider{},
	})
	assert.ErrorIs(t, err, ErrTimeoutRepoNil)
}

func TestTimeoutHandler_NilProviderReturnsError(t *testing.T) {
	_, err := NewTimeoutHandler(TimeoutHandlerConfig{
		VerificationRepo: &mockTimeoutRepo{},
		Provider:         nil,
	})
	assert.ErrorIs(t, err, ErrTimeoutProvNil)
}
